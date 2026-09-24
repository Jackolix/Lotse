package hub

import (
	"encoding/json"
	"slices"
	"sync"
	"time"

	"github.com/Jackolix/Lotse/internal/hub/store"
	"github.com/Jackolix/Lotse/internal/protocol"
)

// sysState is the in-memory view of one system: its latest sample, a ring of recent
// samples for the live chart, and the 1-minute bucket being averaged.
type sysState struct {
	mu       sync.Mutex
	latest   *protocol.Metrics // never mutated after being stored, safe to share
	latestAt int64
	ring     []livePoint

	bucketTS int64
	sum      [store.NumMetricCols]float64
	n        int
}

type livePoint struct {
	T int64
	M *protocol.Metrics
}

// metricValues flattens a sample in store.MetricCols order.
func metricValues(m *protocol.Metrics) [store.NumMetricCols]float64 {
	return [store.NumMetricCols]float64{
		m.CPU, float64(m.MemUsed), float64(m.MemTotal), float64(m.SwapUsed), float64(m.SwapTotal),
		float64(m.DiskUsed), float64(m.DiskTotal), m.DiskRead, m.DiskWrite, m.NetRx, m.NetTx,
		m.Load1, m.Load5, m.Load15,
	}
}

func (h *Hub) state(id int64) *sysState {
	h.mu.Lock()
	defer h.mu.Unlock()
	st := h.states[id]
	if st == nil {
		st = &sysState{}
		h.states[id] = st
	}
	return st
}

type metricsEvent struct {
	ID int64             `json:"id"`
	T  int64             `json:"t"`
	M  *protocol.Metrics `json:"m"`
}

// record handles one sample from an agent. Samples are timestamped with the hub's
// clock so skewed agent clocks cannot misplace data.
func (h *Hub) record(id int64, m *protocol.Metrics) {
	now := time.Now().Unix()
	minute := now / 60 * 60
	st := h.state(id)

	st.mu.Lock()
	var done *store.MetricRow
	if st.n > 0 && st.bucketTS != minute {
		row := st.takeBucket()
		done = &row
	}
	prevLatest, prevAt := st.latest, st.latestAt
	st.bucketTS = minute
	v := metricValues(m)
	for i := range v {
		st.sum[i] += v[i]
	}
	st.n++
	st.latest, st.latestAt = m, now
	st.ring = append(st.ring, livePoint{now, m})
	if len(st.ring) > liveRingSize {
		st.ring = slices.Delete(st.ring, 0, len(st.ring)-liveRingSize)
	}
	st.mu.Unlock()

	if done != nil {
		h.writeBucket(id, *done, prevLatest, prevAt)
	}
	h.broker.publish("metrics", metricsEvent{ID: id, T: now, M: m})
}

func (st *sysState) takeBucket() store.MetricRow {
	row := store.MetricRow{TS: st.bucketTS}
	for i := range st.sum {
		row.Values[i] = st.sum[i] / float64(st.n)
	}
	st.sum = [store.NumMetricCols]float64{}
	st.n = 0
	return row
}

func (h *Hub) writeBucket(id int64, row store.MetricRow, latest *protocol.Metrics, seen int64) {
	if err := h.store.InsertMetrics(id, store.Res1, row); err != nil {
		h.log.Error("storing metrics failed", "system", id, "err", err)
	}
	if latest == nil {
		return
	}
	lm, _ := json.Marshal(latest)
	if err := h.store.UpdateLastSeen(id, seen, string(lm)); err != nil {
		h.log.Error("updating last seen failed", "system", id, "err", err)
	}
}

// flush writes finished 1-minute buckets; force also writes the current partial one.
func (h *Hub) flush(force bool) {
	h.mu.Lock()
	states := make(map[int64]*sysState, len(h.states))
	for id, st := range h.states {
		states[id] = st
	}
	h.mu.Unlock()
	for id, st := range states {
		h.flushState(id, st, force)
	}
}

func (h *Hub) flushState(id int64, st *sysState, force bool) {
	minute := time.Now().Unix() / 60 * 60
	st.mu.Lock()
	if st.n == 0 || (!force && st.bucketTS >= minute) {
		st.mu.Unlock()
		return
	}
	row := st.takeBucket()
	latest, seen := st.latest, st.latestAt
	st.mu.Unlock()
	h.writeBucket(id, row, latest, seen)
}

// liveSeries returns the recent samples kept in memory for the "Live" range.
func (h *Hub) liveSeries(id int64) []store.MetricRow {
	h.mu.Lock()
	st := h.states[id]
	h.mu.Unlock()
	if st == nil {
		return nil
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	rows := make([]store.MetricRow, len(st.ring))
	for i, p := range st.ring {
		rows[i] = store.MetricRow{TS: p.T, Values: metricValues(p.M)}
	}
	return rows
}
