package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Jackolix/Lotse/internal/hub/store"
	"github.com/Jackolix/Lotse/internal/protocol"
)

// alertMetrics are the conditions a rule can watch.
var alertMetrics = map[string]struct {
	label   string
	percent bool
}{
	"offline": {"Offline", false},
	"cpu":     {"CPU", true},
	"memory":  {"Memory", true},
	"disk":    {"Disk", true},
	"load":    {"Load", false},
}

const (
	// A metric alert resolves only after this long back below the threshold, so a
	// value hovering around it does not produce a stream of notifications.
	alertClearDelay = 60 * time.Second
	alertHistory    = 50
)

type alertKey struct{ rule, system int64 }

type alertState struct {
	since      time.Time    // the condition has held since
	clearSince time.Time    // back to normal since (while firing)
	alert      *store.Alert // set while firing
}

type alertEngine struct {
	mu      sync.Mutex // also serializes the alert writes to the database
	rules   []*store.AlertRule
	states  map[alertKey]*alertState
	offline map[int64]time.Time // system → when it went offline
	started time.Time
}

type alertEvent struct {
	rule   *store.AlertRule
	alert  *store.Alert
	firing bool
	value  float64
}

func appliesTo(r *store.AlertRule, systemID int64) bool {
	return r.SystemID == nil || *r.SystemID == systemID
}

func metricValue(metric string, m *protocol.Metrics) (float64, bool) {
	pct := func(used, total uint64) (float64, bool) {
		if total == 0 {
			return 0, false
		}
		return float64(used) / float64(total) * 100, true
	}
	switch metric {
	case "cpu":
		return m.CPU, true
	case "memory":
		return pct(m.MemUsed, m.MemTotal)
	case "disk":
		return pct(m.DiskUsed, m.DiskTotal)
	case "load":
		return m.Load1, true
	}
	return 0, false
}

// loadAlerts reads the rules and restores firing alerts, so a hub restart neither
// forgets them nor notifies twice.
func (h *Hub) loadAlerts() error {
	rules, err := h.store.AlertRules()
	if err != nil {
		return err
	}
	active, err := h.store.ActiveAlerts()
	if err != nil {
		return err
	}
	e := h.alerts
	e.mu.Lock()
	defer e.mu.Unlock()
	e.rules = slices.DeleteFunc(rules, func(r *store.AlertRule) bool { return !r.Enabled })
	for _, a := range active {
		e.states[alertKey{a.RuleID, a.SystemID}] = &alertState{since: time.Unix(a.StartedAt, 0), alert: a}
	}
	return nil
}

// step advances one rule/system pair. It runs with e.mu held.
func (h *Hub) step(r *store.AlertRule, systemID int64, holds bool, value float64, since, now time.Time, clearDelay time.Duration) *alertEvent {
	e := h.alerts
	key := alertKey{r.ID, systemID}
	st := e.states[key]
	if st == nil {
		st = &alertState{}
		e.states[key] = st
	}
	if holds {
		st.clearSince = time.Time{}
		if st.since.IsZero() {
			st.since = since
		}
		if st.alert != nil || now.Sub(st.since) < time.Duration(r.Duration)*time.Second {
			return nil
		}
		name := ""
		if sys, err := h.store.System(systemID); err == nil {
			name = sys.Name
		}
		a := &store.Alert{RuleID: r.ID, RuleName: r.Name, SystemID: systemID, SystemName: name, Metric: r.Metric,
			Threshold: r.Threshold, Value: value, StartedAt: st.since.Unix()}
		if err := h.store.CreateAlert(a); err != nil {
			h.log.Error("storing alert failed", "err", err)
			return nil
		}
		st.alert = a
		return &alertEvent{rule: r, alert: a, firing: true, value: value}
	}

	st.since = time.Time{}
	if st.alert == nil {
		delete(e.states, key)
		return nil
	}
	if st.clearSince.IsZero() {
		st.clearSince = now
	}
	if now.Sub(st.clearSince) < clearDelay {
		return nil
	}
	a := st.alert
	delete(e.states, key)
	if err := h.store.ResolveAlert(a.ID, now.Unix()); err != nil {
		h.log.Error("resolving alert failed", "err", err)
	}
	return &alertEvent{rule: r, alert: a, firing: false, value: value}
}

// evaluateMetrics checks the threshold rules against a new sample.
func (h *Hub) evaluateMetrics(systemID int64, m *protocol.Metrics, now time.Time) {
	var events []*alertEvent
	e := h.alerts
	e.mu.Lock()
	for _, r := range e.rules {
		if r.Metric == "offline" || !appliesTo(r, systemID) {
			continue
		}
		v, ok := metricValue(r.Metric, m)
		if !ok {
			continue
		}
		if ev := h.step(r, systemID, v > r.Threshold, v, now, now, alertClearDelay); ev != nil {
			events = append(events, ev)
		}
	}
	e.mu.Unlock()
	h.dispatch(events, now)
}

