package store

import (
	"fmt"
	"sort"
	"time"
)

// Host status states.
const (
	StateUp       = "up"
	StateDegraded = "degraded"
	StateDown     = "down"
	StateUnknown  = "unknown"
)

// Availability summarizes uptime over a time range.
type Availability struct {
	IP        string    `json:"ip"`
	From      time.Time `json:"from"`
	To        time.Time `json:"to"`
	Sent      int       `json:"sent"`
	Received  int       `json:"received"`
	UptimePct float64   `json:"uptimePct"`
}

// Percentiles holds RTT distribution statistics over a range (nanoseconds).
type Percentiles struct {
	IP       string `json:"ip"`
	Count    int    `json:"count"`
	P50Ns    int64  `json:"p50Ns"`
	P95Ns    int64  `json:"p95Ns"`
	P99Ns    int64  `json:"p99Ns"`
	JitterNs int64  `json:"jitterNs"`
}

// Outage is a detected period of consecutive ping failures.
type Outage struct {
	Start       time.Time  `json:"start"`
	End         *time.Time `json:"end"` // nil = ongoing
	DurationSec float64    `json:"durationSec"`
	FailCount   int        `json:"failCount"`
}

// HostStatus is the current health snapshot for a host (for the NOC wall).
type HostStatus struct {
	IP          string     `json:"ip"`
	DisplayName string     `json:"displayName"`
	State       string     `json:"state"`
	LastRTTNs   *int64     `json:"lastRttNs"`
	AvgRTTNs    *int64     `json:"avgRttNs"`
	LossPct     float64    `json:"lossPct"`
	JitterNs    *int64     `json:"jitterNs"`
	UptimePct   float64    `json:"uptimePct"`
	LastSeen    *time.Time `json:"lastSeen"`
	Breaches    []string   `json:"breaches"`
	Sparkline   []*int64   `json:"sparkline"`
	Groups      []int64    `json:"groups"`
}

// GetAvailability computes uptime over [start, end], reading from the rollup
// tables for long ranges and raw results for short ranges.
func (s *Store) GetAvailability(ip string, start, end time.Time) (Availability, error) {
	defer s.measure()()
	a := Availability{IP: ip, From: start.UTC(), To: end.UTC()}
	res := ResolveResolution(ResolutionAuto, start, end)

	var query string
	switch res {
	case ResolutionHour:
		query = `SELECT COALESCE(SUM(sent),0), COALESCE(SUM(received),0) FROM ping_rollup_hourly
		          WHERE ip = ? AND bucket_start >= ? AND bucket_start < ?`
	case ResolutionDay:
		query = `SELECT COALESCE(SUM(sent),0), COALESCE(SUM(received),0) FROM ping_rollup_daily
		          WHERE ip = ? AND bucket_start >= ? AND bucket_start < ?`
	default:
		query = `SELECT COUNT(*), COALESCE(SUM(success),0) FROM ping_results
		          WHERE ip = ? AND timestamp >= ? AND timestamp < ?`
	}
	if err := s.rdb.QueryRow(query, ip, start.UTC(), end.UTC()).Scan(&a.Sent, &a.Received); err != nil {
		return Availability{}, fmt.Errorf("availability for %q: %w", ip, err)
	}
	if a.Sent > 0 {
		a.UptimePct = float64(a.Received) / float64(a.Sent) * 100
	}
	return a, nil
}

// GetPercentiles computes RTT percentiles and jitter over successful pings in a
// range (raw data).
func (s *Store) GetPercentiles(ip string, start, end time.Time) (Percentiles, error) {
	defer s.measure()()
	rows, err := s.rdb.Query(
		`SELECT rtt_ns FROM ping_results
		  WHERE ip = ? AND success = 1 AND timestamp >= ? AND timestamp < ?
		  ORDER BY timestamp ASC, id ASC`,
		ip, start.UTC(), end.UTC(),
	)
	if err != nil {
		return Percentiles{}, fmt.Errorf("percentiles for %q: %w", ip, err)
	}
	defer rows.Close()

	rtts := []int64{}
	for rows.Next() {
		var rtt int64
		if err := rows.Scan(&rtt); err != nil {
			return Percentiles{}, fmt.Errorf("scan rtt: %w", err)
		}
		rtts = append(rtts, rtt)
	}
	if err := rows.Err(); err != nil {
		return Percentiles{}, err
	}

	p := Percentiles{IP: ip, Count: len(rtts)}
	if len(rtts) == 0 {
		return p, nil
	}
	p.JitterNs = meanAbsSuccessiveDiff(rtts)

	sorted := make([]int64, len(rtts))
	copy(sorted, rtts)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	p.P50Ns = nearestRank(sorted, 0.50)
	p.P95Ns = nearestRank(sorted, 0.95)
	p.P99Ns = nearestRank(sorted, 0.99)
	return p, nil
}

