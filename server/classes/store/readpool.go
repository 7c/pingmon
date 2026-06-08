package store

import (
	"fmt"
	"sync/atomic"
	"time"
)

// nowFunc is the clock used for read-latency timing; overridable in tests.
var nowFunc = time.Now

// maxReadConns bounds the read-only connection pool. WAL allows many concurrent
// readers alongside the single writer, so a small pool keeps heavy analytics
// queries from serializing behind the writer without exhausting SQLite.
const maxReadConns = 4

// readDSN builds the read-only connection string for the read pool. mode=ro
// opens the existing file read-only; query_only is a second guard against
// accidental writes; busy_timeout matches the writer so brief lock contention
// retries rather than failing.
func readDSN(path string) string {
	return fmt.Sprintf(
		"file:%s?_pragma=busy_timeout(5000)&_pragma=query_only(1)&_pragma=foreign_keys(1)&mode=ro",
		path,
	)
}

// readMetrics accumulates read-query latency so operators can spot the database
// becoming a bottleneck before it escalates into stalls. All fields are updated
// with atomics so measurement never takes a lock on the read path.
type readMetrics struct {
	count   atomic.Int64
	totalNs atomic.Int64
	maxNs   atomic.Int64
	lastNs  atomic.Int64
}

// record folds one read's duration into the running totals.
func (m *readMetrics) record(d time.Duration) {
	ns := d.Nanoseconds()
	m.count.Add(1)
	m.totalNs.Add(ns)
	m.lastNs.Store(ns)
	for {
		cur := m.maxNs.Load()
		if ns <= cur || m.maxNs.CompareAndSwap(cur, ns) {
			break
		}
	}
}

// ReadMetrics is a point-in-time snapshot of read-query latency statistics.
type ReadMetrics struct {
	Count   int64   `json:"count"`
	TotalMs float64 `json:"totalMs"`
	AvgMs   float64 `json:"avgMs"`
	MaxMs   float64 `json:"maxMs"`
	LastMs  float64 `json:"lastMs"`
}

func (m *readMetrics) snapshot() ReadMetrics {
	count := m.count.Load()
	total := m.totalNs.Load()
	out := ReadMetrics{
		Count:   count,
		TotalMs: nsToMs(total),
		MaxMs:   nsToMs(m.maxNs.Load()),
		LastMs:  nsToMs(m.lastNs.Load()),
	}
	if count > 0 {
		out.AvgMs = nsToMs(total / count)
	}
	return out
}

func nsToMs(ns int64) float64 { return float64(ns) / 1e6 }

// ReadMetrics returns a snapshot of read-query latency statistics for this store.
func (s *Store) ReadMetrics() ReadMetrics { return s.reads.snapshot() }

// measure times a read operation. Use as a deferred call at the top of a read
// method so the recorded duration spans the full query, including row
// iteration: `defer s.measure()()`.
func (s *Store) measure() func() {
	start := nowFunc()
	return func() { s.reads.record(nowFunc().Sub(start)) }
}
