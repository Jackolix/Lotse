package store

import (
	"path/filepath"
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
