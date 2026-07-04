# Learnings

Running log of non-obvious problems solved and decisions made during
implementation, newest last. Keyed loosely by ticket.

## fee-mls1 — Project scaffolding and tooling

- golangci-lint is v2 (2.12.2), not v1. The config schema differs: it requires a
  top-level `version: "2"`, and `gofmt`/`goimports` are **formatters**, not
  linters. They go under a separate `formatters:` block; listing them under
  `linters.enable` is rejected. Linters use `linters.default: none` plus an
  explicit `enable:` list.
- `revive`'s `package-comments` rule flags `package main` with no doc comment.
  The pre-existing `cmd/feedwatch/main.go` stub tripped it; added a one-line
  `// Command feedwatch ...` comment. Every package needs a doc comment to keep
  `revive` quiet, hence the per-package `doc.go` files.
- CI installs golangci-lint via the official install script (pinned to the same
  version) rather than `golangci/golangci-lint-action`, because `make validate`
  already runs `make lint`; the action would duplicate the lint pass. The
  workflow just needs the binary on `PATH`, then runs `make validate` and
  `make build`.
- `actions/setup-go` `cache-dependency-path: src/go.sum` points at a file that
  does not exist yet (no third-party deps). setup-go warns but does not fail;
  it resolves once the first dependency lands and `go.sum` appears.
- Third-party deps (`urfave/cli/v3`, `mmcdole/gofeed`, `modernc.org/sqlite`) are
  intentionally deferred to the lane that first needs them, to keep `go.mod`
  minimal until then.

## fee-4m17 — Error taxonomy

- The error model lives in `core` (no `errors` subpackage) so `output`, `parse`,
  and `store` share it without importing each other. `errors.go` +
  `errors_test.go` sit beside `types.go`; tests use the `core_test` package.
- `ExitCodeFor` is deliberately asymmetric: a purely feed-scoped `*FeedError`
  (category `network`/`http`/`parse`/`timeout`) maps to **0**, not a nonzero
  code. Exit 2 (all failed) and 3 (partial) are computed by the poll boundary
  from the per-feed outcome aggregate, never derived from a returned error. Only
  the whole-invocation sentinels and `*FeedError`s of category
  `usage`/`config`/`store`/`internal` map to 1. Any unrecognized error also
  defaults to 1 (treated as a hard whole-invocation failure).
- The constructors (`NetworkErr`/`HTTPErr`/`ParseErr`/`TimeoutErr`) are all
  feed-scoped and only set `Err` (the wrapped cause), not `Message`. `Error()`
  prefers `Message` and falls back to `Err.Error()`, so callers that want a
  custom human string set `Message` on the struct literal directly. `Status` is
  only rendered for `CatHTTP`.
- Go-style error strings (lowercase lead, no trailing punctuation) are enforced
  by a test rather than left to convention, since these strings surface verbatim
  in the stderr JSON `message` field.

## fee-chr5 — Interface keystone (Store, Parser, Fetcher, Clock)

- Shared value types (`ListFilter`, `ItemOrder`, `ItemQuery`, `PrunePolicy`,
  `FetchRequest`, `FetchResult`, `Clock`/`SystemClock`) live in `core`, split
  into `query.go`, `fetch.go`, and `clock.go` beside `types.go`. The three
  interfaces each live in their own consumer package (`store`, `parse`,
  `fetch`) and import only `core`, so the graph stays acyclic; verify with
  `go list -deps ./internal/store ./internal/parse ./internal/fetch | grep
feedwatch`.
- Interface-only packages whose only test artifact is a compile-time
  `var _ Iface = (*fake)(nil)` conformance check report `[no tests to run]`
  under `go test` (no `TestXxx` funcs), but the conformance still fails the
  build if a signature drifts. Don't mistake `[no tests to run]` for "untested"
  here: the compile is the test. Genuine behavior tests (e.g. the `Clock`
  fixed-time test) belong in `core`, and per-method behavior tests land with the
  concrete impls in E2/E3/E4.
- The hand-written `fakeStore`/`fakeParser`/`fakeFetcher` doubles seeded here
  are deliberately signature-only stubs; they become the basis of the E9 test
  harness (`fee-zuz2`).

## fee-cmkb — Configuration struct and defaults

- `config.Defaults()` is the single source of the Appendix A default table, but
  it stays a **pure value with no env or filesystem reads**. Two fields are
  deliberately not resolved here: `Store` is left empty (the default
  `$XDG_STATE_HOME/feedwatch/feedwatch.db` path resolution is the cli layer's
  job, `fee-7ons`), and `UserAgent` defaults to the const `DefaultUserAgent`
  (`"feedwatch"`). Keeping XDG/env lookups out of `config` preserves the layering
  the ticket calls for: cli reads the world, `config` just holds the resolved
  result.
- `MinTLS` is stored as a resolved `uint16` (`tls.VersionTLS12`), not the
  `"1.2"`/`"1.3"` string; the cli layer does that mapping. Appendix A lists no
  default for the user agent or DB path, so only the enumerated settings are
  asserted against the table in the test.
- `Validate()` wraps `core.ErrConfig` with `%w` (not a bare `errors.New`) so the
  boundary's `errors.Is(err, core.ErrConfig)` matches and maps to exit 1; the
  test asserts that explicitly rather than checking the message.

## fee-po72 — Output envelope, renderers and color gating

- `core.FeedError` has no JSON tags (its `Error()` is for humans/logs). The
  stderr JSON shape is an **unexported** `errorPayload` in `output`
  (`{category, feed_url omitempty, status omitempty, message}`). The `message`
  field is just the human message (prefers `e.Message`, falls back to
  `e.Err.Error()`); it deliberately drops the `category/url/status` prefix that
  `FeedError.Error()` adds, because those are already structured fields. Keeping
  the shape in `output` (not `core`) avoids leaking presentation tags into the
  domain type.
- `WriteJSON` uses `json.NewEncoder(w).Encode`, which is already compact (no
  indent) and appends exactly one trailing newline. No manual `Marshal` +
  newline needed. Tests assert compactness via `strings.Count(out, "\n") == 1`.
- TTY detection is pure stdlib: `f.Stat()` then `fi.Mode()&os.ModeCharDevice`.
  No `golang.org/x/term` dependency. This keeps the dependency-light invariant
  and is trivially testable: a regular temp file is never a char device, so
  `ResolveColor` returns false for it. The color-enabled (`true`) path needs a
  real tty and is intentionally not unit-tested; all `ResolveColor` assertions
  are negative.
- `NewRenderer` takes `*os.File` (it must `Stat` the stream to resolve color),
  but the `Renderer` struct fields are `io.Writer`. That split is the test seam:
  unit tests construct a `Renderer` literal with `bytes.Buffer` and set
  `OutColor`/`ErrColor` directly, bypassing `NewRenderer`/tty entirely.
- An anonymous `interface{ Write([]byte)(int,error) }` is **not** identical to
  `io.Writer` for method-set matching, so a test double implementing
  `RenderText(w interface{...}, ...)` will silently fail to satisfy a
  `TextRenderer` whose method takes `io.Writer`. Use the named `io.Writer` in
  the double's signature.
- golangci-lint v2 here runs `gosec` and `errcheck` over `_test.go` too:
  `os.Create(varPath)` trips gosec G304 (use `os.CreateTemp(dir, pat)` instead),
  and a bare `os.Unsetenv(...)` trips errcheck. Prefer `t.Setenv` for env
  manipulation in subtests; it auto-restores at the end of each subtest, so
  there is no need to `os.Unsetenv` a value set by a sibling subtest.
- Text status markers pair a symbol with color (`✗` + red for failures) so
  meaning survives color stripping. The symbol is emitted unconditionally; the
  ANSI red wrap is added only when the stream's color is on.

## fee-7ons — CLI skeleton (urfave/cli v3 root, flags, exit boundary)

- urfave/cli v3 (`v3.10.1`) only invokes the `CommandNotFound` hook from its
  help machinery (`ShowCommandHelp`), **not** from normal dispatch. For
  `feedwatch bogus`, `subCmd` resolves to nil, no `DefaultCommand`, so the
  framework just runs the **root `Action`** with `bogus` as a leftover
  positional arg. So unknown-command interception lives in `rootAction` (if
  `cmd.Args().Present()` -> usage `*FeedError`); `CommandNotFound` is set too but
  only as a belt-and-suspenders for the `help <topic>` path. Bare invocation
  (no args) prints root help via `cli.ShowRootCommandHelp` and exits 0.
- `Command.Version` **must be non-empty** or `command_setup.go` force-sets
  `HideVersion = true` and the `--version`/`-v` flag never appears. `main` passes
  `version = "dev"` (override at link time with `-ldflags="-X main.version=..."`).
- The version JSON `{version,commit,go}` is emitted by overriding the
  package-global `cli.VersionPrinter` (a `func(*cli.Command)`). This is a
  controlled mutable-global, in the same category the design carves out for
  `OsExiter`/`ErrWriter`; it is set once from the single `NewRootCommand`
  construction point. `commit` comes from `runtime/debug.ReadBuildInfo()`
  `vcs.revision` — **empty under `go run` and `go test`**, but correctly stamped
  in binaries built by `make build`. `go` is `runtime.Version()`.
- Exit boundary: `cmd.Run` calls the custom `ExitErrHandler` _inside_ Run (via
  `handleExitCoder`) and still returns the error, so `main` also calls
  `cli.HandleExitCoder(err)`. In production `OsExiter == os.Exit`, so the handler
  terminates the process and main's second call is unreached; with `err == nil`
  it is a no-op. In tests `OsExiter` is captured (no exit), and the handler's
  call records the code. The handler checks `errors.As(err, &cli.ExitCoder)`
  **first**: an `exitError{2|3}` (feed-outcome) just sets the code with no stderr
  output (the envelope was already written to stdout by the action); everything
  else is a hard failure rendered as one JSON error object with code from
  `core.ExitCodeFor`.
- Flag validators (`oneOf`) produce parse-time errors that route through
  `OnUsageError` -> usage `*FeedError` -> exit 1. Numeric/duration validation
  (e.g. `--concurrency 0`) is left to `config.Validate()` in the Before hook,
  which wraps `core.ErrConfig` -> exit 1 with category `config`. Two different
  paths, two categories.
- Global flags are inherited by subcommands because `FlagBase.Local` defaults to
  `false` (persistent); no extra wiring needed. Precedence flags > env > defaults
  is native: flag `Value` is the default, `Sources: cli.EnvVars(...)` is the env
  layer, the command line overrides both.
- Tests are **white-box** (`package cli`, not `cli_test`) so they can drive
  `cmd.Run` with an injected stub subcommand (`root.Commands = append(...)`) and
  read the unexported context accessors (`configFrom`/`loggerFrom`/
  `rendererFrom`). Streams are temp files: a regular file is not a char device,
  so `ResolveColor` returns false and text output is never colorized in tests.
  `cli.OsExiter` is swapped per-run and restored with `t.Cleanup`.
- The XDG default DB path (`$XDG_STATE_HOME/feedwatch/feedwatch.db`, falling back
  to `~/.local/state/...`) is resolved by `resolveStorePath` in the cli layer, as
  the `config` package deliberately left it (see fee-cmkb). A non-empty `--db`
  value (path or `postgres://` DSN) passes through untouched.

## fee-63n9 — E1 epic (foundation gate)

- This epic is a **dependency gate, not a code ticket**: its substantive scope
  is fully delivered by its closed children (error taxonomy, interfaces+Clock,
  config, output/color, slog, cli skeleton) plus the signal-aware context wired
  in `cmd/feedwatch/main.go`. Closing it required no new code, only verifying the
  pieces integrate under a green `make build` and a live smoke test of the
  contract (`--version` JSON, `--format text --version`, unknown-command JSON
  error on stderr exit 1).
- Its one open child, fee-c66o (walking skeleton: version + `migrate --status`
  end-to-end), is **intentionally blocked by the lanes the epic gates**
  (`fee-bqne` -> `fee-vlk9` -> `fee-aqkn`). That is not a scheduling bug: the
  capstone integration is proven _after_ the persistence/migrate lanes land, so
  the parent epic closes before that child. Closing the epic is what makes the
  six P1 lanes (`fee-bqne`, `fee-b91x`, `fee-e487`, `fee-lzyw`, `fee-zuz2`,
  `fee-lfyq`) ready — they were each blocked solely by `fee-63n9`.

## fee-bqne — SQLite store implementation

- `core.Clock` is a **func type** (`type Clock func() time.Time`), not an
  interface, and `core.SystemClock` is a `var` of that type, not a struct. Call
  it as `s.clock()`, not `s.clock.Now()`; `WithClock` takes a plain func and
  test doubles are bare functions (`func fixedClock() time.Time`).
- Tests use **temp-file** DBs (`filepath.Join(t.TempDir(), "feedwatch.db")`),
  never `:memory:`. With `*sql.DB`'s connection pool, each new connection to an
  in-memory database gets its own **separate** empty DB, so a migrate on one
  conn is invisible to a query on another. A file DB (WAL) is shared across the
  pool, which is also what the concurrent-poll design needs.
- `alias` is stored as SQL **NULL when empty**, not `''`. `alias TEXT UNIQUE`
  treats NULLs as distinct but would collide every aliasless feed on `''`.
  `aliasArg("")` returns `nil`; reads scan into `sql.NullString` (NULL -> "").
- golangci-lint here is strict on three things this ticket hit:
  - **errorlint** rejects `fmt.Errorf("...: %v: %w", err, sentinel)` — a `%v`
    on an error value is flagged. To wrap a cause _and_ a sentinel in one error,
    use `errors.Join(err, core.ErrStoreUnavailable)` under a single `%w`.
  - **gosec G202** flags SQL built with `+` concatenation of a non-constant
    (e.g. `"SELECT " + strings.Join(cols, ", ")`). Build dynamic queries with a
    `strings.Builder` (`WriteString`) instead; gosec's check is on the `+`
    expression, and the Builder form is the idiomatic, injection-safe pattern.
    Column names and clause fragments come from fixed internal allowlists; every
    caller value still flows through bound `?` params. No `//nolint` needed.
  - `errors.Is(err, sql.ErrNoRows)`, never `err == sql.ErrNoRows` (errorlint).
- **Dedup / tombstone** is a three-way classification in `UpsertItems`, done
  with a `SELECT tombstoned ...` then branch inside the per-feed tx: absent row
  -> INSERT + return as new; live row -> refresh mutable content, not new;
  tombstoned row -> leave untouched (never resurrect, never re-emit). Pruned
  items keep their `(feed_url, dedup_key)` row so a still-advertised item stays
  deduped after a re-poll.
