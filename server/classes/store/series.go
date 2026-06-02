package store

import (
	"fmt"
	"sort"
	"time"
)

// Resolution values for time-series queries.
const (
	ResolutionRaw    = "raw"
	ResolutionMinute = "minute"
	ResolutionHour   = "hour"
	ResolutionDay    = "day"
	ResolutionAuto   = "auto"
)

// Bucket is one aggregated time bucket of ping data. RTT values are nanoseconds;
// a bucket with no successful pings reports zero RTTs and 100% loss.
type Bucket struct {
	Timestamp time.Time `json:"timestamp"`
	Sent      int       `json:"sent"`
	Received  int       `json:"received"`
	LossPct   float64   `json:"lossPct"`
	MinRTTNs  int64     `json:"minRttNs"`
	AvgRTTNs  int64     `json:"avgRttNs"`
	MaxRTTNs  int64     `json:"maxRttNs"`
	P50RTTNs  int64     `json:"p50RttNs"`
	P95RTTNs  int64     `json:"p95RttNs"`
	P99RTTNs  int64     `json:"p99RttNs"`
	JitterNs  int64     `json:"jitterNs"`
}

// Series is a resolved set of buckets for one host.
type Series struct {
	IP         string    `json:"ip"`
	Resolution string    `json:"resolution"`
	From       time.Time `json:"from"`
	To         time.Time `json:"to"`
	Buckets    []Bucket  `json:"buckets"`
}

// ResolveResolution maps "auto" (or an invalid value) to a concrete resolution
// based on the span: <=6h raw, <=14d hour, else day.
func ResolveResolution(requested string, start, end time.Time) string {
	switch requested {
	case ResolutionRaw, ResolutionMinute, ResolutionHour, ResolutionDay:
		return requested
	}
	span := end.Sub(start)
	switch {
	case span <= 6*time.Hour:
		return ResolutionRaw
	case span <= 14*24*time.Hour:
		return ResolutionHour
	default:
		return ResolutionDay
	}
}

// bucketSeconds returns the bucket width for raw/minute resolutions.
func bucketSeconds(resolution string) int64 {
	if resolution == ResolutionMinute {
		return 60
	}
	return 1 // raw
}

// GetSeries returns aggregated buckets for one host over [start, end] at the
// given resolution ("auto" resolves by span).
func (s *Store) GetSeries(ip string, start, end time.Time, resolution string) (Series, error) {
	res := ResolveResolution(resolution, start, end)
	series := Series{IP: ip, Resolution: res, From: start.UTC(), To: end.UTC(), Buckets: []Bucket{}}

	var (
		buckets []Bucket
		err     error
	)
	switch res {
	case ResolutionHour:
		buckets, err = s.bucketsFromRollup("ping_rollup_hourly", ip, start, end)
	case ResolutionDay:
		buckets, err = s.bucketsFromRollup("ping_rollup_daily", ip, start, end)
	default: // raw or minute: compute from raw results in Go
		buckets, err = s.bucketsFromRaw(ip, start, end, bucketSeconds(res))
	}
	if err != nil {
		return Series{}, err
	}
	series.Buckets = buckets
	return series, nil
}

// GetMultiSeries returns series for several hosts (for overlay charts).
func (s *Store) GetMultiSeries(ips []string, start, end time.Time, resolution string) ([]Series, error) {
	out := make([]Series, 0, len(ips))
	for _, ip := range ips {
		series, err := s.GetSeries(ip, start, end, resolution)
		if err != nil {
			return nil, err
		}
		out = append(out, series)
	}
	return out, nil
}

// bucketsFromRaw streams ordered raw results in range and aggregates them into
// fixed-width buckets, computing exact percentiles and jitter per bucket.
func (s *Store) bucketsFromRaw(ip string, start, end time.Time, bucketSec int64) ([]Bucket, error) {
	rows, err := s.db.Query(
		`SELECT timestamp, success, rtt_ns FROM ping_results
		  WHERE ip = ? AND timestamp >= ? AND timestamp < ?
		  ORDER BY timestamp ASC, id ASC`,
		ip, start.UTC(), end.UTC(),
	)
	if err != nil {
		return nil, fmt.Errorf("query raw series for %q: %w", ip, err)
	}
	defer rows.Close()

	var (
		buckets   []Bucket
		curBucket int64 = -1
		acc       bucketAccumulator
	)
	flush := func() {
		if curBucket >= 0 {
			buckets = append(buckets, acc.finalize(time.Unix(curBucket*bucketSec, 0).UTC()))
		}
	}

	for rows.Next() {
		var (
			ts      time.Time
			success int
			rtt     int64
		)
		if err := rows.Scan(&ts, &success, &rtt); err != nil {
			return nil, fmt.Errorf("scan raw result: %w", err)
		}
		b := ts.Unix() / bucketSec
		if b != curBucket {
			flush()
			curBucket = b
			acc = bucketAccumulator{}
		}
		acc.add(success != 0, rtt)
	}
	flush()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if buckets == nil {
		buckets = []Bucket{}
	}
	return buckets, nil
}

