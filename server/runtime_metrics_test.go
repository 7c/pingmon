package main

import (
	"runtime"
	"testing"

	"github.com/7c/pingmon/classes/store"
)

// memSink keeps a large allocation reachable so the runtime cannot free it
// before we observe the resulting heap growth.
var memSink []byte

func TestMemTrackerFirstMaxCurrent(t *testing.T) {
	mt := newMemTracker()
	defer mt.Stop()

	rs := mt.snapshot(store.ReadMetrics{})
	if len(rs.Mem) == 0 {
		t.Fatal("expected tracked memory metrics, got none")
	}
	for _, name := range memMetricNames {
		m, ok := rs.Mem[name]
		if !ok {
			t.Fatalf("missing tracked metric %q", name)
		}
		// By construction max is seeded from first and only ever grows.
		if m.Max < m.First {
			t.Errorf("%s: max %d < first %d", name, m.Max, m.First)
		}
		if m.Max < m.Current {
			t.Errorf("%s: max %d < current %d", name, m.Max, m.Current)
		}
		if m.Current == 0 {
			t.Errorf("%s: current is zero", name)
		}
	}
	if rs.Goroutines <= 0 {
		t.Errorf("goroutines = %d, want > 0", rs.Goroutines)
	}
	if rs.UptimeSec < 0 {
		t.Errorf("uptime = %v, want >= 0", rs.UptimeSec)
	}
}

func TestMemTrackerCurrentGrowsWithAllocation(t *testing.T) {
	mt := newMemTracker()
	defer mt.Stop()

	before := mt.snapshot(store.ReadMetrics{}).Mem["alloc"]

	memSink = make([]byte, 96<<20) // 96 MiB kept live
	for i := range memSink {       // touch pages so the heap actually grows
		memSink[i] = byte(i)
	}
	runtime.KeepAlive(memSink)

	after := mt.snapshot(store.ReadMetrics{}).Mem["alloc"]
	if after.Current <= before.Current {
		t.Fatalf("current alloc did not grow: before=%d after=%d", before.Current, after.Current)
	}
	if after.Max < after.Current {
		t.Fatalf("max %d should be >= current %d", after.Max, after.Current)
	}
	memSink = nil
}

func TestMemTrackerFoldsReadMetrics(t *testing.T) {
	mt := newMemTracker()
	defer mt.Stop()

	rs := mt.snapshot(store.ReadMetrics{Count: 5, MaxMs: 12.5})
	if rs.Reads.Count != 5 || rs.Reads.MaxMs != 12.5 {
		t.Fatalf("read metrics not folded into snapshot: %+v", rs.Reads)
	}
}

func TestMemTrackerStopIdempotent(t *testing.T) {
	mt := newMemTracker()
	mt.Stop()
	mt.Stop() // must not panic
}