// GetOutages detects runs of >= minFails consecutive failures over [start, end].
func (s *Store) GetOutages(ip string, start, end time.Time, minFails int) ([]Outage, error) {
	if minFails < 1 {
		minFails = 1
	}
	defer s.measure()()
	rows, err := s.rdb.Query(
		`SELECT timestamp, success FROM ping_results
		  WHERE ip = ? AND timestamp >= ? AND timestamp < ?
		  ORDER BY timestamp ASC, id ASC`,
		ip, start.UTC(), end.UTC(),
	)
	if err != nil {
		return nil, fmt.Errorf("outages for %q: %w", ip, err)
	}
	defer rows.Close()

	outages := []Outage{}
	var (
		inRun     bool
		runStart  time.Time
		runLastTs time.Time
		runCount  int
	)
	emit := func(recoveredAt *time.Time) {
		if !inRun || runCount < minFails {
			inRun = false
			return
		}
		o := Outage{Start: runStart.UTC(), FailCount: runCount}
		if recoveredAt != nil {
			t := recoveredAt.UTC()
			o.End = &t
			o.DurationSec = t.Sub(runStart).Seconds()
		} else {
			o.DurationSec = runLastTs.Sub(runStart).Seconds()
		}
		outages = append(outages, o)
		inRun = false
	}

	for rows.Next() {
		var (
			ts      time.Time
			success int
		)
		if err := rows.Scan(&ts, &success); err != nil {
			return nil, fmt.Errorf("scan outage row: %w", err)
		}
		if success == 0 {
			if !inRun {
				inRun = true
				runStart = ts
				runCount = 0
			}
			runCount++
			runLastTs = ts
		} else if inRun {
			recovered := ts
			emit(&recovered)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	emit(nil) // trailing ongoing outage, if any
	return outages, nil
}

// GetHostStatuses returns the current health snapshot for every monitored host,
// computed over the most recent lastN results.
func (s *Store) GetHostStatuses(lastN int) ([]HostStatus, error) {
	if lastN <= 0 {
		lastN = 60
	}
	hosts, err := s.ListHostsFull()
	if err != nil {
		return nil, err
	}

	statuses := make([]HostStatus, 0, len(hosts))
	for _, h := range hosts {
		results, err := s.GetResults(h.IP, lastN) // newest first
		if err != nil {
			return nil, err
		}
		statuses = append(statuses, classifyHost(h, results))
	}
	return statuses, nil
}

// classifyHost derives a HostStatus from a host record and its recent results
// (newest first).
func classifyHost(h Host, results []Result) HostStatus {
	st := HostStatus{
		IP:          h.IP,
		DisplayName: h.DisplayName,
		State:       StateUnknown,
		Groups:      h.Groups,
		Breaches:    []string{},
		Sparkline:   []*int64{},
	}
	if len(results) == 0 {
		return st
	}

	// Build sparkline oldest -> newest; gather successful RTTs (arrival order).
	var (
		sent     = len(results)
		received int
		rtts     []int64
		lastRTT  *int64
		lastSeen = results[0].Timestamp
	)
	for i := len(results) - 1; i >= 0; i-- {
		r := results[i]
		if r.Success {
			received++
			v := r.RTTNanos
			rtts = append(rtts, v)
			st.Sparkline = append(st.Sparkline, &v)
		} else {
			st.Sparkline = append(st.Sparkline, nil)
		}
	}
	// Most recent successful RTT for "last RTT".
	for _, r := range results { // newest first
		if r.Success {
			v := r.RTTNanos
			lastRTT = &v
			break
		}
	}

	st.LastSeen = &lastSeen
	st.LastRTTNs = lastRTT
	st.LossPct = float64(sent-received) / float64(sent) * 100
	st.UptimePct = 100 - st.LossPct

	if len(rtts) > 0 {
		var sum int64
		for _, v := range rtts {
			sum += v
		}
		avg := sum / int64(len(rtts))
		st.AvgRTTNs = &avg
		jitter := meanAbsSuccessiveDiff(rtts)
		st.JitterNs = &jitter
	}

	// Threshold breaches.
	if h.AlertLatencyMs > 0 && st.AvgRTTNs != nil && *st.AvgRTTNs > int64(h.AlertLatencyMs)*int64(time.Millisecond) {
		st.Breaches = append(st.Breaches, "latency")
	}
	if h.AlertLossPct > 0 && st.LossPct > h.AlertLossPct {
		st.Breaches = append(st.Breaches, "loss")
	}

	// State classification.
	switch {
	case received == 0:
		st.State = StateDown
	case len(st.Breaches) > 0 || !results[0].Success:
		st.State = StateDegraded
	default:
		st.State = StateUp
	}
	return st
}
