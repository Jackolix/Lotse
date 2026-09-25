package store

import (
	"database/sql"
	"time"
)

// Script is a saved script that can run on many systems at once.
type Script struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Shell       string `json:"shell"` // sh, bash, powershell, cmd
	Content     string `json:"content"`
	Timeout     int    `json:"timeout"` // seconds
	CreatedBy   string `json:"created_by"`
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
}

// ScriptRun is one execution of a script (or one-off command) on a set of systems.
// Targets holds the JSON results per system; FinishedAt is nil while it runs.
type ScriptRun struct {
	ID         int64  `json:"id"`
	ScriptID   *int64 `json:"script_id"`
	Name       string `json:"name"`
	Shell      string `json:"shell"`
	Content    string `json:"content"`
	Timeout    int    `json:"timeout"`
	Username   string `json:"username"`
	StartedAt  int64  `json:"started_at"`
	FinishedAt *int64 `json:"finished_at"`
	Targets    string `json:"-"`
}

// RunRetention is how long script runs and their output are kept.
const RunRetention = 90 * 24 * time.Hour

const scriptCols = "id, name, description, shell, content, timeout, created_by, created_at, updated_at"

func scanScript(row interface{ Scan(...any) error }) (*Script, error) {
	sc := &Script{}
	err := row.Scan(&sc.ID, &sc.Name, &sc.Description, &sc.Shell, &sc.Content, &sc.Timeout, &sc.CreatedBy,
		&sc.CreatedAt, &sc.UpdatedAt)
	return sc, notFound(err)
}

func (s *Store) Scripts() ([]*Script, error) {
	rows, err := s.db.Query("SELECT " + scriptCols + " FROM scripts ORDER BY name COLLATE NOCASE, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Script{}
	for rows.Next() {
		sc, err := scanScript(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

func (s *Store) Script(id int64) (*Script, error) {
	return scanScript(s.db.QueryRow("SELECT "+scriptCols+" FROM scripts WHERE id = ?", id))
}

// SaveScript inserts a script (ID 0) or updates it.
func (s *Store) SaveScript(sc *Script) error {
	now := time.Now().Unix()
	sc.UpdatedAt = now
	if sc.ID == 0 {
		sc.CreatedAt = now
		res, err := s.db.Exec(`INSERT INTO scripts (name, description, shell, content, timeout, created_by, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, sc.Name, sc.Description, sc.Shell, sc.Content, sc.Timeout, sc.CreatedBy, now, now)
		if err != nil {
			return err
		}
		sc.ID, _ = res.LastInsertId()
		return nil
	}
	res, err := s.db.Exec("UPDATE scripts SET name = ?, description = ?, shell = ?, content = ?, timeout = ?, updated_at = ? WHERE id = ?",
		sc.Name, sc.Description, sc.Shell, sc.Content, sc.Timeout, now, sc.ID)
	return affected(res, err)
}

func (s *Store) DeleteScript(id int64) error {
	res, err := s.db.Exec("DELETE FROM scripts WHERE id = ?", id)
	return affected(res, err)
}

// ---- runs ----

const runCols = "id, script_id, name, shell, content, timeout, username, started_at, finished_at, targets"

func scanRun(row interface{ Scan(...any) error }) (*ScriptRun, error) {
	r := &ScriptRun{}
	var finished sql.NullInt64
	err := row.Scan(&r.ID, &r.ScriptID, &r.Name, &r.Shell, &r.Content, &r.Timeout, &r.Username, &r.StartedAt, &finished, &r.Targets)
	if finished.Valid {
		r.FinishedAt = &finished.Int64
	}
	return r, notFound(err)
}

func (s *Store) CreateRun(r *ScriptRun) error {
	res, err := s.db.Exec(`INSERT INTO script_runs (script_id, name, shell, content, timeout, username, started_at, targets)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, r.ScriptID, r.Name, r.Shell, r.Content, r.Timeout, r.Username, r.StartedAt, r.Targets)
	if err != nil {
		return err
	}
	r.ID, _ = res.LastInsertId()
	return nil
}

// FinishRun stores the final results of a run.
func (s *Store) FinishRun(id, finishedAt int64, targets string) error {
	_, err := s.db.Exec("UPDATE script_runs SET finished_at = ?, targets = ? WHERE id = ?", finishedAt, targets, id)
	return err
}

func (s *Store) Run(id int64) (*ScriptRun, error) {
	return scanRun(s.db.QueryRow("SELECT "+runCols+" FROM script_runs WHERE id = ?", id))
}

// Runs returns the newest runs, newest first.
func (s *Store) Runs(limit int) ([]*ScriptRun, error) {
	rows, err := s.db.Query("SELECT "+runCols+" FROM script_runs ORDER BY id DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*ScriptRun{}
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// UnfinishedRuns returns runs that were still going when the hub stopped.
func (s *Store) UnfinishedRuns() ([]*ScriptRun, error) {
	rows, err := s.db.Query("SELECT " + runCols + " FROM script_runs WHERE finished_at IS NULL")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ScriptRun
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) PruneRuns(now time.Time) error {
	_, err := s.db.Exec("DELETE FROM script_runs WHERE started_at < ?", now.Add(-RunRetention).Unix())
	return err
}
