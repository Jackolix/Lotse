// Package store persists hub state in a single SQLite file.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // pure Go driver: keeps the hub a static binary
)

var ErrNotFound = errors.New("not found")

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)")
	if err != nil {
		return nil, err
	}
	// One connection serializes all access. At this scale (tens of agents, one sample
	// per minute each) that is plenty and rules out SQLITE_BUSY entirely.
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

var migrations = []string{`
CREATE TABLE users (
	id            INTEGER PRIMARY KEY,
	username      TEXT NOT NULL UNIQUE COLLATE NOCASE,
	password_hash TEXT NOT NULL,
	created_at    INTEGER NOT NULL
);
CREATE TABLE sessions (
	token_hash BLOB PRIMARY KEY,
	user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	expires_at INTEGER NOT NULL
);
CREATE TABLE enroll_tokens (
	token_hash BLOB PRIMARY KEY,
	created_at INTEGER NOT NULL,
	expires_at INTEGER NOT NULL
);
CREATE TABLE systems (
	id            INTEGER PRIMARY KEY,
	name          TEXT NOT NULL,
	fingerprint   TEXT NOT NULL UNIQUE,
	info          TEXT NOT NULL DEFAULT '{}',
	agent_version TEXT NOT NULL DEFAULT '',
	last_seen     INTEGER NOT NULL DEFAULT 0,
	last_metrics  TEXT,
	created_at    INTEGER NOT NULL
);
CREATE TABLE metrics (
	system_id  INTEGER NOT NULL REFERENCES systems(id) ON DELETE CASCADE,
	res        INTEGER NOT NULL, -- bucket size in minutes: 1, 10 or 60
	ts         INTEGER NOT NULL, -- bucket start, unix seconds
	cpu        REAL NOT NULL DEFAULT 0,
	mem_used   REAL NOT NULL DEFAULT 0,
	mem_total  REAL NOT NULL DEFAULT 0,
	swap_used  REAL NOT NULL DEFAULT 0,
	swap_total REAL NOT NULL DEFAULT 0,
	disk_used  REAL NOT NULL DEFAULT 0,
	disk_total REAL NOT NULL DEFAULT 0,
	disk_read  REAL NOT NULL DEFAULT 0,
	disk_write REAL NOT NULL DEFAULT 0,
	net_rx     REAL NOT NULL DEFAULT 0,
	net_tx     REAL NOT NULL DEFAULT 0,
	load1      REAL NOT NULL DEFAULT 0,
	load5      REAL NOT NULL DEFAULT 0,
	load15     REAL NOT NULL DEFAULT 0,
	PRIMARY KEY (system_id, res, ts)
) WITHOUT ROWID;
`, `
ALTER TABLE users ADD COLUMN totp_secret TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN elevated_until INTEGER NOT NULL DEFAULT 0;
CREATE TABLE audit_log (
	id          INTEGER PRIMARY KEY,
	ts          INTEGER NOT NULL,
	username    TEXT NOT NULL DEFAULT '',
	action      TEXT NOT NULL,
	system_id   INTEGER, -- no foreign key: entries outlive deleted systems
	system_name TEXT NOT NULL DEFAULT '',
	remote      TEXT NOT NULL DEFAULT '',
	detail      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX audit_log_ts ON audit_log (ts);
`, `
CREATE TABLE notifiers (
	id         INTEGER PRIMARY KEY,
	name       TEXT NOT NULL,
	type       TEXT NOT NULL,
	config     TEXT NOT NULL DEFAULT '{}',
	enabled    INTEGER NOT NULL DEFAULT 1,
	last_sent  INTEGER NOT NULL DEFAULT 0,
	last_error TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL
);
CREATE TABLE alert_rules (
	id         INTEGER PRIMARY KEY,
	name       TEXT NOT NULL,
	system_id  INTEGER REFERENCES systems(id) ON DELETE CASCADE, -- NULL: every system
	metric     TEXT NOT NULL,                  -- offline, cpu, memory, disk, load
	threshold  REAL NOT NULL DEFAULT 0,
	duration   INTEGER NOT NULL DEFAULT 300,   -- seconds the condition must hold
	notifiers  TEXT NOT NULL DEFAULT '[]',     -- JSON array of notifier IDs
	enabled    INTEGER NOT NULL DEFAULT 1,
	created_at INTEGER NOT NULL
);
CREATE TABLE alerts (
	id          INTEGER PRIMARY KEY,
	rule_id     INTEGER NOT NULL, -- no foreign keys: the history outlives rules and systems
	rule_name   TEXT NOT NULL,
	system_id   INTEGER NOT NULL,
	system_name TEXT NOT NULL,
	metric      TEXT NOT NULL,
	threshold   REAL NOT NULL,
	value       REAL NOT NULL,
	started_at  INTEGER NOT NULL,
	resolved_at INTEGER
);
CREATE INDEX alerts_resolved ON alerts (resolved_at);
-- Sensible defaults; they show up in the UI and notify once a channel is attached.
INSERT INTO alert_rules (name, metric, threshold, duration, created_at) VALUES
	('System offline', 'offline', 0, 120, unixepoch()),
	('High CPU', 'cpu', 90, 300, unixepoch()),
	('High memory', 'memory', 90, 300, unixepoch()),
	('Disk almost full', 'disk', 90, 60, unixepoch());
`}

