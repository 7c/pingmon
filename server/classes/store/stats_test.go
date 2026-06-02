package store

import (
	"testing"
	"time"
)

func TestStoreStats(t *testing.T) {
	s := newTestStore(t)

	// Empty store.
	st, err := s.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if st.Hosts != 0 || st.Results != 0 || st.Oldest != nil {
		t.Fatalf("expected empty stats, got %+v", st)
	}

	// Seed hosts, tags, a group, an annotation, and results.
	s.AddHost("1.1.1.1")
	s.AddHost("2.2.2.2")
	s.SetHostTags("1.1.1.1", []string{"a", "b"})
	g, _ := s.CreateGroup("edge", "", "")
	s.AssignHostToGroup("1.1.1.1", g.ID)
	ip := "1.1.1.1"
	s.CreateAnnotation(Annotation{IP: &ip, StartTs: time.Now()})

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		s.SaveResult(Result{IP: "1.1.1.1", Seq: i, Timestamp: base.Add(time.Duration(i) * time.Minute), Success: true, RTTNanos: 1000})
	}
	s.Sync()

	st, err = s.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if st.Hosts != 2 || st.Groups != 1 || st.Tags != 2 || st.Annotations != 1 || st.Results != 5 {
		t.Fatalf("unexpected stats: %+v", st)
	}
	if st.Oldest == nil || st.Newest == nil || !st.Oldest.Equal(base) {
		t.Fatalf("expected oldest=%v, got %+v / %+v", base, st.Oldest, st.Newest)
	}
}
