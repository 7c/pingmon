package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Group is a named collection of hosts.
type Group struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name"`
	Color       string     `json:"color"`
	Description string     `json:"description"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   *time.Time `json:"updatedAt,omitempty"`
}

// CreateGroup inserts a new group. Returns ErrConflict if the name is taken.
func (s *Store) CreateGroup(name, color, description string) (Group, error) {
	now := time.Now().UTC()
	res, err := s.db.Exec(
		`INSERT INTO groups (name, color, description, created_at) VALUES (?, ?, ?, ?)`,
		name, color, description, now,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return Group{}, ErrConflict
		}
		return Group{}, fmt.Errorf("create group %q: %w", name, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Group{}, fmt.Errorf("group id: %w", err)
	}
	return Group{ID: id, Name: name, Color: color, Description: description, CreatedAt: now}, nil
}

// UpdateGroup updates a group's name, color, and description.
func (s *Store) UpdateGroup(id int64, name, color, description string) error {
	res, err := s.db.Exec(
		`UPDATE groups SET name = ?, color = ?, description = ?, updated_at = ? WHERE id = ?`,
		name, color, description, time.Now().UTC(), id,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrConflict
		}
		return fmt.Errorf("update group %d: %w", id, err)
	}
	return notFoundIfNoRows(res, "")
}

// DeleteGroup removes a group; host memberships cascade away.
func (s *Store) DeleteGroup(id int64) error {
	res, err := s.db.Exec(`DELETE FROM groups WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete group %d: %w", id, err)
	}
	return notFoundIfNoRows(res, "")
}

// GetGroup returns a single group.
func (s *Store) GetGroup(id int64) (Group, error) {
	var (
		g         Group
		updatedAt sql.NullTime
	)
	err := s.rdb.QueryRow(
		`SELECT id, name, color, description, created_at, updated_at FROM groups WHERE id = ?`, id,
	).Scan(&g.ID, &g.Name, &g.Color, &g.Description, &g.CreatedAt, &updatedAt)
	if err == sql.ErrNoRows {
		return Group{}, ErrNotFound
	}
	if err != nil {
		return Group{}, fmt.Errorf("get group %d: %w", id, err)
	}
	if updatedAt.Valid {
		t := updatedAt.Time
		g.UpdatedAt = &t
	}
	return g, nil
}

// ListGroups returns all groups ordered by name.
func (s *Store) ListGroups() ([]Group, error) {
	rows, err := s.rdb.Query(
		`SELECT id, name, color, description, created_at, updated_at FROM groups ORDER BY name ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list groups: %w", err)
	}
	defer rows.Close()

	groups := make([]Group, 0)
	for rows.Next() {
		var (
			g         Group
			updatedAt sql.NullTime
		)
		if err := rows.Scan(&g.ID, &g.Name, &g.Color, &g.Description, &g.CreatedAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan group: %w", err)
		}
		if updatedAt.Valid {
			t := updatedAt.Time
			g.UpdatedAt = &t
		}
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

// ListHostsInGroup returns the IPs belonging to a group.
func (s *Store) ListHostsInGroup(id int64) ([]string, error) {
	rows, err := s.rdb.Query(
		`SELECT h.ip FROM host_groups hg JOIN hosts h ON h.ip = hg.ip
		  WHERE hg.group_id = ? ORDER BY h.added_at ASC`, id,
	)
	if err != nil {
		return nil, fmt.Errorf("list hosts in group %d: %w", id, err)
	}
	defer rows.Close()
	ips := []string{}
	for rows.Next() {
		var ip string
		if err := rows.Scan(&ip); err != nil {
			return nil, fmt.Errorf("scan ip: %w", err)
		}
		ips = append(ips, ip)
	}
	return ips, rows.Err()
}

// AssignHostToGroup adds a host to a group. Idempotent.
func (s *Store) AssignHostToGroup(ip string, groupID int64) error {
	_, err := s.db.Exec(
		`INSERT INTO host_groups (ip, group_id) VALUES (?, ?) ON CONFLICT DO NOTHING`,
		ip, groupID,
	)
	if err != nil {
		return fmt.Errorf("assign %q to group %d: %w", ip, groupID, err)
	}
	return nil
}

// UnassignHostFromGroup removes a host from a group.
func (s *Store) UnassignHostFromGroup(ip string, groupID int64) error {
	_, err := s.db.Exec(`DELETE FROM host_groups WHERE ip = ? AND group_id = ?`, ip, groupID)
	if err != nil {
		return fmt.Errorf("unassign %q from group %d: %w", ip, groupID, err)
	}
	return nil
}

// isUniqueViolation reports whether err is a SQLite UNIQUE constraint failure.
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
