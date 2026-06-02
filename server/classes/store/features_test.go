package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// TestMigrateLegacyDatabase ensures a pre-v1.2 database (hosts with only
// ip/added_at, plus ping_results) is upgraded in place by store.New.
func TestMigrateLegacyDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")

	// Build the old schema directly and insert a host + a result.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	legacy := `
CREATE TABLE hosts (ip TEXT PRIMARY KEY, added_at TIMESTAMP NOT NULL);
CREATE TABLE ping_results (id INTEGER PRIMARY KEY AUTOINCREMENT, ip TEXT NOT NULL,
  seq INTEGER NOT NULL, timestamp TIMESTAMP NOT NULL, success INTEGER NOT NULL,
  rtt_ns INTEGER NOT NULL, err_msg TEXT);
INSERT INTO hosts (ip, added_at) VALUES ('1.1.1.1', '2026-01-01 00:00:00 +0000 UTC');
`
	if _, err := db.Exec(legacy); err != nil {
		t.Fatalf("seed legacy schema: %v", err)
	}
	db.Close()

	// Opening via the store should migrate additively and preserve data.
	s, err := New(path)
	if err != nil {
		t.Fatalf("New on legacy db: %v", err)
	}
	defer s.Close()

	h, err := s.GetHost("1.1.1.1")
	if err != nil {
		t.Fatalf("GetHost after migration: %v", err)
	}
	if h.Config != DefaultHostConfig() {
		t.Fatalf("expected default config on migrated host, got %+v", h.Config)
	}
	hosts, err := s.ListHostsFull()
	if err != nil || len(hosts) != 1 {
		t.Fatalf("ListHostsFull after migration: %v (%d hosts)", err, len(hosts))
	}
}

// --- host config / meta / tags ---

func TestHostConfigRoundTrip(t *testing.T) {
	s := newTestStore(t)

	// Legacy AddHost yields defaults.
	if err := s.AddHost("1.1.1.1"); err != nil {
		t.Fatalf("AddHost: %v", err)
	}
	h, err := s.GetHost("1.1.1.1")
	if err != nil {
		t.Fatalf("GetHost: %v", err)
	}
	if h.Config != DefaultHostConfig() {
		t.Fatalf("expected default config, got %+v", h.Config)
	}

	// AddHostWithConfig sets custom config; idempotent (no overwrite).
	cfg := HostConfig{IntervalMs: 5000, TimeoutMs: 3000, PacketSize: 128}
	if err := s.AddHostWithConfig("2.2.2.2", cfg); err != nil {
		t.Fatalf("AddHostWithConfig: %v", err)
	}
	h, _ = s.GetHost("2.2.2.2")
	if h.Config != cfg {
		t.Fatalf("expected %+v, got %+v", cfg, h.Config)
	}

	// UpdateHostConfig persists.
	newCfg := HostConfig{IntervalMs: 2000, TimeoutMs: 1500, PacketSize: 64}
	if err := s.UpdateHostConfig("2.2.2.2", newCfg); err != nil {
		t.Fatalf("UpdateHostConfig: %v", err)
	}
	h, _ = s.GetHost("2.2.2.2")
	if h.Config != newCfg {
		t.Fatalf("expected %+v, got %+v", newCfg, h.Config)
	}
	if h.UpdatedAt == nil {
		t.Fatalf("expected updatedAt to be set")
	}

	// Updating a missing host is ErrNotFound.
	if err := s.UpdateHostConfig("9.9.9.9", newCfg); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestGetHostNotFound(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.GetHost("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestHostMetaAndTags(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddHost("1.1.1.1"); err != nil {
		t.Fatalf("AddHost: %v", err)
	}

	if err := s.UpdateHostMeta("1.1.1.1", "core-router", "rack A"); err != nil {
		t.Fatalf("UpdateHostMeta: %v", err)
	}
	if err := s.SetHostTags("1.1.1.1", []string{"edge", "prod", ""}); err != nil {
		t.Fatalf("SetHostTags: %v", err)
	}
	h, _ := s.GetHost("1.1.1.1")
	if h.DisplayName != "core-router" || h.Notes != "rack A" {
		t.Fatalf("meta not persisted: %+v", h)
	}
	if len(h.Tags) != 2 { // empty tag dropped
		t.Fatalf("expected 2 tags, got %v", h.Tags)
	}

	// Replace tags.
	if err := s.SetHostTags("1.1.1.1", []string{"eu"}); err != nil {
		t.Fatalf("SetHostTags replace: %v", err)
	}
	h, _ = s.GetHost("1.1.1.1")
	if len(h.Tags) != 1 || h.Tags[0] != "eu" {
		t.Fatalf("expected [eu], got %v", h.Tags)
	}

	// Tags cascade on host removal.
	if err := s.RemoveHost("1.1.1.1"); err != nil {
		t.Fatalf("RemoveHost: %v", err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM host_tags WHERE ip = ?`, "1.1.1.1").Scan(&n); err != nil {
		t.Fatalf("count tags: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected tags cascade-deleted, got %d", n)
	}
}

