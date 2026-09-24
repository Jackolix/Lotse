package hub

import (
	"net/http"
	"strconv"

	"github.com/Jackolix/Lotse/internal/hub/store"
)

// audit records a security-relevant event. r may be nil for events without a
// browser request (e.g. an agent enrolling).
func (h *Hub) audit(r *http.Request, username, action string, sys *store.System, detail string) {
	e := store.AuditEntry{Username: truncate(username, maxNameLen), Action: action, Detail: detail}
	if r != nil {
		e.Remote = clientIP(r)
	}
	if sys != nil {
		id := sys.ID
		e.SystemID, e.SystemName = &id, sys.Name
	}
	if err := h.store.AddAudit(e); err != nil {
		h.log.Error("writing audit log failed", "action", action, "err", err)
	}
}

func (h *Hub) getAudit(w http.ResponseWriter, r *http.Request, _ *store.Session) {
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	// Fetch one extra row to tell the UI whether there is more.
	entries, err := h.store.Audit(before, limit+1)
	if err != nil {
		h.internalError(w, err)
		return
	}
	more := len(entries) > limit
	if more {
		entries = entries[:limit]
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries, "more": more})
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
