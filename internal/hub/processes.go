package hub

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/Jackolix/Lotse/internal/hub/store"
	"github.com/Jackolix/Lotse/internal/protocol"
)

// onlineAgent returns the connected agent of the system in the path, or writes an error.
func (h *Hub) onlineAgent(w http.ResponseWriter, r *http.Request) (*store.System, *agentConn, bool) {
	sys, ok := h.lookupSystem(w, r)
	if !ok {
		return nil, nil, false
	}
	h.mu.Lock()
	ac := h.agents[sys.ID]
	h.mu.Unlock()
	if ac == nil {
		writeError(w, http.StatusConflict, "the system is offline")
		return nil, nil, false
	}
	return sys, ac, true
}

func (h *Hub) getProcesses(w http.ResponseWriter, r *http.Request, _ *store.Session) {
	_, ac, ok := h.onlineAgent(w, r)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	ok, payload, err := protocol.Send(ac.conn, protocol.ReqProcesses, true, protocol.ProcessQuery{Limit: min(max(limit, 1), 100)}, 15*time.Second)
	if err != nil || !ok {
		writeError(w, http.StatusBadGateway, "the agent could not list its processes")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(payload)
}

// postSignal stops a process. Like a shell, it needs a recent re-authentication,
// and the agent only obeys if it allows remote control.
func (h *Hub) postSignal(w http.ResponseWriter, r *http.Request, s *store.Session) {
	sys, ac, ok := h.onlineAgent(w, r)
	if !ok {
		return
	}
	pid, err := strconv.ParseInt(r.PathValue("pid"), 10, 32)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid process ID")
		return
	}
	var body struct {
		Signal string `json:"signal"`
		Name   string `json:"name"` // for the audit log only
	}
	if !readJSON(w, r, &body) {
		return
	}
	if body.Signal != "terminate" && body.Signal != "kill" {
		writeError(w, http.StatusBadRequest, "signal must be terminate or kill")
		return
	}
	if s.ElevatedUntil < time.Now().Unix() {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "confirm your password first", "reauth_required": true})
		return
	}
	if !ac.has(protocol.FeatureShell) {
		writeError(w, http.StatusForbidden, "stopping processes is disabled on this machine; install the agent with --allow-shell")
		return
	}
	ok, payload, err := protocol.Send(ac.conn, protocol.ReqSignal, true, protocol.SignalMsg{PID: int32(pid), Signal: body.Signal}, 10*time.Second)
	var reply protocol.SignalReply
	_ = json.Unmarshal(payload, &reply)
	detail := fmt.Sprintf("%s PID %d (%s)", body.Signal, pid, truncate(body.Name, 64))
	if err != nil || !ok {
		msg := reply.Error
		if msg == "" {
			msg = "the agent did not answer"
		}
		h.audit(r, s.Username, "process_signal_failed", sys, detail+": "+msg)
		writeError(w, http.StatusUnprocessableEntity, msg)
		return
	}
	h.audit(r, s.Username, "process_signaled", sys, detail)
	w.WriteHeader(http.StatusNoContent)
}