func (s *Store) migrate() error {
	var v int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		return err
	}
	for ; v < len(migrations); v++ {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[v]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", v+1, err)
		}
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", v+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// ---- users & sessions ----

type User struct {
	ID           int64
	Username     string
	PasswordHash string
	TOTPSecret   string // base32; empty when two-factor login is off
}

// Session is a logged-in browser. ElevatedUntil marks a recent re-authentication,
// which sensitive actions such as opening a shell require.
type Session struct {
	User
	TokenHash     []byte
	ElevatedUntil int64
}

const userCols = "id, username, password_hash, totp_secret"

func (s *Store) CountUsers() (int, error) {
	var n int
	err := s.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&n)
	return n, err
}

func (s *Store) CreateUser(username, passwordHash string) (*User, error) {
	res, err := s.db.Exec("INSERT INTO users (username, password_hash, created_at) VALUES (?, ?, ?)",
		username, passwordHash, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &User{ID: id, Username: username, PasswordHash: passwordHash}, nil
}

func (s *Store) UserByName(username string) (*User, error) {
	u := &User{}
	err := s.db.QueryRow("SELECT "+userCols+" FROM users WHERE username = ?", username).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &u.TOTPSecret)
	return u, notFound(err)
}

func (s *Store) SetPassword(userID int64, passwordHash string) error {
	_, err := s.db.Exec("UPDATE users SET password_hash = ? WHERE id = ?", passwordHash, userID)
	return err
}

func (s *Store) SetTOTPSecret(userID int64, secret string) error {
	_, err := s.db.Exec("UPDATE users SET totp_secret = ? WHERE id = ?", secret, userID)
	return err
}

func (s *Store) CreateSession(tokenHash []byte, userID int64, expires time.Time) error {
	_, err := s.db.Exec("INSERT INTO sessions (token_hash, user_id, expires_at) VALUES (?, ?, ?)",
		tokenHash, userID, expires.Unix())
	return err
}

// Session returns a valid, unexpired session with its user.
func (s *Store) Session(tokenHash []byte) (*Session, error) {
	sess := &Session{TokenHash: tokenHash}
	err := s.db.QueryRow(`SELECT u.id, u.username, u.password_hash, u.totp_secret, s.elevated_until
		FROM sessions s JOIN users u ON u.id = s.user_id WHERE s.token_hash = ? AND s.expires_at > ?`,
		tokenHash, time.Now().Unix()).Scan(&sess.ID, &sess.Username, &sess.PasswordHash, &sess.TOTPSecret, &sess.ElevatedUntil)
	return sess, notFound(err)
}

func (s *Store) ElevateSession(tokenHash []byte, until time.Time) error {
	_, err := s.db.Exec("UPDATE sessions SET elevated_until = ? WHERE token_hash = ?", until.Unix(), tokenHash)
	return err
}

