package cli

import (
	"context"
	"strings"

	"github.com/andreswebs/feedwatch/internal/config"
	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/store"
	"github.com/andreswebs/feedwatch/internal/store/sqlite"
)

// backendName classifies the resolved store location by URL scheme: a
// postgres:// (or postgresql://) DSN selects the Postgres backend, anything else
// is a SQLite filesystem path.
func backendName(dsn string) string {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		return "postgres"
	}
	return "sqlite"
}

// openStore opens the store backend selected by the resolved --db value, also
// returning the backend name for reporting. The URL scheme decides the driver;
// Postgres is deferred, so a postgres:// DSN is a configuration error for now.
// The caller owns the returned store and must Close it.
func openStore(cfg config.Config, clock core.Clock) (store.Store, string, error) {
	backend := backendName(cfg.Store)
	if backend == "postgres" {
		return nil, backend, &core.FeedError{
			Category: core.CatConfig,
			Message:  "postgres backend not yet implemented",
			Err:      core.ErrConfig,
		}
	}

	s, err := sqlite.Open(cfg.Store, sqlite.WithClock(orSystemClock(clock)))
	if err != nil {
		return nil, backend, err
	}
	return s, backend, nil
}

// openStoreMigrated opens the store and applies any pending migrations before
// returning it, honoring the "any command applies pending migrations
// idempotently" contract for every command except migrate, which manages
// migrations explicitly through openStore. The caller owns the returned store
// and must Close it; on a migration failure the store is closed here.
func openStoreMigrated(ctx context.Context, cfg config.Config, clock core.Clock) (store.Store, string, error) {
	s, backend, err := openStore(cfg, clock)
	if err != nil {
		return nil, backend, err
	}
	if _, err := s.Migrate(ctx); err != nil {
		_ = s.Close()
		return nil, backend, err
	}
	return s, backend, nil
}

// orSystemClock falls back to the real clock when no clock was injected, so an
// action can open a store without a wired Deps.Clock.
func orSystemClock(c core.Clock) core.Clock {
	if c == nil {
		return core.SystemClock
	}
	return c
}
