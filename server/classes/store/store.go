// Package store provides SQLite-backed persistence for monitored hosts and
// long-term ping result history.
package store

import (
	"database/sql"
	"fmt"
	"log"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const (
	// writeBufferSize bounds how many pending results queue before the ping
	// goroutines start blocking on the writer.
	writeBufferSize = 4096

	// flushBatchSize is the maximum number of results committed in a single
	// transaction by the background writer.
	flushBatchSize = 256

	// flushInterval forces a commit even when the batch is not full, so recent
	// results reach disk promptly.
	flushInterval = 1 * time.Second
)

// Result is a single persisted ping outcome for a host.
type Result struct {
	IP        string    `json:"ip"`
	Seq       int       `json:"seq"`
	Timestamp time.Time `json:"timestamp"`
	Success   bool      `json:"success"`
	RTTNanos  int64     `json:"rttNanos"`
	ErrMsg    string    `json:"errMsg,omitempty"`
}

// Store wraps a SQLite database and an asynchronous batched result writer.
//
// Writes use a single connection (db) because SQLite permits only one writer.
// Reads use a separate read-only pool (rdb) so heavy analytics queries run
// concurrently (WAL) instead of serializing behind the writer.
type Store struct {
	db  *sql.DB // single-writer connection (also used by the rollup job)
	rdb *sql.DB // read-only pool for queries

	reads readMetrics // read-query latency statistics

	results   chan Result
	flushReq  chan chan struct{}
	done      chan struct{}
	closeOnce sync.Once
	closeErr  error
	wg        sync.WaitGroup
}

// New opens (or creates) the SQLite database at path, applies migrations, and
// starts the background result writer.
func New(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite db %q: %w", path, err)
	}

	// A single writer connection avoids "database is locked" contention; SQLite
	// only allows one writer at a time regardless of pool size.
	db.SetMaxOpenConns(1)

	if err := applyPragmas(db); err != nil {
		db.Close()
		return nil, err
	}

	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}

	// Open a separate read-only pool now that the writer has created the file
	// and schema. WAL lets these readers run concurrently with the writer.
	rdb, err := sql.Open("sqlite", readDSN(path))
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("open read pool for %q: %w", path, err)
	}
	rdb.SetMaxOpenConns(maxReadConns)
	rdb.SetMaxIdleConns(maxReadConns)

	s := &Store{
		db:       db,
		rdb:      rdb,
		results:  make(chan Result, writeBufferSize),
		flushReq: make(chan chan struct{}),
		done:     make(chan struct{}),
	}

	s.wg.Add(1)
	go s.writeLoop()

	return s, nil
}

// applyPragmas configures SQLite for a write-heavy workload.
func applyPragmas(db *sql.DB) error {
	pragmas := []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA foreign_keys = ON",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			return fmt.Errorf("exec %q: %w", p, err)
		}
	}
	return nil
}

// migrate creates the schema if it does not already exist and applies additive
// column migrations to existing databases.
func migrate(db *sql.DB) error {
	if err := migrateBaseSchema(db); err != nil {
		return err
	}
	if err := migrateHostColumns(db); err != nil {
		return err
	}
	return nil
}

