package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"golang.org/x/crypto/ssh"

	"github.com/Jackolix/Lotse/internal/hub/store"
	"github.com/Jackolix/Lotse/internal/protocol"
)

// shellSession is an open browser terminal, tracked so logging out ends it.
type shellSession struct {
	session []byte // token hash of the browser session
	userID  int64
	cancel  context.CancelFunc
}

func (h *Hub) closeShells(sessionHash []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for sh := range h.shells {
		if bytes.Equal(sh.session, sessionHash) {
			sh.cancel()
		}
	}
}

// closeUserShells ends every shell of a user who was deleted, demoted or signed out.
func (h *Hub) closeUserShells(userID int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for sh := range h.shells {
		if sh.userID == userID {
			sh.cancel()
		}
	}
}

// termMsg is the JSON control message on the terminal WebSocket. Terminal data
// itself travels as binary messages in both directions.
type termMsg struct {
	Type    string `json:"type"` // browser: resize; hub: ready, exit, error
	Cols    int    `json:"cols,omitempty"`
	Rows    int    `json:"rows,omitempty"`
	Status  *int   `json:"status,omitempty"`
	Code    string `json:"code,omitempty"` // error: reauth_required, offline, disabled, failed
	Message string `json:"message,omitempty"`
}

// handleShell bridges a browser terminal (xterm.js over WebSocket) to an SSH
// session channel on the agent, which runs the shell in a PTY.
func (h *Hub) handleShell(w http.ResponseWriter, r *http.Request, s *store.Session) {
	sys, ok := h.lookupSystem(w, r)
	if !ok {
		return
	}
	// Browsers cannot read the body of a failed WebSocket handshake, so upgrade first
	// and report problems as messages. Accept refuses cross-origin requests.
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer ws.CloseNow()
	ws.SetReadLimit(1 << 20)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	fail := func(code, msg string) {
		wsjson.Write(ctx, ws, termMsg{Type: "error", Code: code, Message: msg})
		ws.Close(websocket.StatusNormalClosure, code)
	}
	if !s.Can(store.RoleOperator) {
		fail("forbidden", "your role ("+s.Role+") cannot open shells")
		return
	}
	if s.ElevatedUntil < time.Now().Unix() {
		fail("reauth_required", "confirm your password to open a shell")
		return
	}
	h.mu.Lock()
	ac := h.agents[sys.ID]
	h.mu.Unlock()
	if ac == nil {
		fail("offline", "the system is offline")
		return
	}
	if !ac.has(protocol.FeatureShell) {
		h.audit(r, s.Username, "shell_denied", sys, "remote shell is disabled on the agent")
		fail("disabled", "Remote shell is disabled on this machine. Reinstall the agent with --allow-shell to enable it.")
		return
	}

	ch, reqs, err := ac.conn.OpenChannel("session", nil)
	if err != nil {
		var oce *ssh.OpenChannelError
		msg := err.Error()
		if errors.As(err, &oce) {
			msg = oce.Message
		}
		fail("failed", "The agent refused the shell: "+msg)
		return
	}
	defer ch.Close()
	go io.Copy(io.Discard, ch.Stderr()) // only used for startup errors; a PTY merges the rest
	exit := make(chan int, 1)
	go func() {
		for req := range reqs {
			var st protocol.ExitStatus
			if req.Type == "exit-status" && ssh.Unmarshal(req.Payload, &st) == nil {
				select {
				case exit <- int(st.Status):
				default:
				}
			}
			req.Reply(false, nil)
		}
	}()

	pty := protocol.PtyRequest{Term: "xterm-256color", Columns: queryDim(r, "cols", 80), Rows: queryDim(r, "rows", 24)}
	if ok, err := ch.SendRequest("pty-req", true, ssh.Marshal(pty)); err != nil || !ok {
		fail("failed", "The agent could not allocate a terminal.")
		return
	}
	if ok, err := ch.SendRequest("shell", true, nil); err != nil || !ok {
		fail("failed", "The agent could not start a shell; check the agent log.")
		return
	}

	sh := &shellSession{session: s.TokenHash, userID: s.ID, cancel: cancel}
	h.mu.Lock()
	h.shells[sh] = struct{}{}
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.shells, sh)
		h.mu.Unlock()
	}()

	start := time.Now()
	h.audit(r, s.Username, "shell_opened", sys, "")
	h.log.Info("shell opened", "system", sys.Name, "user", s.Username)
	wsjson.Write(ctx, ws, termMsg{Type: "ready"})

	var typed, printed atomic.Int64
	output := make(chan struct{})
	go func() { // agent → browser
		defer close(output)
		buf := make([]byte, 32<<10)
		for {
			n, err := ch.Read(buf)
			if n > 0 {
				printed.Add(int64(n))
				if ws.Write(ctx, websocket.MessageBinary, buf[:n]) != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	go func() { // browser → agent
		defer cancel()
		for {
			typ, data, err := ws.Read(ctx)
			if err != nil {
				return
			}
			if typ == websocket.MessageBinary {
				typed.Add(int64(len(data)))
				if _, err := ch.Write(data); err != nil {
					return
				}
				continue
			}
			var m termMsg
			if json.Unmarshal(data, &m) == nil && m.Type == "resize" {
				ch.SendRequest("window-change", false, ssh.Marshal(protocol.WindowChange{
					Columns: clampDim(m.Cols, 80), Rows: clampDim(m.Rows, 24),
				}))
			}
		}
	}()

	go func() {
		// Proxies drop quiet WebSockets (Cloudflare after about 100 s, nginx after 60 s),
		// which would end a terminal left idle. A missing pong means the browser is gone.
		ping := time.NewTicker(25 * time.Second)
		defer ping.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ping.C:
				pctx, pcancel := context.WithTimeout(ctx, 20*time.Second)
				err := ws.Ping(pctx)
				pcancel()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()

	var status *int
	exited := false
	select {
	case <-output: // the shell exited or the agent went away
		select {
		case st := <-exit:
			status = &st
		case <-time.After(500 * time.Millisecond):
		}
		wsjson.Write(ctx, ws, termMsg{Type: "exit", Status: status})
		exited = true
	case <-ctx.Done(): // browser closed the terminal, logout, or hub shutdown
	}

	detail := fmt.Sprintf("%s; %s typed, %s output", time.Since(start).Round(time.Second),
		humanBytes(typed.Load()), humanBytes(printed.Load()))
	if status != nil {
		detail += fmt.Sprintf("; exit status %d", *status)
	}
	h.audit(r, s.Username, "shell_closed", sys, detail)
	h.log.Info("shell closed", "system", sys.Name, "user", s.Username)
	if exited {
		ws.Close(websocket.StatusNormalClosure, "shell exited")
	}
}

func queryDim(r *http.Request, name string, def int) uint32 {
	v, _ := strconv.Atoi(r.URL.Query().Get(name))
	return clampDim(v, def)
}

func clampDim(v, def int) uint32 {
	if v <= 0 || v > 1000 {
		return uint32(def)
	}
	return uint32(v)
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