// evaluateOffline runs periodically and whenever an agent connects or disconnects.
func (h *Hub) evaluateOffline(now time.Time) {
	systems, err := h.store.Systems()
	if err != nil {
		return
	}
	h.mu.Lock()
	online := make(map[int64]bool, len(h.agents))
	for id := range h.agents {
		online[id] = true
	}
	h.mu.Unlock()

	var events []*alertEvent
	e := h.alerts
	e.mu.Lock()
	for _, r := range e.rules {
		if r.Metric != "offline" {
			continue
		}
		for _, sys := range systems {
			if !appliesTo(r, sys.ID) {
				continue
			}
			since, ok := e.offline[sys.ID]
			if !ok {
				since = e.started // not seen since the hub started: give it time to reconnect
			}
			if ev := h.step(r, sys.ID, !online[sys.ID], 0, since, now, 0); ev != nil {
				events = append(events, ev)
			}
		}
	}
	e.mu.Unlock()
	h.dispatch(events, now)
}

func (h *Hub) agentOnline(id int64) {
	h.alerts.mu.Lock()
	delete(h.alerts.offline, id)
	h.alerts.mu.Unlock()
	go h.evaluateOffline(time.Now())
}

func (h *Hub) agentOffline(id int64) {
	h.alerts.mu.Lock()
	h.alerts.offline[id] = time.Now()
	h.alerts.mu.Unlock()
}

// resetAlerts closes the alerts of a changed rule or a deleted system without
// notifying, and forgets their state. Pass 0 for the one not meant.
func (h *Hub) resetAlerts(ruleID, systemID int64) {
	e := h.alerts
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := h.store.ResolveAlertsWhere(ruleID, systemID, time.Now().Unix()); err != nil {
		h.log.Error("resolving alerts failed", "err", err)
	}
	for k := range e.states {
		if k.rule == ruleID || k.system == systemID {
			delete(e.states, k)
		}
	}
	if systemID != 0 {
		delete(e.offline, systemID)
	}
	if ruleID != 0 {
		h.reloadRulesLocked()
	}
	h.broker.publish("alerts", nil)
}

// reloadRulesLocked replaces the rule list with fresh copies. Rules are never
// changed in place, since notifications read them without holding the lock.
func (h *Hub) reloadRulesLocked() {
	rules, err := h.store.AlertRules()
	if err != nil {
		h.log.Error("loading alert rules failed", "err", err)
		return
	}
	h.alerts.rules = slices.DeleteFunc(rules, func(r *store.AlertRule) bool { return !r.Enabled })
}

func (h *Hub) dispatch(events []*alertEvent, now time.Time) {
	if len(events) == 0 {
		return
	}
	h.broker.publish("alerts", nil)
	for _, ev := range events {
		title, msg := alertText(ev, now)
		h.log.Info("alert", "firing", ev.firing, "title", title)
		n := notification{Title: title, Message: msg, Firing: ev.firing, URL: h.systemURL(ev.alert.SystemID), Time: now,
			System: ev.alert.SystemName, Metric: ev.alert.Metric, Value: ev.value, Threshold: ev.alert.Threshold}
		for _, id := range ev.rule.Notifiers {
			go h.deliver(id, n)
		}
	}
}

func (h *Hub) systemURL(id int64) string {
	if h.cfg.PublicURL == "" {
		return ""
	}
	return fmt.Sprintf("%s/systems/%d", h.cfg.PublicURL, id)
}

func alertText(ev *alertEvent, now time.Time) (title, msg string) {
	a := ev.alert
	lasted := humanDuration(now.Sub(time.Unix(a.StartedAt, 0)))
	if a.Metric == "offline" {
		if ev.firing {
			return a.SystemName + " is offline", fmt.Sprintf("No contact with %s for %s.", a.SystemName, lasted)
		}
		return a.SystemName + " is back online", fmt.Sprintf("%s was offline for %s.", a.SystemName, lasted)
	}
	label := alertMetrics[a.Metric].label
	format := func(v float64) string {
		if alertMetrics[a.Metric].percent {
			return fmt.Sprintf("%.0f%%", v)
		}
		return strconv.FormatFloat(math.Round(v*100)/100, 'f', -1, 64)
	}
	if ev.firing {
		return fmt.Sprintf("%s: %s at %s", a.SystemName, label, format(ev.value)),
			fmt.Sprintf("%s has been above %s for %s (rule %q).", label, format(a.Threshold), lasted, a.RuleName)
	}
	return fmt.Sprintf("%s: %s back to normal", a.SystemName, label),
		fmt.Sprintf("%s is at %s again after %s above %s.", label, format(ev.value), lasted, format(a.Threshold))
}

