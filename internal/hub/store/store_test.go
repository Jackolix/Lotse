package store

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func colIndex(t *testing.T, name string) int {
	for i, c := range MetricCols {
		if c == name {
			return i
		}
	}
	t.Fatalf("no column %s", name)
	return -1
}

func TestRollupAveragesAndKeepsCapacityMax(t *testing.T) {
	s := openTest(t)
	sys, err := s.CreateSystem("web-1", "SHA256:x", "{}", "dev")
	if err != nil {
		t.Fatal(err)
	}
	cpu, memTotal := colIndex(t, "cpu"), colIndex(t, "mem_total")
	for i, ts := 0, int64(3600); i < 20; i, ts = i+1, ts+60 { // 20 one-minute rows from 01:00
		var r MetricRow
		r.TS = ts
		r.Values[cpu] = float64(i) // 0..19
		r.Values[memTotal] = float64(100 + i)
		if err := s.InsertMetrics(sys.ID, Res1, r); err != nil {
			t.Fatal(err)
		}
	}

	// At 01:25 the buckets 01:00 and 01:10 are complete, 01:20 is not.
	if err := s.Rollup(Res10, 2*time.Hour, time.Unix(3600+25*60, 0)); err != nil {
		t.Fatal(err)
	}
	rows, err := s.QueryMetrics(sys.ID, Res10, 0, 1<<40)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d 10-minute rows, want 2", len(rows))
	}
	if rows[0].Values[cpu] != 4.5 || rows[1].Values[cpu] != 14.5 {
		t.Errorf("cpu averages = %v, %v; want 4.5, 14.5", rows[0].Values[cpu], rows[1].Values[cpu])
	}
	if rows[1].Values[memTotal] != 119 {
		t.Errorf("mem_total = %v, want max 119", rows[1].Values[memTotal])
	}

	// Rerunning over the same window must not duplicate or change anything.
	s.Rollup(Res10, 2*time.Hour, time.Unix(3600+25*60, 0))
	again, _ := s.QueryMetrics(sys.ID, Res10, 0, 1<<40)
	if len(again) != 2 {
		t.Fatalf("rollup not idempotent: %d rows", len(again))
	}
}

func TestPruneAndCascade(t *testing.T) {
	s := openTest(t)
	sys, _ := s.CreateSystem("db-1", "SHA256:y", "{}", "dev")
	now := time.Now()
	old := MetricRow{TS: now.Add(-72 * time.Hour).Unix()}
	fresh := MetricRow{TS: now.Unix()}
	s.InsertMetrics(sys.ID, Res1, old)
	s.InsertMetrics(sys.ID, Res1, fresh)
	if err := s.PruneMetrics(now); err != nil {
		t.Fatal(err)
	}
	rows, _ := s.QueryMetrics(sys.ID, Res1, 0, 1<<40)
	if len(rows) != 1 || rows[0].TS != fresh.TS {
		t.Fatalf("prune kept %d rows", len(rows))
	}

	if err := s.DeleteSystem(sys.ID); err != nil {
		t.Fatal(err)
	}
	rows, _ = s.QueryMetrics(sys.ID, Res1, 0, 1<<40)
	if len(rows) != 0 {
		t.Fatal("metrics survived system deletion")
	}
}

func TestRunListsLeaveOutOutput(t *testing.T) {
	s := openTest(t)
	r := &ScriptRun{Name: "x", Shell: "sh", Content: "echo hi", Timeout: 5, Username: "u", StartedAt: 1,
		Targets: `[{"system_id":1,"status":"pending","output":""}]`}
	if err := s.CreateRun(r); err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("x", 100<<10)
	if err := s.FinishRun(r.ID, 2, `[{"system_id":1,"status":"done","output":"`+big+`"},{"system_id":2,"status":"skipped","output":""}]`); err != nil {
		t.Fatal(err)
	}
	full, _ := s.Run(r.ID)
	if !strings.Contains(full.Targets, big) || full.Content != "echo hi" {
		t.Fatal("the run itself lost its output or script")
	}
	list, err := s.Runs(10)
	if err != nil || len(list) != 1 {
		t.Fatalf("Runs = %v, %v", list, err)
	}
	var targets []map[string]any
	if err := json.Unmarshal([]byte(list[0].Targets), &targets); err != nil {
		t.Fatalf("summary is not JSON: %v: %s", err, list[0].Targets)
	}
	if len(targets) != 2 || targets[0]["status"] != "done" || targets[1]["system_id"] != 2.0 {
		t.Errorf("summary = %s", list[0].Targets)
	}
	if strings.Contains(list[0].Targets, "output") || list[0].Content != "" {
		t.Errorf("list carries output or script: %+v", list[0])
	}
}

// Databases from before the summary column get it filled in when they are opened.
func TestRunSummaryMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`ALTER TABLE script_runs DROP COLUMN summary; PRAGMA user_version = 4;
		INSERT INTO script_runs (name, shell, content, timeout, username, started_at, finished_at, targets)
		VALUES ('old', 'sh', 'true', 5, 'u', 1, 2, '[{"system_id":7,"status":"done","output":"lots of output"}]'),
		       ('broken', 'sh', 'true', 5, 'u', 1, 2, 'not json')`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatalf("migrating: %v", err)
	}
	defer s.Close()
	list, err := s.Runs(10)
	if err != nil || len(list) != 2 {
		t.Fatalf("Runs = %v, %v", list, err)
	}
	got := map[string]string{list[0].Name: list[0].Targets, list[1].Name: list[1].Targets}
	if got["old"] != `[{"system_id":7,"status":"done"}]` || got["broken"] != "[]" {
		t.Errorf("summaries after migration: %v", got)
	}
}