- **Prune by max-per-feed** uses a window function:
  `ROW_NUMBER() OVER (PARTITION BY feed_url ORDER BY COALESCE(published_at,
fetched_at) DESC, dedup_key DESC)` and tombstones rows where `rn > N`. modernc
  SQLite supports window functions. Age-prune runs first, then max-per-feed
  (both `WHERE tombstoned=0`), so the two `RowsAffected` counts are disjoint and
  sum correctly.
- **Schema ownership vs fee-vlk9**: the `Store` interface forces `Migrate` and
  `SchemaVersion` to exist, so this ticket created `migrations/0001_init.sql`
  (the feeds/items DDL incl. the `tombstoned` column + indexes) and a working
  transactional applier (`applyMigration` BEGIN/exec/record/COMMIT, ROLLBACK on
  error). fee-vlk9 still owns the `dbMax > codeMax` -> `ErrSchemaTooNew` guard,
  the `pending()` count for `migrate --status`, and the adversarial
  rollback/idempotency tests. Build on `migrations/` rather than re-creating it.

## fee-vlk9 — Migrations engine and schema-version guard

- Built directly on the fee-bqne applier; no rewrite. The three missing pieces
  were the too-new guard, the pending count, and the adversarial tests.
- The too-new guard was a real gap, not a rename: before this, a db at a version
  beyond `codeMax` fell through the `if m.version <= current { continue }` loop
  and `Migrate` returned `(0, nil)` — silent no-op on a future db. The guard now
  computes `codeMax = maxVersion(ms)` (last element of the ascending-sorted
  slice, 0 if empty) and returns `fmt.Errorf("...: %w", core.ErrSchemaTooNew)`
  when `current > codeMax`.
- `Pending` is exported and added to the `store.Store` interface (not just the
  concrete type) so the downstream `migrate` command (fee-aqkn) reads
  `SchemaVersion` + `Pending` polymorphically with no type assertion. Cost was
  one extra conformance line in the `fakeStore` double. The design doc wrote
  `pending` lowercase, but exposing it on the interface is the clean way to
  satisfy "exposed for `migrate --status`".
- Test seam for the rollback case: extracted `Migrate` -> unexported
  `applyMigrations(ctx, []migration)`. Injecting a `[]migration` slice directly
  (valid `0001` + deliberately broken `0002`) is simpler than the `embed.FS` /
  `fstest.MapFS` injection the ticket sketched — `migration` is already the
  internal unit, so the test builds two and asserts `SchemaVersion == 1` after
  the broken step aborts (proving per-migration transaction rollback).
- These tests are **white-box** (`package sqlite`, `migrate_internal_test.go`):
  the too-new test stamps a future version via `s.db.ExecContext` and the
  rollback test calls the unexported `applyMigrations` — neither is reachable
  from the external `sqlite_test` package. The happy-path idempotency test stays
  external in `sqlite_test.go`.

## fee-b91x — HTTP client (base Fetcher)

- **Name collision**: `fetch.Fetcher` is already the consumer **interface** (in
  `fetch.go`). The ticket's design block writes `type Fetcher struct{...}` for
  the concrete impl, which does not compile in the same package. The concrete
  type is therefore `fetch.Client`, with `var _ fetch.Fetcher =
(*fetch.Client)(nil)` as the conformance check. Watch for this whenever a
  ticket's sketch names the struct the same as its interface.
- Two deadlines, two mechanisms: the **connect** timeout is the `net.Dialer`
  `Timeout` (and `Transport.TLSHandshakeTimeout`); the **overall** deadline is a
  per-call `context.WithTimeout(ctx, overall)` in `Fetch`, not
  `http.Client.Timeout` (a client-wide timeout would be shared, not per-call,
  and harder to test). The timeout test uses a handler blocked on a channel
  released only in a deferred close, so the server goroutine never leaks.
- Proxy default is `http.ProxyFromEnvironment` (honors
  `HTTP_PROXY`/`HTTPS_PROXY`/`NO_PROXY`); `WithProxy` overrides with
  `http.ProxyURL`. `url.Parse` is lax — `"://not a url"` does parse-fail, but
  many junk strings don't, so the bad-proxy test must pick a genuinely invalid
  URL.
- `MIMEType` is the **bare** media type: `mime.ParseMediaType` strips the
  `; charset=...` param (the charset ticket fee-fp2h consumes the raw header
  separately). `FinalURL` is `resp.Request.URL.String()` (post-redirect), not
  the request URL.
- Error classification lives at the `Fetch` boundary already: deadline
  (`errors.Is(err, context.DeadlineExceeded)`) or `net.Error.Timeout()` ->
  `core.TimeoutErr` (`CatTimeout`); everything else -> `core.NetworkErr`
  (`CatNetwork`). The retry ticket (fee-r0rt) builds on this rather than
  re-classifying.
- Transport internals (TLS `MinVersion`, dialer, `CheckRedirect`) are asserted
  **white-box** (`package fetch`, `fetch_internal_test.go`) since they are
  unexported; behavior is tested **black-box** (`package fetch_test`) via
  `httptest`.
- gosec **G304** fires on `os.ReadFile(caBundlePath)` even for a trusted
  operator-config path. The repo had no prior `//nolint`; resolved with a
  targeted `//nolint:gosec // G304: trusted operator config path` plus a comment
  explaining the path is never network/item input. This is the documented-
  exception pattern, distinct from silencing with `_ =` (which CLAUDE.md
  forbids).
- `fetch.New` deliberately does **not** import `config`: it re-declares its own
  `defaultUserAgent`/timeout consts so the package stays a low-level leaf. The
  cli layer overlays resolved config via the `With*` options.

## fee-e487 — Parser: gofeed wrapper and format coverage

- First ticket to pull in `github.com/mmcdole/gofeed` (v1.3.0); it is now a
  direct `require` in `src/go.mod` (was deferred per fee-mls1). `go mod tidy`
  pulled a handful of transitive deps (goquery, goxpp, etc.) — all indirect.
- **The universal `gofeed.Feed` silently drops RSS `<ttl>`.** `fp.Parse` returns
  a unified model whose `DefaultRSSTranslator` never copies TTL onto the
  universal struct; only the format-specific `rss.Feed.TTL` (a raw minutes
  string) carries it. To map TTL, `extractTTL` re-parses RSS bodies with
  `&rss.Parser{}` and converts minutes -> `time.Duration`; Atom/JSON Feed have
  no equivalent and yield 0. The second parse is cheap for feedwatch's small
  one-shot bodies and leaves the universal path untouched. Watch for the same
  drop on any other RSS-only field (e.g. `skipHours`, `<cloud>`).
- Use `it.Authors[0].Name`, **not** the deprecated `it.Author` — `gofeed.Item`
  marks `Author` deprecated, and `staticcheck` (SA1019, enabled here) fails the
  build on it. gofeed populates `Authors` from the same source.
- This ticket is **raw field mapping only** by design: `Description->Summary`,
  `Content->ContentHTML` are taken verbatim (gofeed already resolves
  `content:encoded`/Atom `content` into `.Content` and CDATA/entities). The
  precedence cascades (content falling back to description, author item->feed->
  `dc:creator`), date normalization, base-URL, and MIME live in fee-kx39; the
  dedup key in fee-lzyw. Keep them out of the parser.
- Test fixtures are embedded with `//go:embed testdata` + `embed.FS` rather than
  `os.ReadFile(filepath.Join("testdata", name))`. The latter trips gosec G304
  (variable file path); `embed.FS.ReadFile` sidesteps it with no `//nolint` and
  bakes fixtures into the test binary.
- The malformed-recoverable fixture must actually break strict XML to be a
  meaningful leniency test: a raw unescaped `&` in element text makes
  `encoding/xml` fail with "invalid character entity & (no semicolon)" while
  gofeed still yields the item. Verified both halves; a fixture that merely has
  leading whitespace is strict-valid and proves nothing.
- The `Parse(ctx, ...)` ctx param is unused: `gofeed.Parse(io.Reader)` is a
  synchronous in-memory parse with no context-aware variant. Unused interface
  params are idiomatic Go and not flagged by the configured linters (`revive`'s
  unused-parameter rule is off).

## fee-zuz2 — Test harness (store double, http mock, fixtures, fake clock)

- `src/internal/testsupport` is a **normal importable package** (not `_test.go`
  files), so it must carry its own `doc.go` for `revive`. It is test-only by
  having no production dependents, not by a build tag. `fixtures.go` importing
  `testing` (so `Fixture(t, name)` can `t.Fatalf` on a missing fixture) is
  **not** flagged by golangci-lint here — acceptable because the whole package
  is test-only, mirroring the testify pattern.
- The `InMemoryStore` double deliberately **mirrors the SQLite store's
  observable semantics**, not just the signatures, so command tests written
  against it match production: `GetFeed` miss returns a `CatUsage` "feed not
  found" `*FeedError` (same as `feeds.go`); alias-to-different-URL is `CatUsage`;
  `SetValidators` skips empty values and no-ops when both empty; `UpsertItems` is
  the same three-way dedup (absent->new, live->refresh, tombstoned->skip) and
  prune tombstones preserve the `(feed_url, dedup_key)` fingerprint. When the
  SQLite behavior changes, this double must change with it.
- **`contains` filter is case-insensitive** in the double (`strings.ToLower`
  both sides) because SQLite `LIKE` is case-insensitive for ASCII by default. A
  naive `strings.Contains` would silently diverge from the real store. Title and
  content fields are joined with a `\x00` separator so a match can't straddle a
  field boundary.
- `--fields` projection is reproduced by building a fresh `core.Item` with only
  the always-retained identity/ordering fields (`feed_url`, `dedup_key`,
  `published_at`, `fetched_at`) plus the mapped requested fields — the same
  always/projected split as `items.go`'s `alwaysColumns`/`fieldColumns`.
- **`FakeFetcher` keys on URL, `FakeParser` keys on baseURL, and poll passes the
  feed URL as the parse baseURL** — so registering the same URL string in both
  doubles composes end-to-end (fetch then parse) with no extra wiring.
- Encoded fixtures are generated as **real bytes via `iconv`** (no editor can be
  trusted to keep them): `iconv -t ISO-8859-1` yields raw `0xE9` for `é`, and
  UTF-16LE needs a hand-prepended `\xff\xfe` BOM (`printf '\xff\xfe' | cat -
...`). `xxd` is absent in this env; use `od -A d -t x1` to verify signatures.
  These fixtures stay **raw/undecoded** — the charset->UTF-8 step is fee-fp2h's
  job; `parse.New()` is only asserted against the UTF-8 fixtures.
- A transient `go test` setup error (`stat .../testdata/internal/testsupport:
directory not found`) appeared once and vanished on a plain re-run; it tracks
  the harness's "modification time in the future / clock skew" warnings, not a
  real `//go:embed` problem. Re-run before chasing it.

## fee-juo8 — SSRF guard, redirect re-check, 301/308 rewrite signal

- **`CheckRedirect` is only called on redirects, never the initial request.**
  This is what makes "direct private URL allowed, public-to-private redirect
  blocked" fall out cleanly: the directly-supplied URL is dialed as given, and
  the guard's policy only engages from the first redirect hop onward. No
  dialer-level exception for the first request is needed.
- Policy in `checkRedirect`: block a hop only when the **origin** (`via[0]`)
  resolves public AND the new **target** resolves private. A private/loopback
  origin (self-hosted LAN reader) is exempt and its redirects into private space
  are allowed; `WithAllowPrivate(true)` lifts the restriction entirely. httptest
  servers all bind `127.0.0.1`, so an end-to-end "public origin" case is not
  expressible against real `httptest` — behaviors 1-2 (block / allow-private) are
  **white-box** tests calling `g.checkRedirect` directly with a fake resolver
  mapping names to chosen public/private IPs; behaviors 3-4 (direct loopback
  allowed, 301 sets `FinalURL`+`Permanent`) are black-box against `httptest`
  (loopback origin -> loopback target is the private-origin exception, so the
  redirect is allowed and the flags get set).
- `net.IP.IsPrivate()` covers RFC1918 **and** unique-local IPv6 (`fc00::/7`) but
  **not** CGNAT (`100.64.0.0/10`, RFC 6598) — that range is checked manually
  against a `net.IPNet`. Full `isPrivate` set: loopback, RFC1918, link-local
  (unicast+multicast, v4+v6), unique-local v6, unspecified, CGNAT. `hostPrivate`
  treats a host as private if **any** resolved IP is private (conservative
  against split-horizon / rebinding DNS that mixes public and private answers).
- **`Permanent` (301/308) needs per-call state, not a Client field.** The
  `*Client` is reused concurrently, so the redirect tracking lives in a
  `*redirectState` placed in the request `context` (`redirectStateKey`) by
  `Fetch` and read back after `Do`. `checkRedirect` reads `req.Response`
  (populated by net/http only during redirects) and sets `permanent` per hop,
  last-hop-wins — the final hop's status is the one that reached `FinalURL`.
  Verified race-free with `go test -race`.
- A custom `CheckRedirect` **replaces** net/http's built-in 10-hop limit, so the
  guard re-implements `maxRedirects = 10` itself (returns a `CatNetwork`
  `*FeedError`); without it, a redirect loop would never terminate.
- The guard returns a `*core.FeedError` (`CatNetwork`) from `checkRedirect`. The
  http client wraps it in a `*url.Error`, so `classify` was changed to
  `errors.As` for an embedded `*core.FeedError` and return it **as-is** before
  the timeout/network re-wrap — otherwise the SSRF message and category would be
  clobbered by a fresh `NetworkErr`. The resolver is an internal `func` seam
  (`defaultResolve`: IP literals resolve to themselves, else
  `net.DefaultResolver`), deliberately not a public option — tests build
  `ssrfGuard` directly.
- The chosen mechanism is **resolve-and-classify inside `CheckRedirect`**, which
  satisfies the ticket's "re-check the resolved address after every redirect
  hop." A separate IP-pinning `DialContext` (to fully close the resolve-vs-dial
  TOCTOU / DNS-rebinding window) was judged out of scope for the 4-behavior
  contract and would add a connect-time resolver seam; noting it here as the
  obvious hardening if a stricter guard is ever wanted.

## fee-lzyw — Dedup-key derivation

