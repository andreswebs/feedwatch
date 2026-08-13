---
id: fee-f3u8
status: closed
deps: [fee-lq28]
links: []
created: 2026-08-13T14:03:47Z
type: task
priority: 1
assignee: Andre Silva
parent: fee-ui25
tags: [lib, app]
---

# feedwatch.Config, App skeleton, and the options constructor

Third step of
[docs/adr/0007-library-and-frontends.md](../docs/adr/0007-library-and-frontends.md).
Creates the root `feedwatch` package: the `App` application service, its
functional-options constructor, the public port interfaces, the resolved
`Config`, and the envelope head that every result type embeds.

`App` gains no use-case methods here. Those land in `fee-rzwl` (store-only),
`fee-kj8z` (network), and `fee-gvuo` (OPML). This ticket exists so those three
can be mechanical: it puts the construction, port resolution, lifecycle, and
configuration in place first, and proves them with tests that do not depend on
any use case existing.

The CLI keeps working throughout. Its `resolver` in
[internal/command/resolve.go](../internal/command/resolve.go) is untouched here
and is retired in `fee-gvuo`, once the last action stops needing it.

## Design

### 1. New root package

Files at the repository root, package `feedwatch`, import path
`github.com/andreswebs/feedwatch`:

```text
doc.go        package doc: the library surface and its four packages
config.go     Config, Defaults, Validate, DefaultStorePath
ports.go      Fetcher, Parser, Warner
envelope.go   SchemaVersion, Head, OKHead
app.go        App, New, Close, lazy port resolution
options.go    Option, WithStore, WithFetcher, WithParser, WithClock, WithWarner
```

The root package imports `core`, `store`, and the `internal/**` adapters. It
must never import a CLI framework: ADR 0003's no-leak rule now covers the
library too, and `internal/command` remains the only package permitted to
import `urfave/cli`.

### 2. Config moves out of internal

`internal/config` is deleted and its contents become `feedwatch.Config`,
`feedwatch.Defaults()`, and `Config.Validate()`, byte-for-byte the same fields,
defaults, and validation rules. `Config` is public because it is the first
argument of `New`, and the ADR's public surface is four packages, so it belongs
in the root package rather than in a fifth.

Importers to update: `internal/command/root.go`, `flags.go`, `context.go`,
`storeopen.go`, `resolve.go`. `globalFlags(base config.Config)` becomes
`globalFlags(base feedwatch.Config)` and `configFrom(ctx)` returns
`feedwatch.Config`. The flag-to-config overlay in `buildConfig` stays in the
CLI: parsing flags and environment is a frontend concern and no other package
does it.

### 3. Default store path moves into the library

`resolveStorePath` and `ensureStoreDir` move out of
[internal/command/storepath.go](../internal/command/storepath.go) into
`config.go`, because every frontend needs the same default and the same
first-run directory creation:

```go
// DefaultStorePath returns the tool-owned default SQLite location, following
// the XDG Base Directory spec.
func DefaultStorePath() string
```

The `isDefault` return disappears, because the library derives it: `Config.Store`
empty means "use the default", and only the default path gets its parent
directory created. That is exactly today's behavior, where `isDefault` is true
precisely when the `--db` flag and `FEEDWATCH_DB` are both empty. The CLI's
`buildConfig` therefore sets `c.Store = cmd.String("db")` verbatim, empty
included, and the `Before` hook no longer calls `ensureStoreDir`.

Resolution rules, unchanged from `resolveStorePath`:

1. `Config.Store` non-empty: used verbatim, no directory creation. An explicit
   path stays strict, and a missing intermediate directory remains a store
   error.
2. `$XDG_STATE_HOME` set: `$XDG_STATE_HOME/feedwatch/feedwatch.db`.
3. Otherwise `~/.local/state/feedwatch/feedwatch.db`.
4. No home directory: `feedwatch/feedwatch.db`.

Directory creation for cases 2 to 4 uses `0o700` and maps a failure to a
store-category `*core.FeedError` wrapping `core.ErrStoreUnavailable`, as today.

### 4. Public ports

`store.Store` is the store port. The other two are declared in the root package
so an external embedder can name them (they cannot name `internal/fetch` or
`internal/parse`):

```go
// Fetcher retrieves a feed body over HTTP.
type Fetcher interface {
    Fetch(ctx context.Context, req core.FetchRequest) (core.FetchResult, error)
}

// Parser turns a decoded feed body into normalized core items.
type Parser interface {
    Parse(ctx context.Context, body []byte, baseURL string) (core.ParsedFeed, error)
}

// Warner receives non-fatal advisories raised during a use case. A frontend
// renders them; the library never writes to a stream itself.
type Warner func(code, message, hint string, details any)
```

