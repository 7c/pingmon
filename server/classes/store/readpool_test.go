package store

import (
	"sync"
	"testing"
	"time"
)

// fakeClock returns a nowFunc that advances by stepMs on every call, so each
// measured read records exactly one step of latency.
func fakeClock(stepMs int64) func() time.Time {
	var n int64
	base := time.Unix(0, 0)
	return func() time.Time {
		t := base.Add(time.Duration(n*stepMs) * time.Millisecond)
		n++
		return t
	}
}

func TestReadMetricsRecord(t *testing.T) {
	var m readMetrics
	m.record(10 * time.Millisecond)
	m.record(30 * time.Millisecond)
	m.record(20 * time.Millisecond)

	got := m.snapshot()
	if got.Count != 3 {
		t.Fatalf("count = %d, want 3", got.Count)
	}
	if got.MaxMs != 30 {
		t.Fatalf("maxMs = %v, want 30", got.MaxMs)
	}
	if got.LastMs != 20 {
		t.Fatalf("lastMs = %v, want 20", got.LastMs)
	}
	if got.TotalMs != 60 {
		t.Fatalf("totalMs = %v, want 60", got.TotalMs)
	}
	if got.AvgMs != 20 {
		t.Fatalf("avgMs = %v, want 20", got.AvgMs)
	}
}

func TestReadMetricsEmptySnapshot(t *testing.T) {
	var m readMetrics
	got := m.snapshot()
	if got.Count != 0 || got.AvgMs != 0 || got.MaxMs != 0 {
		t.Fatalf("zero-value snapshot should be empty, got %+v", got)
	}
}

// TestMeasuredReadsRecord verifies that the measured read methods increment the
// store's read metrics with the elapsed time, using a fake clock for
// determinism.
func TestMeasuredReadsRecord(t *testing.T) {
	old := nowFunc
	defer func() { nowFunc = old }()
	nowFunc = fakeClock(5) // every read takes exactly 5ms of "wall clock"

	s := newTestStore(t)
	if c := s.ReadMetrics().Count; c != 0 {
		t.Fatalf("baseline read count = %d, want 0", c)
	}

	if err := s.AddHost("1.2.3.4"); err != nil { // write — not measured
		t.Fatalf("AddHost: %v", err)
	}
	if _, err := s.Stats(); err != nil { // measured read #1
		t.Fatalf("Stats: %v", err)
	}
	if _, err := s.GetResults("1.2.3.4", 10); err != nil { // measured read #2
		t.Fatalf("GetResults: %v", err)
	}

	rm := s.ReadMetrics()
	if rm.Count != 2 {
		t.Fatalf("read count = %d, want 2", rm.Count)
	}
	if rm.MaxMs != 5 || rm.LastMs != 5 || rm.AvgMs != 5 {
		t.Fatalf("expected 5ms per read, got %+v", rm)
	}
}

// TestConcurrentReadsAndWrite confirms the read pool serves many concurrent
// queries alongside the writer without "database is locked", and that read
// metrics accumulate across goroutines.
func TestConcurrentReadsAndWrite(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddHost("9.9.9.9"); err != nil {
		t.Fatalf("AddHost: %v", err)
	}
	for i := 0; i < 100; i++ {
		s.SaveResult(Result{IP: "9.9.9.9", Seq: i, Timestamp: time.Now(), Success: i%2 == 0, RTTNanos: int64(i) * 1e6})
	}
	s.Sync()

	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.GetResults("9.9.9.9", 50); err != nil {
				t.Errorf("concurrent GetResults: %v", err)
			}
			if _, err := s.Stats(); err != nil {
				t.Errorf("concurrent Stats: %v", err)
			}
		}()
	}
	wg.Wait()

	if c := s.ReadMetrics().Count; c < 48 {
		t.Fatalf("expected >= 48 reads recorded, got %d", c)
	}
}

// TestReadSeesCommittedWrite verifies the read pool observes data committed by
// the writer connection (WAL visibility).
func TestReadSeesCommittedWrite(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddHost("7.7.7.7"); err != nil {
		t.Fatalf("AddHost: %v", err)
	}
	ips, err := s.ListHosts() // reads via the read pool
	if err != nil {
		t.Fatalf("ListHosts: %v", err)
	}
	if len(ips) != 1 || ips[0] != "7.7.7.7" {
		t.Fatalf("read pool did not see committed host, got %v", ips)
	}
}