func TestUpdateHostAlerts(t *testing.T) {
	s := newTestStore(t)
	s.AddHost("1.1.1.1")
	if err := s.UpdateHostAlerts("1.1.1.1", 50, 5.0); err != nil {
		t.Fatalf("UpdateHostAlerts: %v", err)
	}
	h, _ := s.GetHost("1.1.1.1")
	if h.AlertLatencyMs != 50 || h.AlertLossPct != 5.0 {
		t.Fatalf("alerts not persisted: %+v", h)
	}
}

func TestListHostsFull(t *testing.T) {
	s := newTestStore(t)
	s.AddHost("1.1.1.1")
	s.AddHost("2.2.2.2")
	s.SetHostTags("1.1.1.1", []string{"edge"})
	g, _ := s.CreateGroup("edge", "#fff", "")
	s.AssignHostToGroup("1.1.1.1", g.ID)

	hosts, err := s.ListHostsFull()
	if err != nil {
		t.Fatalf("ListHostsFull: %v", err)
	}
	if len(hosts) != 2 {
		t.Fatalf("expected 2 hosts, got %d", len(hosts))
	}
	// Oldest first.
	if hosts[0].IP != "1.1.1.1" {
		t.Fatalf("expected 1.1.1.1 first, got %s", hosts[0].IP)
	}
	if len(hosts[0].Tags) != 1 || len(hosts[0].Groups) != 1 {
		t.Fatalf("tags/groups not assembled: %+v", hosts[0])
	}
}

// --- groups ---