// DeleteOtherSessions logs a user out everywhere except the given session.
func (s *Store) DeleteOtherSessions(userID int64, keep []byte) error {
	_, err := s.db.Exec("DELETE FROM sessions WHERE user_id = ? AND token_hash != ?", userID, keep)
	return err
}

func (s *Store) DeleteSession(tokenHash []byte) error {
	_, err := s.db.Exec("DELETE FROM sessions WHERE token_hash = ?", tokenHash)
	return err
}

// ---- enrollment tokens ----

func (s *Store) CreateEnrollToken(tokenHash []byte, expires time.Time) error {
	_, err := s.db.Exec("INSERT INTO enroll_tokens (token_hash, created_at, expires_at) VALUES (?, ?, ?)",
		tokenHash, time.Now().Unix(), expires.Unix())
	return err
}

func (s *Store) ValidEnrollToken(tokenHash []byte) (bool, error) {
	var n int
	err := s.db.QueryRow("SELECT COUNT(*) FROM enroll_tokens WHERE token_hash = ? AND expires_at > ?",
		tokenHash, time.Now().Unix()).Scan(&n)
	return n > 0, err
}

// DeleteExpired removes expired sessions and enrollment tokens.
func (s *Store) DeleteExpired() error {
	now := time.Now().Unix()
	if _, err := s.db.Exec("DELETE FROM sessions WHERE expires_at <= ?", now); err != nil {
		return err
	}
	_, err := s.db.Exec("DELETE FROM enroll_tokens WHERE expires_at <= ?", now)
	return err
}

// ---- systems ----

type System struct {
	ID           int64
	Name         string
	Fingerprint  string
	Info         string // JSON protocol.SystemInfo
	AgentVersion string
	LastSeen     int64
	LastMetrics  string // JSON protocol.Metrics, may be empty
	CreatedAt    int64
}

const systemCols = "id, name, fingerprint, info, agent_version, last_seen, COALESCE(last_metrics, ''), created_at"

func scanSystem(row interface{ Scan(...any) error }) (*System, error) {
	s := &System{}
	err := row.Scan(&s.ID, &s.Name, &s.Fingerprint, &s.Info, &s.AgentVersion, &s.LastSeen, &s.LastMetrics, &s.CreatedAt)
	return s, err
}

func (s *Store) CreateSystem(name, fingerprint, info, agentVersion string) (*System, error) {
	now := time.Now().Unix()
	res, err := s.db.Exec(`INSERT INTO systems (name, fingerprint, info, agent_version, last_seen, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`, name, fingerprint, info, agentVersion, now, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.System(id)
}

func (s *Store) System(id int64) (*System, error) {
	sys, err := scanSystem(s.db.QueryRow("SELECT "+systemCols+" FROM systems WHERE id = ?", id))
	return sys, notFound(err)
}

func (s *Store) SystemByFingerprint(fp string) (*System, error) {
	sys, err := scanSystem(s.db.QueryRow("SELECT "+systemCols+" FROM systems WHERE fingerprint = ?", fp))
	return sys, notFound(err)
}

func (s *Store) Systems() ([]*System, error) {
	rows, err := s.db.Query("SELECT " + systemCols + " FROM systems ORDER BY name COLLATE NOCASE, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*System
	for rows.Next() {
		sys, err := scanSystem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sys)
	}
	return out, rows.Err()
}

func (s *Store) UpdateSystemInfo(id int64, info, agentVersion string) error {
	_, err := s.db.Exec("UPDATE systems SET info = ?, agent_version = ? WHERE id = ?", info, agentVersion, id)
	return err
}

func (s *Store) RenameSystem(id int64, name string) error {
	res, err := s.db.Exec("UPDATE systems SET name = ? WHERE id = ?", name, id)
	return affected(res, err)
}

func (s *Store) DeleteSystem(id int64) error {
	res, err := s.db.Exec("DELETE FROM systems WHERE id = ?", id)
	return affected(res, err)
}

func (s *Store) UpdateLastSeen(id, ts int64, lastMetrics string) error {
	_, err := s.db.Exec("UPDATE systems SET last_seen = ?, last_metrics = ? WHERE id = ?", ts, lastMetrics, id)
	return err
}

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func affected(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
