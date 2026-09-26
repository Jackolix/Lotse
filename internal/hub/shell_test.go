package hub

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// openTerminal connects to the shell endpoint like the browser does.
func openTerminal(t *testing.T, ctx context.Context, srvURL string, systemID int64, cookie string) *websocket.Conn {
	t.Helper()
	url := strings.Replace(srvURL, "http", "ws", 1) + fmt.Sprintf("/api/systems/%d/shell?cols=100&rows=30", systemID)
	ws, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Cookie": {"session=" + cookie}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws.CloseNow() })
	return ws
}

// readControl returns the next JSON control message, collecting terminal output on the way.
func readControl(t *testing.T, ctx context.Context, ws *websocket.Conn, output *strings.Builder) termMsg {
	t.Helper()
	for {
		typ, data, err := ws.Read(ctx)
		if err != nil {
			t.Fatalf("reading terminal: %v (output so far: %q)", err, output.String())
		}
		if typ == websocket.MessageBinary {
			output.Write(data)
			continue
		}
		var m termMsg
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
}

func TestShellRoundTrip(t *testing.T) {
	h, srv := newTestHub(t)
	sys := enroll(t, h, srv, true)
	cookie, _ := newSession(t, h, true)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	ws := openTerminal(t, ctx, srv.URL, sys.ID, cookie)
	var out strings.Builder
	if m := readControl(t, ctx, ws, &out); m.Type != "ready" {
		t.Fatalf("first message = %+v, want ready", m)
	}
	// The arithmetic makes the expected text differ from the echoed input.
	ws.Write(ctx, websocket.MessageText, []byte(`{"type":"resize","cols":120,"rows":40}`))
	ws.Write(ctx, websocket.MessageBinary, []byte("echo lotse-$((40+2)) $(tput cols 2>/dev/null || stty size); exit 3\n"))

	m := readControl(t, ctx, ws, &out)
	if m.Type != "exit" {
		t.Fatalf("got %+v, want exit (output %q)", m, out.String())
	}
	if !strings.Contains(out.String(), "lotse-42") {
		t.Errorf("shell output missing command result: %q", out.String())
	}
	if !strings.Contains(out.String(), "120") {
		t.Errorf("resize did not reach the terminal: %q", out.String())
	}
	if m.Status == nil || *m.Status != 3 {
		t.Errorf("exit status = %v, want 3", m.Status)
	}
	ws.Close(websocket.StatusNormalClosure, "")

	waitFor(t, "audit entries", 5*time.Second, func() bool {
		entries, _ := h.store.Audit(0, 20)
		var opened, closed bool
		for _, e := range entries {
			opened = opened || e.Action == "shell_opened"
			closed = closed || (e.Action == "shell_closed" && strings.Contains(e.Detail, "exit status 3"))
		}
		return opened && closed
	})
}

func TestShellRequiresReauthentication(t *testing.T) {
	h, srv := newTestHub(t)
	sys := enroll(t, h, srv, true)
	cookie, _ := newSession(t, h, false)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var out strings.Builder
	m := readControl(t, ctx, openTerminal(t, ctx, srv.URL, sys.ID, cookie), &out)
	if m.Type != "error" || m.Code != "reauth_required" {
		t.Fatalf("got %+v, want reauth_required", m)
	}
}

func TestShellMustBeAllowedByTheAgent(t *testing.T) {
	h, srv := newTestHub(t)
	sys := enroll(t, h, srv, false)
	cookie, _ := newSession(t, h, true)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var out strings.Builder
	m := readControl(t, ctx, openTerminal(t, ctx, srv.URL, sys.ID, cookie), &out)
	if m.Type != "error" || m.Code != "disabled" {
		t.Fatalf("got %+v, want disabled", m)
	}

	// Even a hub that ignores the advertised features cannot get a shell: the agent refuses the channel.
	h.mu.Lock()
	ac := h.agents[sys.ID]
	h.mu.Unlock()
	if _, _, err := ac.conn.OpenChannel("session", nil); err == nil {
		t.Fatal("agent without --allow-shell accepted a session channel")
	}
}

func TestShellRejectsCrossOriginBrowsers(t *testing.T) {
	h, srv := newTestHub(t)
	sys := enroll(t, h, srv, true)
	cookie, _ := newSession(t, h, true)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	url := strings.Replace(srv.URL, "http", "ws", 1) + fmt.Sprintf("/api/systems/%d/shell", sys.ID)
	_, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{
		"Cookie": {"session=" + cookie},
		"Origin": {"http://evil.example"},
	}})
	if err == nil {
		t.Fatal("cross-origin WebSocket was accepted")
	}
	if resp != nil && resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

// openEvents opens the browser's event stream and waits for its first line.
func openEvents(t *testing.T, ctx context.Context, srvURL, cookie string) *http.Response {
	t.Helper()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srvURL+"/api/events", nil)
	req.Header.Set("Cookie", "session="+cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event stream = %d", resp.StatusCode)
	}
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil || !strings.HasPrefix(line, ": connected") {
		t.Fatalf("event stream started with %q, %v", line, err)
	}
	return resp
}

// waitClosed fails unless the stream ends before ctx does.
func waitClosed(t *testing.T, ctx context.Context, what string, read func() error) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		for read() == nil {
		}
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatalf("%s stayed open", what)
	}
}

// Changing the password signs out other sessions, and whatever they had open with
// it: a stolen session's root shell must not outlive the password change.
func TestPasswordChangeEndsOtherSessionsConnections(t *testing.T) {
	h, srv := newTestHub(t)
	sys := enroll(t, h, srv, true)
	stolen, _ := newSession(t, h, true)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	ws := openTerminal(t, ctx, srv.URL, sys.ID, stolen)
	var out strings.Builder
	if m := readControl(t, ctx, ws, &out); m.Type != "ready" {
		t.Fatalf("first message = %+v, want ready", m)
	}
	events := openEvents(t, ctx, srv.URL, stolen)

	mine, _ := newSession(t, h, true)
	status, body := call(t, "POST", srv.URL+"/api/me/password", mine, map[string]string{"current": testPassword, "new": "a brand new password"})
	if status != http.StatusNoContent {
		t.Fatalf("password change = %d %v", status, body)
	}
	waitClosed(t, ctx, "the other session's terminal", func() error { _, _, err := ws.Read(ctx); return err })
	waitClosed(t, ctx, "the other session's event stream", func() error { _, err := events.Body.Read(make([]byte, 512)); return err })

	// The session that made the change keeps working.
	if status, _ := call(t, "GET", srv.URL+"/api/me", mine, nil); status != http.StatusOK {
		t.Errorf("own session after password change = %d", status)
	}
}

func TestLogoutEndsEventStream(t *testing.T) {
	h, srv := newTestHub(t)
	cookie, _ := newSession(t, h, false)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	events := openEvents(t, ctx, srv.URL, cookie)
	if status, _ := call(t, "POST", srv.URL+"/api/logout", cookie, nil); status != http.StatusNoContent {
		t.Fatalf("logout = %d", status)
	}
	waitClosed(t, ctx, "the event stream", func() error { _, err := events.Body.Read(make([]byte, 512)); return err })
}