- `parse.DedupKey(core.Item) string` is a **pure function only**; this ticket
  does not wire it in. The parser's `mapItem` still leaves `DedupKey` empty (its
  doc comment explicitly defers the dedup key to "later layers"), so the
  assignment belongs to the consumer `fee-q6t3` (poll: dedup-and-consume), which
  will call `parse.DedupKey` and set `core.Item.DedupKey` before items reach the
  store. Don't add the assignment to the parser — it would duplicate work and
  contradict the parser's raw-mapping-only contract.
- Precedence is `GUID -> Link -> Title`, matching the store's
  `(feed_url, dedup_key)` uniqueness. There is **no `link+published` rung** (by
  design, per docs/cli-design.md Poll Semantics): keying on the date would make a
  re-dated item look new and break dedup.
- "Never empty" last resort: when GUID, link, and title are all empty, return
  `"sha256:" + hex(sha256(title + "\x00" + link))`. For a fully empty item this
  is a fixed constant, which is the documented degenerate case — it keeps the
  key non-empty rather than attempting to disambiguate items that carry no
  identity at all.

## fee-kx39 — Item normalization (dates, precedence, base URL, MIME)

- `parse.Normalize(raw RawItem, feedBase, feedURL, feedAuthor string) core.Item`
  is a pure mapping and **is wired into `GofeedParser.Parse`** this time
  (unlike `parse.DedupKey` in fee-lzyw, which poll wires). It replaces the old
  raw `mapItem`; the parser now emits fully normalized items. `FeedURL` and
  `DedupKey` are still left empty here on purpose: the store sets `FeedURL` in
  `UpsertItems`, and poll (`fee-q6t3`) sets `DedupKey` via `parse.DedupKey`.
- **gofeed's universal model silently drops an entry's `xml:base` AND the Atom
  content `type`.** This is the same class of loss as the `<ttl>` drop noted in
  fee-e487. The atom `Entry` struct has no `XMLBase` field at all, and the
  universal `Item.Content` is a bare string with no type. So `RawItem` carries
  `XMLBase`/`ContentType` fields, but on the gofeed path they are always empty
  and `BaseURL` effectively resolves to item link -> feed link -> feed URL,
  while `ContentMIMEType` stays empty. `Normalize` still implements the full
  documented precedence (`xml:base` first, MIME passthrough) so a stricter
  parser swapped in behind the `Parser` interface can populate them, and the
  unit tests build `RawItem` literals with those fields set to prove the logic.
- The `Normalize` signature has both `feedBase` and `feedURL`. They are distinct
  tiers: `feedBase` is the feed's declared base (passed as `feed.Link` from the
  parser), `feedURL` is the canonical subscription URL (the `Parse` `baseURL`
  arg). Base-URL precedence is `firstNonEmpty(raw.XMLBase, item.Link, feedBase,
feedURL)` — the doc's three tiers (`xml:base`, item link, feed URL) plus the
  feed-link tier in the middle, which only matters for items with no own link.
- Author cascade is `item author -> feed managingEditor -> dc:creator`. gofeed
  already unifies RSS `managingEditor` (with `dc:author`/iTunes fallbacks) onto
  `feed.Authors[0]`, so `feedAuthorName(feed)` reads that; the **final** tier is
  `it.DublinCoreExt.Creator[0]` (dc:creator), which is distinct from the
  dc:author that gofeed folds into the item author. Don't conflate the two.
- `content_text` is de-tagged with `golang.org/x/net/html` (`html.Parse` + a
  text-node walk), now a **direct** dep (`go mod tidy` promoted it from the
  indirect block; it was already in the graph via goquery/cascadia). The walk
  skips `script`/`style`, emits `\n` around block elements so adjacent blocks
  don't run together, then `collapseLines` trims/collapses per line and drops
  blanks. `html.Parse` also resolves entities (`&amp;` -> `&`), which is what we
  want for plaintext. A regex tag-strip would not handle entities or nesting.
- Dates: `toUTC` converts a non-nil `*time.Time` to UTC and passes nil through
  unchanged. gofeed's `PublishedParsed` is already nil for unparseable dates, so
  the "never fabricated" rule falls out for free; we only add the `.UTC()`
  normalization (the store later writes fixed-width RFC3339).

## fee-fp2h — Charset decode to UTF-8

- **Do not use `charset.DetermineEncoding` for this precedence.** The ticket
  requires `BOM > XML declaration > Content-Type > lossy UTF-8`, but
  `golang.org/x/net/html/charset.DetermineEncoding` orders `BOM > Content-Type >
content sniff (XML/meta) > windows-1252 default`. Its Content-Type-before-XML
  ordering violates the contract, and its windows-1252 fallback is not the lossy
  UTF-8 the ticket wants. `decodeBody` implements the four rungs explicitly
  instead, using `charset.Lookup(name)` for the XML-decl and Content-Type rungs.
- **BOM bytes must be consumed, or a stray U+FEFF leaks into the output.**
  `charset.Lookup("utf-16le")` returns an `IgnoreBOM` decoder that would keep the
  BOM as a leading ZWNBSP. `bomEncoding` returns `(encoding.Encoding, bomLen)` and
  the caller decodes `raw[bomLen:]`, so the BOM is sliced off before decoding.
  UTF-16 endianness from the BOM table: `FE FF` is big-endian, `FF FE` is
  little-endian (the `utf16-bom.xml` fixture is LE). UTF-8 BOM is `EF BB BF`,
  3 bytes, then `unicode.UTF8`.
- **Lossy fallback is `bytes.ToValidUTF8(raw, replacement)`**, not the
  `unicode.UTF8` decoder — the latter _errors_ on invalid sequences rather than
  replacing them. The garbage-charset and BOM-decode-failure paths both route
  here. The `replacement` is U+FFFD as a package var.
- BOM-wins-over-Content-Type is provable end to end: feed UTF-16LE bytes with a
  conflicting `charset=utf-8` header; if Content-Type won, decoding UTF-16 as
  UTF-8 yields garbage, so asserting the exact decoded string proves the BOM
  rung fired first.
