package store

import (
	"database/sql"
	"fmt"
	"time"
)

// Default per-host ping configuration. These mirror the pinger package defaults
// but are expressed in milliseconds for human-friendly API use.
const (
	DefaultIntervalMs = 1000
	DefaultTimeoutMs  = 2000
	DefaultPacketSize = 56
)

// HostConfig holds the per-host ping parameters (durations in milliseconds).
type HostConfig struct {
	IntervalMs int `json:"intervalMs"`
	TimeoutMs  int `json:"timeoutMs"`
	PacketSize int `json:"packetSize"`
}

// DefaultHostConfig returns the default per-host ping configuration.
func DefaultHostConfig() HostConfig {
	return HostConfig{
		IntervalMs: DefaultIntervalMs,
		TimeoutMs:  DefaultTimeoutMs,
		PacketSize: DefaultPacketSize,
	}
}

// Host is the full persisted record for a monitored host, including metadata,
// tags, group membership, config, and alert thresholds.
type Host struct {
	IP             string     `json:"ip"`
	AddedAt        time.Time  `json:"addedAt"`
	UpdatedAt      *time.Time `json:"updatedAt,omitempty"`
	DisplayName    string     `json:"displayName"`
	Notes          string     `json:"notes"`
	Tags           []string   `json:"tags"`
	Groups         []int64    `json:"groups"`
	Config         HostConfig `json:"config"`
	AlertLatencyMs int        `json:"alertLatencyMs"`
	AlertLossPct   float64    `json:"alertLossPct"`
}

// AddHostWithConfig records an IP as monitored with the given ping config. It is
// idempotent: an existing host's config is left untouched.
func (s *Store) AddHostWithConfig(ip string, cfg HostConfig) error {
	now := time.Now().UTC()
	_, err := s.db.Exec(
		`INSERT INTO hosts (ip, added_at, interval_ms, timeout_ms, packet_size, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(ip) DO NOTHING`,
		ip, now, cfg.IntervalMs, cfg.TimeoutMs, cfg.PacketSize, now,
	)
	if err != nil {
		return fmt.Errorf("add host %q: %w", ip, err)
	}
	return nil
}

// UpdateHostConfig updates the per-host ping configuration.
func (s *Store) UpdateHostConfig(ip string, cfg HostConfig) error {
	res, err := s.db.Exec(
		`UPDATE hosts SET interval_ms = ?, timeout_ms = ?, packet_size = ?, updated_at = ?
		  WHERE ip = ?`,
		cfg.IntervalMs, cfg.TimeoutMs, cfg.PacketSize, time.Now().UTC(), ip,
	)
	if err != nil {
		return fmt.Errorf("update config for %q: %w", ip, err)
	}
	return notFoundIfNoRows(res, ip)
}

// UpdateHostMeta updates the display name and notes for a host.
func (s *Store) UpdateHostMeta(ip, displayName, notes string) error {
	res, err := s.db.Exec(
		`UPDATE hosts SET display_name = ?, notes = ?, updated_at = ? WHERE ip = ?`,
		displayName, notes, time.Now().UTC(), ip,
	)
	if err != nil {
		return fmt.Errorf("update meta for %q: %w", ip, err)
	}
	return notFoundIfNoRows(res, ip)
}

// UpdateHostAlerts updates the alert thresholds for a host. A zero value
// disables that threshold.
func (s *Store) UpdateHostAlerts(ip string, latencyMs int, lossPct float64) error {
	res, err := s.db.Exec(
		`UPDATE hosts SET alert_latency_ms = ?, alert_loss_pct = ?, updated_at = ? WHERE ip = ?`,
		latencyMs, lossPct, time.Now().UTC(), ip,
	)
	if err != nil {
		return fmt.Errorf("update alerts for %q: %w", ip, err)
	}
	return notFoundIfNoRows(res, ip)
}

