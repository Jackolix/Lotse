package store

import (
	"strings"
	"time"
)

// MetricCols are the stored metric columns. Names match protocol.Metrics JSON keys.
var MetricCols = [...]string{
	"cpu", "mem_used", "mem_total", "swap_used", "swap_total", "disk_used", "disk_total",
	"disk_read", "disk_write", "net_rx", "net_tx", "load1", "load5", "load15",
}

const NumMetricCols = len(MetricCols)

// Capacity columns roll up with MAX, everything else with AVG.
var maxCols = map[string]bool{"mem_total": true, "swap_total": true, "disk_total": true}

type MetricRow struct {
	TS     int64 // bucket start, unix seconds
	Values [NumMetricCols]float64
}

// Resolutions (bucket size in minutes) and how long each is kept.
const (
	Res1  = 1
	Res10 = 10
	Res60 = 60
)

var Retention = map[int]time.Duration{
	Res1:  48 * time.Hour,
	Res10: 31 * 24 * time.Hour,
	Res60: 366 * 24 * time.Hour,
}

var (
	colList         = strings.Join(MetricCols[:], ", ")
	insertMetricSQL = "INSERT OR REPLACE INTO metrics (system_id, res, ts, " + colList + ") VALUES (?, ?, ?" +
		strings.Repeat(", ?", NumMetricCols) + ")"
	queryMetricSQL = "SELECT ts, " + colList + " FROM metrics WHERE system_id = ? AND res = ? AND ts >= ? AND ts <= ? ORDER BY ts"
	rollupSQL      = func() string {
		aggs := make([]string, NumMetricCols)
		for i, c := range MetricCols {
			fn := "AVG"
			if maxCols[c] {
				fn = "MAX"
			}
			aggs[i] = fn + "(" + c + ")"
		}
		return "INSERT OR REPLACE INTO metrics (system_id, res, ts, " + colList + ") " +
			"SELECT system_id, ?, (ts / ?) * ? AS bucket, " + strings.Join(aggs, ", ") +
			" FROM metrics WHERE res = ? AND ts >= ? AND ts < ? GROUP BY system_id, bucket"
	}()
)

func (s *Store) InsertMetrics(systemID int64, res int, row MetricRow) error {
	args := make([]any, 0, 3+NumMetricCols)
	args = append(args, systemID, res, row.TS)
	for _, v := range row.Values {
		args = append(args, v)
	}
	_, err := s.db.Exec(insertMetricSQL, args...)
	return err
}

// QueryMetrics returns rows with from <= ts <= to, oldest first.
func (s *Store) QueryMetrics(systemID int64, res int, from, to int64) ([]MetricRow, error) {
	rows, err := s.db.Query(queryMetricSQL, systemID, res, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MetricRow
	for rows.Next() {
		var r MetricRow
		dest := make([]any, 0, 1+NumMetricCols)
		dest = append(dest, &r.TS)
		for i := range r.Values {
			dest = append(dest, &r.Values[i])
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Rollup builds toRes buckets from 1-minute rows for every complete bucket in the
// window before now. It is idempotent, so overlapping windows are fine.
func (s *Store) Rollup(toRes int, window time.Duration, now time.Time) error {
	bucket := int64(toRes) * 60
	until := now.Unix() / bucket * bucket
	since := (until - int64(window/time.Second)) / bucket * bucket
	_, err := s.db.Exec(rollupSQL, toRes, bucket, bucket, Res1, since, until)
	return err
}

// PruneMetrics deletes rows older than their resolution's retention.
func (s *Store) PruneMetrics(now time.Time) error {
	for res, keep := range Retention {
		if _, err := s.db.Exec("DELETE FROM metrics WHERE res = ? AND ts < ?", res, now.Add(-keep).Unix()); err != nil {
			return err
		}
	}
	return nil
}