// migrateBaseSchema creates all tables and indexes. CREATE TABLE IF NOT EXISTS
// is safe on both fresh and existing databases.
func migrateBaseSchema(db *sql.DB) error {
	schema := `
CREATE TABLE IF NOT EXISTS hosts (
	ip       TEXT PRIMARY KEY,
	added_at TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS ping_results (
	id        INTEGER PRIMARY KEY AUTOINCREMENT,
	ip        TEXT NOT NULL,
	seq       INTEGER NOT NULL,
	timestamp TIMESTAMP NOT NULL,
	success   INTEGER NOT NULL,
	rtt_ns    INTEGER NOT NULL,
	err_msg   TEXT
);

CREATE INDEX IF NOT EXISTS idx_ping_results_ip_ts
	ON ping_results (ip, timestamp);

CREATE INDEX IF NOT EXISTS idx_ping_results_ts
	ON ping_results (timestamp);

CREATE TABLE IF NOT EXISTS host_tags (
	ip  TEXT NOT NULL,
	tag TEXT NOT NULL,
	PRIMARY KEY (ip, tag),
	FOREIGN KEY (ip) REFERENCES hosts(ip) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_host_tags_tag ON host_tags (tag);

CREATE TABLE IF NOT EXISTS groups (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	name        TEXT NOT NULL UNIQUE,
	color       TEXT NOT NULL DEFAULT '',
	description TEXT NOT NULL DEFAULT '',
	created_at  TIMESTAMP NOT NULL,
	updated_at  TIMESTAMP
);

CREATE TABLE IF NOT EXISTS host_groups (
	ip       TEXT NOT NULL,
	group_id INTEGER NOT NULL,
	PRIMARY KEY (ip, group_id),
	FOREIGN KEY (ip)       REFERENCES hosts(ip)  ON DELETE CASCADE,
	FOREIGN KEY (group_id) REFERENCES groups(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_host_groups_group ON host_groups (group_id);

CREATE TABLE IF NOT EXISTS annotations (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	ip         TEXT,
	type       TEXT NOT NULL DEFAULT 'comment',
	title      TEXT NOT NULL DEFAULT '',
	text       TEXT NOT NULL DEFAULT '',
	color      TEXT NOT NULL DEFAULT '',
	author     TEXT NOT NULL DEFAULT '',
	start_ts   TIMESTAMP NOT NULL,
	end_ts     TIMESTAMP,
	created_at TIMESTAMP NOT NULL,
	updated_at TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_annotations_ip_start ON annotations (ip, start_ts);

CREATE TABLE IF NOT EXISTS ping_rollup_hourly (
	ip           TEXT NOT NULL,
	bucket_start TIMESTAMP NOT NULL,
	sent         INTEGER NOT NULL,
	received     INTEGER NOT NULL,
	min_rtt_ns   INTEGER NOT NULL DEFAULT 0,
	avg_rtt_ns   INTEGER NOT NULL DEFAULT 0,
	max_rtt_ns   INTEGER NOT NULL DEFAULT 0,
	p95_rtt_ns   INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (ip, bucket_start)
);

CREATE TABLE IF NOT EXISTS ping_rollup_daily (
	ip           TEXT NOT NULL,
	bucket_start TIMESTAMP NOT NULL,
	sent         INTEGER NOT NULL,
	received     INTEGER NOT NULL,
	min_rtt_ns   INTEGER NOT NULL DEFAULT 0,
	avg_rtt_ns   INTEGER NOT NULL DEFAULT 0,
	max_rtt_ns   INTEGER NOT NULL DEFAULT 0,
	p95_rtt_ns   INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (ip, bucket_start)
);

CREATE TABLE IF NOT EXISTS rollup_state (
	tier            TEXT PRIMARY KEY,
	last_bucket_end TIMESTAMP NOT NULL
);
`
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("apply base schema: %w", err)
	}
	return nil
}

// migrateHostColumns adds the per-host configuration and metadata columns to an
// existing hosts table. Each ALTER is guarded so re-running is a no-op.
func migrateHostColumns(db *sql.DB) error {
	cols := []struct{ name, ddl string }{
		{"interval_ms", "interval_ms INTEGER NOT NULL DEFAULT 1000"},
		{"timeout_ms", "timeout_ms INTEGER NOT NULL DEFAULT 2000"},
		{"packet_size", "packet_size INTEGER NOT NULL DEFAULT 56"},
		{"display_name", "display_name TEXT NOT NULL DEFAULT ''"},
		{"notes", "notes TEXT NOT NULL DEFAULT ''"},
		{"alert_latency_ms", "alert_latency_ms INTEGER NOT NULL DEFAULT 0"},
		{"alert_loss_pct", "alert_loss_pct REAL NOT NULL DEFAULT 0"},
		{"updated_at", "updated_at TIMESTAMP"},
	}
	for _, c := range cols {
		if err := addColumnIfMissing(db, "hosts", c.name, c.ddl); err != nil {
			return err
		}
	}
	return nil
}

// columnExists reports whether a column is present on a table.
func columnExists(db *sql.DB, table, col string) (bool, error) {
	rows, err := db.Query(`SELECT 1 FROM pragma_table_info(?) WHERE name = ?`, table, col)
	if err != nil {
		return false, fmt.Errorf("check column %s.%s: %w", table, col, err)
	}
	defer rows.Close()
	return rows.Next(), rows.Err()
}

// addColumnIfMissing adds a column via ALTER TABLE only when it does not already
// exist. The ddl is the column definition (e.g. "foo INTEGER NOT NULL DEFAULT 0").
func addColumnIfMissing(db *sql.DB, table, col, ddl string) error {
	exists, err := columnExists(db, table, col)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	if _, err := db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s", table, ddl)); err != nil {
		return fmt.Errorf("add column %s.%s: %w", table, col, err)
	}
	return nil
}

// AddHost records an IP as monitored. It is idempotent.
func (s *Store) AddHost(ip string) error {
	_, err := s.db.Exec(
		`INSERT INTO hosts (ip, added_at) VALUES (?, ?)
		 ON CONFLICT(ip) DO NOTHING`,
		ip, time.Now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("add host %q: %w", ip, err)
	}
	return nil
}