func TestGroupsCRUDAndMembership(t *testing.T) {
	s := newTestStore(t)
	s.AddHost("1.1.1.1")

	g, err := s.CreateGroup("edge", "#abc", "edge nodes")
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	if _, err := s.CreateGroup("edge", "", ""); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict on duplicate name, got %v", err)
	}

	if err := s.UpdateGroup(g.ID, "edge2", "#def", "renamed"); err != nil {
		t.Fatalf("UpdateGroup: %v", err)
	}
	got, _ := s.GetGroup(g.ID)
	if got.Name != "edge2" || got.UpdatedAt == nil {
		t.Fatalf("update not persisted: %+v", got)
	}

	groups, _ := s.ListGroups()
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}

	// Membership.
	if err := s.AssignHostToGroup("1.1.1.1", g.ID); err != nil {
		t.Fatalf("AssignHostToGroup: %v", err)
	}
	if err := s.AssignHostToGroup("1.1.1.1", g.ID); err != nil {
		t.Fatalf("idempotent assign: %v", err)
	}
	ips, _ := s.ListHostsInGroup(g.ID)
	if len(ips) != 1 || ips[0] != "1.1.1.1" {
		t.Fatalf("expected [1.1.1.1], got %v", ips)
	}
	if err := s.UnassignHostFromGroup("1.1.1.1", g.ID); err != nil {
		t.Fatalf("Unassign: %v", err)
	}
	ips, _ = s.ListHostsInGroup(g.ID)
	if len(ips) != 0 {
		t.Fatalf("expected empty membership, got %v", ips)
	}

	// Delete group; missing -> ErrNotFound.
	if err := s.DeleteGroup(g.ID); err != nil {
		t.Fatalf("DeleteGroup: %v", err)
	}
	if err := s.DeleteGroup(g.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestGroupMembershipCascadeOnHostDelete(t *testing.T) {
	s := newTestStore(t)
	s.AddHost("1.1.1.1")
	g, _ := s.CreateGroup("edge", "", "")
	s.AssignHostToGroup("1.1.1.1", g.ID)

	if err := s.RemoveHost("1.1.1.1"); err != nil {
		t.Fatalf("RemoveHost: %v", err)
	}
	ips, _ := s.ListHostsInGroup(g.ID)
	if len(ips) != 0 {
		t.Fatalf("expected membership cascade-deleted, got %v", ips)
	}
}

// --- annotations ---

func TestAnnotationsCRUDAndOverlap(t *testing.T) {
	s := newTestStore(t)
	ip := "1.1.1.1"
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	// Point annotation at base+30m.
	point, err := s.CreateAnnotation(Annotation{IP: &ip, Type: AnnotationComment, Title: "note", StartTs: base.Add(30 * time.Minute)})
	if err != nil {
		t.Fatalf("CreateAnnotation point: %v", err)
	}
	// Range annotation base+2h .. base+3h.
	end := base.Add(3 * time.Hour)
	rng, err := s.CreateAnnotation(Annotation{IP: &ip, Type: AnnotationIncident, Title: "incident", StartTs: base.Add(2 * time.Hour), EndTs: &end})
	if err != nil {
		t.Fatalf("CreateAnnotation range: %v", err)
	}

	// Window [base, base+1h] should match only the point.
	got, err := s.ListAnnotations(ip, base, base.Add(time.Hour), nil)
	if err != nil {
		t.Fatalf("ListAnnotations: %v", err)
	}
	if len(got) != 1 || got[0].ID != point.ID {
		t.Fatalf("expected only point in first hour, got %+v", got)
	}

	// Window overlapping the range.
	got, _ = s.ListAnnotations(ip, base.Add(2*time.Hour+30*time.Minute), base.Add(4*time.Hour), nil)
	if len(got) != 1 || got[0].ID != rng.ID {
		t.Fatalf("expected range annotation, got %+v", got)
	}

	// Type filter.
	got, _ = s.ListAnnotations(ip, base, end.Add(time.Hour), []string{AnnotationIncident})
	if len(got) != 1 || got[0].Type != AnnotationIncident {
		t.Fatalf("type filter failed: %+v", got)
	}

	// Update + delete.
	rng.Title = "updated"
	if err := s.UpdateAnnotation(rng); err != nil {
		t.Fatalf("UpdateAnnotation: %v", err)
	}
	reloaded, _ := s.GetAnnotation(rng.ID)
	if reloaded.Title != "updated" {
		t.Fatalf("update not persisted: %+v", reloaded)
	}
	if err := s.DeleteAnnotation(rng.ID); err != nil {
		t.Fatalf("DeleteAnnotation: %v", err)
	}
	if _, err := s.GetAnnotation(rng.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestAnnotationsSurviveHostRemoval(t *testing.T) {
	s := newTestStore(t)
	ip := "1.1.1.1"
	s.AddHost(ip)
	now := time.Now().UTC()
	if _, err := s.CreateAnnotation(Annotation{IP: &ip, Title: "keep", StartTs: now}); err != nil {
		t.Fatalf("CreateAnnotation: %v", err)
	}
	if err := s.RemoveHost(ip); err != nil {
		t.Fatalf("RemoveHost: %v", err)
	}
	got, _ := s.ListAnnotations(ip, now.Add(-time.Hour), now.Add(time.Hour), nil)
	if len(got) != 1 {
		t.Fatalf("annotations should survive host removal, got %d", len(got))
	}
}

// --- series & rollup ---

// seedRaw writes count results for ip starting at start, one per second, with the
// given rtt (ns) and success pattern (failEvery N: every Nth fails; 0 = none).
func seedRaw(t *testing.T, s *Store, ip string, start time.Time, count int, rtt int64, failEvery int) {
	t.Helper()
	for i := 0; i < count; i++ {
		success := true
		if failEvery > 0 && i%failEvery == 0 {
			success = false
		}
		r := Result{IP: ip, Seq: i, Timestamp: start.Add(time.Duration(i) * time.Second), Success: success}
		if success {
			r.RTTNanos = rtt
		}
		s.SaveResult(r)
	}
	s.Sync()
}

func TestGetSeriesRaw(t *testing.T) {
	s := newTestStore(t)
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// 120 pings over 2 minutes, all successful, 10ms each.
	seedRaw(t, s, "1.1.1.1", start, 120, 10*int64(time.Millisecond), 0)

	// Minute resolution -> 2 buckets.
	series, err := s.GetSeries("1.1.1.1", start, start.Add(5*time.Minute), ResolutionMinute)
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	if series.Resolution != ResolutionMinute {
		t.Fatalf("expected minute resolution, got %s", series.Resolution)
	}
	if len(series.Buckets) != 2 {
		t.Fatalf("expected 2 minute buckets, got %d", len(series.Buckets))
	}
	b := series.Buckets[0]
	if b.Sent != 60 || b.Received != 60 {
		t.Fatalf("expected 60/60, got %d/%d", b.Sent, b.Received)
	}
	if b.AvgRTTNs != 10*int64(time.Millisecond) || b.P95RTTNs != 10*int64(time.Millisecond) {
		t.Fatalf("unexpected rtt stats: avg=%d p95=%d", b.AvgRTTNs, b.P95RTTNs)
	}
	if b.LossPct != 0 {
		t.Fatalf("expected 0 loss, got %f", b.LossPct)
	}
}

func TestResolveResolution(t *testing.T) {
	now := time.Now()
	cases := []struct {
		span time.Duration
		want string
	}{
		{1 * time.Hour, ResolutionRaw},
		{6 * time.Hour, ResolutionRaw},
		{48 * time.Hour, ResolutionHour},
		{30 * 24 * time.Hour, ResolutionDay},
	}
	for _, c := range cases {
		got := ResolveResolution(ResolutionAuto, now.Add(-c.span), now)
		if got != c.want {
			t.Errorf("span %v: want %s, got %s", c.span, c.want, got)
		}
	}
	// Explicit wins.
	if got := ResolveResolution(ResolutionDay, now.Add(-time.Minute), now); got != ResolutionDay {
		t.Errorf("explicit day should win, got %s", got)
	}
}

func TestRollupHourlyAndIdempotency(t *testing.T) {
	s := newTestStore(t)
	// Seed 3 full hours of pings, 1 per second is too many; use 1 per minute.
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for h := 0; h < 3; h++ {
		for m := 0; m < 60; m++ {
			ts := base.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute)
			s.SaveResult(Result{IP: "1.1.1.1", Seq: h*60 + m, Timestamp: ts, Success: true, RTTNanos: int64(h+1) * int64(time.Millisecond)})
		}
	}
	s.Sync()

	// "now" well after the 3 hours so all are finalized.
	now := base.Add(5 * time.Hour)
	if err := s.RunHourlyRollup(now, 10*time.Minute); err != nil {
		t.Fatalf("RunHourlyRollup: %v", err)
	}

	series, _ := s.GetSeries("1.1.1.1", base, base.Add(3*time.Hour), ResolutionHour)
	if len(series.Buckets) != 3 {
		t.Fatalf("expected 3 hourly buckets, got %d", len(series.Buckets))
	}
	for i, b := range series.Buckets {
		wantRtt := int64(i+1) * int64(time.Millisecond)
		if b.Sent != 60 || b.Received != 60 || b.AvgRTTNs != wantRtt {
			t.Fatalf("bucket %d: %+v (want avg %d)", i, b, wantRtt)
		}
	}

	// Idempotent re-run yields identical data.
	if err := s.RunHourlyRollup(now, 10*time.Minute); err != nil {
		t.Fatalf("re-run: %v", err)
	}
	var rows int
	s.db.QueryRow(`SELECT COUNT(*) FROM ping_rollup_hourly WHERE ip = ?`, "1.1.1.1").Scan(&rows)
	if rows != 3 {
		t.Fatalf("expected 3 rollup rows after re-run, got %d", rows)
	}
}

func TestRollupDailyFromHourly(t *testing.T) {
	s := newTestStore(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// One ping per minute for a full day (1440) keeps the test fast enough.
	for m := 0; m < 1440; m++ {
		ts := base.Add(time.Duration(m) * time.Minute)
		s.SaveResult(Result{IP: "1.1.1.1", Seq: m, Timestamp: ts, Success: true, RTTNanos: 10 * int64(time.Millisecond)})
	}
	s.Sync()

	now := base.Add(48 * time.Hour)
	if err := s.RunHourlyRollup(now, 10*time.Minute); err != nil {
		t.Fatalf("hourly: %v", err)
	}
	if err := s.RunDailyRollup(now, time.Hour); err != nil {
		t.Fatalf("daily: %v", err)
	}
	series, _ := s.GetSeries("1.1.1.1", base, base.Add(24*time.Hour), ResolutionDay)
	if len(series.Buckets) != 1 {
		t.Fatalf("expected 1 daily bucket, got %d", len(series.Buckets))
	}
	if series.Buckets[0].Sent != 1440 || series.Buckets[0].Received != 1440 {
		t.Fatalf("expected 1440/1440, got %+v", series.Buckets[0])
	}
}

func TestPruneRawRespectsWatermark(t *testing.T) {
	s := newTestStore(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for m := 0; m < 180; m++ { // 3 hours, one per minute
		ts := base.Add(time.Duration(m) * time.Minute)
		s.SaveResult(Result{IP: "1.1.1.1", Seq: m, Timestamp: ts, Success: true, RTTNanos: int64(time.Millisecond)})
	}
	s.Sync()

	now := base.Add(5 * time.Hour)
	// No rollup yet -> prune is a no-op even with tiny retention.
	n, err := s.PruneRawResults(now, time.Minute)
	if err != nil {
		t.Fatalf("Prune no-watermark: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected no prune before any rollup, deleted %d", n)
	}

	// Roll up hourly (watermark advances), then prune with tiny retention.
	if err := s.RunHourlyRollup(now, 10*time.Minute); err != nil {
		t.Fatalf("hourly: %v", err)
	}
	n, err = s.PruneRawResults(now, time.Minute)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if n == 0 {
		t.Fatalf("expected some rows pruned after rollup")
	}
	// Hourly rollups remain queryable after pruning raw.
	series, _ := s.GetSeries("1.1.1.1", base, base.Add(3*time.Hour), ResolutionHour)
	if len(series.Buckets) == 0 {
		t.Fatalf("rollups should survive raw pruning")
	}
}

// --- analytics ---

func TestPercentilesAndJitter(t *testing.T) {
	s := newTestStore(t)
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// RTTs 1..100 ms, one per second, all successful.
	for i := 1; i <= 100; i++ {
		s.SaveResult(Result{IP: "1.1.1.1", Seq: i, Timestamp: start.Add(time.Duration(i) * time.Second), Success: true, RTTNanos: int64(i) * int64(time.Millisecond)})
	}
	s.Sync()

	p, err := s.GetPercentiles("1.1.1.1", start, start.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("GetPercentiles: %v", err)
	}
	if p.Count != 100 {
		t.Fatalf("expected 100 samples, got %d", p.Count)
	}
	// Nearest-rank: p50 -> 50ms, p95 -> 95ms, p99 -> 99ms.
	if p.P50Ns != 50*int64(time.Millisecond) || p.P95Ns != 95*int64(time.Millisecond) || p.P99Ns != 99*int64(time.Millisecond) {
		t.Fatalf("percentiles off: p50=%d p95=%d p99=%d", p.P50Ns, p.P95Ns, p.P99Ns)
	}
	// Jitter: successive diffs all 1ms -> mean 1ms.
	if p.JitterNs != int64(time.Millisecond) {
		t.Fatalf("expected 1ms jitter, got %d", p.JitterNs)
	}
}

func TestAvailability(t *testing.T) {
	s := newTestStore(t)
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seedRaw(t, s, "1.1.1.1", start, 100, int64(time.Millisecond), 4) // every 4th fails -> 25 fails

	a, err := s.GetAvailability("1.1.1.1", start, start.Add(5*time.Minute))
	if err != nil {
		t.Fatalf("GetAvailability: %v", err)
	}
	if a.Sent != 100 || a.Received != 75 {
		t.Fatalf("expected 100/75, got %d/%d", a.Sent, a.Received)
	}
	if a.UptimePct != 75 {
		t.Fatalf("expected 75%% uptime, got %f", a.UptimePct)
	}
}

func TestOutageDetection(t *testing.T) {
	s := newTestStore(t)
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Pattern: 5 ok, 4 fail, 5 ok (one outage of 4), one per second.
	pattern := []bool{true, true, true, true, true, false, false, false, false, true, true, true, true, true}
	for i, ok := range pattern {
		r := Result{IP: "1.1.1.1", Seq: i, Timestamp: start.Add(time.Duration(i) * time.Second), Success: ok}
		if ok {
			r.RTTNanos = int64(time.Millisecond)
		}
		s.SaveResult(r)
	}
	s.Sync()

	outages, err := s.GetOutages("1.1.1.1", start, start.Add(time.Minute), 3)
	if err != nil {
		t.Fatalf("GetOutages: %v", err)
	}
	if len(outages) != 1 {
		t.Fatalf("expected 1 outage, got %d", len(outages))
	}
	if outages[0].FailCount != 4 || outages[0].End == nil {
		t.Fatalf("unexpected outage: %+v", outages[0])
	}

	// Higher threshold -> no outage.
	outages, _ = s.GetOutages("1.1.1.1", start, start.Add(time.Minute), 5)
	if len(outages) != 0 {
		t.Fatalf("expected no outage with minFails=5, got %d", len(outages))
	}
}

func TestHostStatusClassification(t *testing.T) {
	s := newTestStore(t)
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// up host: all success.
	s.AddHost("1.1.1.1")
	seedRaw(t, s, "1.1.1.1", start, 30, int64(time.Millisecond), 0)

	// down host: all failures.
	s.AddHost("2.2.2.2")
	for i := 0; i < 10; i++ {
		s.SaveResult(Result{IP: "2.2.2.2", Seq: i, Timestamp: start.Add(time.Duration(i) * time.Second), Success: false})
	}
	s.Sync()

	// no-data host: unknown.
	s.AddHost("3.3.3.3")

	statuses, err := s.GetHostStatuses(60)
	if err != nil {
		t.Fatalf("GetHostStatuses: %v", err)
	}
	byIP := map[string]HostStatus{}
	for _, st := range statuses {
		byIP[st.IP] = st
	}
	if byIP["1.1.1.1"].State != StateUp {
		t.Fatalf("1.1.1.1 expected up, got %s", byIP["1.1.1.1"].State)
	}
	if byIP["2.2.2.2"].State != StateDown {
		t.Fatalf("2.2.2.2 expected down, got %s", byIP["2.2.2.2"].State)
	}
	if byIP["3.3.3.3"].State != StateUnknown {
		t.Fatalf("3.3.3.3 expected unknown, got %s", byIP["3.3.3.3"].State)
	}
	if len(byIP["1.1.1.1"].Sparkline) != 30 {
		t.Fatalf("expected 30 sparkline points, got %d", len(byIP["1.1.1.1"].Sparkline))
	}
}