- The XML-declaration regexp is matched against the leading bytes **as ASCII**
  (an XML declaration is ASCII even when the body is single-byte ISO-8859-1).
  This rung is effectively skipped for BOM-less UTF-16 (declaration bytes are
  null-interleaved and won't match), which is fine because real UTF-16 feeds
  carry a BOM and hit rung 1.
- `go mod tidy` promoted `golang.org/x/text` from `// indirect` to a direct
  require once `encoding` and `encoding/unicode` were imported; `golang.org/x/net`
  (for `html/charset`) was already direct via the parser's `html` use.
- A literal BOM character pasted into a Go test source file fails compilation
  with `illegal byte order mark`. Use the `'\uFEFF'` rune escape in assertions,
  never the raw glyph.

## fee-r0rt \u2014 Transient in-call retry classification

- `WithRetry(attempts, backoff)` is a `fetch.Option`, so retry lives **inside**
  `Client.Fetch`, not in a separate decorator. Made it **opt-in**: `New()`
  defaults `retryAttempts=1` (single shot), so the existing single-request tests
  (`TestFetchUnreachableHostIsNetworkError`, `TestFetchOverallTimeout`) stay fast
  and unchanged, and a dead host isn't silently retried by every caller. The cli
  layer overlays `config.RetryAttempts` (already 3 in `config.Defaults`) via the
  option later; `fetch` still doesn't import `config` (consistent with fee-b91x).
- **This ticket introduced HTTP-status\u2192error mapping at the fetch boundary.**
  Before, `Fetch` returned 4xx/5xx as a _successful_ `FetchResult` with the
  status set. The retry loop needs to know 5xx/429 are transient and "return the
  last error" on exhaustion (test 4), and 404 is deterministic (test 2), so the
  natural design is: `Fetch` now returns a `core.HTTPErr(url, status)` (CatHTTP)
  for any non-2xx, non-304 status that is not (or no longer) retried, and a
  result only for 2xx/304. No caller relied on the old behavior (poll/fee-u0i4
  isn't built yet), and it gives poll a clean contract. The single-attempt helper
  `attempt()` therefore only reads/decodes the body for 2xx; other statuses get a
  status-only result. If a future ticket claims to own HTTP classification,
  reconcile with this.
- **Distinguishing a retryable network error from an SSRF block** (both are
  `CatNetwork`): a genuine transport failure wraps a `net.Error`
  (`*net.OpError`/`*net.DNSError`), whereas the SSRF/redirect guard builds a
  `*core.FeedError{Category: CatNetwork, Message: ...}` with **`Err == nil`**. So
  `isTransient` retries `CatNetwork` only when `errors.As(fe.Err, &net.Error)`
  succeeds. This is why the guard sets `Message` not `Err` \u2014 don't "fix" that to
  wrap a cause or SSRF blocks become retryable. The maxRedirects error wraps a
  plain `fmt.Errorf` (not a `net.Error`) so a redirect loop is also non-transient.
- **Retry-After + "capped by the overall deadline" falls out of one mechanism.**
  The whole retry loop runs under one `context.WithTimeout(ctx, f.overall)`, and
  the inter-attempt wait is `sleepContext(ctx, d)` which `select`s on `ctx.Done()`
  vs a timer. A `Retry-After: 1` (or any value) larger than the remaining budget
  just trips `ctx.Done()` and the loop returns the last error \u2014 no explicit cap
  arithmetic. `parseRetryAfter` handles both delta-seconds and HTTP-date forms.
- **Test seam for honoring Retry-After without sleeping**: an unexported
  `f.sleep` field (defaults to `sleepContext`) is overridden in a white-box test
  to record the requested delay and return immediately, asserting the 429
  `Retry-After: 1` produced a 1s wait (over the configured fixed backoff). The
  four httptest behaviors are black-box with a 1ms `fastBackoff` to stay fast;
  `isTransient` is table-tested white-box. `go test -race ./internal/fetch` clean
  (the per-call `redirectState` already lives in the request context, so the
  added loop introduced no shared state).

## fee-8cau — E3 epic (fetching gate)

- Closed as a **dependency gate, not a code ticket** (same pattern as fee-63n9):
  all five children (`fee-b91x` HTTP client, `fee-t2ra` conditional GET,
  `fee-juo8` SSRF, `fee-r0rt` retry, `fee-fp2h` charset) were already closed and
  integrate under a green `make build` with `go test -race ./internal/fetch`
  clean. Closing required no new code, only verifying the handoff contract.
- The verified handoff to the downstream poll lane is the `core` fetch types:
  `FetchRequest{URL,ETag,LastModified}` (conditional GET) and
  `FetchResult{NotModified,Status,FinalURL,Permanent,ETag,LastModified,Body,
MIMEType}` (304 skip, 301/308 URL rewrite, validators, UTF-8 body, MIME).
  `internal/fetch` depends only on `internal/core` (acyclic — `go list -deps`).
- **The epic's prose says it "covers requirements 10 and 11", but requirement 11
  (worker pool, per-host serialize + delay, stable-order aggregation) has no code
  and no child here.** Only the config knobs exist (`Concurrency=8`,
  `PerHostDelay=1s`) plus a concurrency-safe `Client` (per-call redirect state in
  the request context). Req 11's orchestration is owned by the poll lane
  `fee-u0i4` (`internal/poll` is still just `doc.go`), which E3 blocks. Treat the
  tickets, not the epic blurb, as the source of truth for scope: E3 delivers the
  HTTP-level primitives; the pool lives in poll.
- Dependency map (verified from each ticket's `deps:` field, not the epic's
  over-broad "Blocking" list): `fee-0l84` (discover) depends ONLY on the two
  parsing/fetching gates `[fee-8cau, fee-2heq]`, so once both E3 and E4 are
  closed it becomes **ready**. The `add` (`fee-4q22`) and poll-orchestration
  lanes are the ones additionally blocked by E2 (`fee-gyos`). Don't lump the
  three command lanes together — check each ticket's actual deps.

## fee-aqkn — migrate command

- **First subcommand to land**, so it also introduced the subcommand-tree wiring:
  a new `Deps.commands()` method returns `[]*cli.Command` and is set on the root
  via `Commands:` in `NewRootCommand`. Subcommands that need `Deps` fields
  (`Clock`, later `Store`/`Fetch`/`Parse`) are methods on `Deps`
  (`d.migrateCommand()`), closing over `d` — the same closure pattern as
  `d.before()`/`d.exitErrHandler()`. The `Action` is `d.migrateAction`, a method
  value with the urfave `func(ctx, *cli.Command) error` signature.
- **The store is opened by the command, not injected.** `migrate` is about schema
  lifecycle and runs before a store is usable, so it opens its own store from the
  resolved `--db` via a new `openStore(cfg, clock)` helper in
  `internal/cli/storeopen.go`; `Deps.Store` (for the later poll/add lanes) is left
  untouched. `openStore` returns `(store.Store, backend string, error)` and the
  action `defer st.Close()`s it. The cli layer is the composition root, so it can
  import both `store` and `store/sqlite` without a cycle (there is **no**
  `store.Open` dispatcher — adding one would make `store` import `sqlite`).
- **Backend is decided by URL scheme**, factored into `backendName(dsn)`
  (`postgres://`|`postgresql://` -> `postgres`, else `sqlite`) so `--status` can
  report it and the postgres deferral is a single branch. A `postgres://` DSN
  returns a `CatConfig` `*FeedError` ("postgres backend not yet implemented") that
  the boundary maps to exit 1 on stderr — verified end-to-end.
- `core.Clock` is a nil-comparable func type, so `orSystemClock` falls back to
  `core.SystemClock` when `Deps.Clock` is unset (the `runRoot`/`runWithStub` test
  harness does set it, but a bare `Deps{}` would not).
- On a fresh db `SchemaVersion` returns 0 (its `no such table` branch) and
  `Pending` returns the embedded-migration count, so `migrate --status` works
  **before** any migrate — `--status` never applies. Tests drive `cmd.Run` with a
  temp-file db (never `:memory:`, per fee-bqne) through the existing `runRoot`
  helper by prepending `--db <tmp>`. **NOTE: this `--status` non-applying
  decision was reversed by fee-c66o (see below).**

## fee-c66o — Walking skeleton (version + migrate --status end-to-end)

- This is the E2 capstone integration; almost all the code already existed
  (version printer, migrate command, store, migrations). The substantive work
  was the four end-to-end behavior tests **and one design reconciliation the
  integration surfaced**.
- **fee-aqkn's "`--status` never applies" decision was wrong and is reversed
  here.** fee-c66o behavior 2 requires `migrate --status` on a _fresh_ db to
  report `schema_version >= 1, pending == 0`, the exact opposite of fee-aqkn's
  `TestMigrateStatusFreshDB` (`version==0, pending>=1`). The authoritative
  sources both say apply-on-any-command: the ticket design block ("opens/creates
  the store, **ensures schema**, prints `pending:0`") and `docs/cli-design.md`
  Schema Lifecycle ("On **any command**, feedwatch checks a stored schema version
  and **applies pending migrations idempotently**"); the doc's own `migrate
--status` example even shows `pending:0`. Fix was surgical: the `--status`
  branch of `migrateAction` now calls `st.Migrate(ctx)` (discarding the count)
  before reading `SchemaVersion`/`Pending`. Updated `TestMigrateStatusFreshDB`
  to the new ensure-then-report semantics.
- **Bare `migrate` is deliberately left untouched** — it still applies and
  reports a real `applied` count, so `TestMigrateAppliesThenStatusClean` stays
  valid (bare migrate applies N>=1, then `--status` re-applies idempotently to 0
  and reports the matching version, pending 0). Don't "simplify" by routing both
  through one path; the two envelopes (`{applied,schema_version}` vs
  `{schema_version,pending,backend}`) are the point.
- **Apply-on-any-command is only wired for `migrate` so far, NOT globally.** The
  poll/add/list/etc. commands don't exist yet (all blocked on E2). When they
  land they should ensure the schema **on store open** (the natural home is
  `openStore` in `storeopen.go`, the single open site) rather than each
  re-implementing the ensure or duplicating the migrate command. Do **not** add
  the ensure to `openStore` now: bare `migrate` opens via the same `openStore`
  and would then always report `applied==0`, breaking its contract. The clean
  resolution when wiring real commands is to give `migrate` an open path that
  skips the auto-ensure (it manages migrations explicitly) and let every other
  command auto-ensure.
- Behavior 4 (unwritable `--db` -> exit 1) uses a `--db` whose **parent dir is
  missing** (`filepath.Join(t.TempDir(), "missing-dir", "feedwatch.db")`):
  modernc SQLite's `PingContext` then fails with "unable to open database file
  (14)", which `sqlite.Open` wraps as `core.ErrStoreUnavailable` -> the boundary
  renders a `CatStore` JSON error and exits 1. Asserting on the `store` category
  (not the driver message) keeps the test stable.

## fee-gyos — E2 epic (persistence gate)

- Closed as a **dependency gate, not a code ticket** (same pattern as fee-63n9
  and fee-8cau): all four children (`fee-bqne` SQLite store, `fee-vlk9`
  migrations engine + too-new guard, `fee-aqkn` migrate command, `fee-c66o`
  walking skeleton) were already closed and integrate under a green `make build`.
  Closing required no new code, only verifying requirement 6 end-to-end on a
  native build.
- Verified the four req-6 capabilities against the live binary: migrate applies
  `0001` (`{applied:1,schema_version:1}`); fresh-db `migrate --status` reports
  `{schema_version:1,pending:0,backend:sqlite}` (apply-on-status, per fee-c66o);
  a `postgres://` DSN returns `{error:{category:config,...}}` exit 1 (deferred
  backend, per fee-aqkn); durability pragmas in `sqlite.go` are `busy_timeout`,
  `journal_mode(WAL)`, `foreign_keys(ON)`, `synchronous(NORMAL)` (never OFF);
  the `ErrSchemaTooNew` guard lives in `migrate.go`.
- Closing E2 is what makes the downstream **command lanes** ready as their other
  deps resolve: `fee-4q22` add, `fee-55gy` rm, `fee-on7r` list, `fee-ydl6` items,
  `fee-7r0v` prune, `fee-as1j` enable, `fee-t8ez` disable, `fee-jf82`/`fee-nkks`
  OPML, and `fee-vzu8` failure lifecycle. Several also depend on the E5/E6/E7
  epics, so check each ticket's own `deps` rather than assuming E2 alone unblocks
  them.

## fee-vzu8 — Failure lifecycle (count, backoff, auto-disable, reset)

- The persisted lifecycle is **two layers**: the store's low-level
  `RecordSuccess`/`RecordFailure` (already present from fee-bqne) just write the
  columns and take a pre-computed `nextDue`; the **policy** lives in pure
  functions `poll.RecordSuccess`/`poll.RecordFailure` in
  `internal/poll/lifecycle.go`. The store's `RecordFailure` does **not** change
  status — the auto-disable decision is made in `poll.RecordFailure` (count
  reaches threshold -> `SetStatus(disabled)`). Don't push the threshold/backoff
  math into the store; keep `internal/poll` the deterministic policy seam.
- `poll.RecordFailure` reads the current `FailureCount` via `GetFeed`, computes
  `newCount = count+1`, then calls the store's incrementing `RecordFailure`
  (which lands on the same `newCount`). This GetFeed-then-write is race-free
  **only because poll serializes per feed** (a feed's rows are never written
  concurrently — feeds are grouped onto one worker by host); documented in the
  function comment. If that invariant ever changes, move the count read into the
  same SQL statement.
- Backoff is `base * 2^(newCount-1)` clamped to `[base, maxBackoff]`, computed
  by **iterative doubling with an overflow guard** (`d *= 2; if d <= 0 return
max`), not `base << (n-1)`: an unbounded shift would wrap `time.Duration`
  (int64 ns) negative and schedule a retry in the past. `base <= 0` degenerates
  to `maxBackoff` (defensive; the orchestrator always passes the effective
  interval > 0).
- **`max`/`min` are Go 1.21 builtins** and `revive`'s `redefines-builtin-id`
  rule (enabled here) fails the build on a param or local named `max`. Name
  backoff ceilings `maxBackoff`, never `max`. Same trap as the `Fetcher` struct
  name collision (fee-b91x): the obvious name is taken.
- Handoff: the orchestrator `fee-u0i4` passes `cfg.FailureThreshold` (10),
  `cfg.MaxBackoff` (24h), and the **effective interval as `baseBackoff`** (and
  as the `interval` to `RecordSuccess`), applying a parsed `<ttl>` before
  choosing that interval. `enable`/`disable` (`fee-as1j`) use the store's
  `SetStatus` directly; only poll drives the lifecycle functions.

## fee-u0i4 — poll: fetch-orchestration

- The fetch-orchestration stage lives in `internal/poll` (`orchestrate.go`,
  `select.go`) as **read-only orchestration**: it selects, fetches, and parses,
  returning one `feedOutcome{feed,result,parsed,err}` per feed. It deliberately
  persists **nothing** — `RecordSuccess`/`RecordFailure`/`UpsertItems`/
  `SetValidators` and the `<ttl>`-aware next-due math belong to `fee-q6t3`
  (dedup-and-consume), and the envelope/exit code to `fee-12gs`. `feedOutcome`,
  `orchestrate`, and `fetchAndParse` stay **unexported** (the two sibling poll
  tickets share the package); only `Deps{Store,Fetcher,Parser,Clock,
Concurrency,PerHostDelay}` is exported, for the cli wiring `fee-12gs` will add.
- **Per-host politeness is the worker unit, not the feed.** Feeds are grouped by
  `url.Parse(...).Host` (raw string fallback for unparseable URLs); each host
  group is one `errgroup.Go` task that processes its feeds **sequentially** with
  `PerHostDelay` between them (skipped before the first). The `errgroup`
  `SetLimit(Concurrency)` then bounds how many **host groups** run in parallel.
  Consequence for tests: same-host paths never overlap, so the concurrency-bound
  test (behavior 4) needs **N distinct httptest servers** (distinct hosts) to
  observe parallelism, while the cancellation test (behavior 5) uses **one host,
  three paths** so they serialize onto one worker and only the first is ever
  in-flight.
- **Failure isolation falls out of "task funcs always return nil."** Each task
  captures its feed's error into `outcome.err` and returns `nil`, so the
  `errgroup`'s derived context is never cancelled by a sibling failure — only the
  parent (signal-aware) context cancels it. Don't return the per-feed error from
  the `g.Go` func or one bad feed would cancel the rest.
- **Stable output order + dropped-on-cancel** via `outcomes := make([]*feedOutcome,
len(feeds))` written at each feed's input index, then collected non-nil in
  order. A cancelled run leaves unscheduled/aborted feeds as `nil` slots, which
  are filtered out — so `len(result) < len(feeds)` is the observable signal that
  scheduling stopped (behavior 5). Cancellation is checked with a
  `select{<-gctx.Done()}` at the top of each feed iteration plus a
  `sleepContext` that aborts the per-host delay.
- **gofeed.Parser is NOT concurrency-safe** — it mutates shared translator state
  during `Parse` (`rssTrans` writes a parser field), so the old
  `GofeedParser{fp: gofeed.NewParser()}` (one shared parser) raced under the
  concurrent worker pool (caught by `go test -race`). Fix: `parse.GofeedParser`
  is now a zero-size struct whose `Parse` constructs a **fresh
  `gofeed.NewParser()` per call** (cheap, and the documented way to use gofeed
  concurrently). This is the same class of gofeed gotcha as the `<ttl>` and
  `xml:base` drops (fee-e487/fee-kx39): the library's convenience surface hides a
  sharp edge. Any future concurrent caller of a `parse.Parser` can now rely on
  concurrency safety.
- Behaviors 1-3 use the `testsupport` `InMemoryStore` + `FakeFetcher`/
  `FakeParser` (the store is read-only here, so the double's DueFeeds/ListFeeds/
  GetFeed semantics are all that matter); 4-5 use real `httptest` + `fetch.New()`
  - `parse.New()`. `golang.org/x/sync` became a direct require for `errgroup`.

## fee-q6t3 — poll: dedup-and-consume

- The dedup-and-consume stage (`internal/poll/consume.go`) is the persistence
  half of poll, the counterpart to fee-u0i4's read-only `orchestrate`. `consume`
  walks `[]feedOutcome` and, per success/304: `SetValidators` -> assign
  `parse.DedupKey` to each item -> `UpsertItems` (new-only) -> `RecordSuccess`;
  per failure: `RecordFailure` plus collecting the `*core.FeedError`. It adds
  three lifecycle knobs to `poll.Deps` (`DefaultInterval`, `FailureThreshold`,
  `MaxBackoff`) that the cli layer fills from config.
- **The ticket sketch's 2-tuple return `(pollTotals, []*core.FeedError)` is
  wrong for the error model; consume returns a third `error`.** The design draws
  a hard line: whole-invocation failures (incl. a store write that fails ->
  `CatStore`) are the `error` (exit 1), while per-feed fetch/parse failures are
  the `*core.FeedError` slice (exit 2/3). A store error swallowed into the feed
  slice would mis-map to exit 2. `consume` returns early on the first store
  error; feeds persisted before it stay committed (each feed's writes are
  independent), matching "partial completed work is persisted." fee-12gs needs
  this third value to pick the exit code.
- **Dropped the `skipped` field from the sketched `pollTotals`.** The not-due
  count is not derivable from `outcomes` (skipped feeds are never fetched, so
  they never become outcomes), and staticcheck's `unused` (U1000) flags a struct
  field that is never written — it would fail `make build`. The output-shaping
  stage (fee-12gs) owns `skipped` because it holds the selection result (total
  vs. selected). Don't add struct fields a ticket can't populate.
- **"Changed-only / never empty-clobber" validators needs no logic in consume.**
  `store.SetValidators` already skips empty `etag`/`last_modified` and no-ops when
  both are empty (fee-bqne), so `consume` just forwards `result.ETag`/
  `result.LastModified` unconditionally. The behavior-2 test feeds an empty-ETag
  outcome after a stored one and asserts the stored value survives — exercising
  the store's skip, reachable through consume's public path.
- **Effective interval precedence is `feed.Interval -> parsed <ttl> -> default`**
  (`effectiveInterval`), passed to `RecordSuccess`; a failure has no parsed body
  so its backoff base is `effectiveInterval(feed.Interval, 0, default)`. This
  matches the fee-vzu8 handoff ("effective interval as `baseBackoff`, applying a
  parsed `<ttl>` before choosing the interval").
- **301/308 URL rewrite is deliberately NOT in this stage.** It is outside the
  acceptance criteria and there is no `Store.RenameFeed`; `FetchResult.FinalURL`/
  `Permanent` are carried but unused here. Whoever wires the rewrite (a later
  ticket) must first add a store method to move a feed's `(feed_url)` rows.
- Tests are **white-box** (`package poll`) because `consume`/`feedOutcome` are
  unexported, but they persist into the **real** `sqlite.Store` on a temp-file db
  (per fee-bqne: never `:memory:`), reusing the package's `fixedTime` clock from
  `orchestrate_test.go`. Behavior 5 proves dedup keys are GUID-first by
  re-advertising the same GUID with a changed title+link and asserting 0 new — a
  title-keyed impl would count it as new.

## fee-12gs — poll: output-shaping and exit code (poll command wired)

- This is the **capstone** that ties the three poll lanes together: the exported
  `poll.Run(ctx, Deps, names, force)` runs `selectFeeds -> orchestrate -> consume`
  and returns an exported `Result{Polled,Skipped,NewItems,Failed,Items}` plus the
  per-feed `[]*core.FeedError` and a hard `error`. Before this, the whole poll
  pipeline was unexported (`orchestrate`/`consume`/`feedOutcome`) with no runnable
  entry point. `Run` is the only new exported surface besides `Result`.
- **Exit code lives on `Result`, not derived from a returned error.**
  `Result.ExitCode()`: `Polled==0 || Failed==0 -> 0`; `Failed==Polled -> 2`; else
  `3`. The cli action returns `exitError{code}` only for non-zero, so the boundary
  sets the code with no extra stderr (the envelope is already on stdout). A hard
  failure (store unreachable / failed write) is the returned Go `error` -> exit 1,
  a different path. `len(feedErrs) == totals.failed` by construction (consume
  appends to `feedErrs` exactly when it increments `failed`), so the cli could use
  either; `Result.Failed` keeps the derivation inside `poll`.
- **The stdout `PollResult` envelope has NO failed count** (`{polled,skipped,
new_items,items}`), matching the design's streams contract: the exit code
  reports _whether_ feeds failed, stderr (`renderer.Errors` -> `{"errors":[...]}`)
  reports _which_. `poll.Result` carries `Failed` for the exit-code math but the
  cli maps `Result -> PollResult` dropping it. Don't add `failed` to the stdout
  envelope.
- **Stable item order from a per-feed map:** `consume`'s `totals.newByFeed` is
  keyed by URL (a map, unordered), so `Run` rebuilds the items slice by iterating
  the **selected `feeds` slice** (input order) and appending `newByFeed[f.URL]`.
  This is exactly why fee-q6t3 returned a per-feed map rather than a flat slice —
  the output stage owns ordering.
- **`skipped` cost one extra query, only on the due path.** `skippedCount` returns
  0 for named/forced runs (they target regardless of schedule); for the unnamed
  due path it runs `ListFeeds(active)` and reports `len(active) - polled`
  (active-but-not-due). Taken before orchestrate/consume so a feed auto-disabled
  _during_ the run stays counted in `polled`, not `skipped`.
- **Test seam: `pollDeps` prefers injected `Deps.Store/Fetch/Parse`, builds
  production ones otherwise.** The cli `Deps` already had these fields (nil in
  `main`). The poll command tests inject `testsupport` doubles via a `runPoll`
  harness (own `Deps` literal + captured `OsExiter`) and drive `cmd.Run` with
  `poll` — the TDD-plan "stub outcomes" path. Production builds `fetch.New(...)`
  from config and `parse.New()`. An injected store is **not** auto-migrated;
  an opened one is.
- **`buildFetcher` passes `WithRetry(cfg.RetryAttempts, 0)`** — config has a
  `RetryAttempts` field but **no retry-backoff field**, and `fetch.New` clamps a
  `<= 0` backoff back to its own `defaultRetryBackoff`. So a 0 backoff honors the
  configured attempt count (3) while keeping the library default delay. `New`'s
  own default attempts is 1 (single-shot, per fee-r0rt), so the option is required
  to get retries at all.
- **`openStoreMigrated(ctx, cfg, clock)` is the "every command except migrate
  auto-ensures schema on open" resolution** flagged in the fee-c66o note. It wraps
  `openStore` + `Migrate` (closing the store on a migrate failure). `migrate`
  keeps using bare `openStore` so its bare-migrate `applied` count contract holds;
  every real command (poll first) uses `openStoreMigrated`.
- **301/308 URL rewrite still NOT wired** (same as fee-q6t3): no `Store.RenameFeed`
  yet; `FetchResult.FinalURL`/`Permanent` are carried through but unused. SIGINT/
  SIGTERM partial-persist + 130/143 already lives in `main`'s signal context plus
  `orchestrate`'s cancellation handling; this output stage just emits the
  completed work's envelope.

## fee-4q22 — add command

- **`add` reuses `poll`'s deps pattern but for one feed, not the orchestrator.**
  `addDeps` mirrors `pollDeps` (prefer injected `Deps.Store/Fetch/Parse`, else
  build production via `openStoreMigrated`/`buildFetcher`/`parse.New`), so the
  `runAdd` test harness injects `testsupport` doubles exactly like `runPoll`. The
  fetcher/parser resolution (~12 lines) is duplicated between the two; kept inline
  rather than abstracted since the other command lanes (rm/list/enable/disable)
  need only the store. Extract a shared `resolveFetcher`/`resolveParser` only if a
  third fetch+parse consumer appears.
- **Validation is two gates, both mapped to `CatUsage` (exit 1).** Gate 1
  (`validateFeedURL`): `url.Parse` then require scheme `http`/`https` AND non-empty
  `Host` — a bare host like `example.com` parses with empty scheme/host and is
  rejected _before any fetch_ (asserted via `fetcher.Requests(url)` being empty).
  Gate 2 (`validateParsesAsFeed`): fetch then parse; a parse failure means "not a
  feed" and the message points at `discover`. **Both fetch errors and parse errors
  are deliberately re-wrapped as `CatUsage`**, not surfaced with their native
  `network`/`http`/`parse` category. This is required, not cosmetic: a raw
  feed-scoped `*FeedError` (network/http/parse/timeout) maps to **exit 0** via
  `core.ExitCodeFor` (feed outcomes drive 2/3 from the poll aggregate, never a
  returned error), so an unwrapped fetch failure on `add` would wrongly exit 0.
  Wrapping under `CatUsage` (the whole-invocation category) is what makes a failed
  `add` exit 1. The original cause is preserved in `Err` for the chain; the
  rendered `message` is the explicit `Message`.
- **`created` is derived by a pre-AddFeed `GetFeed`, keyed on the not-found
  signal.** `store.AddFeed` is an unconditional upsert that returns the stored
  feed but does **not** report whether it inserted or updated. Both stores
  (sqlite + `InMemoryStore`) return a `CatUsage` "feed not found" `*FeedError` on a
  `GetFeed` miss, so `feedIsNew` treats `errors.As(...CatUsage)` as new
  (`created:true`), nil as existing (`created:false`), and any other error as a
  hard store failure that propagates. This leans on the not-found-is-CatUsage
  convention; if a store ever returns CatUsage for a different GetFeed failure,
  this would misclassify.
- **`add` always fetches+parses, even on an idempotent re-add.** Behavior 4
  (re-add with a new `--alias`) still runs both validation gates before the upsert,
  so a feed that stopped being a feed can't be silently re-confirmed. The test
  registers the fetcher/parser for the URL and asserts `created:false` + the alias
  updated in the store.
- **`AddResult.Interval` is emitted only when non-zero** (`feed.Interval.String()`),
  matching the `omitempty` on alias/interval; a 0 interval means "use the
  configured default" and is omitted rather than rendered as `"0s"`.

## fee-on7r — list command

- **The generic `renderText` fallback in `output` is useless for a
  collection-bearing result.** It dumps one `label: value` line per struct field,
  so a `ListResult{Feeds []FeedView}` renders as
  `feeds: [{...} {...}]` under `--format text`. Any command whose envelope is a
  slice must implement `output.TextRenderer` (`RenderText(io.Writer, color bool)`)
  to get a real table. `list` uses `text/tabwriter` with a header row
  (`URL ALIAS STATUS FAILURES LAST ERROR`) and a dash for empty optional columns.
  Per the color rule, status carries its own word, so the `color` arg is unused
  and no ANSI is emitted (no color is the sole carrier of meaning).
- **Empty-list shape: build with `make([]FeedView, 0, len(feeds))`, not `var
s []FeedView`.** A nil slice marshals to `null`; the design and the agent-first
  contract want `{"feeds":[]}` so an agent can iterate without a nil check. The
  behavior-3 test asserts the literal `"feeds":[]` substring, not just
  `len==0`.
- **Store-only commands get a lighter deps helper than add/poll.** `add`/`poll`
  build a fetcher and parser too (`addDeps`/`pollDeps`); a read-only command like
  `list` only needs the store, so `listStore(ctx, cfg)` just prefers
  `d.Store` (test seam) else `openStoreMigrated` and returns a no-op closer for
  the injected case. Don't reuse `pollDeps` for read-only commands — it would
  needlessly construct an HTTP client. The sibling read-only commands
  (`rm`/`enable`/`disable`/`items`) should follow this same store-only pattern.
- `openStoreMigrated` (not bare `openStore`) is the right open path for ordinary
  commands: it auto-ensures the schema on open, per the fee-c66o decision that
  only `migrate` skips the auto-ensure. A fresh `--db` therefore lists cleanly
  with no separate `migrate` step (verified live).

## fee-55gy — rm command

- **`rm` must `GetFeed(ref)` BEFORE `RemoveFeed`, for two reasons.** (1) The
  store's `RemoveFeed` resolves URL-or-alias but **is a no-op on a missing
  feed** (sqlite: `DELETE ... WHERE url=? OR alias=?` affects 0 rows; the
  `InMemoryStore` returns nil when `resolveLocked` misses). So calling it
  directly on an unknown ref would wrongly exit 0 — the ticket requires exit 1.
  `GetFeed` returns the `CatUsage` "feed not found" `*FeedError` on a miss
  (same convention `add`'s `feedIsNew` leans on), which the boundary maps to
  exit 1. (2) Resolving first yields the **canonical URL** so the
  `{removed:<url>}` envelope reports the URL even when the ref was an alias;
  the subsequent `RemoveFeed(feed.URL)` is then keyed on the canonical URL.
- **The same no-op-on-missing trap applies to the sibling lifecycle commands.**
  `SetStatus` is `UPDATE feeds SET status=? WHERE url=?` — also a silent no-op
  on an unknown URL. So `disable`/`enable` (`fee-t8ez`/`fee-as1j`) must follow
  the identical `GetFeed`-first-then-mutate shape to satisfy their "unknown ref
  -> exit 1" criteria; don't call `SetStatus` blind. They reuse the store-only
  `rmStore`/`listStore` deps pattern (no fetcher/parser).

## fee-as1j — enable command

- `enable` resolves the ref with `GetFeed` first (unknown ref -> `CatUsage`
  `*FeedError` -> exit 1, the same shape as `rm`/`disable`), then
  `SetStatus(url, active)`, then resets the failure lifecycle. It reuses the
  store-only `enableStore` deps pattern (no fetcher/parser) and the `FeedView`
  envelope as `{"feed": FeedView}` to report the post-enable state.
- **"Due again" was read as immediately due.** The reset goes through
  `store.RecordSuccess(url, now, now)` (clears `failure_count`, `last_error`,
  `last_error_at`) with `nextDue = now`, so `DueFeeds(now)` includes the feed on
  the very next poll. `poll.RecordSuccess` was deliberately NOT used: it
  schedules `now + interval`, which would leave a freshly-enabled feed _not_ due
  until an interval elapses, contradicting the ticket's "clear backoff so the
  feed is due again." The store method is the literal failure-lifecycle reset
  path; the `poll` helper adds the forward scheduling that enable does not want.
- Side effect of reusing `RecordSuccess`: `last_fetch_at` is stamped to `now` on
  enable even though no fetch happened. Judged acceptable (enable is a
  fresh-start reset) and the tests do not assert `last_fetch_at`, so a future
  dedicated reset method could drop that without breaking them. If `disable`
  (`fee-t8ez`) lands its behavior-3 round-trip test, it can call `enable` and
  assert the status flips back to active.

## fee-ydl6 — items command

- A thin flag-to-`core.ItemQuery` translator; all query semantics (since/until
  coalescing null `published_at` to `fetched_at`, the `contains` substring,
  `--fields` projection, ordering, and limit/offset pagination) already live in
  the store (`sqlite/items.go` and the `InMemoryStore` double, which mirror each
  other). The command adds **only** the flag parsing — do not re-implement query
  logic in the cli layer.
- `--order` is a single space-separated specifier (`"published desc"`), not two
  flags. `parseItemOrder` splits on whitespace: field must be `published` or
  `fetched`, direction `asc`/`desc` defaulting to `desc`; anything else is a
  `CatUsage`/`ErrUsage` `*FeedError` -> exit 1, empty stdout. Validation happens
  in the action (before opening the store), not via a urfave flag validator, so
  the error carries the `usage` category the boundary expects.
- `--since`/`--until` accept RFC3339 **or** a relative duration. Go's
  `time.ParseDuration` rejects `7d` (only ns..h), so `parseRelativeDuration`
  falls back to a trailing `d` (days) / `w` (weeks) unit and converts to hours.
  A relative bound means `now - dur`; `now` comes from `d.Clock` (via
  `orSystemClock`), keeping the window deterministic under the fixed-clock tests
  rather than calling `time.Now`.
- **Test seam gotcha**: the `--feed` filter resolves a url-or-alias against the
  **feeds table** in both the SQLite store
  (`feed_url IN (SELECT url FROM feeds WHERE url IN (...) OR alias IN (...))`)
  and the double (`resolveLocked`). So a test that seeds items via `UpsertItems`
  must `AddFeed` the same URL first, or the feed filter resolves to nothing and
  the query returns empty. `seedItem` does the `AddFeed` (idempotent upsert) up
  front.
- Reused the `dashIfEmpty` helper from `list.go` for the `--format text` table;
  `r.Result` dispatches to `ItemsResult.RenderText` when format is text and to
  compact JSON otherwise. Registered in `Deps.commands()` in `root.go`.
- E6 (`fee-5d25`) now has only `prune` (`fee-7r0v`) open before the epic can
  close.

## fee-7r0v — prune command

- Thin command over `store.PruneItems` (`internal/cli/prune.go`): map
  `--keep-days`/`--max-items` to a `core.PrunePolicy`, report `{pruned:N}`. The
  tombstone mechanics (and dedup-fingerprint preservation) live in the store;
  the command adds no logic beyond flag translation and the bound check.
- **`--keep-days 0` is meaningful, so 0 cannot mean "unset".** `keep-days 0`
  resolves the cutoff to `now`, i.e. prune everything older than now — a valid
  (if aggressive) operation. So `buildPrunePolicy` keys off `cmd.IsSet(name)`,
  not a zero-value check, to decide whether each bound applies. This is the same
  trap as any IntFlag whose 0 is a real value; reach for `IsSet` rather than
  comparing to the zero value.
- **Bare `prune` (no bound) is a usage error (exit 1, empty stdout), not a
  silent no-op.** The ticket offered either; chose the usage error because a
  no-bound prune is almost certainly a mistake and failing fast is the
  agent-first behavior. Negative `--keep-days`/`--max-items` are usage errors
  too. All routed through the shared `usageErr` helper (`CatUsage` + `ErrUsage`).
- Reused the per-command store-accessor seam verbatim (`pruneStore` mirrors
  `disableStore`/`itemsStore`): injected `Deps.Store` in tests,
  `openStoreMigrated` in production. This duplication across commands is the
  established pattern here, not worth abstracting yet.
- Closing this was the **last open child of E6 (`fee-5d25`)**, so the epic can
  now close. `poll` (orchestration/dedup/output), `items`, and `prune` are all
  landed.

## fee-171q — E5 epic (subscriptions and feed lifecycle gate)

- Closed as a **dependency gate, not a code ticket** (same pattern as fee-63n9,
  fee-8cau, fee-gyos): all six children were already closed (add `fee-4q22`, rm
  `fee-55gy`, list `fee-on7r`, enable `fee-as1j`, disable `fee-t8ez`, failure
  lifecycle `fee-vzu8`). Closing required no new code, only verifying that
  requirements 7 and 15 integrate end-to-end under a green `make build`.
- **An epic's `## Blocking` list is downstream, not deps.** E5's blocking list
  (`fee-jf82`, `fee-wwyw`, `fee-8eph`) are tickets that depend on E5; they do not
  hold E5 back. E5's actual `deps:` are all closed children plus the prior gates,
  so `tk ready` correctly surfaced it. Read the `deps:` field, never the prose
  "Blocking" section, to decide readiness.
- **E5 is the keystone of the remaining graph.** Both E6 (`fee-5d25`) and E7
  (`fee-isds`) list `fee-171q` in their own `deps`, and `fee-jf82` (OPML import)
  is blocked solely by it. So among the ready tickets (E5, E6, E7 gates plus the
  discover/export tasks) E5 had to close first — closing it is what lets E6/E7
  ever become closeable and makes import ready.
- Live req-7 smoke test against the native binary + a local `python3 -m
http.server` RSS feed: `add --alias` (`created:true`), `list` (active,
  failures 0), `disable` (`status:disabled`), `enable` (back to active, failures
  reset to 0), `rm` (`removed`, list empty) — all exit 0 with the documented JSON
  envelopes. `add` needs `--allow-private` because the loopback server is private
  address space (the SSRF guard only exempts the _initial_ directly-supplied URL
  from redirects, but `add`'s validation fetch of a 127.0.0.1 URL is allowed; the
  flag is belt-and-suspenders here and required once a redirect is involved).
- Live req-15 smoke test: add the valid feed, kill the server, then `poll
--force smoke` with short timeouts. Result recorded `failures:1` + `last_error`
  on the feed row, emitted a structured `network` `*FeedError` on stderr, and
  exited **2** (all targeted feeds failed). The full backoff / auto-disable /
  reset-on-success policy is covered by `fee-vzu8`'s unit tests; one forced
  failing poll is enough to prove the persisted lifecycle is wired into poll.

## fee-5d25 — E6 epic (poll, query and retention gate)

- Closed as a **dependency gate, not a code ticket** (same pattern as fee-63n9,
  fee-8cau, fee-gyos): all five children (`fee-u0i4` fetch-orchestration,
  `fee-q6t3` dedup-and-consume, `fee-12gs` output-shaping+exit, `fee-ydl6`
  items, `fee-7r0v` prune) and all other deps were already closed; closing
  required no new code, only verifying reqs 9/13/16 integrate on a native build.
- Verified end-to-end against a local `python3 -m http.server` RSS feed on a
  `CGO_ENABLED=0 go build ./cmd/feedwatch` binary: `add --alias` ->
  `{polled:1,new_items:2}` with stable item order; an immediate `poll --force`
  -> `new_items:0` (auto-consume/dedup); `items --fields title,link` projects
  and orders published-desc; `prune --max-items 1` -> `{pruned:1}` keeping the
  newest item and preserving its dedup fingerprint. All exits 0.
- Closing E6 unblocks the schema lane: `fee-wwyw` (schema command) and `fee-8eph`
  (E8) become ready as their other deps resolve. **E7 (`fee-isds`) is NOT yet
  closeable** — its three children `fee-0l84` (discover), `fee-jf82` (OPML
  import), `fee-nkks` (OPML export) are still open and are the next real work.

## fee-0l84 — discover command

- **Two layers, store-free.** Pure discovery logic lives in `internal/discover`
  (`discover.go`) depending only on `fetch.Fetcher` + `parse.Parser`; the cli
  command (`internal/cli/discover.go`) is a thin wrapper. `discover.Deps` carries
  only the fetcher and parser, so "no store writes" (behavior 5) is structural —
  the package has no store to write. `discoverDeps` is the first command deps
  helper that opens **no** store at all (lighter than even the read-only
  `listStore`); behavior 5 is asserted at the cli level by injecting an
  `InMemoryStore` and checking `ListFeeds` stays empty after the run.
- **The content-type guard and parse-validation are complementary, not
  redundant.** `validate` rejects `text/html`/`application/xhtml+xml` **before**
  parsing (so a homepage returned for an unknown probe path is never fed to the
  lenient gofeed), then `parse.Parser.Parse` is the real feed gate. Verified
  empirically that gofeed returns `"Failed to detect feed type"` on a sitemap
  `<urlset>`, so generic XML (`application/xml`/`text/xml`) that is not a feed is
  dropped by the parse step — the content-type guard does **not** reject generic
  XML wholesale (real feeds are routinely served as `text/xml`), only HTML. The
  doc's "content-type short-circuit that rejects generic XML" is satisfied by
  parse-detection of the root element, not by a content-type denylist.
- **ENABLING CHANGE: added `Title` to `parse.ParsedFeed`** (populated from
  `gofeed.Feed.Title` in `gofeed.go`). The `Parser` interface previously exposed
  no feed title (only `TTL` + `Items`), but a `Candidate` needs one for probe
  hits (autodiscovery can borrow the `<link title>` attr, a probe cannot). The
  field is additive — every existing consumer (poll) ignores it and no test
  asserted a 2-field struct, so it dropped in clean. This is the right
  deep-module move vs. re-parsing with gofeed inside `discover` or widening the
  interface signature.
- **Autodiscovery runs first + a `seen` set dedupes**, so a feed both declared
  via `<link>` and reachable at a probe path is returned once as
  `autodiscovery` (the more authoritative source). Autodiscovery `Type` is the
  declared `<link type>` attr; probe `Type` is the validated response MIME. Title
  prefers the `<link title>` attr, falling back to the parsed feed title.
- **Probe paths resolve against the origin, not the page directory.**
  `resolveProbe` rebuilds `scheme://host` and joins the absolute path, so
  `discover https://x/blog/` probes `https://x/feed`, not `https://x/blog/feed`.
  Matches the doc's origin-relative probe list.
- **Tests use real `fetch.New()` + `parse.New()` against `httptest`** (not the
  `testsupport` doubles): discover's whole job is HTML link extraction + content-
  type/parse classification, which the doubles can't exercise faithfully.
  Loopback `httptest` is dialed directly with no `--allow-private` (the SSRF
  guard only engages on redirects, per fee-juo8), so the production fetcher works
  unmodified. The cli test injects the same real collaborators via the
  `Deps.Fetch`/`Deps.Parse` interface seam.
- E7 (`fee-isds`) now has only the two OPML tasks (`fee-jf82` import, `fee-nkks`
  export) open before the epic can close.

## fee-jf82 — OPML import command

- New `internal/opml` package owns parsing only (deep-module split): `Parse(io.
Reader) ([]Feed, []Invalid, error)` decodes with `encoding/xml` and walks
  outlines recursively. Classification per outline: a resolvable URL (`xmlUrl`
  then `url` fallback) -> `Feed`; no URL but a feed `type` (`rss`/`atom`/`rdf`)
  -> `Invalid` (this is how a "malformed entry" is detected and reported);
  neither -> a plain folder, recursed into but not emitted. A folder with
  children and no URL is therefore silently traversed, which is what keeps
  arbitrarily nested outlines working without false "failed" entries.
- The import command (`internal/cli/import.go`) does all store wiring: it
  pre-scans `ListFeeds` once into URL and alias sets, then for each parsed feed
  dedups by URL (skip+count) and assigns the outline title as alias **only when
  free**, tracking both sets across the run so an in-file duplicate or a second
  feed wanting the same title doesn't collide. `AddFeed` is the same idempotent
  upsert `add` uses; a store-level add error is appended to `Failed` and the
  loop continues — one bad entry never aborts the import (exit stays 0). Only a
  missing/unreadable source or invalid XML is a hard `CatUsage` failure (exit 1).
- **stdin needed a new test seam.** `cli.Deps` had `Out`/`Err` but no `In`;
  added `In *os.File` (wired to `os.Stdin` in `main`, falls back to `os.Stdin`
  when nil). The `import -` path reads `d.In`; tests inject a seeked temp file.
  Export (`fee-nkks`) is unblocked and can round-trip against this importer.
- Alias resolution is case-sensitive and verbatim: outline `text="Legacy"` ->
  alias `Legacy` (InMemoryStore/sqlite `resolveLocked` matches the alias string
  exactly, no lowercasing). A test that looked up the lowercase alias failed;
  the importer does not normalize case.

## fee-nkks — OPML export command

- **Export's output is the OPML PAYLOAD, not a JSON envelope.** Unlike every
  other command, `export` writes the raw OPML 2.0 document to the `-o` file or to
  the renderer's `Out` (stdout) directly, bypassing `r.Result(...)`. This is
  required, not stylistic: the doc's `feedwatch export | curl ...` usage and the
  import round-trip both need OPML on stdout, and `import -` reads OPML from
  stdin. So the streams contract ("stdout is pure result JSON") has one
  deliberate exception — when the result _is_ a document, the document is the
  result. The action reaches `r.Out` via `rendererFrom(ctx)` and writes there.
- **Serialization is `opml.Write(io.Writer, []Feed)` in `internal/opml`**, the
  deep-module counterpart to `opml.Parse` (same split as the import side). It
  uses `encoding/xml` marshaling of an unexported `exportDoc`/`exportOutline`
  tree, **not** hand-built strings, so attribute escaping of `&`/`<`/`"` in
  titles and URLs is handled by the stdlib (proven by
  `TestWriteEscapesSpecialChars`: a `Tom & "Jerry" <news>` title and an
  ampersand-bearing URL round-trip verbatim through `Write` -> `Parse`). Build
  with `xml.Header` + `enc.Indent("", "  ")` + a trailing newline.
- **Go's `encoding/xml` emits `<outline></outline>`, never self-closing
  `<outline/>`.** This is a known stdlib limitation (no self-close support); the
  output is still valid OPML 2.0 and `opml.Parse` (and other readers) accept it.
  Don't waste effort trying to force self-closing tags.
- **Outline `text`/`title` = alias when set, else the URL.** OPML outlines
  require a `text` attribute, so an aliasless feed falls back to its URL rather
  than emitting an empty label. Round-trip quirk: re-importing such a feed makes
  the URL its alias (import assigns a free title as alias). Accepted and
  documented per the ticket's "text/title from the alias or URL"; round-trip
  _cleanliness_ means the feeds re-import (matching `added` count) and aliased
  feeds preserve their alias, which `TestExportRoundTripsWithImport` asserts via
  `GetFeed("Alpha")`.
- **gosec G304 fires on `os.ReadFile(varPath)` in tests too**, even when the path
  is `t.TempDir()`-rooted (the export-to-file test reads back the written file).
  The `readFile` helper sidesteps it by reading `f.Name()` off an `*os.File`
  (a method call, which gosec doesn't flag); a bare string path needs a targeted
  `//nolint:gosec // G304: ... test fixture` — same documented-exception pattern
  the production `os.Open`/`os.Create` paths use. `exportDest`'s production
  `os.Create(path)` carries the same nolint (operator-supplied output path).
- `exportStore` mirrors `listStore` (store-only deps, no fetcher/parser — export
  never fetches), and `exportDest` returns `(io.Writer, closer, error)` opening
  the `-o` file (an unwritable path -> `CatUsage` exit 1) or passing stdout
  through with a no-op closer. Registered in `Deps.commands()` after `import`.
- **This was the last open child of E7 (`fee-isds`).** With discover (`fee-0l84`),
  import (`fee-jf82`), and export all closed, the epic is now closeable; closing
  it makes `fee-wwyw` (schema command) and `fee-8eph` (E8) the next work as their
  other deps resolve.

## fee-isds — E7 epic (discovery and interop gate)

- Closed as a **dependency gate, not a code ticket** (same pattern as `fee-63n9`,
  `fee-8cau`, `fee-gyos`): all three children — discover (`fee-0l84`), OPML import
  (`fee-jf82`), OPML export (`fee-nkks`) — were already closed and integrate under
  a green `make build`. Closing required no new code, only verifying requirements
  8 and 17 end-to-end on a native build.
- Verified against the live binary: (1) `discover` on an HTML page with a
  `<link rel="alternate">` returns one parse-validated `autodiscovery` candidate
  and writes **zero** store rows (store-free by construction); (2) `import` of a
  nested outline with one typed-but-`xmlUrl`-less entry reports `added:2` with the
  bad entry under `failed`, and is idempotent on re-import (`added:0, skipped:2`),
  exit 0 throughout — one bad entry never aborts the import; (3) `export` emits
  valid OPML 2.0 with aliases as `text`/`title`; (4) the `export | import -`
  stdin round-trip re-adds every feed (`added` == feed count, `failed` empty),
  confirming the `Deps.In` stdin seam and the OPML-payload-on-stdout exception.
- Closing E7 makes `fee-wwyw` (schema command) ready and advances `fee-8eph` (E8)
  toward ready as its other deps resolve.

## fee-wwyw — schema command

- **`TypeName()` is the wrong source for flag types and is the trap this ticket
  turns on.** `urfave`'s `FlagBase.TypeName()` returns the slice _element_ type
  for a slice flag, so a `*cli.StringSliceFlag` reports `"string"`, identical to
  a plain `*cli.StringFlag` — the TDD plan's "report stringSlice correctly"
  fails if you lean on it. The fix is the design's own prescription: a **type
  switch over the concrete `*cli.XxxFlag`** (`StringFlag`/`BoolFlag`/`IntFlag`/
  `DurationFlag`/`StringSliceFlag`), which also yields a JSON-friendly default
  (duration as `"10s"`, not nanoseconds). `TypeName()` survives only as the
  `default:` fallback for an unknown future flag type — and even there it is on
  the `cli.DocGenerationFlag` interface, **not** the base `cli.Flag` interface
  (`f.TypeName()` does not compile; `f.(cli.DocGenerationFlag).TypeName()` does).
- **The `cli.Argument` interface exposes no name** — only `HasName`, `Parse`,
  `Usage`, `Get`. So argument introspection also needs a type switch
  (`*cli.StringArg` -> singular, `*cli.StringArgs` -> variadic) to read the
  `Name` field off the concrete type. Same shape as the flag switch.
- **Default omitted when zero** (`any` default field + `omitempty`, set only for
  non-zero values) so the output matches the design's `{"name":"--force",
"type":"bool"}` example with no `default` key. A bool `false`, int `0`, empty
  string, `0s` duration, and empty slice all render as "no default".
- **Filter the framework's conventional help/version surface.** urfave
  auto-adds a non-hidden `help` **command** and `--help`/`--version` **flags** to
  every command; left in, they pollute the schema (e.g. poll would list
  `--force` AND `--help`). The design treats `--help`/`--version` separately from
  the machine-readable contract, so `skipCommand` drops `Hidden` commands (the
  completion helper) plus `help`, and `flagSchemas` skips the `help`/`version`
  flag names. This is what makes `schema poll` emit exactly `--force`.
- **Registry vs introspection split (the anti-drift design):** flags and args
  come from the live tree (`cmd.Root().Commands`), so they cannot drift; only the
  exit-code table and output JSON Schema are hand-maintained in
  `schema_registry.go` (keyed by command name, with a `defaultExitCodes` +
  permissive `{"type":"object"}` fallback for any unregistered command, e.g. a
  test-injected one). The drift-guard test appends a command with a flag to a
  live root and asserts the flag appears with no registry entry — proving the
  introspected half tracks reality.
- **Schema reflects the real tree, not the doc's aspiration.** `poll` declares
  no `Arguments` (it reads `cmd.Args().Slice()` directly), so `schema poll` shows
  `"args":[]`, not the `{name:feed,variadic:true}` from docs/cli-design.md. If
  that discoverability is wanted later, add `&cli.StringArgs{Name:"feed"}` to
  `pollCommand` — the schema will then pick it up automatically.
- Output envelope shapes: bare `schema` -> `{commands:[CommandSchema...],
global_flags:[FlagSchema...]}` (global flags reuse the same introspection on
  `root.Flags`); `schema <command>` -> a bare `CommandSchema`. Unknown command
  reuses `root.go`'s `unknownCommandErr` (`CatUsage`) -> exit 1, empty stdout.

## fee-8eph — E8 epic (schema and discoverability gate)

- Closed as a **dependency gate, not a code ticket** (same pattern as fee-63n9,
  fee-8cau, fee-gyos). Requirement 4 is fully delivered by its one closed child
  fee-wwyw (schema command) plus the conventional `--help`/`-h` already provided
  by the urfave/cli skeleton (fee-7ons). Closing required no new code, only a
  live contract verification on a native binary.
- Verified end-to-end against `go build -o /tmp/feedwatch ./cmd/feedwatch`: bare
  `schema` emits all **13** commands (`migrate poll add list rm enable disable
items prune discover import export schema`) plus all 13 `global_flags`, each
  `CommandSchema` carrying `command`/`args`/`flags`(name,type,default)/
  `exit_codes`/`output_schema`; `schema <command>` narrows to one
  `CommandSchema`; every per-command schema parses as JSON with the five
  required keys; `schema bogus` -> exit 1 with a `{"error":{"category":"usage",
...}}` object on stderr and **empty stdout**; `--help` and subcommand `-h`
  print human usage to stdout (unaffected by `--format`).
- Closing E8 makes its two blocked children **ready**: fee-d38c (golden-file
  end-to-end suite) and fee-v2e5 (user documentation), which jointly gate
  fee-299l (E9: Quality and docs). Those were each blocked solely by fee-8eph.

## fee-d38c — Golden-file end-to-end suite

- The suite lives in `src/internal/e2e` (`package e2e_test`, black-box over the
  real binary) plus a `doc.go` for `revive`. `TestMain` `go build`s the binary
  **once** to a temp dir via the full import path
  (`go build -o <tmp> github.com/andreswebs/feedwatch/cmd/feedwatch`), which is
  module-aware so it resolves from any cwd. Each test gets its own temp `--db`
  (`t.TempDir()`, never `:memory:`) and `testsupport.NewFeedServer`.
- **`--quiet` is what makes stderr golden-able.** The default `LogLevel` is
  `slog.LevelInfo`, so without it a poll would interleave timestamped JSON log
  lines onto stderr and the goldens would be volatile. `--quiet` raises the floor
  to errors-only; the **per-feed error envelope is unaffected** because
  `Renderer.Errors` writes it via `output.WriteErrors` directly to the stream,
  not through `slog`. So a clean poll has an empty `.stderr` golden and a failing
  poll has exactly `{"errors":[...]}`.
- **Almost nothing in the item JSON is actually volatile**, which keeps
  normalization tiny. `core.Item.FetchedAt` and `Seen` are `json:"-"`, so the
  only poll-time value never serializes; `published_at`/`base_url`/`link`/`id`
  all come from the fixed fixture (use `example.test` literal links so `base_url`
  resolves to the item link, not the server URL). The single server-dependent
  field is the subscription `feed_url`. The normalizer therefore rewrites only:
  the httptest base URL -> `http://feedserver`, and `--version`'s `commit`/`go`
  (build-stamped) -> tokens. Fixture timestamps stay verbatim, so a regression in
  date handling still fails the diff.
- **To stage an all-failed / partial poll you cannot `add` a dead feed** — `add`
  fetch-validates that the body parses. Pattern: register the path with a valid
  body, `add` it (exit 0), then `srv.Register(path, Endpoint{Status: 404})` to
  flip it before `poll --force`. Use **404, not 5xx**: 4xx-other-than-429 is
  deterministic and not retried (fast, `CatHTTP`), whereas a 500 burns the
  3-attempt retry budget. All-failed -> exit 2 with a still-valid empty stdout
  envelope (`{"polled":1,...,"items":[]}`); one bad of two -> exit 3. Keep both
  feeds on **one** `FeedServer` (distinct paths) so a single base-URL
  normalization covers them and item order stays stable (poll writes
  position-indexed slots).
- TDD flow for a golden suite: write the suite (red: goldens missing), run with
  `-update` to generate, **inspect every generated golden by hand** against the
  documented contract, then run without `-update` (green). The reviewed golden
  content _is_ the assertion, so the inspection step is the real test-writing.
- gosec on `_test.go` fires three times here, all genuine documented exceptions
  (not `_ =` silencing): **G204** on both `exec.Command` calls (building and
  running the binary), **G101** false-positive on `const feedHostToken =
"http://feedserver"` (reads as a hardcoded credential), and **G304/G306** on
  the golden `os.ReadFile`/`os.WriteFile` with a computed path. Each got a
  one-line `//nolint:gosec // <rule>: <why>`.

## fee-v2e5 — User documentation (README, usage, scheduling)

- **Documentation-only ticket; the binary is the source of truth, not the
  design doc.** `docs/cli-design.md` sketches a broader env-var surface (default
  interval, timeouts via env) than is actually wired. Only **four** `FEEDWATCH_*`
  env vars are real flag sources (`grep -rn EnvVars internal/cli`):
  `FEEDWATCH_DB`, `FEEDWATCH_FORMAT`, `FEEDWATCH_USER_AGENT`,
  `FEEDWATCH_CONCURRENCY`. The other env influences live outside the cli flag
  layer: `HTTP_PROXY`/`HTTPS_PROXY`/`NO_PROXY` (via
  `http.ProxyFromEnvironment` in `fetch`), `NO_COLOR`/`TERM=dumb` (in
  `output/color.go`), and `XDG_STATE_HOME` (in `cli/storepath.go`). Documented
  exactly those; do not promise env knobs that don't exist.
- **Verify output envelopes against the live binary, not from memory.** Spot-ran
  `migrate`, `migrate --status`, `list`, `items`, and `poll` on a throwaway
  `--db $(mktemp -d)/t.db` and copied the exact JSON: `poll` emits
  `{"polled","skipped","new_items","items"}` (the design doc's older examples
  omit `new_items`/`skipped` in places). The `add` envelope renders `interval`
  as a Go duration string (`"30m0s"`).
- **Valid `--fields` names come from `fieldColumns` in
  `store/sqlite/items.go`**, not the JSON struct tags: `id`, `title`, `link`,
  `summary`, `content_html`, `content_text`, `content_mime_type`, `base_url`,
  `author`, `categories`, `enclosures`, `published_at`, `updated_at`. Note `id`
  maps to the `guid` column; `feed_url`/`dedup_key`/`published_at`/`fetched_at`
  are always retained regardless of projection.
- **markdownlint:** the ticket text references `~/.markdownlint.yaml`, which does
  not exist in this env. The repo carries its own `.markdownlint.yaml`
  (`MD013: false`, `MD060: false`) which `markdownlint-cli2` auto-discovers when
  run from the repo root — the same config the `.tickets/*.md` lint uses. Lint
  with `markdownlint-cli2 --fix README.md docs/usage.md` then re-run without
  `--fix` to confirm `0 error(s)`; no `--config` flag needed.
- **Build output naming for install docs:** `make build` produces
  `bin/feedwatch-<os>-<arch>` (host platform), not `bin/feedwatch`. The plain
  `feedwatch` name only appears inside the `dist` staging tarball. README points
  at `make build` / `make build-all` and `go install ./cmd/feedwatch`.

## fee-299l — E9 epic (quality and docs gate)

- Closed as a **dependency-gate/roll-up epic**, the same pattern as the other
  epics (`fee-63n9`, `fee-8cau`, `fee-gyos`): no new code, only verification. All
  four children were already closed and their deliverables are present and
  integrate under a green gate: test harness (`src/internal/testsupport/`),
  golden-file e2e suite (`src/internal/e2e/`), CI workflow + `src/.golangci.yml`,
  and user docs (`README.md`, `docs/usage.md`). Verified `make validate` (vet,
  `golangci-lint` 0 issues, all tests ok) and `make build` both pass.
- **This was the last open ticket; the project board is now fully closed.**
- **One deliberate loose end remains: CI is still `ci.yml.disabled`.** This was a
  documented deferral by the `fee-lfyq` implementer (CI activation and the
  broken-change-fails-CI verification were left for a follow-up that was never
  filed). It is out of scope for this roll-up epic, and activating a GitHub
  Actions workflow on the user's repo is the user's call. The workflow file is
  complete and ready (`setup-go` 1.26, module cache, `make validate` + `make
build` on push/PR); activating it is just a rename of
  `.github/workflows/ci.yml.disabled` to `ci.yml`, plus a one-time check that a
  deliberately-broken commit fails CI.

## fee-ev4k — Version stamping (-X injection, version package, reproducible flags)

- The producer side was the only gap: the Makefile already computed `VERSION`
  via `git describe` but `LDFLAGS` was just `-s -w` and `main.go` declared a
  dead `var version = "dev"`. The consumer side (`internal/cli` version printer,
  `Deps.Version`, `vcs.revision` commit lookup) was already correct and needed
  no change — only compute -> inject -> resolve was missing.
- New `src/internal/version` package is the single source of truth: a `var
Override` (set at link time) and `Current()` with a three-tier fallback
  `Override -> debug.ReadBuildInfo().Main.Version -> "dev"`. The middle tier
  **must** skip both `""` and `"(devel)"`: `Main.Version` is `"(devel)"` for a
  bare `go build` and is only a real semver under `go install ...@vX.Y.Z`, so
  without the `"(devel)"` guard a bare build would report `(devel)` instead of
  the intended `dev` fallback. The `TestCurrent_DevFallback` test (asserting
  `"dev"` under `go test`) is what locks this in.
- `version.Current()` is called at the `main.go` composition root and threaded
  through the existing `Deps.Version` field, not called deeper in the tree —
  keeps the single source of truth while preserving the testable `Deps` seam.
- Makefile: `LDFLAGS := -s -w -buildid= -X
github.com/andreswebs/feedwatch/internal/version.Override=$(VERSION)` plus new
  `BUILDFLAGS := -trimpath`, applied to `build-local`, the `build-target`
  cross-compile template, and `run`. `-buildid=`/`-trimpath` make builds
  reproducible; the `-X` path is the full module path to the package var, not
  `main.version`.
- The `commit` field in `--version` is independent of this change: it still
  comes from `debug.ReadBuildInfo()` `vcs.revision` and is populated even on a
  bare `go build` (where `version` is `dev`) but empty under `go run`/`go test`.
- No tags exist yet, so `git describe --tags --dirty --always` yields a short
  hash (e.g. `dd320e4-dirty`); that is the stamped `version`, which is the
  acceptance criterion ("git describe value, not dev"), not a semver tag.

## fee-qhx0 — Derive command output schemas from result structs

- New `src/internal/jsonschema` package reflects a Go value into a draft-07
  schema (`Reflect`/`OneOf`/`Scalar`), stdlib-only. `internal/cli/schema_registry.go`
  now derives every command's `output` from its result struct instead of
  hand-authored JSON strings, so the result type is the single source of truth
  and the output half of the `schema` contract can no longer drift. The flag/arg
  halves were already introspected from the urfave tree; exit codes stay
  hand-maintained (not derivable from any type).
- Integer kinds map to `"integer"`, not `"number"` — feedwatch's authored
  schemas used `"integer"` for counts, deliberately diverging from the
  `terminology` reference impl that maps every numeric kind to `"number"`. Get
  this wrong and `TestOutputSchemaContractPreserved` fails on every count field.
- The `jsonschema:"opaque"` field tag halts recursion and renders the field as a
  bare `{"type":"object"}` (or array-of-object for a slice). Required on
  `PollResult.Items` and `ItemsResult.Items`: `items --fields` projects to a
  caller-chosen subset, so the per-item shape is dynamic and no fixed schema is
  correct. Without the tag, `Reflect` would expand the full `core.Item` shape.
- One intended semantic delta: deriving `ImportResult` marks the `failed`
  element's `xmlUrl`/`reason` as `required` (both non-`omitempty`, always
  emitted). The old authored schema omitted `required` there; the derived one is
  strictly more accurate. Every other command is semantically identical; only
  property key order changes (alphabetical, since `encoding/json` sorts map
  keys), which JSON Schema treats as insignificant.
- `TestOutputSchemaContractPreserved` is white-box (`package cli`): it reads
  `registryFor(name).output` directly rather than shelling through the CLI, and
  pins each command's `properties`/`required` sets (plus the migrate `oneOf`
  alternatives and the export/schema scalars). It is the output-half twin of
  `TestSchemaDriftGuard`; a field added to any result struct now surfaces in its
  `output_schema` with no registry edit.
- Environment note: `cmd/qafixtures/*` exhibited transient `fmt-check` and
  test failures (`TestServeFeedContentTypeOverride`) during this run, with
  `gofmt -d` and isolated reruns immediately showing clean/passing. It was an
  external process touching those files concurrently, not a real regression and
  unrelated to this ticket; `make build` is green.

## fee-etoi: list shows per-feed interval

- The interval was already loaded (`ListFeeds` -> `scanFeed` sets
  `core.Feed.Interval`); the gap was purely in the `list` view. Added an
  `Interval` field to `FeedView` with the same `> 0` guard `add` uses, so a
  default (zero) interval is omitted from JSON and rendered as `-` in text.
- The output `schema` is derived from `ListResult` by reflection, so adding the
  field made `interval` appear in `list`/`enable`/`disable` automatically. The
  white-box `TestSchemaOutputContract` pins `feedViewProps`, so that list needed
  `interval` added to stay green; no registry edit was required.

## fee-bpq3: completion unknown shell exits 3 with no error object

- The built-in urfave/cli v3 completion command has no `Action`, so an
  unrecognized shell token (e.g. `completion powershell`) fell through the help
  machinery to `Exit(_, 3)`. The exit boundary treats any `cli.ExitCoder` as an
  already-reported outcome, so it emitted nothing: exit 3, empty stdout/stderr.
- Fix: attach a `CommandNotFound` handler to the completion subcommand via the
  root's `ConfigureShellCompletionCommand` hook, mirroring the root-level
  `commandNotFound`. It renders a single `usage`-category JSON `*FeedError` on
  stderr and calls `OsExiter(1)`. The real shell subcommands (bash, zsh, fish,
  pwsh) keep their own actions and are unaffected.
- `ConfigureShellCompletionCommand func(*cli.Command)` runs against the
  generated completion command; setting fields there (not on the root) is how
  you customize a framework-built subcommand.

## fee-h4bz: import accepts malformed/non-absolute feed URLs

- `import` was leniency-only: a present-but-malformed `xmlUrl` (e.g.
  `not-a-valid-url`) was stored as an un-pollable subscription, while `add`
  rejected the same value. Only entirely missing URLs (`opml.Invalid`) reached
  `failed`.
- Fix: extracted the bare predicate `isAbsoluteHTTPURL` from `validateFeedURL`
  in `add.go`; `add` keeps its add-specific message and delegates the test, so
  its behavior is unchanged. `importFeeds` now routes any `feed.XMLURL` failing
  that predicate into `failed`.
- Ordering matters: the duplicate-skip check stays before validation, so a
  duplicate (even a malformed one already stored) is still counted as `skipped`,
  not `failed`. Missing-URL outlines remain handled by the existing `opml.Invalid`
  pre-seed into `failed`.

## fee-yigg — store: auto-create default XDG directory on fresh machine

- The fix lives entirely in the cli layer, not in `sqlite.Open`: the directory
  side-effect is kept out of the pure path resolver and out of the store. The
  `default-vs-explicit` distinction is the gate — only the tool-owned default
  location (`$XDG_STATE_HOME/feedwatch/...` or its `~/.local/state` fallback)
  gets its parent `MkdirAll`'d; an explicit `--db`/`FEEDWATCH_DB` path stays
  strict, so a missing intermediate directory there is still a `store` error
  (TC-STORE-002, `TestMigrateUnwritableDBExits1`, unchanged).
- `resolveStorePath` now returns `(path, isDefault bool)`; `buildConfig` returns
  `(config.Config, bool)` to thread that one flag up to `before`, which calls
  `ensureStoreDir(cfg.Store)` only when `isDefault && backendName == "sqlite"`.
  Gating on `backendName` keeps a default-but-postgres path (not currently
  reachable, since a postgres DSN is always explicit) from getting an `MkdirAll`
  on a bogus filesystem path.
- `ensureStoreDir` maps an `os.MkdirAll` failure to a `CatStore` `*FeedError`
  wrapping `core.ErrStoreUnavailable`, so a genuinely unwritable default parent
  (e.g. a read-only XDG dir) surfaces through the same boundary as any other
  store-unavailable failure rather than as an internal error. Mode is `0o700`
  (private state dir).
- Wiring this in `before` (not `openStore`) means every command benefits, and it
  runs before the store is opened, so the `migrate` open path that deliberately
  skips auto-ensure (fee-c66o) is unaffected — the directory exists by the time
  any open happens.

## fee-d974 — Charset: neutralize stale XML declaration after decode

- The defect was **double-decoding**, not a charset-resolution bug: `decodeBody`
  already converted the body to correct UTF-8 by the required precedence, but
  the decoded bytes still carried their original XML declaration
  (`encoding="ISO-8859-1"` / `"UTF-16"`). gofeed installs
  `golang.org/x/net/html/charset` as `encoding/xml`'s `CharsetReader`, so it
  re-decoded the already-UTF-8 bytes per that stale declaration — mojibake for
  Latin-1 (`café` -> `cafÃ©`), and outright "Failed to detect feed type" for
  UTF-16 (UTF-8 bytes decoded as UTF-16).
- Fix: `canonicalizeXMLDeclEncoding` rewrites the leading declaration's encoding
  value to `UTF-8` (a no-op when absent), applied to the output of the three
  **successful non-UTF-8** decode rungs (BOM, XML-decl, Content-Type). It is
  deliberately **not** applied to the lossy `bytes.ToValidUTF8` fallback paths,
  so the garbage/unknown-charset case (exit 0, lossy) stays unchanged. This
  keeps the layering intact (the Content-Type rung is only knowable at the fetch
  layer; gofeed never sees HTTP headers) by making gofeed's reader a no-op.
- The regexp splice operates on the same leading 1024-byte head window that
  `xmlDeclEncoding` caps to, so a stray later `encoding=...` occurrence in body
  text is never rewritten.
- **The test gap that let this through**: the prior charset tests
  (`charset_internal_test.go`, `charset_test.go`) stopped at the byte level —
  they asserted `decodeBody`/`Fetch` produced valid UTF-8 but never ran the
  result through gofeed, where the re-decode happens. The new
  `charset_parse_test.go` (`fetch_test`) fetches each fixture then parses the
  body via `parse.New()`, asserting both feed and item titles. `fetch_test`
  importing `parse` is cycle-free (parse imports only core/gofeed) and test-only.
  Serve the ISO-8859-1 fixture **without** a Content-Type charset so the XML-decl
  rung (the actual bug path) fires rather than the Content-Type rung.

## fee-j4w1 — items: --fields projection subset and unknown-field rejection

- The pre-fix `--fields` bug was two-layered. The store/double already projected
  _columns_ correctly (always-on `feed_url`/`dedup_key`/`published_at`/
  `fetched_at` plus the requested ones), but the CLI serialized the result as a
  `core.Item`, whose JSON tags have **no `omitempty` on `feed_url`/`title`/
  `link`/`published_at`** — so those four always appeared regardless of the
  projection. Adding `omitempty` to `core.Item` was rejected (it would suppress
  the documented `null` `published_at` in the full-item path and churn goldens).
  The fix projects into a `map[string]any` keyed by the requested field names at
  the CLI layer instead, so the JSON reflects exactly the request.
- `core.ValidItemFields` (in `core/query.go`) is the single source of truth for
  projection validity; `core.ProjectItem(it, fields)` builds the per-item map,
  always seeding `feed_url`. The store's `fieldColumns` map stays keyed by the
  same names (now a pure read optimization — the CLI does the user-facing
  projection). Validation lives in `buildItemQuery`: the first unknown name is a
  `CatUsage` error (exit 1, empty stdout), so `title,bogus` fails too.
- **Widening `ItemsResult.Items` to `any` broke the schema contract test.**
  `jsonschema.opaqueSchema` renders a slice/array as `{type:array,items:{object}}`
  but an **interface** kind as a bare `{type:object}`, so `items` lost its array
  shape. Fix: keep two concrete-typed envelopes — `ItemsResult{Items []core.Item}`
  (full) and `ProjectedItemsResult{Items []map[string]any}` (projected). Both
  reflect to `array of object`, the schema registry keeps using `ItemsResult{}`,
  and the contract test is untouched. Don't reach for `any` on an `opaque` field.
- `feed_url` is documented as the always-on identity field (usage.md, REQ 13):
  `--fields summary` returns exactly `{feed_url, summary}`. A requested field is
  always present in the projection even when empty, so output is deterministic.

## fee-n6j6 — fetch: permanent redirect (301/308) rewrites stored feed URL

- The fetch layer already recorded the redirect target (`core.FetchResult.FinalURL`
  and `.Permanent`, set only for 301/308 by the SSRF `checkRedirect` hook); the gap
  was purely persistence. Fix folds the rewrite into `RecordSuccess` so the rename
  and success bookkeeping commit in one transaction. The interface gained a
  `finalURL string` arg; `consumeSuccess` passes it only when
  `result.Permanent && FinalURL != "" && FinalURL != feed.URL`, so 302/307 (which
  set `Permanent=false`) never rewrite, and the SSRF guard having already failed a
  fetch into blocked private space means the private-address criterion holds for
  free. A 304-not-modified permanent redirect still flows through `consumeSuccess`,
  so the rewrite applies there too.
- **Renaming a SQLite primary key that child rows reference trips the immediate
  `items -> feeds` foreign key mid-transaction.** The FK (`feed_url REFERENCES
feeds(url) ON DELETE CASCADE`) is not `DEFERRABLE`, so updating `feeds.url` first
  orphans the items, and updating `items.feed_url` first points them at a not-yet-
  existing parent — both fail at statement end. Fix: `PRAGMA defer_foreign_keys =
ON` as the first statement inside the rename transaction. It defers all FK checks
  to COMMIT (regardless of how the constraint was declared), is scoped to that
  transaction's connection, and auto-resets at COMMIT — no migration/table rebuild
  needed. Order of the two UPDATEs then no longer matters.
- The rewrite is the **last** write in `consumeSuccess` and the in-memory
  `oc.feed.URL` is never mutated, so the earlier `SetValidators`/`UpsertItems`
  (keyed on the original URL) and the `Run` item-assembly stay correct. Side effect:
  the poll envelope's `items[].feed_url` still shows the _old_ URL for the poll that
  performs the rename (assembled in-memory pre-rename); the next `items` query
  returns them under the new URL. Acceptable and confirmed by manual QA.
- A redirect target already subscribed as a different feed **skips** the rewrite
  (no merge): `RecordSuccess` checks `SELECT 1 FROM feeds WHERE url = finalURL`
  inside the tx and keeps the original URL on conflict, avoiding a PK collision.

## fee-fz8p — poll: SIGINT/SIGTERM exit 130/143 and persist completed work

- **Two independent bugs presented as one symptom** (exit 1 + empty stdout +
  `internal` "context canceled" on interrupt): (1) the persistence stage ran on
  the signal-cancelled context, so completed feeds' store writes aborted; (2) the
  signal-to-exit-code mapping was missing.
- **Persist on a detached context.** `poll.Run` now keeps the cancellable `ctx`
  for `orchestrate` (so an interrupt stops scheduling new fetches — it already
  returned `nil` on `gctx.Done()`) but runs `consume` on
  `context.WithTimeout(context.WithoutCancel(ctx), persistGrace)` (5s). The
  detached context still carries a deadline so an interrupted poll can't hang on
  an unresponsive store. No `ctx.Done()` guard was added to `consume`: guarding
  there would skip persisting outcomes when the signal arrived during the fetch
  phase, which is the common case and the exact source of the "0 rows" symptom.
- **The cli boundary exits from _inside_ `Run`, not after it.** urfave/cli's
  `ExitErrHandler` (our `exitErrHandler`) maps a feed-outcome `ExitCoder` to a
  code by calling `cliv3.OsExiter(code)` — and `OsExiter` defaults to `os.Exit`.
  So a post-`Run` signal check in `main` is dead code: the process has already
  exited (with 3, in the partial case) before `Run` returns. The fix wraps
  `cliv3.OsExiter` in `main` to override the code with `128+signum` when a signal
  was caught. A happens-before chain makes this race-free: the handler goroutine
  does `caught <- s` (buffered) _before_ `cancel()`, and only that `cancel()`
  drives the cancellation that unwinds `Run` into `OsExiter`, so the buffered
  signal is always observable by the time the wrapped exiter runs. A second
  post-`Run` check covers the clean-exit path (exit 0, `OsExiter` never called).
- **Backstop in `feedErrorFor`** (`cli/root.go`): `errors.Is(err,
context.Canceled|DeadlineExceeded)` now maps to `CatTimeout` instead of falling
  through to `CatInternal`, so any residual cancellation at other store sites
  cannot leak as an `internal` error. An explicit `*FeedError` in the chain still
  wins (checked first).
- **Testing seams.** Unit test drives `poll.Run` with a store double whose write
  methods return `ctx.Err()` on a cancelled context (mirroring real SQLite) plus a
  fetcher that signals when the fast feed completed and the slow one is in flight,
  so the interrupt is delivered deterministically (no `time.Sleep`). The
  signal-to-exit-code mapping lives in `main`, so it needs an end-to-end test:
  spawn the real binary, two feeds on _different_ httptest hosts (same host would
  serialize onto one worker via per-host grouping and never fetch the fast one
  before the interrupt), `SIGINT`/`SIGTERM` after a short sleep, assert exit
  130/143, completed-feed items queryable, and no `"category":"internal"` on
  stderr. `import` seeds the slow feed without blocking (it calls `AddFeed`
  directly and never fetches), unlike `add`.

## fee-udsl — poll: unconditional 5s persistence deadline can abort a successful poll

- **fee-fz8p's fix (above) was itself the bug**, just dormant until scale
  exposed it: `context.WithTimeout(context.WithoutCancel(ctx), persistGrace)`
  applies the 5s deadline unconditionally, from the moment persistence starts,
  not from the moment of an interrupt. A normal, uninterrupted run at customer
  scale (133 feeds, 5,335 items, one transaction per feed) can take longer than
  5s to persist on slow storage, so the deadline fires mid-`consume`, `Run`
  returns a hard error, and the items already committed per-feed before the
  expiry are stranded: stored, but absent from the envelope and from
  `new_items`.
- **Fix separates "has an interrupt happened" from "how long since it
  happened."** `graceAfterCancel(parent, grace)` returns a context that mirrors
  `parent` while it is live (no deadline at all) and only starts a `grace`
  countdown once `parent.Done()` fires, via a watcher goroutine race between the
  parent's cancellation and an explicit `stop()`. `context.WithTimeout` has no
  way to express "no deadline until X happens," so this couldn't be built from
  stdlib context constructors alone.
- `persistGrace` changed from a `const` to a package `var` specifically so
  `TestGraceAfterCancelCancelsAfterGraceOnParentCancel`-style tests can shrink
  it to milliseconds; a `Deps` field was the other option the ticket allowed but
  would have forced every existing `poll.Deps{...}` literal (there's only the
  one in `cli/poll.go`, but more may come) to reason about a zero-value grace
  meaning "cancel immediately," which is exactly the bug being fixed.
- **The existing interrupt test needed no changes.** `graceAfterCancel`
  preserves the observable behavior `TestRunInterruptPersistsCompletedFeeds`
  depends on (persistence survives cancellation, bounded eventually) — only the
  *un*interrupted path's behavior changed.
- **e2e regression seam**: spreading N feeds across N distinct `httptest`
  servers (one host each), not N paths on one server, matters for reasons
  beyond the fee-fz8p per-host-grouping issue — `PerHostDelay` (1s default)
  serializes same-host requests, so 20 feeds on one host made the test take
  ~19s for a reason unrelated to what it verifies. Per-host servers cut it to
  well under a second.
