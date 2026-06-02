package store

import (
	"database/sql"
	"fmt"
	"time"
)

// Rollup tiers.
const (
	tierHourly = "hourly"
	tierDaily  = "daily"
)

// maxRollupWindow bounds how much history a single rollup run processes, so a
// first run on a large database (or after long downtime) does not stall. The
// scheduler catches up over subsequent ticks.
const maxRollupWindow = 7 * 24 * time.Hour

// RunHourlyRollup aggregates finalized hourly buckets of raw ping_results into
// ping_rollup_hourly. Buckets within `safety` of now are left untouched so the
// current, still-filling bucket is never rolled up prematurely. Idempotent.
func (s *Store) RunHourlyRollup(now time.Time, safety time.Duration) error {
	winEnd := now.UTC().Add(-safety).Truncate(time.Hour)

	winStart, ok, err := s.getWatermark(tierHourly)
	if err != nil {
		return err
	}
	if !ok {
		earliest, has, err := s.earliestRawTimestamp()
		if err != nil {
			return err
		}
		if !has {
			return nil // no raw data yet
		}
		winStart = earliest.Truncate(time.Hour)
	}
	if winStart.Before(winEnd.Add(-maxRollupWindow)) {
		winStart = winEnd.Add(-maxRollupWindow)
	}
	if !winStart.Before(winEnd) {
		return nil // nothing finalized to roll up
	}

	if err := s.aggregateHourlyFromRaw(winStart, winEnd); err != nil {
		return err
	}
	return s.setWatermark(tierHourly, winEnd)
}

// RunDailyRollup aggregates finalized daily buckets from the hourly rollups
// (so daily data survives raw pruning). Bounded by the hourly watermark.
func (s *Store) RunDailyRollup(now time.Time, safety time.Duration) error {
	hourlyMark, ok, err := s.getWatermark(tierHourly)
	if err != nil {
		return err
	}
	winEnd := now.UTC().Add(-safety).Truncate(24 * time.Hour)
	if ok && hourlyMark.Before(winEnd) {
		winEnd = hourlyMark.Truncate(24 * time.Hour)
	}

	winStart, ok, err := s.getWatermark(tierDaily)
	if err != nil {
		return err
	}
	if !ok {
		earliest, has, err := s.earliestHourlyBucket()
		if err != nil {
			return err
		}
		if !has {
			return nil
		}
		winStart = earliest.Truncate(24 * time.Hour)
	}
	if winStart.Before(winEnd.Add(-maxRollupWindow)) {
		winStart = winEnd.Add(-maxRollupWindow)
	}
	if !winStart.Before(winEnd) {
		return nil
	}

	if err := s.aggregateDailyFromHourly(winStart, winEnd); err != nil {
		return err
	}
	return s.setWatermark(tierDaily, winEnd)
}

