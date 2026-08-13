// Package feedwatch is the feedwatch library: an application service, App,
// exposing one method per use case over the request and result types that every
// frontend renders.
//
// Four packages are public (docs/adr/0007-library-and-frontends.md). This one
// holds App, its constructor and options, the resolved Config, the public port
// interfaces, and the envelope head every result type embeds. Package core
// holds the domain types and the error category taxonomy, package store holds
// the Store interface that an alternative backend implements, and package
// daemon holds the poll scheduler. The shipped store, fetcher, and parser
// adapters, the poll orchestrator, the output renderer, and the CLI stay
// internal: an embedder replacing a collaborator needs the interface, not the
// adapter.
//
// The library is the substance and the CLI is one frontend among several. A
// frontend translates its own input encoding into a request, calls one App
// method, and renders the result in its own encoding; it holds no domain logic,
// so no two frontends can disagree about what a use case does.
//
// An App is built from a Config plus functional options. Omitted ports are
// constructed from the config on first use, so the default path needs no
// options:
//
//	app, err := feedwatch.New(feedwatch.Defaults())
//	if err != nil {
//		return err
//	}
//	defer func() { _ = app.Close() }()
//
// # Ports
//
// App holds five ports, each replaceable through an Option:
//
//   - store.Store, the persistence backend (WithStore). The supported extension
//     point: an embedder supplies a Postgres, in-memory, or otherwise custom
//     backend.
//   - Fetcher, the HTTP retrieval port (WithFetcher).
//   - Parser, the feed normalization port (WithParser).
//   - core.Clock, the time source (WithClock), which keeps polling, backoff, and
//     due calculations deterministic under test.
//   - Warner, the sink for non-fatal advisories (WithWarner). Without one,
//     advisories are dropped: the library never writes to a stream of its own.
//
// # Lifecycle
//
// New validates the configuration and performs no I/O. The store opens lazily,
// on the first use case that needs it, and pending migrations are applied once
// per App at that point, so constructing an App for a use case that never
// touches the store (Discover, for one) leaves no database file behind. Close
// releases only what the App itself opened: a store passed to WithStore belongs
// to the embedder and is never closed by the App. Close is idempotent, so it is
// always safe to defer.
//
// # Results
//
// Each use case returns a result type that is the output contract. Every one
// embeds Head, so schema_version and ok lead the JSON a frontend renders, and
// collections coalesce to [] rather than null on the wire. SchemaVersion is the
// version of that contract (docs/adr/0005-output-contract.md), independent of
// the Go module version.
//
// # Errors
//
// A returned error is a whole-invocation failure. Every one carries a
// core.Category, recovered with errors.As on *core.FeedError, from which a
// frontend derives its own encoding: the CLI maps a category to a sysexits.h
// exit code, an HTTP server would map it to a status. Static failures are the
// sentinels in core (core.ErrUsage and friends), matched with errors.Is. Nothing
// is ever classified by matching a message string.
//
// Per-feed failures are not errors. They are result data, reported in the
// Failures list of the poll and check envelopes with the feed URL, its category,
// an HTTP status where applicable, and a message, so one unreachable feed never
// fails an invocation that other feeds survived.
//
// # Stability
//
// The four public packages (feedwatch, core, store, daemon) are the supported
// surface. Everything under internal/ is not, and changes without notice.
//
// The project is pre-1.0: the Go API may change in a minor version bump, and any
// such change is called out in the changelog. The JSON output contract is
// versioned separately by SchemaVersion, and a breaking envelope change bumps
// it, so an embedder tracking the Go API and an agent parsing the JSON have
// independent compatibility signals.
package feedwatch
