package hub

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Jackolix/Lotse/internal/hub/store"
	"github.com/Jackolix/Lotse/internal/protocol"
)

// Handler returns the hub's complete HTTP handler.
func (h *Hub) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/setup", h.getSetup)
	mux.HandleFunc("POST /api/setup", h.postSetup)
	mux.HandleFunc("POST /api/login", h.postLogin)
	mux.HandleFunc("POST /api/logout", h.postLogout)
	mux.HandleFunc("GET /api/me", h.requireUser(h.getMe))
	mux.HandleFunc("POST /api/me/password", h.requireUser(h.postPassword))
	mux.HandleFunc("POST /api/me/totp/setup", h.requireUser(h.postTOTPSetup))
	mux.HandleFunc("POST /api/me/totp/enable", h.requireUser(h.postTOTPEnable))
	mux.HandleFunc("POST /api/me/totp/disable", h.requireUser(h.postTOTPDisable))
	mux.HandleFunc("POST /api/elevate", h.requireUser(h.postElevate))
	mux.HandleFunc("GET /api/audit", h.requireUser(h.getAudit))
	mux.HandleFunc("GET /api/events", h.requireUser(h.handleEvents))
	mux.HandleFunc("GET /api/systems", h.requireUser(h.getSystems))
	mux.HandleFunc("GET /api/systems/{id}", h.requireUser(h.getSystem))
	mux.HandleFunc("PATCH /api/systems/{id}", h.requireUser(h.patchSystem))
	mux.HandleFunc("DELETE /api/systems/{id}", h.requireUser(h.deleteSystem))
	mux.HandleFunc("GET /api/systems/{id}/metrics", h.requireUser(h.getMetrics))
	mux.HandleFunc("GET /api/systems/{id}/shell", h.requireUser(h.handleShell))
	mux.HandleFunc("POST /api/systems/{id}/wake", h.requireUser(h.postWake))
	mux.HandleFunc("POST /api/enroll", h.requireUser(h.postEnroll))
	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not found")
	})

	mux.HandleFunc("GET "+protocol.AgentPath, h.handleAgent)
	mux.HandleFunc("GET /install.sh", h.getInstallScript)
	mux.HandleFunc("GET /install.ps1", h.getInstallScript)
	mux.HandleFunc("GET /download/{file}", h.getAgentBinary)
	mux.Handle("/", h.static())
	return securityHeaders(sameOrigin(mux))
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdr := w.Header()
		hdr.Set("X-Content-Type-Options", "nosniff")
		hdr.Set("Referrer-Policy", "same-origin")
		hdr.Set("X-Frame-Options", "DENY")
		hdr.Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; "+
			"connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		next.ServeHTTP(w, r)
	})
}

