package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"time"

	_ "modernc.org/sqlite" // registers the pure-Go "sqlite" driver

	"github.com/andreswebs/feedwatch/internal/core"
)

// timeLayout is the fixed-width RFC3339 UTC layout every timestamp is stored
// in. Uniform zone and width make the lexicographic comparisons behind
// since/until/order correct.
const timeLayout = "2006-01-02T15:04:05.000000000Z"

// Store is a SQLite-backed implementation of store.Store using the pure-Go
// modernc.org/sqlite driver (no CGO). It is safe for concurrent use across
// distinct feeds, which never share rows.
type Store struct {
	db    *sql.DB
	clock core.Clock
}

// Option configures a Store at Open time.
type Option func(*Store)

// WithClock injects the clock used for created/updated timestamps, keeping
// writes deterministic under test.
func WithClock(c core.Clock) Option {
	return func(s *Store) { s.clock = c }
}

// Open opens (creating if absent) the SQLite database at path, applies the
// durability and concurrency pragmas, and verifies the connection. A failure
// to open or reach the database wraps core.ErrStoreUnavailable.
func Open(path string, opts ...Option) (*Store, error) {
	s := &Store{clock: core.SystemClock}
	for _, opt := range opts {
		opt(s)
	}

	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", path, core.ErrStoreUnavailable)
	}
	if err := db.PingContext(context.Background()); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open sqlite %q: %w", path,
			errors.Join(err, core.ErrStoreUnavailable))
	}
	s.db = db
	return s, nil
}

// dsn builds the connection string: the file path plus the required pragmas.
// WAL journaling, a busy timeout, foreign keys, and NORMAL synchronous keep the
// store durable and crash-safe; synchronous is never OFF.
func dsn(path string) string {
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "foreign_keys(ON)")
	q.Add("_pragma", "synchronous(NORMAL)")
	return "file:" + path + "?" + q.Encode()
}

// Close releases the underlying database handle.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) now() time.Time { return s.clock().UTC() }

// formatTime renders a time as fixed-width RFC3339 UTC.
func formatTime(t time.Time) string { return t.UTC().Format(timeLayout) }

// formatTimePtr renders an optional time, mapping nil to a NULL-bound value.
func formatTimePtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return formatTime(*t)
}

// parseTimePtr maps a nullable stored string back to an optional time.
func parseTimePtr(ns sql.NullString) (*time.Time, error) {
	if !ns.Valid || ns.String == "" {
		return nil, nil
	}
	t, err := time.Parse(timeLayout, ns.String)
	if err != nil {
		return nil, fmt.Errorf("parse stored time %q: %w", ns.String, err)
	}
	t = t.UTC()
	return &t, nil
}
