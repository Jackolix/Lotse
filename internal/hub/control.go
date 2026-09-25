package hub

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/Jackolix/Lotse/internal/hub/store"
	"github.com/Jackolix/Lotse/internal/protocol"
)

// Reboot, shutdown and services. Like stopping processes, these need the agent's
// opt-in (--allow-shell), the operator role and a recent re-authentication.

// serviceTimeout is longer than the agent's own limit, so a slow service manager
// does not make the hub drop the agent link (protocol.Send closes it on timeout).
const serviceTimeout = 30 * time.Second

// sendAction sends a request answered with an ActionReply and returns the agent's
// error message, if any.
func sendAction(ac *agentConn, name string, msg any, timeout time.Duration) string {
	ok, payload, err := protocol.Send(ac.conn, name, true, msg, timeout)
	var reply protocol.ActionReply
	_ = json.Unmarshal(payload, &reply)
	switch {
	case reply.Error != "":
		return reply.Error
	case err != nil || !ok:
		return "the agent did not answer; it may need an update"
	}
	return ""
}

func (h *Hub) postPower(w http.ResponseWriter, r *http.Request, s *store.Session) {
	sys, ac, ok := h.onlineAgent(w, r)
	if !ok {
		return
	}
	var body protocol.PowerMsg
	if !readJSON(w, r, &body) {
		return
	}
	if body.Action != "reboot" && body.Action != "shutdown" {
		writeError(w, http.StatusBadRequest, "action must be reboot or shutdown")
		return
	}
	if !requireElevated(w, s) {
		return
	}
	if !ac.has(protocol.FeatureShell) {
		writeError(w, http.StatusForbidden, "rebooting is disabled on this machine; install the agent with --allow-shell")
		return
	}
	if msg := sendAction(ac, protocol.ReqPower, body, 10*time.Second); msg != "" {
		h.audit(r, s.Username, body.Action+"_failed", sys, msg)
		writeError(w, http.StatusUnprocessableEntity, msg)
		return
	}
	h.audit(r, s.Username, body.Action, sys, "")
	w.WriteHeader(http.StatusNoContent)
}

func (h *Hub) getServices(w http.ResponseWriter, r *http.Request, _ *store.Session) {
	_, ac, ok := h.onlineAgent(w, r)
	if !ok {
		return
	}
	ok, payload, err := protocol.Send(ac.conn, protocol.ReqServices, true, struct{}{}, serviceTimeout)
	if err != nil || !ok {
		writeError(w, http.StatusBadGateway, "the agent could not list its services; it may need an update")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(payload)
}

func (h *Hub) postService(w http.ResponseWriter, r *http.Request, s *store.Session) {
	sys, ac, ok := h.onlineAgent(w, r)
	if !ok {
		return
	}
	var body protocol.ServiceMsg
	if !readJSON(w, r, &body) {
		return
	}
	switch body.Action {
	case "start", "stop", "restart":
	default:
		writeError(w, http.StatusBadRequest, "action must be start, stop or restart")
		return
	}
	if body.Name == "" || len(body.Name) > 256 {
		writeError(w, http.StatusBadRequest, "invalid service name")
		return
	}
	if !requireElevated(w, s) {
		return
	}
	if !ac.has(protocol.FeatureShell) {
		writeError(w, http.StatusForbidden, "controlling services is disabled on this machine; install the agent with --allow-shell")
		return
	}
	detail := fmt.Sprintf("%s %s", body.Action, truncate(body.Name, 128))
	if msg := sendAction(ac, protocol.ReqService, body, serviceTimeout); msg != "" {
		h.audit(r, s.Username, "service_control_failed", sys, detail+": "+msg)
		writeError(w, http.StatusUnprocessableEntity, msg)
		return
	}
	h.audit(r, s.Username, "service_controlled", sys, detail)
	w.WriteHeader(http.StatusNoContent)
}
