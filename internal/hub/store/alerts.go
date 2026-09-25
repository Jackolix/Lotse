package store

import (
	"database/sql"
	"encoding/json"
	"time"
)

// Notifier is a notification channel (ntfy, Discord, email, ...). Config is the
// type-specific JSON, including secrets.
type Notifier struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Config    string `json:"-"`
	Enabled   bool   `json:"enabled"`
	LastSent  int64  `json:"last_sent"`
	LastError string `json:"last_error"`
	CreatedAt int64  `json:"created_at"`
}

// AlertRule fires when Metric stays above Threshold (or a system stays offline)
// for Duration seconds. SystemID nil means every system.
type AlertRule struct {
	ID        int64   `json:"id"`
	Name      string  `json:"name"`
	SystemID  *int64  `json:"system_id"`
	Metric    string  `json:"metric"`
	Threshold float64 `json:"threshold"`
	Duration  int64   `json:"duration"`
	Notifiers []int64 `json:"notifiers"`
	Enabled   bool    `json:"enabled"`
	CreatedAt int64   `json:"created_at"`
}

// Alert is one firing of a rule for one system; ResolvedAt is nil while active.
type Alert struct {
	ID         int64   `json:"id"`
	RuleID     int64   `json:"rule_id"`
	RuleName   string  `json:"rule_name"`
	SystemID   int64   `json:"system_id"`
	SystemName string  `json:"system_name"`
	Metric     string  `json:"metric"`
	Threshold  float64 `json:"threshold"`
	Value      float64 `json:"value"`
	StartedAt  int64   `json:"started_at"`
	ResolvedAt *int64  `json:"resolved_at"`
}

// AlertRetention is how long resolved alerts stay in the history.
const AlertRetention = 90 * 24 * time.Hour

// ---- notifiers ----

const notifierCols = "id, name, type, config, enabled, last_sent, last_error, created_at"

func scanNotifier(row interface{ Scan(...any) error }) (*Notifier, error) {
	n := &Notifier{}
	err := row.Scan(&n.ID, &n.Name, &n.Type, &n.Config, &n.Enabled, &n.LastSent, &n.LastError, &n.CreatedAt)
	return n, err
}