// PruneRawResults deletes raw results older than the retention window, but never
// deletes data that has not yet been rolled up to hourly. retain <= 0 disables
// pruning. Returns the number of rows deleted.
func (s *Store) PruneRawResults(now time.Time, retain time.Duration) (int64, error) {
	if retain <= 0 {
		return 0, nil
	}
	cutoff := now.UTC().Add(-retain)

	if mark, ok, err := s.getWatermark(tierHourly); err != nil {
		return 0, err
	} else if ok && mark.Before(cutoff) {
		cutoff = mark // do not prune beyond what has been rolled up
	} else if !ok {
		return 0, nil // nothing rolled up yet; keep all raw
	}

	res, err := s.db.Exec(`DELETE FROM ping_results WHERE timestamp < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("prune raw results: %w", err)
	}
	return res.RowsAffected()
}

// aggregateHourlyFromRaw computes per-(ip, hour) stats from raw rows and upserts.
func (s *Store) aggregateHourlyFromRaw(winStart, winEnd time.Time) error {
	rows, err := s.db.Query(
		`SELECT ip, timestamp, success, rtt_ns FROM ping_results
		  WHERE timestamp >= ? AND timestamp < ?
		  ORDER BY ip ASC, timestamp ASC, id ASC`,
		winStart, winEnd,
	)
	if err != nil {
		return fmt.Errorf("scan raw for hourly rollup: %w", err)
	}
	defer rows.Close()

	type key struct {
		ip     string
		bucket int64
	}
	var (
		curKey  key
		hasCur  bool
		acc     bucketAccumulator
		upserts []hourlyUpsert
	)
	flush := func() {
		if !hasCur {
			return
		}
		b := acc.finalize(time.Unix(curKey.bucket, 0).UTC())
		upserts = append(upserts, hourlyUpsert{ip: curKey.ip, bucketStart: b.Timestamp, b: b})
	}

	for rows.Next() {
		var (
			ip      string
			ts      time.Time
			success int
			rtt     int64
		)
		if err := rows.Scan(&ip, &ts, &success, &rtt); err != nil {
			return fmt.Errorf("scan raw row: %w", err)
		}
		k := key{ip: ip, bucket: ts.Unix() / 3600 * 3600}
		if !hasCur || k != curKey {
			flush()
			curKey = k
			hasCur = true
			acc = bucketAccumulator{}
		}
		acc.add(success != 0, rtt)
	}
	flush()
	if err := rows.Err(); err != nil {
		return err
	}

	return s.commitHourlyUpserts(upserts)
}

type hourlyUpsert struct {
	ip          string
	bucketStart time.Time
	b           Bucket
}

func (s *Store) commitHourlyUpserts(upserts []hourlyUpsert) error {
	if len(upserts) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin hourly tx: %w", err)
	}
	stmt, err := tx.Prepare(
		`INSERT INTO ping_rollup_hourly (ip, bucket_start, sent, received, min_rtt_ns, avg_rtt_ns, max_rtt_ns, p95_rtt_ns)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(ip, bucket_start) DO UPDATE SET
		   sent=excluded.sent, received=excluded.received,
		   min_rtt_ns=excluded.min_rtt_ns, avg_rtt_ns=excluded.avg_rtt_ns,
		   max_rtt_ns=excluded.max_rtt_ns, p95_rtt_ns=excluded.p95_rtt_ns`,
	)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("prepare hourly upsert: %w", err)
	}
	defer stmt.Close()
	for _, u := range upserts {
		if _, err := stmt.Exec(u.ip, u.bucketStart, u.b.Sent, u.b.Received,
			u.b.MinRTTNs, u.b.AvgRTTNs, u.b.MaxRTTNs, u.b.P95RTTNs); err != nil {
			tx.Rollback()
			return fmt.Errorf("hourly upsert: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit hourly rollup: %w", err)
	}
	return nil
}

// aggregateDailyFromHourly rolls hourly buckets into daily buckets. Daily p95 is
// approximated as the max of the day's hourly p95 values (documented).
func (s *Store) aggregateDailyFromHourly(winStart, winEnd time.Time) error {
	rows, err := s.db.Query(
		`SELECT ip, bucket_start, sent, received, min_rtt_ns, avg_rtt_ns, max_rtt_ns, p95_rtt_ns
		   FROM ping_rollup_hourly
		  WHERE bucket_start >= ? AND bucket_start < ?
		  ORDER BY ip ASC, bucket_start ASC`,
		winStart, winEnd,
	)
	if err != nil {
		return fmt.Errorf("scan hourly for daily rollup: %w", err)
	}
	defer rows.Close()

	type key struct {
		ip     string
		bucket int64
	}
	var (
		curKey  key
		hasCur  bool
		acc     dailyAccumulator
		upserts []hourlyUpsert
	)
	flush := func() {
		if !hasCur {
			return
		}
		upserts = append(upserts, hourlyUpsert{
			ip:          curKey.ip,
			bucketStart: time.Unix(curKey.bucket, 0).UTC(),
			b:           acc.finalize(),
		})
	}

	for rows.Next() {
		var (
			ip                         string
			bucketStart                time.Time
			sent, recv                 int
			minNs, avgNs, maxNs, p95Ns int64
		)
		if err := rows.Scan(&ip, &bucketStart, &sent, &recv, &minNs, &avgNs, &maxNs, &p95Ns); err != nil {
			return fmt.Errorf("scan hourly row: %w", err)
		}
		dayBucket := bucketStart.Unix() / 86400 * 86400
		k := key{ip: ip, bucket: dayBucket}
		if !hasCur || k != curKey {
			flush()
			curKey = k
			hasCur = true
			acc = dailyAccumulator{}
		}
		acc.add(sent, recv, minNs, avgNs, maxNs, p95Ns)
	}
	flush()
	if err := rows.Err(); err != nil {
		return err
	}

	return s.commitDailyUpserts(upserts)
}

func (s *Store) commitDailyUpserts(upserts []hourlyUpsert) error {
	if len(upserts) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin daily tx: %w", err)
	}
	stmt, err := tx.Prepare(
		`INSERT INTO ping_rollup_daily (ip, bucket_start, sent, received, min_rtt_ns, avg_rtt_ns, max_rtt_ns, p95_rtt_ns)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(ip, bucket_start) DO UPDATE SET
		   sent=excluded.sent, received=excluded.received,
		   min_rtt_ns=excluded.min_rtt_ns, avg_rtt_ns=excluded.avg_rtt_ns,
		   max_rtt_ns=excluded.max_rtt_ns, p95_rtt_ns=excluded.p95_rtt_ns`,
	)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("prepare daily upsert: %w", err)
	}
	defer stmt.Close()
	for _, u := range upserts {
		if _, err := stmt.Exec(u.ip, u.bucketStart, u.b.Sent, u.b.Received,
			u.b.MinRTTNs, u.b.AvgRTTNs, u.b.MaxRTTNs, u.b.P95RTTNs); err != nil {
			tx.Rollback()
			return fmt.Errorf("daily upsert: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit daily rollup: %w", err)
	}
	return nil
}

// dailyAccumulator combines hourly buckets into a day.
type dailyAccumulator struct {
	sent       int
	received   int
	minNs      int64
	maxNs      int64
	p95Ns      int64
	avgWeight  int64 // sum(avg * received)
	avgDivisor int64 // sum(received)
	haveMin    bool
}

func (a *dailyAccumulator) add(sent, recv int, minNs, avgNs, maxNs, p95Ns int64) {
	a.sent += sent
	a.received += recv
	if recv > 0 {
		if !a.haveMin || minNs < a.minNs {
			a.minNs = minNs
			a.haveMin = true
		}
		if maxNs > a.maxNs {
			a.maxNs = maxNs
		}
		if p95Ns > a.p95Ns {
			a.p95Ns = p95Ns
		}
		a.avgWeight += avgNs * int64(recv)
		a.avgDivisor += int64(recv)
	}
}

func (a *dailyAccumulator) finalize() Bucket {
	b := Bucket{Sent: a.sent, Received: a.received, MinRTTNs: a.minNs, MaxRTTNs: a.maxNs, P95RTTNs: a.p95Ns}
	if a.avgDivisor > 0 {
		b.AvgRTTNs = a.avgWeight / a.avgDivisor
	}
	if a.sent > 0 {
		b.LossPct = float64(a.sent-a.received) / float64(a.sent) * 100
	} else {
		b.LossPct = 100
	}
	return b
}

// --- watermark + bounds helpers ---

func (s *Store) getWatermark(tier string) (time.Time, bool, error) {
	var t time.Time
	err := s.db.QueryRow(`SELECT last_bucket_end FROM rollup_state WHERE tier = ?`, tier).Scan(&t)
	if err == sql.ErrNoRows {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("get watermark %s: %w", tier, err)
	}
	return t.UTC(), true, nil
}

func (s *Store) setWatermark(tier string, t time.Time) error {
	_, err := s.db.Exec(
		`INSERT INTO rollup_state (tier, last_bucket_end) VALUES (?, ?)
		 ON CONFLICT(tier) DO UPDATE SET last_bucket_end = excluded.last_bucket_end`,
		tier, t.UTC(),
	)
	if err != nil {
		return fmt.Errorf("set watermark %s: %w", tier, err)
	}
	return nil
}

func (s *Store) earliestRawTimestamp() (time.Time, bool, error) {
	return s.earliestTimestamp(`SELECT MIN(timestamp) FROM ping_results`)
}

func (s *Store) earliestHourlyBucket() (time.Time, bool, error) {
	return s.earliestTimestamp(`SELECT MIN(bucket_start) FROM ping_rollup_hourly`)
}

func (s *Store) earliestTimestamp(query string) (time.Time, bool, error) {
	// Aggregate results (MIN) lose column type affinity in modernc-sqlite and
	// come back as text, so scan a string and parse it ourselves.
	var raw sql.NullString
	if err := s.db.QueryRow(query).Scan(&raw); err != nil {
		return time.Time{}, false, fmt.Errorf("earliest timestamp: %w", err)
	}
	if !raw.Valid || raw.String == "" {
		return time.Time{}, false, nil
	}
	t, err := parseDBTime(raw.String)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("parse earliest timestamp %q: %w", raw.String, err)
	}
	return t.UTC(), true, nil
}

// parseDBTime parses a timestamp string as stored by the sqlite driver, trying
// the common layouts.
func parseDBTime(s string) (time.Time, error) {
	layouts := []string{
		"2006-01-02 15:04:05.999999999 -0700 MST", // Go's default time.Time text form
		"2006-01-02 15:04:05 -0700 MST",
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05-07:00",
		time.RFC3339Nano,
		time.RFC3339,
	}
	var firstErr error
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, nil
		} else if firstErr == nil {
			firstErr = err
		}
	}
	return time.Time{}, firstErr
}