// sameOrigin refuses state-changing requests from other origins (CSRF defense on
// top of SameSite=Strict cookies).
func sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if origin := r.Header.Get("Origin"); origin != "" {
				if u, err := url.Parse(origin); err != nil || u.Host != r.Host {
					writeError(w, http.StatusForbidden, "cross-origin request refused")
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

// ---- systems ----

type systemDTO struct {
	ID           int64               `json:"id"`
	Name         string              `json:"name"`
	Online       bool                `json:"online"`
	Fingerprint  string              `json:"fingerprint"`
	Info         protocol.SystemInfo `json:"info"`
	Features     []string            `json:"features"` // of the connected agent; empty while offline
	AgentVersion string              `json:"agent_version"`
	LastSeen     int64               `json:"last_seen"`
	CreatedAt    int64               `json:"created_at"`
	Metrics      *protocol.Metrics   `json:"metrics"`
}

func (h *Hub) toDTO(s *store.System) systemDTO {
	d := systemDTO{
		ID: s.ID, Name: s.Name, Fingerprint: s.Fingerprint, AgentVersion: s.AgentVersion,
		LastSeen: s.LastSeen, CreatedAt: s.CreatedAt,
	}
	_ = json.Unmarshal([]byte(s.Info), &d.Info)

	d.Features = []string{}
	h.mu.Lock()
	if ac := h.agents[s.ID]; ac != nil {
		d.Online, d.Features = true, ac.features
	}
	st := h.states[s.ID]
	h.mu.Unlock()
	if st != nil {
		st.mu.Lock()
		if st.latest != nil {
			d.Metrics, d.LastSeen = st.latest, st.latestAt
		}
		st.mu.Unlock()
	}
	if d.Metrics == nil && s.LastMetrics != "" {
		var m protocol.Metrics
		if json.Unmarshal([]byte(s.LastMetrics), &m) == nil {
			d.Metrics = &m
		}
	}
	return d
}

func (h *Hub) getSystems(w http.ResponseWriter, _ *http.Request, _ *store.Session) {
	systems, err := h.store.Systems()
	if err != nil {
		h.internalError(w, err)
		return
	}
	out := make([]systemDTO, len(systems))
	for i, s := range systems {
		out[i] = h.toDTO(s)
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Hub) getSystem(w http.ResponseWriter, r *http.Request, _ *store.Session) {
	sys, ok := h.lookupSystem(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, h.toDTO(sys))
}

func (h *Hub) patchSystem(w http.ResponseWriter, r *http.Request, s *store.Session) {
	sys, ok := h.lookupSystem(w, r)
	if !ok {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" || utf8.RuneCountInString(name) > maxNameLen {
		writeError(w, http.StatusBadRequest, "name must be 1-64 characters")
		return
	}
	if err := h.store.RenameSystem(sys.ID, name); err != nil {
		h.internalError(w, err)
		return
	}
	h.audit(r, s.Username, "system_renamed", sys, fmt.Sprintf("renamed from %q to %q", sys.Name, name))
	sys.Name = name
	h.mu.Lock()
	if ac := h.agents[sys.ID]; ac != nil {
		ac.name = name
	}
	h.mu.Unlock()
	h.broker.publish("systems", nil)
	writeJSON(w, http.StatusOK, h.toDTO(sys))
}

// deleteSystem forgets a system and drops its agent link. The agent cannot rejoin
// without a new enrollment token.
func (h *Hub) deleteSystem(w http.ResponseWriter, r *http.Request, s *store.Session) {
	sys, ok := h.lookupSystem(w, r)
	if !ok {
		return
	}
	h.mu.Lock()
	ac := h.agents[sys.ID]
	delete(h.agents, sys.ID)
	delete(h.states, sys.ID)
	h.mu.Unlock()
	if ac != nil {
		ac.conn.Close()
	}
	if err := h.store.DeleteSystem(sys.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
		h.internalError(w, err)
		return
	}
	h.log.Info("system deleted", "system", sys.Name, "id", sys.ID, "by", s.Username)
	h.audit(r, s.Username, "system_deleted", sys, "")
	h.broker.publish("systems", nil)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Hub) lookupSystem(w http.ResponseWriter, r *http.Request) (*store.System, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "system not found")
		return nil, false
	}
	sys, err := h.store.System(id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "system not found")
		return nil, false
	}
	if err != nil {
		h.internalError(w, err)
		return nil, false
	}
	return sys, true
}

// ---- metrics ----

// Chart ranges: which stored resolution backs each, and how far back it reaches.
var ranges = map[string]struct {
	res  int
	span time.Duration
}{
	"1h":  {store.Res1, time.Hour},
	"24h": {store.Res1, 24 * time.Hour},
	"7d":  {store.Res10, 7 * 24 * time.Hour},
	"30d": {store.Res60, 30 * 24 * time.Hour},
	"1y":  {store.Res60, 365 * 24 * time.Hour},
}

// liveSpan is how much of the in-memory ring the "Live" range shows.
const liveSpan = 5 * 60

// seriesDTO is column-oriented, the shape uPlot consumes directly.
type seriesDTO struct {
	Step   int64                  `json:"step"` // seconds per point; 0 for live samples
	From   int64                  `json:"from"`
	To     int64                  `json:"to"`
	T      []int64                `json:"t"`
	Values map[string][]jsonFloat `json:"values"`
}

// jsonFloat encodes NaN as null, which charts draw as a gap.
type jsonFloat float64