These are structurally identical to `internal/fetch.Fetcher` and
`internal/parse.Parser` (which is why `fee-d32a` moved `ParsedFeed` into
`core`), so a `feedwatch.Fetcher` value can be assigned straight into
`poll.Deps.Fetcher` with no adapter and no import of the root package from
`internal/**`.

`Warner` mirrors the existing `poll.Deps.Warn` callback signature, which the
CLI wires to `output.Renderer.Warn`.

### 5. Envelope head

`SchemaVersion`, `Head`, and `OKHead` move from `internal/output` into
`envelope.go`, because the result types that embed `Head` become library types
in the next three tickets. `internal/output` keeps `WriteJSON`, `EmitError`,
`EmitWarning`, `ExitCodeFor`, the `Renderer`, and color gating, and imports the
root package for `SchemaVersion` in the error and warning envelopes. No cycle:
the root package never imports `internal/output`, since rendering is a frontend
concern.

Update every current embedder (`internal/command/*.go` result structs) from
`output.Head` to `feedwatch.Head`, and move `internal/output/head_test.go` to
the root package.

### 6. App and lifecycle

```go
// App is feedwatch's application service: one method per use case over the
// ports it holds. It is safe for concurrent use.
type App struct { /* cfg, ports, lazily-opened store, warner */ }

func New(cfg Config, opts ...Option) (*App, error)
func (a *App) Close() error
```

`New` validates the config and records the options. It performs **no I/O**: it
opens no store, creates no directory, and makes no network call. That matters
for `discover`, which today never opens a store, and must not start creating a
database file as a side effect of construction.

The store is opened lazily on first use, guarded so concurrent use cases open
it once:

- An injected store (`WithStore`) is used as-is.
- Otherwise the backend is chosen by the `Config.Store` scheme, reusing the
  logic in [internal/command/storeopen.go](../internal/command/storeopen.go):
  a `postgres://` or `postgresql://` DSN is still a config-category error
  ("postgres backend not yet implemented"), anything else is SQLite via
  `sqlite.Open` with the App's clock.
- Immediately after opening, pending migrations are applied once per App, so
  the existing "any command applies pending migrations idempotently" contract
  survives. The `Migrate` use case in `fee-rzwl` bypasses this guard so it can
  still report a truthful `applied` count.
- Before opening a default-path SQLite store, the parent directory is created
  per section 3.

Verify while implementing that `testsupport.InMemoryStore.Migrate` and
`FailingStore.Migrate` are idempotent and do not perturb their `SchemaVersion`
or `Pending` reporting, since an injected double now sees a `Migrate` call it
did not previously receive.

`Close` releases only what the App opened. An injected store belongs to the
embedder and is never closed by the App. `Close` is idempotent.

Fetcher and parser resolve the same way: injected value if present, otherwise
built from config on first use (`fetch.New` with the eight existing options
from `buildFetcher`, and `parse.New()`).

### 7. Options

```go
type Option func(*App)

func WithStore(s store.Store) Option
func WithFetcher(f Fetcher) Option
func WithParser(p Parser) Option
func WithClock(c core.Clock) Option
func WithWarner(w Warner) Option
```

These are both the supported extension point and the sanctioned test seam. The
unexported `store`, `fetch`, and `parse` fields on `command.Deps` are retired in
favour of them once the last use case moves (`fee-gvuo`); leave them alone here.

## TDD notes

Every behavior below is observable through the public surface with no use case
in place, so work them as vertical slices, one test to one implementation step,
in this order:

1. `New(Defaults())` returns a usable `*App` and a nil error.
2. `New(Config{})` returns an error satisfying `errors.Is(err, core.ErrConfig)`
   (zero concurrency and an unknown format both fail `Validate`).
3. `New` performs no I/O: with `XDG_STATE_HOME` pointed at a `t.TempDir()`,
   after `New` plus `Close` the directory contains no `feedwatch` subdirectory
   and no database file.
4. `DefaultStorePath` honors `XDG_STATE_HOME`, then the home directory, in that
   order. Table-driven with `t.Setenv`.
5. `WithStore` is honored: a use-case-free proof is an unexported accessor
   exercised from an internal test file (`app_internal_test.go`, package
   `feedwatch`), asserting the resolved store is the injected value and that no
   file was created at the default path.
6. `Close` is idempotent, and does not close a store injected via `WithStore`
   (assert with a double whose `Close` increments a counter).
