package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/andreswebs/feedwatch/internal/core"
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