// bucketAccumulator collects per-bucket samples.
type bucketAccumulator struct {
	sent     int
	received int
	rtts     []int64 // successful RTTs in arrival order
}

func (a *bucketAccumulator) add(success bool, rtt int64) {
	a.sent++
	if success {
		a.received++
		a.rtts = append(a.rtts, rtt)
	}
}

func (a *bucketAccumulator) finalize(bucketStart time.Time) Bucket {
	b := Bucket{Timestamp: bucketStart, Sent: a.sent, Received: a.received}
	if a.sent > 0 {
		b.LossPct = float64(a.sent-a.received) / float64(a.sent) * 100
	} else {
		b.LossPct = 100
	}
	if len(a.rtts) == 0 {
		return b
	}

	b.JitterNs = meanAbsSuccessiveDiff(a.rtts)

	sorted := make([]int64, len(a.rtts))
	copy(sorted, a.rtts)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	b.MinRTTNs = sorted[0]
	b.MaxRTTNs = sorted[len(sorted)-1]
	var sum int64
	for _, v := range sorted {
		sum += v
	}
	b.AvgRTTNs = sum / int64(len(sorted))
	b.P50RTTNs = nearestRank(sorted, 0.50)
	b.P95RTTNs = nearestRank(sorted, 0.95)
	b.P99RTTNs = nearestRank(sorted, 0.99)
	return b
}

// nearestRank returns the nearest-rank percentile of a sorted slice.
func nearestRank(sorted []int64, p float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(p*float64(len(sorted)) + 0.9999999) // ceil
	if rank < 1 {
		rank = 1
	}
	if rank > len(sorted) {
		rank = len(sorted)
	}
	return sorted[rank-1]
}

// meanAbsSuccessiveDiff computes mean(|rtt[i] - rtt[i-1]|) over arrival order.
func meanAbsSuccessiveDiff(rtts []int64) int64 {
	if len(rtts) < 2 {
		return 0
	}
	var sum int64
	for i := 1; i < len(rtts); i++ {
		d := rtts[i] - rtts[i-1]
		if d < 0 {
			d = -d
		}
		sum += d
	}
	return sum / int64(len(rtts)-1)
}

// bucketsFromRollup reads precomputed buckets from a rollup table. p50/p99/jitter
// are not materialized in rollups and are reported as zero.
func (s *Store) bucketsFromRollup(table, ip string, start, end time.Time) ([]Bucket, error) {
	//nolint:gosec // table is a fixed internal constant, never user input.
	rows, err := s.db.Query(
		`SELECT bucket_start, sent, received, min_rtt_ns, avg_rtt_ns, max_rtt_ns, p95_rtt_ns
		   FROM `+table+`
		  WHERE ip = ? AND bucket_start >= ? AND bucket_start < ?
		  ORDER BY bucket_start ASC`,
		ip, start.UTC(), end.UTC(),
	)
	if err != nil {
		return nil, fmt.Errorf("query rollup %s for %q: %w", table, ip, err)
	}
	defer rows.Close()

	buckets := []Bucket{}
	for rows.Next() {
		var b Bucket
		if err := rows.Scan(&b.Timestamp, &b.Sent, &b.Received, &b.MinRTTNs, &b.AvgRTTNs, &b.MaxRTTNs, &b.P95RTTNs); err != nil {
			return nil, fmt.Errorf("scan rollup row: %w", err)
		}
		if b.Sent > 0 {
			b.LossPct = float64(b.Sent-b.Received) / float64(b.Sent) * 100
		} else {
			b.LossPct = 100
		}
		buckets = append(buckets, b)
	}
	return buckets, rows.Err()
}
