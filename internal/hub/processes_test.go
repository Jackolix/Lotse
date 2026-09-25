package hub

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Jackolix/Lotse/internal/protocol"
)

func authedGet(t *testing.T, url, cookie string) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Cookie", "session="+cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, body
}

func authedPost(t *testing.T, url, cookie, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Cookie", "session="+cookie)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestProcessList(t *testing.T) {
	h, srv := newTestHub(t)
	withShell := enroll(t, h, srv, true)
	withoutShell := enroll(t, h, srv, false)
	cookie, _ := newSession(t, h, false)

	for _, tc := range []struct {
		id           int64
		wantCommands bool
	}{{withShell.ID, true}, {withoutShell.ID, false}} {
		status, body := authedGet(t, fmt.Sprintf("%s/api/systems/%d/processes?limit=500", srv.URL, tc.id), cookie)
		if status != http.StatusOK {
			t.Fatalf("processes = %d %s", status, body)
		}
		var list protocol.ProcessList
		json.Unmarshal(body, &list)
		if len(list.Processes) == 0 || len(list.Processes) > 100 || list.Total < len(list.Processes) {
			t.Fatalf("got %d processes of %d", len(list.Processes), list.Total)
		}
		hasCommand := false
		for _, p := range list.Processes {
			hasCommand = hasCommand || p.Command != ""
		}
		if hasCommand != tc.wantCommands {
			t.Errorf("system %d: command lines present = %v, want %v", tc.id, hasCommand, tc.wantCommands)
		}
	}
}

func TestSignalProcess(t *testing.T) {
	h, srv := newTestHub(t)
	sys := enroll(t, h, srv, true)
	locked := enroll(t, h, srv, false)

	sleeper := exec.Command("sleep", "60")
	if err := sleeper.Start(); err != nil {
		t.Skip("no sleep binary:", err)
	}
	exited := make(chan struct{})
	go func() { sleeper.Wait(); close(exited) }()
	defer sleeper.Process.Kill()
	url := func(id int64) string {
		return fmt.Sprintf("%s/api/systems/%d/processes/%d/signal", srv.URL, id, sleeper.Process.Pid)
	}

	plain, _ := newSession(t, h, false)
	if status, body := authedPost(t, url(sys.ID), plain, `{"signal":"terminate","name":"sleep"}`); status != http.StatusForbidden || body["reauth_required"] != true {
		t.Fatalf("without re-authentication = %d %v", status, body)
	}
	elevated, _ := newSession(t, h, true)
	if status, _ := authedPost(t, url(locked.ID), elevated, `{"signal":"terminate"}`); status != http.StatusForbidden {
		t.Fatalf("agent without --allow-shell = %d", status)
	}
	if status, body := authedPost(t, url(sys.ID), elevated, `{"signal":"terminate","name":"sleep"}`); status != http.StatusNoContent {
		t.Fatalf("terminate = %d %v", status, body)
	}
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("process was not terminated")
	}
	entries, _ := h.store.Audit(0, 10)
	if entries[0].Action != "process_signaled" || !strings.Contains(entries[0].Detail, "terminate") {
		t.Errorf("audit entry: %+v", entries[0])
	}
}