// RemoveHost stops a host from being monitored. Historical ping results are
// retained for long-term analysis.
func (s *Store) RemoveHost(ip string) error {
	if _, err := s.db.Exec(`DELETE FROM hosts WHERE ip = ?`, ip); err != nil {
		return fmt.Errorf("remove host %q: %w", ip, err)
	}
	return nil
}

// ListHosts returns all monitored IPs, oldest first.
func (s *Store) ListHosts() ([]string, error) {
	rows, err := s.rdb.Query(`SELECT ip FROM hosts ORDER BY added_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("list hosts: %w", err)
	}
	defer rows.Close()

	var ips []string
	for rows.Next() {
		var ip string
		if err := rows.Scan(&ip); err != nil {
			return nil, fmt.Errorf("scan host: %w", err)
		}
		ips = append(ips, ip)
	}
	return ips, rows.Err()
}

// SaveResult enqueues a ping result for asynchronous persistence. It never
// blocks the caller for disk I/O; if the buffer is full the result is dropped
// and logged rather than stalling the ping loop.
func (s *Store) SaveResult(r Result) {
	select {
	case <-s.done:
		// Store is shutting down; discard.
	case s.results <- r:
	default:
		log.Printf("[STORE] result buffer full, dropping result for %s seq=%d", r.IP, r.Seq)
	}
}

// GetResults returns up to limit of the most recent results for an IP, newest
// first. A limit <= 0 defaults to 1000.
func (s *Store) GetResults(ip string, limit int) ([]Result, error) {
	if limit <= 0 {
		limit = 1000
	}
	defer s.measure()()

	rows, err := s.rdb.Query(
		`SELECT ip, seq, timestamp, success, rtt_ns, err_msg
		   FROM ping_results
		  WHERE ip = ?
		  ORDER BY timestamp DESC, id DESC
		  LIMIT ?`,
		ip, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("get results for %q: %w", ip, err)
	}
	defer rows.Close()

	results := make([]Result, 0, limit)
	for rows.Next() {
		var (
			r       Result
			success int
			errMsg  sql.NullString
		)
		if err := rows.Scan(&r.IP, &r.Seq, &r.Timestamp, &success, &r.RTTNanos, &errMsg); err != nil {
			return nil, fmt.Errorf("scan result: %w", err)
		}
		r.Success = success != 0
		r.ErrMsg = errMsg.String
		results = append(results, r)
	}
	return results, rows.Err()
}

// writeLoop drains the result channel, committing in batches for throughput.
func (s *Store) writeLoop() {
	defer s.wg.Done()

	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()

	batch := make([]Result, 0, flushBatchSize)

	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := s.commitBatch(batch); err != nil {
			log.Printf("[STORE] failed to persist %d results: %v", len(batch), err)
		}
		batch = batch[:0]
	}

	// drain moves everything currently queued into the batch without blocking.
	drain := func() {
		for {
			select {
			case r := <-s.results:
				batch = append(batch, r)
			default:
				return
			}
		}
	}

	for {
		select {
		case <-s.done:
			// Drain anything still queued before exiting.
			drain()
			flush()
			return
		case r := <-s.results:
			batch = append(batch, r)
			if len(batch) >= flushBatchSize {
				flush()
			}
		case ack := <-s.flushReq:
			// Drain everything currently queued, then flush synchronously.
			drain()
			flush()
			close(ack)
		case <-ticker.C:
			flush()
		}
	}
}

// Sync blocks until all results enqueued before the call have been written to
// disk. It is a no-op once the store is closing.
func (s *Store) Sync() {
	ack := make(chan struct{})
	select {
	case <-s.done:
	case s.flushReq <- ack:
		<-ack
	}
}

// commitBatch writes a batch of results in a single transaction.
func (s *Store) commitBatch(batch []Result) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}

	stmt, err := tx.Prepare(
		`INSERT INTO ping_results (ip, seq, timestamp, success, rtt_ns, err_msg)
		 VALUES (?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("prepare insert: %w", err)
	}
	defer stmt.Close()

	for _, r := range batch {
		success := 0
		if r.Success {
			success = 1
		}
		var errMsg interface{}
		if r.ErrMsg != "" {
			errMsg = r.ErrMsg
		}
		if _, err := stmt.Exec(r.IP, r.Seq, r.Timestamp.UTC(), success, r.RTTNanos, errMsg); err != nil {
			tx.Rollback()
			return fmt.Errorf("insert result: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

// Close stops the background writer, flushes pending results, and closes the
// database. It is safe to call multiple times.
func (s *Store) Close() error {
	s.closeOnce.Do(func() {
		close(s.done)
		s.wg.Wait()
		if s.rdb != nil {
			if err := s.rdb.Close(); err != nil {
				s.closeErr = err
			}
		}
		if err := s.db.Close(); err != nil {
			s.closeErr = err
		}
	})
	return s.closeErr
}