// SetHostTags replaces the full set of tags for a host atomically.
func (s *Store) SetHostTags(ip string, tags []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM host_tags WHERE ip = ?`, ip); err != nil {
		tx.Rollback()
		return fmt.Errorf("clear tags for %q: %w", ip, err)
	}
	for _, tag := range tags {
		if tag == "" {
			continue
		}
		if _, err := tx.Exec(
			`INSERT INTO host_tags (ip, tag) VALUES (?, ?) ON CONFLICT DO NOTHING`,
			ip, tag,
		); err != nil {
			tx.Rollback()
			return fmt.Errorf("insert tag %q for %q: %w", tag, ip, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tags: %w", err)
	}
	return nil
}

// GetHost returns the full record for a single host.
func (s *Store) GetHost(ip string) (Host, error) {
	var (
		h         Host
		updatedAt sql.NullTime
	)
	err := s.rdb.QueryRow(
		`SELECT ip, added_at, updated_at, display_name, notes,
		        interval_ms, timeout_ms, packet_size, alert_latency_ms, alert_loss_pct
		   FROM hosts WHERE ip = ?`,
		ip,
	).Scan(
		&h.IP, &h.AddedAt, &updatedAt, &h.DisplayName, &h.Notes,
		&h.Config.IntervalMs, &h.Config.TimeoutMs, &h.Config.PacketSize,
		&h.AlertLatencyMs, &h.AlertLossPct,
	)
	if err == sql.ErrNoRows {
		return Host{}, ErrNotFound
	}
	if err != nil {
		return Host{}, fmt.Errorf("get host %q: %w", ip, err)
	}
	if updatedAt.Valid {
		t := updatedAt.Time
		h.UpdatedAt = &t
	}

	tags, err := s.hostTags(ip)
	if err != nil {
		return Host{}, err
	}
	h.Tags = tags

	groups, err := s.hostGroupIDs(ip)
	if err != nil {
		return Host{}, err
	}
	h.Groups = groups
	return h, nil
}

// ListHostsFull returns every monitored host with metadata, tags, and groups
// assembled, oldest first. Tags and groups are fetched in bulk to avoid N+1.
func (s *Store) ListHostsFull() ([]Host, error) {
	defer s.measure()()
	rows, err := s.rdb.Query(
		`SELECT ip, added_at, updated_at, display_name, notes,
		        interval_ms, timeout_ms, packet_size, alert_latency_ms, alert_loss_pct
		   FROM hosts ORDER BY added_at ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list hosts: %w", err)
	}
	defer rows.Close()

	hosts := make([]Host, 0)
	index := make(map[string]int)
	for rows.Next() {
		var (
			h         Host
			updatedAt sql.NullTime
		)
		if err := rows.Scan(
			&h.IP, &h.AddedAt, &updatedAt, &h.DisplayName, &h.Notes,
			&h.Config.IntervalMs, &h.Config.TimeoutMs, &h.Config.PacketSize,
			&h.AlertLatencyMs, &h.AlertLossPct,
		); err != nil {
			return nil, fmt.Errorf("scan host: %w", err)
		}
		if updatedAt.Valid {
			t := updatedAt.Time
			h.UpdatedAt = &t
		}
		h.Tags = []string{}
		h.Groups = []int64{}
		index[h.IP] = len(hosts)
		hosts = append(hosts, h)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Bulk-load tags.
	tagRows, err := s.rdb.Query(`SELECT ip, tag FROM host_tags ORDER BY tag ASC`)
	if err != nil {
		return nil, fmt.Errorf("list host tags: %w", err)
	}
	defer tagRows.Close()
	for tagRows.Next() {
		var ip, tag string
		if err := tagRows.Scan(&ip, &tag); err != nil {
			return nil, fmt.Errorf("scan host tag: %w", err)
		}
		if i, ok := index[ip]; ok {
			hosts[i].Tags = append(hosts[i].Tags, tag)
		}
	}
	if err := tagRows.Err(); err != nil {
		return nil, err
	}

	// Bulk-load group memberships.
	grpRows, err := s.rdb.Query(`SELECT ip, group_id FROM host_groups ORDER BY group_id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list host groups: %w", err)
	}
	defer grpRows.Close()
	for grpRows.Next() {
		var (
			ip  string
			gid int64
		)
		if err := grpRows.Scan(&ip, &gid); err != nil {
			return nil, fmt.Errorf("scan host group: %w", err)
		}
		if i, ok := index[ip]; ok {
			hosts[i].Groups = append(hosts[i].Groups, gid)
		}
	}
	return hosts, grpRows.Err()
}

// hostTags returns the tags for a single host, sorted.
func (s *Store) hostTags(ip string) ([]string, error) {
	rows, err := s.rdb.Query(`SELECT tag FROM host_tags WHERE ip = ? ORDER BY tag ASC`, ip)
	if err != nil {
		return nil, fmt.Errorf("get tags for %q: %w", ip, err)
	}
	defer rows.Close()
	tags := []string{}
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			return nil, fmt.Errorf("scan tag: %w", err)
		}
		tags = append(tags, tag)
	}
	return tags, rows.Err()
}

// hostGroupIDs returns the group IDs a host belongs to.
func (s *Store) hostGroupIDs(ip string) ([]int64, error) {
	rows, err := s.rdb.Query(`SELECT group_id FROM host_groups WHERE ip = ? ORDER BY group_id ASC`, ip)
	if err != nil {
		return nil, fmt.Errorf("get groups for %q: %w", ip, err)
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan group id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