func (s *Store) Notifiers() ([]*Notifier, error) {
	rows, err := s.db.Query("SELECT " + notifierCols + " FROM notifiers ORDER BY name COLLATE NOCASE, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Notifier{}
	for rows.Next() {
		n, err := scanNotifier(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Store) Notifier(id int64) (*Notifier, error) {
	n, err := scanNotifier(s.db.QueryRow("SELECT "+notifierCols+" FROM notifiers WHERE id = ?", id))
	return n, notFound(err)
}

func (s *Store) SaveNotifier(n *Notifier) error {
	if n.ID == 0 {
		n.CreatedAt = time.Now().Unix()
		res, err := s.db.Exec("INSERT INTO notifiers (name, type, config, enabled, created_at) VALUES (?, ?, ?, ?, ?)",
			n.Name, n.Type, n.Config, n.Enabled, n.CreatedAt)
		if err != nil {
			return err
		}
		n.ID, _ = res.LastInsertId()
		return nil
	}
	res, err := s.db.Exec("UPDATE notifiers SET name = ?, type = ?, config = ?, enabled = ? WHERE id = ?",
		n.Name, n.Type, n.Config, n.Enabled, n.ID)
	return affected(res, err)
}

func (s *Store) DeleteNotifier(id int64) error {
	res, err := s.db.Exec("DELETE FROM notifiers WHERE id = ?", id)
	return affected(res, err)
}

// NotifierResult records the outcome of the latest delivery attempt.
func (s *Store) NotifierResult(id int64, sentAt int64, errMsg string) error {
	_, err := s.db.Exec("UPDATE notifiers SET last_sent = ?, last_error = ? WHERE id = ?", sentAt, errMsg, id)
	return err
}

// ---- rules ----

const ruleCols = "id, name, system_id, metric, threshold, duration, notifiers, enabled, created_at"

func scanRule(row interface{ Scan(...any) error }) (*AlertRule, error) {
	r := &AlertRule{}
	var sysID sql.NullInt64
	var notifiers string
	if err := row.Scan(&r.ID, &r.Name, &sysID, &r.Metric, &r.Threshold, &r.Duration, &notifiers, &r.Enabled, &r.CreatedAt); err != nil {
		return nil, err
	}
	if sysID.Valid {
		r.SystemID = &sysID.Int64
	}
	r.Notifiers = []int64{}
	_ = json.Unmarshal([]byte(notifiers), &r.Notifiers)
	return r, nil
}

func (s *Store) AlertRules() ([]*AlertRule, error) {
	rows, err := s.db.Query("SELECT " + ruleCols + " FROM alert_rules ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*AlertRule{}
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) AlertRule(id int64) (*AlertRule, error) {
	r, err := scanRule(s.db.QueryRow("SELECT "+ruleCols+" FROM alert_rules WHERE id = ?", id))
	return r, notFound(err)
}

func (s *Store) SaveAlertRule(r *AlertRule) error {
	notifiers, _ := json.Marshal(r.Notifiers)
	if r.ID == 0 {
		r.CreatedAt = time.Now().Unix()
		res, err := s.db.Exec(`INSERT INTO alert_rules (name, system_id, metric, threshold, duration, notifiers, enabled, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, r.Name, r.SystemID, r.Metric, r.Threshold, r.Duration, string(notifiers), r.Enabled, r.CreatedAt)
		if err != nil {
			return err
		}
		r.ID, _ = res.LastInsertId()
		return nil
	}
	res, err := s.db.Exec(`UPDATE alert_rules SET name = ?, system_id = ?, metric = ?, threshold = ?, duration = ?,
		notifiers = ?, enabled = ? WHERE id = ?`, r.Name, r.SystemID, r.Metric, r.Threshold, r.Duration, string(notifiers), r.Enabled, r.ID)
	return affected(res, err)
}

func (s *Store) DeleteAlertRule(id int64) error {
	res, err := s.db.Exec("DELETE FROM alert_rules WHERE id = ?", id)
	return affected(res, err)
}

// ---- alerts ----

const alertCols = "id, rule_id, rule_name, system_id, system_name, metric, threshold, value, started_at, resolved_at"

func (s *Store) queryAlerts(where string, args ...any) ([]*Alert, error) {
	rows, err := s.db.Query("SELECT "+alertCols+" FROM alerts "+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Alert{}
	for rows.Next() {
		a := &Alert{}
		if err := rows.Scan(&a.ID, &a.RuleID, &a.RuleName, &a.SystemID, &a.SystemName, &a.Metric, &a.Threshold,
			&a.Value, &a.StartedAt, &a.ResolvedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) ActiveAlerts() ([]*Alert, error) {
	return s.queryAlerts("WHERE resolved_at IS NULL ORDER BY started_at DESC")
}

func (s *Store) ResolvedAlerts(limit int) ([]*Alert, error) {
	return s.queryAlerts("WHERE resolved_at IS NOT NULL ORDER BY resolved_at DESC LIMIT ?", limit)
}

func (s *Store) CreateAlert(a *Alert) error {
	res, err := s.db.Exec(`INSERT INTO alerts (rule_id, rule_name, system_id, system_name, metric, threshold, value, started_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, a.RuleID, a.RuleName, a.SystemID, a.SystemName, a.Metric, a.Threshold, a.Value, a.StartedAt)
	if err != nil {
		return err
	}
	a.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) ResolveAlert(id, at int64) error {
	_, err := s.db.Exec("UPDATE alerts SET resolved_at = ? WHERE id = ? AND resolved_at IS NULL", at, id)
	return err
}

// ResolveAlertsWhere closes every active alert of a rule or of a system (pass 0 for the other).
func (s *Store) ResolveAlertsWhere(ruleID, systemID, at int64) error {
	_, err := s.db.Exec(`UPDATE alerts SET resolved_at = ? WHERE resolved_at IS NULL
		AND ((? != 0 AND rule_id = ?) OR (? != 0 AND system_id = ?))`, at, ruleID, ruleID, systemID, systemID)
	return err
}

func (s *Store) PruneAlerts(now time.Time) error {
	_, err := s.db.Exec("DELETE FROM alerts WHERE resolved_at IS NOT NULL AND resolved_at < ?", now.Add(-AlertRetention).Unix())
	return err
}
