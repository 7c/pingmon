package store

import (
	"database/sql"
	"fmt"
	"time"
)

// Stats summarizes what is currently persisted in the store.
type Stats struct {
	Hosts        int        `json:"hosts"`
	Groups       int        `json:"groups"`
	Tags         int        `json:"tags"`        // host_tags rows
	Annotations  int        `json:"annotations"` // comments/incidents/maintenance
	Results      int64      `json:"results"`     // raw ping_results rows
	RollupHourly int64      `json:"rollupHourly"`
	RollupDaily  int64      `json:"rollupDaily"`
	Oldest       *time.Time `json:"oldest"` // earliest raw result timestamp
	Newest       *time.Time `json:"newest"` // latest raw result timestamp
}

// Stats returns a snapshot of stored-state counts.
func (s *Store) Stats() (Stats, error) {
	defer s.measure()()
	var st Stats
	counts := []struct {
		query string
		dst   interface{}
	}{
		{`SELECT COUNT(*) FROM hosts`, &st.Hosts},
		{`SELECT COUNT(*) FROM groups`, &st.Groups},
		{`SELECT COUNT(*) FROM host_tags`, &st.Tags},
		{`SELECT COUNT(*) FROM annotations`, &st.Annotations},
		{`SELECT COUNT(*) FROM ping_results`, &st.Results},
		{`SELECT COUNT(*) FROM ping_rollup_hourly`, &st.RollupHourly},
		{`SELECT COUNT(*) FROM ping_rollup_daily`, &st.RollupDaily},
	}
	for _, c := range counts {
		if err := s.rdb.QueryRow(c.query).Scan(c.dst); err != nil {
			return Stats{}, fmt.Errorf("stats count: %w", err)
		}
	}

	if t, ok, err := s.boundaryTimestamp(`SELECT MIN(timestamp) FROM ping_results`); err != nil {
		return Stats{}, err
	} else if ok {
		st.Oldest = &t
	}
	if t, ok, err := s.boundaryTimestamp(`SELECT MAX(timestamp) FROM ping_results`); err != nil {
		return Stats{}, err
	} else if ok {
		st.Newest = &t
	}
	return st, nil
}

// boundaryTimestamp scans a MIN/MAX(timestamp) aggregate (returned as text by
// the driver) and parses it.
func (s *Store) boundaryTimestamp(query string) (time.Time, bool, error) {
	var raw sql.NullString
	if err := s.rdb.QueryRow(query).Scan(&raw); err != nil {
		return time.Time{}, false, fmt.Errorf("boundary timestamp: %w", err)
	}
	if !raw.Valid || raw.String == "" {
		return time.Time{}, false, nil
	}
	t, err := parseDBTime(raw.String)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("parse boundary timestamp %q: %w", raw.String, err)
	}
	return t.UTC(), true, nil
}
