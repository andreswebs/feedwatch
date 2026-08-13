package feedwatch

import (
	"context"
	"sync"

	"github.com/andreswebs/feedwatch/core"
	"github.com/andreswebs/feedwatch/internal/fetch"
	"github.com/andreswebs/feedwatch/internal/parse"
	"github.com/andreswebs/feedwatch/internal/store/sqlite"
	"github.com/andreswebs/feedwatch/store"
)

// App is feedwatch's application service: one method per use case over the ports
// it holds. It is safe for concurrent use; collaborators are resolved once,
// under a mutex, and shared by every use case running on the App.
type App struct {
	cfg   Config
	clock core.Clock
	warn  Warner

	// injectedStore is the embedder's store, if any. It is kept apart from the
	// resolved store below because Close must never release it.
	injectedStore store.Store

	mu       sync.Mutex
	store    store.Store
	owned    bool // the App opened store and must close it
	migrated bool // pending migrations were applied on this store
	fetcher  Fetcher
	parser   Parser
}

// New builds an App from the resolved configuration and any collaborator
// overrides. It validates the configuration and performs no I/O: no store is
// opened, no directory created, and no request made, so constructing an App for
// a use case that never touches the store leaves nothing behind. The caller owns
// the App and must Close it.
func New(cfg Config, opts ...Option) (*App, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	a := &App{cfg: cfg, clock: core.SystemClock}
	for _, opt := range opts {
		opt(a)
	}
	return a, nil
}

// Close releases what the App opened. A store injected with WithStore belongs to
// the embedder and is never closed here. Close is idempotent, so it is always
// safe to defer.
func (a *App) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.closeStoreLocked()
}

// closeStoreLocked releases an App-opened store and forgets it, so a later use
// case reopens rather than reaching through a closed handle. It is a no-op for
// an injected store and when nothing is open.
func (a *App) closeStoreLocked() error {
	if a.store == nil || !a.owned {
		return nil
	}
	err := a.store.Close()
	a.store, a.owned, a.migrated = nil, false, false
	return err
}

// resolveStore returns the store, opening it on first use and applying pending
// migrations once per App so the "any command applies pending migrations
// idempotently" contract holds however the App is driven. An injected store is
// used as-is, but is migrated the same way: a backend the embedder supplied
// still has to be at the schema version the use cases expect.
func (a *App) resolveStore(ctx context.Context) (store.Store, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	st, err := a.storeLocked()
	if err != nil {
		return nil, err
	}
	if !a.migrated {
		if _, err := st.Migrate(ctx); err != nil {
			_ = a.closeStoreLocked()
			return nil, err
		}
		a.migrated = true
	}
	return st, nil
}

// resolveStoreUnmigrated returns the store without applying pending migrations,
// so the migrate use case can report how many it applied itself rather than
// finding the work already done by the guard in resolveStore.
func (a *App) resolveStoreUnmigrated() (store.Store, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.storeLocked()
}

// markMigrated records that pending migrations have been applied on the open
// store, so a later use case on the same App does not repeat the work.
func (a *App) markMigrated() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.migrated = true
}

// storeLocked returns the store, opening it on first use. An injected store is
// adopted as-is and stays the embedder's to close. The caller holds a.mu.
func (a *App) storeLocked() (store.Store, error) {
	if a.store != nil {
		return a.store, nil
	}
	if a.injectedStore != nil {
		a.store, a.owned = a.injectedStore, false
		return a.store, nil
	}
	opened, err := a.openStore()
	if err != nil {
		return nil, err
	}
	a.store, a.owned = opened, true
	return a.store, nil
}

// openStore opens the backend the configuration selects. The URL scheme decides
// the driver; Postgres is deferred, so a postgres:// DSN is a configuration
// error for now, reported here on first use rather than at construction.
func (a *App) openStore() (store.Store, error) {
	if a.cfg.Backend() == BackendPostgres {
		return nil, &core.FeedError{
			Category: core.CatConfig,
			Message:  "postgres backend not yet implemented",
			Err:      core.ErrConfig,
		}
	}
	path, err := a.cfg.StorePath()
	if err != nil {
		return nil, err
	}
	return sqlite.Open(path, sqlite.WithClock(a.clock))
}

// resolveFetcher returns the HTTP fetcher, building one from the configuration's
// user agent, timeouts, TLS, proxy, and retry settings on first use. A zero
// retry backoff lets the adapter apply its own default while still honoring the
// configured attempt count.
func (a *App) resolveFetcher() (Fetcher, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.fetcher != nil {
		return a.fetcher, nil
	}
	f, err := fetch.New(
		fetch.WithUserAgent(a.cfg.UserAgent),
		fetch.WithConnectTimeout(a.cfg.ConnectTimeout),
		fetch.WithTimeout(a.cfg.Timeout),
		fetch.WithMinTLS(a.cfg.MinTLS),
		fetch.WithProxy(a.cfg.Proxy),
		fetch.WithCABundle(a.cfg.CABundle),
		fetch.WithAllowPrivate(a.cfg.AllowPrivate),
		fetch.WithRetry(a.cfg.RetryAttempts, 0),
	)
	if err != nil {
		return nil, err
	}
	a.fetcher = f
	return f, nil
}

// resolveParser returns the feed parser, building the shipped one on first use.
func (a *App) resolveParser() Parser {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.parser == nil {
		a.parser = parse.New()
	}
	return a.parser
}

// warnf delivers a non-fatal advisory to the configured Warner. With no Warner
// wired the advisory is dropped, since the library owns no output stream.
func (a *App) warnf(code, message, hint string, details any) {
	if a.warn != nil {
		a.warn(code, message, hint, details)
	}
}
