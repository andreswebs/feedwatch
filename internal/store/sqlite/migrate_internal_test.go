package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/andreswebs/feedwatch/core"
)

func migrateTestNow() time.Time { return time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC) }

// openTestStore opens an unmigrated store on a temp-file database with a fixed
// clock, for white-box migration tests that drive the unexported seam.
func openTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "feedwatch.db")
	s, err := Open(path, WithClock(migrateTestNow))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if cerr := s.Close(); cerr != nil {
			t.Errorf("Close: %v", cerr)
		}
	})
	return s
}

// TestMigrateReachesCurrentVersion checks a fresh store lands on the highest
// embedded migration version with nothing left pending.
func TestMigrateReachesCurrentVersion(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	if _, err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	v, err := s.SchemaVersion(ctx)
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if v != 2 {
		t.Errorf("SchemaVersion = %d, want 2", v)
	}

	pending, err := s.Pending(ctx)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if pending != 0 {
		t.Errorf("Pending = %d, want 0", pending)
	}
}

// TestMigrateAddsTagsToExistingFeeds migrates a store to version 1 only, seeds a
// feed the way an older binary would have, then completes the migration: the
// pre-existing feed gains an empty tags array and keeps every other value.
func TestMigrateAddsTagsToExistingFeeds(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	ms, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	if _, err := s.applyMigrations(ctx, ms[:1]); err != nil {
		t.Fatalf("applyMigrations to v1: %v", err)
	}

	const url = "https://blog.example/feed.xml"
	stamp := formatTime(s.now())
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO feeds (url, alias, interval_seconds, status, failure_count, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		url, "blog", 900, "active", 3, stamp, stamp); err != nil {
		t.Fatalf("seed v1 feed: %v", err)
	}

	if _, err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate to current: %v", err)
	}

	var (
		alias    string
		interval int
		status   string
		failures int
		tags     string
	)
	if err := s.db.QueryRowContext(ctx,
		`SELECT alias, interval_seconds, status, failure_count, tags FROM feeds WHERE url = ?`, url,
	).Scan(&alias, &interval, &status, &failures, &tags); err != nil {
		t.Fatalf("read migrated feed: %v", err)
	}
	if tags != "[]" {
		t.Errorf("tags = %q, want %q", tags, "[]")
	}
	if alias != "blog" || interval != 900 || status != "active" || failures != 3 {
		t.Errorf("pre-existing columns changed: alias=%q interval=%d status=%q failures=%d",
			alias, interval, status, failures)
	}
}

// TestMigrateIsIdempotentAtCurrentVersion checks a second Migrate on an
// already-current store applies nothing and leaves the version alone.
func TestMigrateIsIdempotentAtCurrentVersion(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	if _, err := s.Migrate(ctx); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}

	applied, err := s.Migrate(ctx)
	if err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	if applied != 0 {
		t.Errorf("second Migrate applied = %d, want 0", applied)
	}

	v, err := s.SchemaVersion(ctx)
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if v != 2 {
		t.Errorf("SchemaVersion = %d, want 2", v)
	}
}

// TestMigrateRefusesNewerSchema verifies a database stamped with a version
// beyond the highest embedded migration is refused rather than silently left
// alone.
func TestMigrateRefusesNewerSchema(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	if _, err := s.Migrate(ctx); err != nil {
		t.Fatalf("initial Migrate: %v", err)
	}
	codeMax, err := s.SchemaVersion(ctx)
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}

	// Stamp a version the running binary does not understand.
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
		codeMax+1, formatTime(s.now())); err != nil {
		t.Fatalf("stamp future version: %v", err)
	}

	_, err = s.Migrate(ctx)
	if !errors.Is(err, core.ErrSchemaTooNew) {
		t.Fatalf("Migrate on too-new db: err = %v, want errors.Is(ErrSchemaTooNew)", err)
	}
}

// TestPendingReflectsUnappliedMigrations checks that Pending counts the
// embedded migrations not yet applied and drops to zero once Migrate runs.
func TestPendingReflectsUnappliedMigrations(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	ms, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}

	pending, err := s.Pending(ctx)
	if err != nil {
		t.Fatalf("Pending before migrate: %v", err)
	}
	if pending != len(ms) {
		t.Errorf("Pending before migrate = %d, want %d", pending, len(ms))
	}

	if _, err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	pending, err = s.Pending(ctx)
	if err != nil {
		t.Fatalf("Pending after migrate: %v", err)
	}
	if pending != 0 {
		t.Errorf("Pending after migrate = %d, want 0", pending)
	}
}

// TestApplyMigrationsRollsBackOnError applies a valid first migration followed
// by a broken one and verifies the failure aborts atomically: the error
// surfaces and the recorded schema version stops at the last good migration,
// proving the broken step was rolled back rather than logged and skipped.
func TestApplyMigrationsRollsBackOnError(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	ms := []migration{
		{version: 1, name: "0001_ok.sql", sql: `CREATE TABLE probe (id INTEGER);`},
		{version: 2, name: "0002_broken.sql", sql: `THIS IS NOT VALID SQL;`},
	}

	applied, err := s.applyMigrations(ctx, ms)
	if err == nil {
		t.Fatal("applyMigrations: want error from broken migration, got nil")
	}
	if applied != 1 {
		t.Errorf("applied = %d, want 1 (only the good migration)", applied)
	}

	v, err := s.SchemaVersion(ctx)
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if v != 1 {
		t.Errorf("SchemaVersion = %d, want 1 (broken migration rolled back)", v)
	}
}