func (f jsonFloat) MarshalJSON() ([]byte, error) {
	v := float64(f)
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return []byte("null"), nil
	}
	return strconv.AppendFloat(nil, math.Round(v*100)/100, 'f', -1, 64), nil
}

func (h *Hub) getMetrics(w http.ResponseWriter, r *http.Request, _ *store.Session) {
	sys, ok := h.lookupSystem(w, r)
	if !ok {
		return
	}
	now := time.Now().Unix()
	name := r.URL.Query().Get("range")
	if name == "live" {
		var rows []store.MetricRow
		for _, row := range h.liveSeries(sys.ID) {
			if row.TS >= now-liveSpan {
				rows = append(rows, row)
			}
		}
		writeJSON(w, http.StatusOK, buildSeries(rows, 0, now-liveSpan, now))
		return
	}
	rg, ok := ranges[name]
	if !ok {
		writeError(w, http.StatusBadRequest, "range must be one of live, 1h, 24h, 7d, 30d, 1y")
		return
	}
	from := now - int64(rg.span/time.Second)
	rows, err := h.store.QueryMetrics(sys.ID, rg.res, from, now)
	if err != nil {
		h.internalError(w, err)
		return
	}
	// The minute in progress is only stored once it ends; show it anyway so the
	// line reaches the right edge.
	if row, ok := h.currentBucket(sys.ID); ok && rg.res == store.Res1 && (len(rows) == 0 || row.TS > rows[len(rows)-1].TS) {
		rows = append(rows, row)
	}
	writeJSON(w, http.StatusOK, buildSeries(rows, int64(rg.res)*60, from, now))
}

func buildSeries(rows []store.MetricRow, step, from, to int64) seriesDTO {
	s := seriesDTO{Step: step, From: from, To: to, T: make([]int64, 0, len(rows)), Values: map[string][]jsonFloat{}}
	cols := make([][]jsonFloat, store.NumMetricCols)
	for i := range cols {
		cols[i] = make([]jsonFloat, 0, len(rows))
	}
	for i, row := range rows {
		// More than two missing buckets means the agent was offline: add a null point
		// so the chart breaks the line instead of bridging the gap.
		if step > 0 && i > 0 && row.TS-rows[i-1].TS > 2*step {
			s.T = append(s.T, rows[i-1].TS+step)
			for c := range cols {
				cols[c] = append(cols[c], jsonFloat(math.NaN()))
			}
		}
		s.T = append(s.T, row.TS)
		for c, v := range row.Values {
			cols[c] = append(cols[c], jsonFloat(v))
		}
	}
	for c, name := range store.MetricCols {
		s.Values[name] = cols[c]
	}
	return s
}

// ---- enrollment ----

type enrollDTO struct {
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expires_at"`
	HubURL    string `json:"hub_url"` // suggestion; the UI lets the user change it
	HubKey    string `json:"hub_key"`
}

// postEnroll creates a token that enrolls any number of agents until it expires.
func (h *Hub) postEnroll(w http.ResponseWriter, r *http.Request, s *store.Session) {
	token := randomToken()
	expires := time.Now().Add(enrollTTL)
	if err := h.store.CreateEnrollToken(hashToken(token), expires); err != nil {
		h.internalError(w, err)
		return
	}
	h.log.Info("enrollment token created", "by", s.Username, "expires", expires.Format(time.RFC3339))
	h.audit(r, s.Username, "token_created", nil, "valid until "+expires.Format(time.DateTime))
	writeJSON(w, http.StatusOK, enrollDTO{Token: token, ExpiresAt: expires.Unix(), HubURL: h.publicURL(r), HubKey: h.PublicKey()})
}

func (h *Hub) publicURL(r *http.Request) string {
	if h.cfg.PublicURL != "" {
		return h.cfg.PublicURL
	}
	scheme := "http"
	if isHTTPS(r) {
		scheme = "https"
	}
	host := r.Host
	if fh := r.Header.Get("X-Forwarded-Host"); fh != "" {
		host = fh
	}
	return scheme + "://" + host
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

func (h *Hub) internalError(w http.ResponseWriter, err error) {
	h.log.Error("request failed", "err", err)
	writeError(w, http.StatusInternalServerError, "internal error")
}
