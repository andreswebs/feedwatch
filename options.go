package feedwatch

import (
	"github.com/andreswebs/feedwatch/core"
	"github.com/andreswebs/feedwatch/store"
)

// Option overrides one of an App's collaborators. The options are both the
// supported extension point for an embedder and the sanctioned test seam; a
// collaborator left unset is constructed from the Config on first use.
type Option func(*App)

// WithStore supplies the store backend, bypassing the Config.Store scheme
// selection. The store belongs to the caller: App.Close never closes it.
func WithStore(s store.Store) Option {
	return func(a *App) { a.injectedStore = s }
}

// WithFetcher supplies the HTTP fetcher, bypassing the one built from the
// Config's user agent, timeouts, TLS, proxy, and retry settings.
func WithFetcher(f Fetcher) Option {
	return func(a *App) { a.fetcher = f }
}

// WithParser supplies the feed parser, bypassing the shipped one.
func WithParser(p Parser) Option {
	return func(a *App) { a.parser = p }
}

// WithClock supplies the time source, which keeps polling, backoff, and due
// calculations deterministic under test. A nil clock is ignored, so the system
// clock stays in place.
func WithClock(c core.Clock) Option {
	return func(a *App) {
		if c != nil {
			a.clock = c
		}
	}
}

// WithWarner supplies the sink for non-fatal advisories. Without one, warnings
// are dropped: the library never writes to a stream of its own.
func WithWarner(w Warner) Option {
	return func(a *App) { a.warn = w }
}