func humanDuration(d time.Duration) string {
	d = d.Round(time.Minute)
	switch {
	case d < time.Minute:
		return "less than a minute"
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh %dmin", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%d days", int(d.Hours()/24))
}

// deliver sends one notification through one channel, retrying twice, and
// records the outcome for the UI.
func (h *Hub) deliver(notifierID int64, n notification) {
	nf, err := h.store.Notifier(notifierID)
	if err != nil || !nf.Enabled {
		return
	}
	var cfg notifierConfig
	_ = json.Unmarshal([]byte(nf.Config), &cfg)
	for attempt, wait := range []time.Duration{0, 10 * time.Second, time.Minute} {
		time.Sleep(wait)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err = send(ctx, nf.Type, cfg, n)
		cancel()
		if err == nil {
			break
		}
		h.log.Warn("notification failed", "channel", nf.Name, "attempt", attempt+1, "err", err)
	}
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	if err := h.store.NotifierResult(notifierID, time.Now().Unix(), msg); err != nil {
		h.log.Error("recording notification result failed", "err", err)
	}
	h.broker.publish("alerts", nil)
}

// ---- API ----

func (h *Hub) getAlerts(w http.ResponseWriter, _ *http.Request, _ *store.Session) {
	active, err := h.store.ActiveAlerts()
	if err != nil {
		h.internalError(w, err)
		return
	}
	history, err := h.store.ResolvedAlerts(alertHistory)
	if err != nil {
		h.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"active": active, "history": history})
}

func (h *Hub) getAlertRules(w http.ResponseWriter, _ *http.Request, _ *store.Session) {
	rules, err := h.store.AlertRules()
	if err != nil {
		h.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rules)
}

func (h *Hub) saveAlertRule(w http.ResponseWriter, r *http.Request, s *store.Session) {
	var rule store.AlertRule
	if !readJSON(w, r, &rule) {
		return
	}
	rule.ID = 0
	if idStr := r.PathValue("id"); idStr != "" {
		id, _ := strconv.ParseInt(idStr, 10, 64)
		if _, err := h.store.AlertRule(id); err != nil {
			writeError(w, http.StatusNotFound, "rule not found")
			return
		}
		rule.ID = id
	}
	if msg := h.validateRule(&rule); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if err := h.store.SaveAlertRule(&rule); err != nil {
		h.internalError(w, err)
		return
	}
	h.resetAlerts(rule.ID, 0)
	h.audit(r, s.Username, "alert_rule_saved", nil, fmt.Sprintf("%q: %s", rule.Name, describeRule(&rule)))
	go h.evaluateOffline(time.Now())
	writeJSON(w, http.StatusOK, rule)
}

func (h *Hub) validateRule(r *store.AlertRule) string {
	m, ok := alertMetrics[r.Metric]
	if !ok {
		return "choose what the rule watches"
	}
	r.Name = strings.TrimSpace(r.Name)
	if r.Name == "" {
		r.Name = m.label
	}
	if utf8.RuneCountInString(r.Name) > maxNameLen {
		return "name must be at most 64 characters"
	}
	switch {
	case r.Metric == "offline":
		r.Threshold = 0
		if r.Duration < 30 {
			return "wait at least 30 seconds before reporting a system as offline"
		}
	case m.percent && (r.Threshold <= 0 || r.Threshold >= 100):
		return "the threshold must be between 0 and 100 %"
	case !m.percent && r.Threshold <= 0:
		return "the threshold must be above 0"
	}
	if r.Duration < 0 || r.Duration > 86400 {
		return "the duration must be between 0 seconds and 24 hours"
	}
	if r.SystemID != nil {
		if _, err := h.store.System(*r.SystemID); err != nil {
			return "that system does not exist"
		}
	}
	if r.Notifiers == nil {
		r.Notifiers = []int64{}
	}
	for _, id := range r.Notifiers {
		if _, err := h.store.Notifier(id); err != nil {
			return "a selected notification channel does not exist"
		}
	}
	return ""
}

func describeRule(r *store.AlertRule) string {
	if r.Metric == "offline" {
		return fmt.Sprintf("offline for %s", time.Duration(r.Duration)*time.Second)
	}
	return fmt.Sprintf("%s above %g for %s", r.Metric, r.Threshold, time.Duration(r.Duration)*time.Second)
}

