package store

import (
	"path/filepath"
	"testing"
	"time"
)

// newTestStore creates a Store backed by a fresh temp-dir database and ensures
// it is closed when the test finishes.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := New(path)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestAddListRemoveHost(t *testing.T) {
	s := newTestStore(t)

	// Empty to start.
	ips, err := s.ListHosts()
	if err != nil {
		t.Fatalf("ListHosts() error: %v", err)
	}
	if len(ips) != 0 {
		t.Fatalf("expected no hosts, got %v", ips)
	}

	// Create.
	for _, ip := range []string{"1.1.1.1", "8.8.8.8"} {
		if err := s.AddHost(ip); err != nil {
			t.Fatalf("AddHost(%q) error: %v", ip, err)
		}
	}

	// Read: insertion order preserved.
	ips, err = s.ListHosts()
	if err != nil {
		t.Fatalf("ListHosts() error: %v", err)
	}
	if len(ips) != 2 || ips[0] != "1.1.1.1" || ips[1] != "8.8.8.8" {
		t.Fatalf("unexpected hosts: %v", ips)
	}

	// AddHost is idempotent.
	if err := s.AddHost("1.1.1.1"); err != nil {
		t.Fatalf("idempotent AddHost error: %v", err)
	}
	ips, _ = s.ListHosts()
	if len(ips) != 2 {
		t.Fatalf("expected 2 hosts after duplicate add, got %v", ips)
	}

	// Delete.
	if err := s.RemoveHost("1.1.1.1"); err != nil {
		t.Fatalf("RemoveHost() error: %v", err)
	}
	ips, _ = s.ListHosts()
	if len(ips) != 1 || ips[0] != "8.8.8.8" {
		t.Fatalf("expected only 8.8.8.8 after removal, got %v", ips)
	}

	// Removing a non-existent host is not an error.
	if err := s.RemoveHost("9.9.9.9"); err != nil {
		t.Fatalf("RemoveHost(missing) error: %v", err)
	}
}

func TestSaveAndGetResults(t *testing.T) {
	s := newTestStore(t)

	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		s.SaveResult(Result{
			IP:        "1.1.1.1",
			Seq:       i,
			Timestamp: base.Add(time.Duration(i) * time.Second),
			Success:   i%2 == 0,
			RTTNanos:  int64(i) * 1000,
		})
	}
	// A failed result with an error message.
	s.SaveResult(Result{
		IP:        "1.1.1.1",
		Seq:       5,
		Timestamp: base.Add(5 * time.Second),
		Success:   false,
		ErrMsg:    "timeout",
	})
	s.Sync()

	results, err := s.GetResults("1.1.1.1", 0)
	if err != nil {
		t.Fatalf("GetResults() error: %v", err)
	}
	if len(results) != 6 {
		t.Fatalf("expected 6 results, got %d", len(results))
	}

	// Newest first.
	if results[0].Seq != 5 {
		t.Fatalf("expected newest (seq=5) first, got seq=%d", results[0].Seq)
	}
	if results[0].ErrMsg != "timeout" {
		t.Fatalf("expected error message preserved, got %q", results[0].ErrMsg)
	}
	if results[len(results)-1].Seq != 0 {
		t.Fatalf("expected oldest (seq=0) last, got seq=%d", results[len(results)-1].Seq)
	}

	// Success flag round-trips.
	if !results[len(results)-1].Success {
		t.Fatalf("expected seq=0 to be a success")
	}
}

func TestGetResultsLimit(t *testing.T) {
	s := newTestStore(t)

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 10; i++ {
		s.SaveResult(Result{
			IP:        "1.1.1.1",
			Seq:       i,
			Timestamp: base.Add(time.Duration(i) * time.Second),
			Success:   true,
		})
	}
	s.Sync()

	results, err := s.GetResults("1.1.1.1", 3)
	if err != nil {
		t.Fatalf("GetResults() error: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results with limit, got %d", len(results))
	}
	// The 3 most recent are seq 9, 8, 7.
	if results[0].Seq != 9 || results[2].Seq != 7 {
		t.Fatalf("unexpected limited window: %d..%d", results[0].Seq, results[2].Seq)
	}
}

func TestGetResultsEmpty(t *testing.T) {
	s := newTestStore(t)

	results, err := s.GetResults("nobody", 0)
	if err != nil {
		t.Fatalf("GetResults() error: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected empty results, got %d", len(results))
	}
}

func TestRemoveHostKeepsResults(t *testing.T) {
	s := newTestStore(t)

	if err := s.AddHost("1.1.1.1"); err != nil {
		t.Fatalf("AddHost() error: %v", err)
	}
	s.SaveResult(Result{IP: "1.1.1.1", Seq: 0, Timestamp: time.Now(), Success: true})
	s.Sync()

	if err := s.RemoveHost("1.1.1.1"); err != nil {
		t.Fatalf("RemoveHost() error: %v", err)
	}

	// Long-term history must survive host removal.
	results, err := s.GetResults("1.1.1.1", 0)
	if err != nil {
		t.Fatalf("GetResults() error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected history retained after removal, got %d results", len(results))
	}
}

func TestPersistenceAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reopen.db")

	s1, err := New(path)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if err := s1.AddHost("1.1.1.1"); err != nil {
		t.Fatalf("AddHost() error: %v", err)
	}
	s1.SaveResult(Result{IP: "1.1.1.1", Seq: 0, Timestamp: time.Now(), Success: true})
	if err := s1.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	// Reopen the same file: hosts and results should still be there.
	s2, err := New(path)
	if err != nil {
		t.Fatalf("reopen New() error: %v", err)
	}
	defer s2.Close()

	ips, err := s2.ListHosts()
	if err != nil {
		t.Fatalf("ListHosts() error: %v", err)
	}
	if len(ips) != 1 || ips[0] != "1.1.1.1" {
		t.Fatalf("hosts not persisted across reopen: %v", ips)
	}

	results, err := s2.GetResults("1.1.1.1", 0)
	if err != nil {
		t.Fatalf("GetResults() error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results not persisted across reopen: got %d", len(results))
	}
}

func TestCloseFlushesPendingResults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flush.db")

	s, err := New(path)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	// Enqueue without calling Sync; Close must flush them.
	for i := 0; i < 50; i++ {
		s.SaveResult(Result{IP: "1.1.1.1", Seq: i, Timestamp: time.Now(), Success: true})
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	s2, err := New(path)
	if err != nil {
		t.Fatalf("reopen error: %v", err)
	}
	defer s2.Close()

	results, err := s2.GetResults("1.1.1.1", 0)
	if err != nil {
		t.Fatalf("GetResults() error: %v", err)
	}
	if len(results) != 50 {
		t.Fatalf("Close() did not flush all results: got %d, want 50", len(results))
	}
}
