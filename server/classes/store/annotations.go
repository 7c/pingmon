package store

import (
	"database/sql"
	"fmt"
	"time"
)

// Annotation types.
const (
	AnnotationComment     = "comment"
	AnnotationIncident    = "incident"
	AnnotationMaintenance = "maintenance"
)

// Annotation is a user comment/incident/maintenance marker pinned to a point or
// range on a host's time axis. A nil IP denotes a global annotation.
type Annotation struct {
	ID        int64      `json:"id"`
	IP        *string    `json:"ip"`
	Type      string     `json:"type"`
	Title     string     `json:"title"`
	Text      string     `json:"text"`
	Color     string     `json:"color"`
	Author    string     `json:"author"`
	StartTs   time.Time  `json:"startTs"`
	EndTs     *time.Time `json:"endTs,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
}

// CreateAnnotation inserts a new annotation and returns it with its ID set.
func (s *Store) CreateAnnotation(a Annotation) (Annotation, error) {
	now := time.Now().UTC()
	a.CreatedAt = now
	a.UpdatedAt = now
	if a.Type == "" {
		a.Type = AnnotationComment
	}
	res, err := s.db.Exec(
		`INSERT INTO annotations (ip, type, title, text, color, author, start_ts, end_ts, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		nullString(a.IP), a.Type, a.Title, a.Text, a.Color, a.Author,
		a.StartTs.UTC(), nullTime(a.EndTs), now, now,
	)
	if err != nil {
		return Annotation{}, fmt.Errorf("create annotation: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Annotation{}, fmt.Errorf("annotation id: %w", err)
	}
	a.ID = id
	return a, nil
}

// UpdateAnnotation overwrites the mutable fields of an annotation.
func (s *Store) UpdateAnnotation(a Annotation) error {
	res, err := s.db.Exec(
		`UPDATE annotations SET type = ?, title = ?, text = ?, color = ?, author = ?,
		        start_ts = ?, end_ts = ?, updated_at = ? WHERE id = ?`,
		a.Type, a.Title, a.Text, a.Color, a.Author,
		a.StartTs.UTC(), nullTime(a.EndTs), time.Now().UTC(), a.ID,
	)
	if err != nil {
		return fmt.Errorf("update annotation %d: %w", a.ID, err)
	}
	return notFoundIfNoRows(res, "")
}

// DeleteAnnotation removes an annotation by ID.
func (s *Store) DeleteAnnotation(id int64) error {
	res, err := s.db.Exec(`DELETE FROM annotations WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete annotation %d: %w", id, err)
	}
	return notFoundIfNoRows(res, "")
}

// GetAnnotation returns a single annotation.
func (s *Store) GetAnnotation(id int64) (Annotation, error) {
	row := s.rdb.QueryRow(
		`SELECT id, ip, type, title, text, color, author, start_ts, end_ts, created_at, updated_at
		   FROM annotations WHERE id = ?`, id,
	)
	a, err := scanAnnotation(row)
	if err == sql.ErrNoRows {
		return Annotation{}, ErrNotFound
	}
	if err != nil {
		return Annotation{}, fmt.Errorf("get annotation %d: %w", id, err)
	}
	return a, nil
}

// ListAnnotations returns annotations for an IP that overlap [start, end],
// optionally filtered by type. An annotation overlaps the window when its start
// is before end and its end (or start, for point annotations) is at/after start.
func (s *Store) ListAnnotations(ip string, start, end time.Time, types []string) ([]Annotation, error) {
	defer s.measure()()
	query := `SELECT id, ip, type, title, text, color, author, start_ts, end_ts, created_at, updated_at
	            FROM annotations
	           WHERE ip = ? AND start_ts < ? AND COALESCE(end_ts, start_ts) >= ?`
	args := []interface{}{ip, end.UTC(), start.UTC()}

	if len(types) > 0 {
		placeholders := ""
		for i, t := range types {
			if i > 0 {
				placeholders += ","
			}
			placeholders += "?"
			args = append(args, t)
		}
		query += " AND type IN (" + placeholders + ")"
	}
	query += " ORDER BY start_ts ASC"

	rows, err := s.rdb.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list annotations for %q: %w", ip, err)
	}
	defer rows.Close()

	out := make([]Annotation, 0)
	for rows.Next() {
		a, err := scanAnnotation(rows)
		if err != nil {
			return nil, fmt.Errorf("scan annotation: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// rowScanner abstracts *sql.Row and *sql.Rows for shared scanning.
type rowScanner interface {
	Scan(dest ...interface{}) error
}

func scanAnnotation(sc rowScanner) (Annotation, error) {
	var (
		a     Annotation
		ip    sql.NullString
		endTs sql.NullTime
	)
	if err := sc.Scan(
		&a.ID, &ip, &a.Type, &a.Title, &a.Text, &a.Color, &a.Author,
		&a.StartTs, &endTs, &a.CreatedAt, &a.UpdatedAt,
	); err != nil {
		return Annotation{}, err
	}
	if ip.Valid {
		s := ip.String
		a.IP = &s
	}
	if endTs.Valid {
		t := endTs.Time
		a.EndTs = &t
	}
	return a, nil
}

func nullString(s *string) interface{} {
	if s == nil {
		return nil
	}
	return *s
}

func nullTime(t *time.Time) interface{} {
	if t == nil {
		return nil
	}
	return t.UTC()
}