func (h *Hub) deleteAlertRule(w http.ResponseWriter, r *http.Request, s *store.Session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	rule, err := h.store.AlertRule(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "rule not found")
		return
	}
	if err := h.store.DeleteAlertRule(id); err != nil {
		h.internalError(w, err)
		return
	}
	h.resetAlerts(id, 0)
	h.audit(r, s.Username, "alert_rule_deleted", nil, fmt.Sprintf("%q", rule.Name))
	w.WriteHeader(http.StatusNoContent)
}

type notifierDTO struct {
	*store.Notifier
	Config map[string]any `json:"config"`
}

func (h *Hub) getNotifiers(w http.ResponseWriter, _ *http.Request, _ *store.Session) {
	list, err := h.store.Notifiers()
	if err != nil {
		h.internalError(w, err)
		return
	}
	out := make([]notifierDTO, len(list))
	for i, n := range list {
		var cfg notifierConfig
		_ = json.Unmarshal([]byte(n.Config), &cfg)
		out[i] = notifierDTO{n, cfg.masked()}
	}
	writeJSON(w, http.StatusOK, out)
}

type notifierInput struct {
	ID      int64          `json:"id"`
	Name    string         `json:"name"`
	Type    string         `json:"type"`
	Enabled bool           `json:"enabled"`
	Config  notifierConfig `json:"config"`
}

// parseNotifier validates input, keeping stored secrets the browser never received.
func (h *Hub) parseNotifier(in *notifierInput) (*store.Notifier, string) {
	if !notifierTypes[in.Type] {
		return nil, "choose a channel type"
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || utf8.RuneCountInString(in.Name) > maxNameLen {
		return nil, "name must be 1-64 characters"
	}
	if in.ID != 0 {
		stored, err := h.store.Notifier(in.ID)
		if err != nil {
			return nil, "channel not found"
		}
		var old notifierConfig
		_ = json.Unmarshal([]byte(stored.Config), &old)
		in.Config.keepSecrets(old)
	}
	if err := in.Config.validate(in.Type); err != nil {
		return nil, err.Error()
	}
	raw, _ := json.Marshal(in.Config)
	return &store.Notifier{ID: in.ID, Name: in.Name, Type: in.Type, Config: string(raw), Enabled: in.Enabled}, ""
}

func (h *Hub) saveNotifier(w http.ResponseWriter, r *http.Request, s *store.Session) {
	var in notifierInput
	if !readJSON(w, r, &in) {
		return
	}
	in.ID, _ = strconv.ParseInt(r.PathValue("id"), 10, 64)
	n, msg := h.parseNotifier(&in)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if err := h.store.SaveNotifier(n); err != nil {
		h.internalError(w, err)
		return
	}
	h.audit(r, s.Username, "notifier_saved", nil, fmt.Sprintf("%s channel %q", n.Type, n.Name))
	h.broker.publish("alerts", nil)
	writeJSON(w, http.StatusOK, map[string]int64{"id": n.ID})
}

func (h *Hub) deleteNotifier(w http.ResponseWriter, r *http.Request, s *store.Session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	n, err := h.store.Notifier(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}
	// Detach it from rules first.
	rules, err := h.store.AlertRules()
	if err != nil {
		h.internalError(w, err)
		return
	}
	for _, rule := range rules {
		if i := slices.Index(rule.Notifiers, id); i >= 0 {
			rule.Notifiers = slices.Delete(rule.Notifiers, i, i+1)
			if err := h.store.SaveAlertRule(rule); err != nil {
				h.internalError(w, err)
				return
			}
		}
	}
	if err := h.store.DeleteNotifier(id); err != nil {
		h.internalError(w, err)
		return
	}
	h.alerts.mu.Lock()
	h.reloadRulesLocked()
	h.alerts.mu.Unlock()
	h.audit(r, s.Username, "notifier_deleted", nil, fmt.Sprintf("%s channel %q", n.Type, n.Name))
	w.WriteHeader(http.StatusNoContent)
}

// testNotifier sends a test message with the (possibly unsaved) settings.
func (h *Hub) testNotifier(w http.ResponseWriter, r *http.Request, s *store.Session) {
	var in notifierInput
	if !readJSON(w, r, &in) {
		return
	}
	if in.Name == "" {
		in.Name = "test"
	}
	n, msg := h.parseNotifier(&in)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	var cfg notifierConfig
	_ = json.Unmarshal([]byte(n.Config), &cfg)
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	err := send(ctx, n.Type, cfg, notification{
		Title:   "Lotse test notification",
		Message: fmt.Sprintf("%s set up this channel. Alerts will arrive like this.", s.Username),
		URL:     h.cfg.PublicURL,
		Time:    time.Now(),
	})
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