7. An injected fetcher and parser are returned by the resolver rather than
   freshly built ones.
8. A `postgres://` `Config.Store` surfaces a config-category `*core.FeedError`
   on first store use, not at `New`.

Keep the black-box tests in `package feedwatch_test`, so they exercise the same
import path an embedder uses; only slices 5 and 7, which need an unexported
accessor, live in `package feedwatch`.

The `internal/command` goldens are the regression net for the config and
store-path moves and must compare byte-identical with no `-update` run. Pay
particular attention to `testdata/err/**`, where a store-unavailable message
embeds the resolved path.

## Acceptance Criteria

- The root package `feedwatch` exists with `Config`, `Defaults`, `Validate`,
  `DefaultStorePath`, `Fetcher`, `Parser`, `Warner`, `SchemaVersion`, `Head`,
  `OKHead`, `App`, `New`, `Close`, and the five options.
- `internal/config` no longer exists; no `.go` file imports it.
- `internal/command/storepath.go` no longer exists; the CLI passes the raw
  `--db` value (empty included) into `Config.Store`.
- `SchemaVersion`, `Head`, and `OKHead` no longer live in `internal/output`;
  `internal/output` imports the root package for `SchemaVersion`.
- The root package imports no CLI framework; `urfave/cli` is imported only by
  `internal/command`.
- `New` performs no filesystem or network I/O, proven by a test.
- `Close` is idempotent and never closes an injected store.
- Every `internal/command/testdata/**` golden compares byte-identical with no
  `-update` run.
- `make build` passes.

## Files

```text
doc.go                        (new)
config.go                     (new; from internal/config + internal/command/storepath.go)
ports.go                      (new)
envelope.go                   (new; from internal/output/output.go)
app.go                        (new; store/fetcher/parser resolution from internal/command/resolve.go
                               and storeopen.go, adapted to lazy per-App resolution)
options.go                    (new)
app_test.go                   (new, package feedwatch_test)
app_internal_test.go          (new, package feedwatch)
config_test.go                (new; from internal/config/config_test.go)
envelope_test.go              (new; from internal/output/head_test.go)
internal/config/**            (deleted)
internal/command/storepath.go (deleted)
internal/command/root.go      (Config type; Before no longer calls ensureStoreDir)
internal/command/flags.go     (Config type)
internal/command/context.go   (Config type)
internal/command/*.go         (result structs embed feedwatch.Head)
internal/output/output.go     (Head/OKHead/SchemaVersion removed; imports root for SchemaVersion)
```

## Notes

**2026-08-13T14:41:56Z**

Root package feedwatch created: doc.go, config.go, ports.go, envelope.go, app.go, options.go, with black-box tests (app_test.go, config_test.go, envelope_test.go, imports_test.go) and app_internal_test.go for the unexported resolvers.

Deviations and decisions worth knowing:
- Store-path resolution landed as Config.StorePath() (public method) rather than an unexported helper, because both App.openStore and the CLI's surviving openStore need the same resolution plus first-run directory creation. DefaultStorePath() stays pure (no I/O); StorePath() is the one that creates the default parent at 0o700. The isDefault return is gone: empty Config.Store means default.
- backendName moved to Config.Backend() with named constants BackendSQLite/BackendPostgres, so driver selection (App) and migrate --status reporting (CLI) share one scheme check instead of duplicating it across the library/frontend line.
- Before no longer creates the store directory, so discover (which never opens a store) no longer provisions one. Creation happens on first store open. TestDefaultStoreDirAutoCreated still passes via migrate --status.
- resolveStore migrates once per App, injected stores included, and Close releases only what the App opened (idempotent). fee-rzwl's Migrate use case needs to bypass that guard to report a truthful applied count; the seam is not built yet, add a no-migrate resolution path there.
- warnf is exercised by TestWarnerReceivesAdvisories so the unused linter stays quiet until a use case raises warnings.
- storeopen.go and resolve.go survive as the CLI's own store/fetcher/parser resolution and now duplicate App.openStore and App.resolveFetcher. That is the transitional state the ticket calls for; fee-rzwl/fee-kj8z/fee-gvuo delete them as each action moves to an App method.
- imports_test.go walks the whole module imports-only and fails any urfave/cli import outside internal/command, extending ADR 0003's no-leak rule to the library.
- internal/config and internal/command/storepath.go deleted; Head/OKHead/SchemaVersion out of internal/output (which now imports the root package for SchemaVersion). Every internal/command/testdata golden compared byte-identical with no -update run. make build passes.
