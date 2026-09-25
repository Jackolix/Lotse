package hub

import (
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Jackolix/Lotse/internal/agent"
	"github.com/Jackolix/Lotse/internal/hub/store"
	"github.com/Jackolix/Lotse/internal/protocol"
	"github.com/Jackolix/Lotse/internal/update"
	"github.com/Jackolix/Lotse/internal/version"
)

// trustTestKey makes hub and agents accept updates signed with a fresh key.
func trustTestKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	old := update.TrustedKeys
	update.TrustedKeys = []string{base64.StdEncoding.EncodeToString(pub)}
	t.Cleanup(func() { update.TrustedKeys = old })
	return priv
}

// fakeAgentBinary writes a script that answers "version" like an agent would.
func fakeAgentBinary(t *testing.T, dir, ver string) string {
	t.Helper()
	path := filepath.Join(dir, update.AgentFile(runtime.GOOS, runtime.GOARCH))
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho "+version.AgentName+" "+ver+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// publishUpdate puts a signed, gzipped agent binary into the hub's agent directory,
// like the Docker image does.
func publishUpdate(t *testing.T, agentDir, ver string, key ed25519.PrivateKey) string {
	t.Helper()
	bin := fakeAgentBinary(t, t.TempDir(), ver)
	m, err := update.Sign(bin, ver, key)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(bin)
	f, _ := os.Create(filepath.Join(agentDir, m.File+".gz"))
	zw := gzip.NewWriter(f)
	zw.Write(data)
	zw.Close()
	f.Close()
	raw, _ := json.Marshal(m)
	os.WriteFile(filepath.Join(agentDir, m.File+update.ManifestExt), raw, 0o644)
	return string(data)
}

func TestSignedAgentUpdate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake agent binary is a shell script")
	}
	key := trustTestKey(t)
	agentDir := t.TempDir()
	newBinary := publishUpdate(t, agentDir, "99.0.0", key)
	// A manifest signed with an unknown key is ignored.
	_, foreign, _ := ed25519.GenerateKey(rand.Reader)
	publishUpdate(t, t.TempDir(), "98.0.0", foreign)

	h, err := New(Config{DataDir: t.TempDir(), AgentDir: agentDir}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptestServer(t, h)
	token := randomToken()
	h.store.CreateEnrollToken(hashToken(token), time.Now().Add(time.Hour))

	exe := filepath.Join(t.TempDir(), "lotse-agent")
	os.WriteFile(exe, []byte("old binary"), 0o755)
	restarted := make(chan string, 1)
	a, _ := startConfiguredAgent(t, srv.URL, h.PublicKey(), token, false, func(a *agent.Agent) {
		a.Executable = exe
		a.Restart = func(path string) { restarted <- path }
	})
	var sys *store.System
	waitFor(t, "agent online", 10*time.Second, func() bool {
		s, err := h.store.SystemByFingerprint(a.Fingerprint())
		if err == nil && h.online(s.ID) {
			sys = s
			return true
		}
		return false
	})
	if dto := h.toDTO(sys); dto.Update != "99.0.0" {
		t.Fatalf("offered update = %q, want 99.0.0", dto.Update)
	}

	h.mu.Lock()
	ac := h.agents[sys.ID]
	h.mu.Unlock()
	// The agent itself refuses manifests that were changed after signing.
	m := h.updates[update.AgentFile(runtime.GOOS, runtime.GOARCH)]
	forged := *m
	forged.Version = "100.0.0"
	raw, _ := json.Marshal(forged)
	if _, _, err := ac.conn.OpenChannel(protocol.ChanUpdate, raw); err == nil || !strings.Contains(err.Error(), "not signed") {
		t.Errorf("forged manifest: %v", err)
	}

	operator, _ := sessionFor(t, h, "otto", store.RoleOperator, true)
	url := fmt.Sprintf("%s/api/systems/%d/update", srv.URL, sys.ID)
	if status, _ := call(t, "POST", url, operator, nil); status != http.StatusForbidden {
		t.Errorf("operator started an update: %d", status)
	}
	admin, _ := sessionFor(t, h, "ada", store.RoleAdmin, false)
	if status, body := call(t, "POST", url, admin, nil); status != http.StatusOK || body["version"] != "99.0.0" {
		t.Fatalf("update = %d %v", status, body)
	}
	select {
	case path := <-restarted:
		if path != exe {
			t.Errorf("restarted %s, want %s", path, exe)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the agent did not restart after updating")
	}
	if data, _ := os.ReadFile(exe); string(data) != newBinary {
		t.Errorf("binary after update = %q", data)
	}
}

func TestAgentRefusesDowngrades(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake agent binary is a shell script")
	}
	key := trustTestKey(t)
	old := version.Version
	version.Version = "5.0.0"
	t.Cleanup(func() { version.Version = old }) // runs after the agent has stopped

	h, srv := newTestHub(t)
	exe := filepath.Join(t.TempDir(), "lotse-agent")
	os.WriteFile(exe, []byte("current"), 0o755)
	token := randomToken()
	h.store.CreateEnrollToken(hashToken(token), time.Now().Add(time.Hour))
	a, _ := startConfiguredAgent(t, srv.URL, h.PublicKey(), token, false, func(a *agent.Agent) { a.Executable = exe })
	var ac *agentConn
	waitFor(t, "agent online", 10*time.Second, func() bool {
		s, err := h.store.SystemByFingerprint(a.Fingerprint())
		if err != nil {
			return false
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		ac = h.agents[s.ID]
		return ac != nil
	})

	m, err := update.Sign(fakeAgentBinary(t, t.TempDir(), "4.9.0"), "4.9.0", key)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(m)
	if _, _, err := ac.conn.OpenChannel(protocol.ChanUpdate, raw); err == nil || !strings.Contains(err.Error(), "not newer") {
		t.Errorf("downgrade: %v", err)
	}
	if data, _ := os.ReadFile(exe); string(data) != "current" {
		t.Error("the binary was replaced")
	}
}

func TestPowerAndServicesNeedTheAgentsOptIn(t *testing.T) {
	h, srv := newTestHub(t)
	locked := enroll(t, h, srv, false)
	open := enroll(t, h, srv, true)
	cookie, _ := sessionFor(t, h, "otto", store.RoleOperator, true)

	url := fmt.Sprintf("%s/api/systems/%d", srv.URL, locked.ID)
	if status, _ := call(t, "POST", url+"/power", cookie, map[string]string{"action": "reboot"}); status != http.StatusForbidden {
		t.Errorf("reboot without --allow-shell = %d", status)
	}
	if status, _ := call(t, "POST", url+"/services", cookie, map[string]string{"name": "ssh", "action": "restart"}); status != http.StatusForbidden {
		t.Errorf("service action without --allow-shell = %d", status)
	}
	if status, _ := call(t, "POST", fmt.Sprintf("%s/api/systems/%d/power", srv.URL, open.ID), cookie, map[string]string{"action": "explode"}); status != http.StatusBadRequest {
		t.Errorf("unknown power action = %d", status)
	}

	// Even a hub that skips its own checks cannot reboot or control services.
	h.mu.Lock()
	ac := h.agents[locked.ID]
	h.mu.Unlock()
	if msg := sendAction(ac, protocol.ReqPower, protocol.PowerMsg{Action: "reboot"}, 5*time.Second); !strings.Contains(msg, "--allow-shell") {
		t.Errorf("agent without --allow-shell answered a reboot with %q", msg)
	}
	if msg := sendAction(ac, protocol.ReqService, protocol.ServiceMsg{Name: "ssh", Action: "stop"}, 5*time.Second); !strings.Contains(msg, "--allow-shell") {
		t.Errorf("agent without --allow-shell answered a service action with %q", msg)
	}

	// Listing works for everyone; without a service manager the agent says so.
	viewer, _ := sessionFor(t, h, "vera", store.RoleViewer, false)
	status, raw := authedGet(t, url+"/services", viewer)
	var list protocol.ServiceList
	if status != http.StatusOK || json.Unmarshal(raw, &list) != nil || (list.Error == "" && len(list.Services) == 0) {
		t.Errorf("services = %d %s", status, raw)
	}
}
