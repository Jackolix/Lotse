package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/Jackolix/Lotse/internal/agent"
	"github.com/Jackolix/Lotse/internal/hub/store"
	"github.com/Jackolix/Lotse/internal/protocol"
	"github.com/Jackolix/Lotse/internal/sshkey"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func newTestHub(t *testing.T) (*Hub, *httptest.Server) {
	t.Helper()
	h, err := New(Config{DataDir: t.TempDir(), AgentDir: t.TempDir()}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h.Handler())
	t.Cleanup(func() {
		srv.CloseClientConnections()
		srv.Close()
		h.store.Close()
	})
	return h, srv
}

// startAgent runs a real agent against the test hub and returns its config path.
func startAgent(t *testing.T, hubURL, hubKey, token string) (*agent.Agent, string) {
	return startAgentWith(t, hubURL, hubKey, token, false)
}

func startAgentWith(t *testing.T, hubURL, hubKey, token string, allowShell bool) (*agent.Agent, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agent.json")
	cfg, err := agent.NewConfig(path, hubURL, hubKey, token, allowShell)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	a, err := agent.New(cfg, quiet)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		a.Run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return a, path
}

func waitFor(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// enroll starts an agent with a fresh token and waits until it is online.
func enroll(t *testing.T, h *Hub, srv *httptest.Server, allowShell bool) *store.System {
	t.Helper()
	token := randomToken()
	if err := h.store.CreateEnrollToken(hashToken(token), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	a, _ := startAgentWith(t, srv.URL, h.PublicKey(), token, allowShell)
	var sys *store.System
	waitFor(t, "agent online", 10*time.Second, func() bool {
		s, err := h.store.SystemByFingerprint(a.Fingerprint())
		if err == nil && h.online(s.ID) {
			sys = s
			return true
		}
		return false
	})
	return sys
}

const testPassword = "correct horse battery"

// newSession creates a user (once) and a logged-in session, returning its cookie value.
func newSession(t *testing.T, h *Hub, elevated bool) (string, *store.User) {
	t.Helper()
	u, err := h.store.UserByName("admin")
	if err != nil {
		hash, _ := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)
		if u, err = h.store.CreateUser("admin", string(hash)); err != nil {
			t.Fatal(err)
		}
	}
	token := randomToken()
	if err := h.store.CreateSession(hashToken(token), u.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if elevated {
		h.store.ElevateSession(hashToken(token), time.Now().Add(time.Minute))
	}
	return token, u
}

func (h *Hub) online(id int64) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.agents[id] != nil
}

func TestAgentEnrollsAndReports(t *testing.T) {
	h, srv := newTestHub(t)
	if err := h.store.CreateEnrollToken(hashToken("secret-token"), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	a, cfgPath := startAgent(t, srv.URL, h.PublicKey(), "secret-token")

	var sys *store.System
	waitFor(t, "enrollment", 10*time.Second, func() bool {
		systems, _ := h.store.Systems()
		if len(systems) == 1 {
			sys = systems[0]
		}
		return sys != nil && h.online(sys.ID)
	})
	if sys.Fingerprint != a.Fingerprint() {
		t.Errorf("fingerprint = %s, want %s", sys.Fingerprint, a.Fingerprint())
	}
	var info protocol.SystemInfo
	if err := json.Unmarshal([]byte(sys.Info), &info); err != nil || info.OS == "" || info.Cores == 0 {
		t.Errorf("system info not recorded: %s", sys.Info)
	}
	waitFor(t, "first sample", 5*time.Second, func() bool { return h.toDTO(sys).Metrics != nil })

	cfg, err := agent.LoadConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Token != "" {
		t.Error("agent kept its enrollment token after enrolling")
	}

	// Opening the UI (an event subscriber) switches agents to live reporting.
	events, unsubscribe := h.broker.subscribe()
	defer unsubscribe()
	samples := 0
	timeout := time.After(8 * time.Second)
	for samples < 3 {
		select {
		case msg := <-events:
			if bytes.HasPrefix(msg, []byte("event: metrics")) {
				samples++
			}
		case <-timeout:
			t.Fatalf("got %d live samples, want 3", samples)
		}
	}

	// Deleting the system drops the link, and the agent cannot silently re-enroll.
	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	req.SetPathValue("id", strconv.FormatInt(sys.ID, 10))
	h.deleteSystem(httptest.NewRecorder(), req, &store.Session{User: store.User{Username: "test"}})
	time.Sleep(2 * time.Second)
	if systems, _ := h.store.Systems(); len(systems) != 0 {
		t.Errorf("deleted system came back: %d systems", len(systems))
	}
}

func TestHubRejectsUnknownAgents(t *testing.T) {
	h, srv := newTestHub(t)
	h.store.CreateEnrollToken(hashToken("valid"), time.Now().Add(time.Hour))

	startAgent(t, srv.URL, h.PublicKey(), "")      // no token
	startAgent(t, srv.URL, h.PublicKey(), "wrong") // bad token
	other, _ := sshkey.LoadOrCreate(filepath.Join(t.TempDir(), "k"))
	startAgent(t, srv.URL, sshkey.AuthorizedKey(other.PublicKey()), "valid") // pins a different hub

	time.Sleep(3 * time.Second)
	if systems, _ := h.store.Systems(); len(systems) != 0 {
		t.Fatalf("unauthorized agents enrolled: %d systems", len(systems))
	}
}

func TestExpiredTokenIsRejected(t *testing.T) {
	h, srv := newTestHub(t)
	h.store.CreateEnrollToken(hashToken("old"), time.Now().Add(-time.Minute))
	startAgent(t, srv.URL, h.PublicKey(), "old")
	time.Sleep(2 * time.Second)
	if systems, _ := h.store.Systems(); len(systems) != 0 {
		t.Fatal("expired token enrolled an agent")
	}
}

func TestSetupLoginAndAuth(t *testing.T) {
	_, srv := newTestHub(t)
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	post := func(path, body string) *http.Response {
		resp, err := c.Post(srv.URL+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}
	get := func(path string) int {
		resp, err := c.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	if code := get("/api/systems"); code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated /api/systems = %d", code)
	}
	if resp := post("/api/setup", `{"username":"admin","password":"short"}`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("weak password accepted: %d", resp.StatusCode)
	}
	if resp := post("/api/setup", `{"username":"admin","password":"correct horse battery"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("setup = %d", resp.StatusCode)
	}
	if code := get("/api/systems"); code != http.StatusOK {
		t.Fatalf("after setup /api/systems = %d", code)
	}
	if resp := post("/api/setup", `{"username":"evil","password":"correct horse battery"}`); resp.StatusCode != http.StatusConflict {
		t.Fatalf("second setup = %d, want 409", resp.StatusCode)
	}
	post("/api/logout", "")
	if code := get("/api/systems"); code != http.StatusUnauthorized {
		t.Fatalf("after logout /api/systems = %d", code)
	}
	if resp := post("/api/login", `{"username":"admin","password":"wrong password"}`); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong password = %d", resp.StatusCode)
	}
	if resp := post("/api/login", `{"username":"ADMIN","password":"correct horse battery"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("login = %d", resp.StatusCode)
	}

	// Cross-origin writes are refused even with a valid session.
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/enroll", nil)
	req.Header.Set("Origin", "http://evil.example")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin POST = %d, want 403", resp.StatusCode)
	}
}

func TestLoginLockout(t *testing.T) {
	l := newLoginLimiter()
	for range loginFailures {
		if !l.allow("1.2.3.4") {
			t.Fatal("locked out too early")
		}
		l.fail("1.2.3.4")
	}
	if l.allow("1.2.3.4") {
		t.Fatal("not locked out after repeated failures")
	}
	if !l.allow("5.6.7.8") {
		t.Fatal("other IPs must not be affected")
	}
}

// metricValues must follow store.MetricCols, whose names are the Metrics JSON keys.
func TestMetricValuesOrder(t *testing.T) {
	m := protocol.Metrics{CPU: 1, MemUsed: 2, MemTotal: 3, SwapUsed: 4, SwapTotal: 5, DiskUsed: 6, DiskTotal: 7,
		DiskRead: 8, DiskWrite: 9, NetRx: 10, NetTx: 11, Load1: 12, Load5: 13, Load15: 14}
	raw, _ := json.Marshal(m)
	var byKey map[string]float64
	json.Unmarshal(raw, &byKey)
	values := metricValues(&m)
	for i, col := range store.MetricCols {
		if byKey[col] != values[i] {
			t.Errorf("column %s: metricValues has %v, JSON has %v", col, values[i], byKey[col])
		}
	}
}

func TestBuildSeriesMarksGaps(t *testing.T) {
	rows := []store.MetricRow{{TS: 0}, {TS: 60}, {TS: 600}}
	s := buildSeries(rows, 60, 0, 600)
	if len(s.T) != 4 || s.T[2] != 120 {
		t.Fatalf("expected a gap marker at 120, got %v", s.T)
	}
	out, _ := json.Marshal(s.Values["cpu"])
	if string(out) != "[0,0,null,0]" {
		t.Fatalf("cpu = %s", out)
	}
}
