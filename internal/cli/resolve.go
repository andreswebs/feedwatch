package cli

import (
	"context"

	"github.com/andreswebs/feedwatch/internal/config"
	"github.com/andreswebs/feedwatch/internal/fetch"
	"github.com/andreswebs/feedwatch/internal/parse"
	"github.com/andreswebs/feedwatch/internal/store"
)

// resolver lazily supplies a command's collaborators, preferring any wired into
// Deps (the test seam) and constructing production ones otherwise. It records a
// closer for every resource it opens, so a command defers a single Close that
// releases them in reverse order and runs even when a later resolution fails.
type resolver struct {
	d      Deps
	cfg    config.Config
	closes []func()
}

// newResolver binds a resolver to the resolved config. Each command requests
// only the collaborators it needs and defers Close.
func newResolver(d Deps, cfg config.Config) *resolver {
	return &resolver{d: d, cfg: cfg}
}

// Store returns the store, opening and migrating a production one when none was
// injected and recording its closer.
func (r *resolver) Store(ctx context.Context) (store.Store, error) {
	if r.d.Store != nil {
		return r.d.Store, nil
	}
	opened, _, err := openStoreMigrated(ctx, r.cfg, r.d.Clock)
	if err != nil {
		return nil, err
	}
	r.closes = append(r.closes, func() { _ = opened.Close() })
	return opened, nil
}

// Fetcher returns the HTTP fetcher, building a production one from the config
// when none was injected.
func (r *resolver) Fetcher() (fetch.Fetcher, error) {
	if r.d.Fetch != nil {
		return r.d.Fetch, nil
	}
	return buildFetcher(r.cfg)
}

// Parser returns the feed parser, building the default one when none was
// injected.
func (r *resolver) Parser() parse.Parser {
	if r.d.Parse != nil {
		return r.d.Parse
	}
	return parse.New()
}

// Close releases every resource the resolver opened, in reverse order. It is a
// no-op when nothing was opened, so it is always safe to defer.
func (r *resolver) Close() {
	for i := len(r.closes) - 1; i >= 0; i-- {
		r.closes[i]()
	}
}

// buildFetcher constructs the production HTTP fetcher from the resolved config.
// A zero retry backoff lets fetch.New apply its own default while still honoring
// the configured attempt count.
func buildFetcher(cfg config.Config) (fetch.Fetcher, error) {
	return fetch.New(
		fetch.WithUserAgent(cfg.UserAgent),
		fetch.WithConnectTimeout(cfg.ConnectTimeout),
		fetch.WithTimeout(cfg.Timeout),
		fetch.WithMinTLS(cfg.MinTLS),
		fetch.WithProxy(cfg.Proxy),
		fetch.WithCABundle(cfg.CABundle),
		fetch.WithAllowPrivate(cfg.AllowPrivate),
		fetch.WithRetry(cfg.RetryAttempts, 0),
	)
}
