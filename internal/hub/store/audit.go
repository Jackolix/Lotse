package store

import "time"

// AuditEntry records who did what, from where. Entries are never edited.
type AuditEntry struct {
	ID         int64  `json:"id"`
	Time       int64  `json:"t"`
	Username   string `json:"username"`
	Action     string `json:"action"`
	SystemID   *int64 `json:"system_id,omitempty"`
	SystemName string `json:"system_name,omitempty"`
	Remote     string `json:"remote,omitempty"`
	Detail     string `json:"detail,omitempty"`
}

// AuditRetention is how long audit entries are kept.
const AuditRetention = 366 * 24 * time.Hour

func (s *Store) AddAudit(e AuditEntry) error {
	if e.Time == 0 {
		e.Time = time.Now().Unix()
	}
	_, err := s.db.Exec(`INSERT INTO audit_log (ts, username, action, system_id, system_name, remote, detail)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, e.Time, e.Username, e.Action, e.SystemID, e.SystemName, e.Remote, e.Detail)
	return err
}

// Audit returns up to limit entries older than beforeID (0 = newest), newest first.
func (s *Store) Audit(beforeID int64, limit int) ([]AuditEntry, error) {
	if beforeID <= 0 {
		beforeID = 1 << 62
	}
	rows, err := s.db.Query(`SELECT id, ts, username, action, system_id, system_name, remote, detail
		FROM audit_log WHERE id < ? ORDER BY id DESC LIMIT ?`, beforeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.Time, &e.Username, &e.Action, &e.SystemID, &e.SystemName, &e.Remote, &e.Detail); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) PruneAudit(now time.Time) error {
	_, err := s.db.Exec("DELETE FROM audit_log WHERE ts < ?", now.Add(-AuditRetention).Unix())
	return err
}
