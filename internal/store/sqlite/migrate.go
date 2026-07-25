package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/andreswebs/feedwatch/internal/core"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// migration is one embedded versioned SQL file.
type migration struct {
	version int
	name    string
	sql     string
}

// loadMigrations reads the embedded migration files, parsing the leading
// integer of each name as its version, sorted ascending.
func loadMigrations() ([]migration, error) {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}
	var ms []migration
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".sql") {
			continue
		}
		prefix, _, ok := strings.Cut(name, "_")
		if !ok {
			return nil, fmt.Errorf("migration %q: missing version prefix", name)
		}
		v, err := strconv.Atoi(prefix)
		if err != nil {
			return nil, fmt.Errorf("migration %q: bad version prefix: %w", name, err)
		}
		body, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", name, err)
		}
		ms = append(ms, migration{version: v, name: name, sql: string(body)})
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].version < ms[j].version })
	return ms, nil
}

// maxVersion returns the highest version among migrations sorted ascending, or
// 0 when there are none.
func maxVersion(ms []migration) int {
	if len(ms) == 0 {
		return 0
	}
	return ms[len(ms)-1].version
}

// Migrate applies every embedded migration whose version exceeds the stored
// schema version, each in its own transaction, and returns the count applied.
func (s *Store) Migrate(ctx context.Context) (applied int, err error) {
	ms, err := loadMigrations()
	if err != nil {
		return 0, err
	}
	return s.applyMigrations(ctx, ms)
}

// applyMigrations records the schema-migrations table, refuses a database newer
// than the supplied migration set, then applies each pending migration in
// ascending order, each in its own transaction. A failed migration aborts the
// run and is rolled back; it is never logged and skipped.
func (s *Store) applyMigrations(ctx context.Context, ms []migration) (applied int, err error) {
	if _, err := s.db.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		return 0, fmt.Errorf("ensure schema_migrations: %w", err)
	}

	current, err := s.SchemaVersion(ctx)
	if err != nil {
		return 0, err
	}

	if codeMax := maxVersion(ms); current > codeMax {
		return 0, fmt.Errorf(
			"stored schema version %d newer than supported %d: %w",
			current, codeMax, core.ErrSchemaTooNew)
	}

	for _, m := range ms {
		if m.version <= current {
			continue
		}
		if err := s.applyMigration(ctx, m); err != nil {
			return applied, err
		}
		applied++
	}
	return applied, nil
}

// applyMigration runs one migration and records its version atomically.
func (s *Store) applyMigration(ctx context.Context, m migration) (err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration %q: %w", m.name, err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if _, err = tx.ExecContext(ctx, m.sql); err != nil {
		return fmt.Errorf("apply migration %q: %w", m.name, err)
	}
	if _, err = tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
		m.version, formatTime(s.now())); err != nil {
		return fmt.Errorf("record migration %q: %w", m.name, err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %q: %w", m.name, err)
	}
	return nil
}

// Pending reports how many embedded migrations have not yet been applied to the
// database, for `migrate --status`.
func (s *Store) Pending(ctx context.Context) (int, error) {
	current, err := s.SchemaVersion(ctx)
	if err != nil {
		return 0, err
	}
	ms, err := loadMigrations()
	if err != nil {
		return 0, err
	}
	pending := 0
	for _, m := range ms {
		if m.version > current {
			pending++
		}
	}
	return pending, nil
}

// SchemaVersion reports the highest applied migration version, or 0 when the
// database has no migrations table yet.
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var v sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT max(version) FROM schema_migrations`).Scan(&v)
	if err != nil {
		// A missing table means nothing has been applied.
		if strings.Contains(err.Error(), "no such table") {
			return 0, nil
		}
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	if !v.Valid {
		return 0, nil
	}
	return int(v.Int64), nil
}
