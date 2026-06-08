package main

import (
	"runtime"
	"sync"
	"time"

	"github.com/7c/pingmon/classes/store"
)

// memSampleInterval is how often the tracker refreshes the running max/current
// memory figures in the background. A snapshot read also refreshes on demand, so
// this only bounds how stale the max is between requests.
const memSampleInterval = 5 * time.Second

// memMetricNames lists the runtime.MemStats fields tracked, in display order.
// Keeping them named lets the stats client render whatever the server reports
// without hardcoding the set on both sides.
var memMetricNames = []string{
	"alloc",      // bytes of allocated heap objects (live)
	"heapInuse",  // bytes in in-use heap spans
	"heapSys",    // heap memory obtained from the OS
	"stackInuse", // bytes in stack spans
	"sys",        // total memory obtained from the OS
}

// memValues extracts the tracked fields from a MemStats sample.
func memValues(m *runtime.MemStats) map[string]uint64 {
	return map[string]uint64{
		"alloc":      m.Alloc,
		"heapInuse":  m.HeapInuse,
		"heapSys":    m.HeapSys,
		"stackInuse": m.StackInuse,
		"sys":        m.Sys,
	}
}

// MemMetric tracks one memory figure across the process lifetime.
type MemMetric struct {
	First   uint64 `json:"first"`   // value at startup
	Max     uint64 `json:"max"`     // peak observed
	Current uint64 `json:"current"` // most recent sample
}

// RuntimeStats is the live runtime snapshot exposed at /api/runtime and shown by
// `pingmon stats` when the server is reachable.
type RuntimeStats struct {
	StartedAt  time.Time            `json:"startedAt"`
	UptimeSec  float64              `json:"uptimeSec"`
	Goroutines int                  `json:"goroutines"`
	NumGC      uint32               `json:"numGC"`
	Mem        map[string]MemMetric `json:"mem"`
	MemOrder   []string             `json:"memOrder"`
	Reads      store.ReadMetrics    `json:"reads"`
}

// runtimeMetrics is the process-wide memory tracker, initialized lazily so both
// the running server and tests share a single instance.
var (
	runtimeMetrics *memTracker
	runtimeOnce    sync.Once
)

// getRuntimeMetrics returns the shared memory tracker, starting it on first use.
func getRuntimeMetrics() *memTracker {
	runtimeOnce.Do(func() { runtimeMetrics = newMemTracker() })
	return runtimeMetrics
}

// memTracker maintains first/max/current for each tracked memory figure plus
// process uptime. It is safe for concurrent use.
type memTracker struct {
	mu        sync.Mutex
	startedAt time.Time
	metrics   map[string]*MemMetric
	done      chan struct{}
	closeOnce sync.Once
	now       func() time.Time // injectable clock for tests
}

// newMemTracker captures the initial sample (first == current == max) and starts
// the background sampler.
func newMemTracker() *memTracker {
	t := &memTracker{
		startedAt: time.Now(),
		metrics:   make(map[string]*MemMetric),
		done:      make(chan struct{}),
		now:       time.Now,
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	for name, v := range memValues(&ms) {
		t.metrics[name] = &MemMetric{First: v, Max: v, Current: v}
	}
	go t.loop()
	return t
}

// loop refreshes the metrics on a fixed interval until Stop.
func (t *memTracker) loop() {
	ticker := time.NewTicker(memSampleInterval)
	defer ticker.Stop()
	for {
		select {
		case <-t.done:
			return
		case <-ticker.C:
			t.sample()
		}
	}
}

// sample reads MemStats once and folds the values into first/max/current.
func (t *memTracker) sample() {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	t.mu.Lock()
	defer t.mu.Unlock()
	for name, v := range memValues(&ms) {
		m := t.metrics[name]
		if m == nil {
			m = &MemMetric{First: v}
			t.metrics[name] = m
		}
		m.Current = v
		if v > m.Max {
			m.Max = v
		}
	}
}

// snapshot refreshes on demand and returns the current runtime stats, folding in
// the store's read-latency metrics.
func (t *memTracker) snapshot(reads store.ReadMetrics) RuntimeStats {
	t.sample() // ensure "current" is fresh and max accounts for this instant
	t.mu.Lock()
	defer t.mu.Unlock()

	mem := make(map[string]MemMetric, len(t.metrics))
	for name, m := range t.metrics {
		mem[name] = *m
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return RuntimeStats{
		StartedAt:  t.startedAt,
		UptimeSec:  t.now().Sub(t.startedAt).Seconds(),
		Goroutines: runtime.NumGoroutine(),
		NumGC:      ms.NumGC,
		Mem:        mem,
		MemOrder:   memMetricNames,
		Reads:      reads,
	}
}

// Stop ends the background sampler. Safe to call more than once.
func (t *memTracker) Stop() {
	t.closeOnce.Do(func() { close(t.done) })
}
