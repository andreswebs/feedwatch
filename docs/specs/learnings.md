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

## fee-e1s2 — poll: failure visibility in the result envelope (Req 1)

- **Pure projection, no new computation.** `poll.Run` already returns
  `(Result, []*core.FeedError, error)`, and `consume` appends exactly one
  `*core.FeedError` per failed outcome, so `len(feedErrs) == result.Failed`. The
  envelope's new `failures` list is just a projection of `feedErrs`
  (`{feed_url, category, status}`); `succeeded` is `result.Polled - result.Failed`.
  Nothing in the `poll` package, store, or fetch layer changed — the work was
  confined to `cli/poll.go`.
- **Empty list, never null.** `failures` is built with
  `make([]PollFailure, 0, len(feedErrs))` so it marshals to `[]` when no feed
  failed. A regression test asserts this on the raw JSON (compacted
  `json.RawMessage` comparison against `[]`), since unmarshalling into a Go slice
  cannot distinguish `[]` from `null`.
- **Status omission is `omitempty` + zero value.** `core.FeedError.Status` is 0
  for non-HTTP categories, and `Status int \`json:"status,omitempty"\``drops the
key for those. No conditional logic needed;`network`/`parse`/`timeout`failures naturally omit`status`.
- **Golden + contract fixtures both move.** The e2e `poll.stdout` goldens
  regenerate with `go test ./internal/e2e -update` (additive: new
  `succeeded`/`failed`/`failures` keys). Separately, the reflection-based
  `TestOutputSchemaContractPreserved` in `cli/schema_test.go` pins poll's
  property/required sets by hand and had to be updated to add the three keys —
  the schema itself regenerates automatically from `PollResult{}` via
  `jsonschema.Reflect`, but that contract guard does not.
- **Text mode untouched.** `PollResult` has no `RenderText`, so `--format text`
  falls to the generic struct dump, which now prints the new counts for free. A
  bespoke renderer was out of scope; the JSON contract is what the spec governs.

## fee-ah78 — items: fetch-time query axis (fetched_at field and --time-field, Req 2)

- **Field exposure was a one-line tag flip, but it rippled into goldens.**
  Changing `Item.FetchedAt` from `json:"-"` to `json:"fetched_at"` makes the
  field appear on every item-bearing envelope (`poll` items and the default
  `items` output), so the e2e `poll`/`items` goldens regenerate. `fetched_at` is
  the wall-clock fetch moment, so it is volatile across runs; the e2e harness
  gained a `reFetchedAt` normalizer (alongside the commit/go ones) that rewrites
  `"fetched_at":"..."` to a stable token before golden comparison.
- **Surfaced a real SQLite/double parity bug.** The in-memory double stamped
  `it.FetchedAt = now` on insert and returned that, but SQLite's `insertItem`
  resolved the fetch time into a local variable without writing it back to the
  returned item. With `fetched_at` hidden this was invisible; exposing it made
  `poll` report `fetched_at:"0001-01-01T00:00:00Z"` (Go zero time) for freshly
  inserted items. Fixed by resolving `FetchedAt` once at the `UpsertItems` entry
  point (a single `now` for the batch, mirroring the double) so the returned
  `newItems` carry the stamped time; `insertItem`/`upsertOne` no longer thread a
  `now` parameter. AC requires `fetched_at` be populated (never null) on every
  stored item, and the poll envelope is the path that exposed the gap. A
  store-level regression test (`TestUpsertItemsResolvesFetchedAt`) pins it.
- **Filter axis vs sort axis are independent.** `--time-field` selects the
  column the `--since`/`--until` window compares (publication coalesce, or
  strictly `fetched_at`); `--order` is unchanged and still picks the sort column.
  In SQLite this is a one-line `axis` switch in `itemFilters`; in the double a
  one-line switch in `matchesItemFilters`. A SQLite-vs-double parity test backs
  both.
- **Discovered stale e1s2 goldens.** The committed e2e `poll` goldens still
  lacked the `succeeded`/`failed`/`failures` keys that `fee-e1s2` added to
  `PollResult`; the suite was only green via the Go test cache. Running
  `go test ./internal/e2e -update` for this ticket also corrected that
  pre-existing drift. Lesson: a ticket that adds envelope fields must regenerate
  e2e goldens, and `make build` can mask the omission through caching.
- **Schema needed no contract-test edit.** Item JSON is `jsonschema:"opaque"`,
  so `fetched_at` does not enter the reflected schema; `--time-field` appears
  automatically from flag introspection. Only `usage.md` prose was updated.

## fee-7dsa — Validated import (--no-validate to skip, Req 4)

- **Reused add's validator verbatim.** `import` and `add` share the `cli`
  package, so import calls `validateParsesAsFeed(ctx, fetcher, parser, url)`
  directly; the production fetcher from `buildFetcher` already carries
  `WithRetry`, so the transient-retry AC came for free with no extra wiring.
- **Three-phase importFeeds isolates concurrency from determinism.** Phase 1
  classifies each outline entry sequentially (dedup against existing subs and an
  OPML-internal `urls` reservation, plus the absolute-http(s) syntax check),
  producing order-preserving candidates. Phase 2 validates candidates
  concurrently with an `errgroup` limited to `cfg.Concurrency`, writing into
  position-indexed slots; each `g.Go` returns `nil` even on a validation failure
  so one bad feed never cancels its siblings. Phase 3 subscribes the survivors
  sequentially, so alias assignment stays deterministic. Reserving the URL in
  phase 1 (not phase 3) preserves the prior behavior that an OPML-internal
  duplicate counts as `skipped`.
- **Alias assignment stays in phase 3, not phase 1.** A feed that fails
  validation must not consume an alias a later valid sibling could use, so the
  `aliases` reservation happens only when a candidate is actually added.
- **`validateCandidates` returns nil under --no-validate.** Callers treat a nil
  error slice as "all valid" without allocating, and importAction skips
  resolving the fetcher/parser entirely, so --no-validate performs no fetch
  (asserted by a recording `FakeFetcher` with zero requests).
- **Schema carries no per-flag description.** `FlagSchema` exposes only
  name/type/default, so the reachability caveat could not live in the
  machine-readable schema. It lives in the `--no-validate` flag `Usage` (shown by
  `--help`) and in usage.md instead; a machine-readable `validated` envelope
  boolean was left out of scope per the ticket, keeping the result shape stable.
- **e2e fan-out.** Existing classification-focused cli/import tests and the
  export round-trip moved to `--no-validate` (they assert OPML parsing/dedup, not
  reachability). `TestImportExportPrune` keeps the default validating path
  against a real local feed server for genuine e2e coverage; the signal test's
  import must use `--no-validate` because its slow feed is deliberately
  unreachable until cancelled and both feeds must be subscribed for the poll to
  have an in-flight target.

## fee-n4p6 — items: field selection ergonomics (feed_url no-op, did-you-mean, Req 5)

- **`feed_url` skipped before the membership check, not added to the valid set.**
  `feed_url` stays absent from `core.ValidItemFields` (it is the always-on
  identity field, not a selectable projection field), so `buildItemQuery` short-
  circuits it with an explicit `if f == "feed_url" { continue }` ahead of the
  `ValidItemFields` lookup. Passing it through in `q.Fields` is harmless:
  `core.ProjectItem` always seeds `feed_url` and has no `case "feed_url"`, and the
  SQLite `projectedColumns` selects it via `alwaysColumns` regardless, so no store
  or core change was needed.
- **Candidate set derived from `ValidItemFields`, not hardcoded.** Added
  `core.ItemFieldNames()` (sorted keys plus `feed_url`) so suggestions pick up new
  fields automatically (e.g. `fetched_at` from Req 2) and never leak map-iteration
  nondeterminism. `nearestField` ranges over it in sorted order and keeps the
  first strictly-closest match, so ties are broken deterministically by the sorted
  candidate order.
- **Suggestion thresholds guard against absurd matches.** A candidate is offered
  only when its Levenshtein distance is `<= 2` _and_ strictly less than
  `len(name)`. The second bound is what makes `--fields ""` (empty) and very short
  garbage names yield no suggestion: distance-to-`id` would otherwise be 2 for an
  empty string.
- **Test assertions decode the stderr error object.** The structured error on
  stderr JSON-escapes the embedded quotes (`did you mean \"title\"?`), so a raw
  substring match on the stderr bytes fails. The `did-you-mean` test unmarshals
  `{"error":{"message":...}}` and asserts on the decoded message instead.

## fee-aag4 — items: honest handling of missing publication dates (Req 3)

- **SQL three-valued logic does the exclusion for free.** Dropping the
  `COALESCE(published_at, fetched_at)` in `itemFilters` and filtering on
  `published_at` directly means a null row makes `published_at >= ?` /
  `<= ?` evaluate to NULL (not true), so dateless items fall out of a
  publication-axis window with no explicit `IS NOT NULL` clause. The fetch axis
  keeps filtering on the always-present `fetched_at`, so it is untouched.
- **SQLite NULL ordering already matches the contract.** SQLite sorts NULL below
  any value, so `published_at DESC` puts dateless items last and `ASC` puts them
  first, exactly as Req 3 demands; no `NULLS LAST/FIRST` was needed. Noted in the
  `itemOrder` comment that the deferred Postgres backend defaults the opposite
  way and will need explicit NULLS ordering behind the `Store` seam.
- **The omitted count is computed where the filter lives, not via a second Store
  method.** `QueryItems` now returns `core.ItemQueryResult{Items, OmittedNoDate}`.
  Factoring `nonDateFilters` out of `itemFilters` lets the row query and a
  `COUNT(*) ... AND published_at IS NULL` count query share the exact same
  feed/contains/tombstone predicates, so they can never drift. The count is taken
  only on the publication axis with an active `--since`/`--until`.
- **In-memory double split into match/count phases.** `matchesItemFilters` became
  `matchesNonDateFilters` + `matchesDateFilter`; `QueryItems` increments
  `OmittedNoDate` for a null-published item inside a publication window before the
  date check. Gotcha: `matchesDateFilter` must early-return true when no
  `--since`/`--until` is set, otherwise a no-filter query wrongly drops every
  dateless item (caught by the round-trip and full-item tests). `coalesce` is kept
  only for `PruneItems`, whose age semantics are out of scope for this ticket.
- **Breaking-change test updates.** `TestItemsSinceUntilWindow` and the SQLite
  null-ordering test asserted the old coalesce behavior and were rewritten to the
  honest contract. `TestOutputSchemaContractPreserved` gains `omitted_no_date` as
  a property (not required, via `omitempty`), which the reflected schema picks up
  automatically.

## fee-9otc — poll: permanent-redirect rename visibility (Req 6)

- **Report what the store did, not what poll intended.** `fee-n6j6` already
  renamed a feed on a 301/308, but the store declines the rename when the
  redirect target is already subscribed (no merge). Deriving the `renamed` entry
  from the intended `finalURL` would falsely claim a rename that never happened.
  The fix threads the _actual_ landing URL back out: `Store.RecordSuccess` now
  returns `(renamedTo string, err error)`, `""` when the URL was unchanged
  (including a declined rename). `consumeSuccess` builds the `core.FeedRename`
  only when `renamedTo != ""`.
- **Signature change rippled through every `RecordSuccess` implementer and
  caller.** The SQLite store, the in-memory double, the `store_test` fakeStore
  mock, the `run_interrupt_test` wrapper, `poll.RecordSuccess` (the lifecycle
  helper), and `cli/enable.go` all needed updating. The compiler drove this;
  non-rename call sites just take `_, err :=`.
- **Envelope list initialized empty, never null.** `PollResult.Renamed` is filled
  with `make([]core.FeedRename, 0, len(...))` + append so it marshals to `[]` when
  empty, matching the existing `failures` contract. Asserted distinctly from
  `null` via `pollEnvelopeHasField(..., "renamed", "[]")`.
- **The info log line rides the default level.** `--log-level` defaults to
  `info`, and a clean poll emits no logs, so the one-line
  `"renamed feeds after permanent redirect"` (with `count`) shows up on stderr at
  the default level; the cli test decodes it with the shared `decodeLogLine`.
- **Schema and goldens regenerate, contract test does not.** `renamed` appears in
  the reflected `poll` schema automatically, but
  `TestOutputSchemaContractPreserved` pins the expected property/required sets by
  hand and had to gain `renamed`. The e2e poll goldens were regenerated with
  `go test ./internal/e2e -update` (they gain `"renamed":[]`).

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

## fee-oyw2 — poll: emit partial result envelope when persistence fails mid-run

- **fee-udsl fixed the timeout; this ticket is the general case it exposed.**
  Even without a spurious deadline, `consume` can hit a genuine store-write
  failure partway through a run's feeds. Before this fix, `Run` discarded
  `totals` on any `consume` error and returned a zero `Result`, so every feed
  persisted before the failure became invisible on stdout even though it was
  durably written — the same "stored but never reported" failure mode, just
  from a real error instead of a fake one.
- **The fix is almost entirely reshaping, not new logic.** `consume` already
  returned its accumulated `pollTotals` alongside a non-nil error; `Run` just
  needed to build the `Result` from `totals` unconditionally and return it on
  both the success and error paths, instead of returning `Result{}` on error.
  Extracted the feed-ordering loop into `orderedItems(feeds, totals)` so both
  paths use the same code and can't drift.
- **`result.Polled > 0` is the signal that distinguishes a mid-persist failure
  from an early one.** `selectFeeds`/`skippedCount` failing before any feed is
  fetched still returns a zero `Result` (nothing was ever fetched, so
  `totals.polled` is never set) and stdout correctly stays empty. `pollAction`
  checks this instead of trying to classify the error itself.
- **Extracted `shapePollResult(result, feedErrs) PollResult`** so the success
  path and the new mid-persist-failure path build the identical envelope
  shape from the same inputs — the ticket explicitly called out the risk of
  the two constructions drifting if written separately.
- **Test ordering gotcha**: both the `store.Store` fake (`DueFeeds`) and the
  in-memory CLI double select and persist feeds in URL-sorted order. A test
  simulating "the Nth feed's write fails" has to name its feeds so the
  surviving one sorts before the failing one (e.g. `aaa-good.example` /
  `zzz-bad.example`), or the failure hits before anything is persisted and the
  "partial" assertion is vacuously about zero items.
- **New test double**: `testsupport.FailingUpsertStore` wraps any
  `store.Store` and fails `UpsertItems` only for a configured feed URL,
  delegating everything else — reusable for any future test needing a
  mid-persist write failure without hand-rolling a mock of the whole
  interface.

## fee-rgmp — Migrate exit codes to the fleet taxonomy (ADR 0001)

- **Only `core.ExitCodeFor` changed to reclassify failures.** The sentinels
  (`ErrUsage`, `ErrConfig`, `ErrStoreUnavailable`, `ErrSchemaTooNew`) and the
  `FeedError` categories were already distinguishable with `errors.Is`/
  `errors.As`, so the whole reclassification is one `switch`: usage 64, config
  78, store 69, schema-too-new 65, internal and the unclassified fallback 70.
  Feed-scoped categories (network, http, parse, timeout) still map to 0, and
  `poll`/`check` result sub-codes 2/3 come from `Result.ExitCode`, untouched.
- **Two funnels hardcoded `OsExiter(1)` and did not flow through
  `ExitCodeFor`.** `commandNotFound` and `completionShellNotFound` in `root.go`
  built usage-category errors but exited 1 directly. The ticket assumed they
  followed automatically; they did not. Both now call
  `core.ExitCodeFor(err)` so an unsupported completion shell exits 64 like every
  other usage error. `TestUnknownCommand` already routed through the returned-
  error boundary (urfave treats an unknown top-level command as a usage error,
  not via `CommandNotFound`), which is why only the completion test caught this.
- **The mid-persist poll write failure maps to 70, not 69.** A store write that
  fails partway is a bare, unclassified error (not wrapped as `CatStore` or
  `ErrStoreUnavailable`), so it hits the `ExitCodeFor` fallback (70, internal).
  This is consistent with the ADR's "internal and the final fallback 70" rule;
  the ticket did not ask to reclassify these errors, only to remap codes.
- **Conformance is a data cross-check, not prose.** The
  `TestExitCodeTablesCoverExitCodeFor` guard enumerates one error per failure
  class, asserts none returns a
  code in the 1-63 result range, and asserts every produced code is a declared
  key in `defaultExitCodes`/`pollExitCodes`/`checkExitCodes`. It fails if
  `ExitCodeFor` ever grows a class the registry does not describe, satisfying the
  ADR's registry-as-data requirement.
- **No golden files embed the exit-code tables**, so nothing needed regenerating
  with `-update`; the schema tables are asserted structurally in `schema_test.go`
  and `check_test.go`, not byte-for-byte.
- **`add` classifies every validation failure (bad URL, unfetchable, blocked
  redirect) as `CatUsage`/`ErrUsage`**, so all of them exit 64 — worth knowing
  when documenting exit codes for SSRF-redirect and non-feed rejection cases.

## fee-d32a — Promote core to the public API surface

- **The move is mechanical, but three test files needed a hand fix the sed pass
  could not do.** Rewriting `parse.ParsedFeed` to `core.ParsedFeed` left the
  `internal/parse` import unused in nine files (all the `poll` and `command`
  tests that only imported it to name the type), and left
  `internal/parse/parse_test.go` naming `core` without importing it. `go vet`
  reports both classes at once, so vetting after the sweep is faster than
  reading the diff.
- **`internal/testsupport/parser.go` no longer imports `internal/parse` at
  all.** The `FakeParser` type only mentioned `parse` for the `ParsedFeed`
  return; the `var _ parse.Parser = (*testsupport.FakeParser)(nil)` assertion
  that keeps the double honest lives in `parser_test.go`, so the port
  conformance check survives the import removal.
- **The relocation is behavior-preserving, confirmed by data rather than
  inspection.** Every `internal/command/testdata/**` golden compared
  byte-identical with no `-update` run, including `discover`'s, because
  `core.Candidate` carries the JSON tags verbatim.
- **`core` still imports `internal/terr`, and that is intentional.** A public
  package may import an internal one inside the same module; only external
  importers are blocked. `core.FeedError` exposes `Code()`, `ExitCode()`, and
  `Hint()` as methods, so an embedder classifies through `errors.As` and never
  names `terr.Coded`. `go list -deps ./core | grep feedwatch` is the one-line
  check that the dependency stays that narrow.
- **`core/doc_test.go` is a compile pin, not a behavior test.** It constructs
  `ParsedFeed` and `Candidate` from `package core_test`, so a later ticket that
  pushes either type back behind `internal/` fails to compile here rather than
  failing silently at the point an embedder tries to use the library.

## fee-lq28 — Promote the Store interface to a public store package

- **The RED step was a real compile failure, not a formality.** Moving
  `store_test.go` to `store/` and pointing it at
  `github.com/andreswebs/feedwatch/store` before the interface itself moved
  makes `go vet ./store/` fail with "no non-test Go files", which is exactly
  the "an external package cannot implement this interface" state the ticket
  removes. The pin stays useful afterwards: `fakeStore` lives in
  `package store_test` and names only `core` and `store` types, so a later
  ticket that pushes an argument type back behind `internal/` breaks here.
- **The sed sweep needed no hand fixes this time.** Unlike the `core` move,
  every rewritten file already imported the interface package under the name
  `store`, and the package clause was unchanged, so nothing was left importing
  a package it no longer names. `internal/store/sqlite` import paths keep their
  `internal/` prefix because the pattern anchored on the trailing quote
  (`feedwatch/internal/store"`).
- **`internal/store/` still exists, and only holds `sqlite/`.** An internal
  package importing a public one in the same module is the same legal direction
  as `core` importing `internal/terr`; only external importers are blocked from
  `internal/**`.
- **The compliance assertions are the compatibility tripwire.** `sqlite.Store`
  and `testsupport.InMemoryStore` already had one; `FailingUpsertStore` did not,
  because it embeds `store.Store` and satisfies the interface structurally.
  That is precisely the case worth pinning: an embedded interface silently
  absorbs any method added later, so `internal/testsupport/failing_store_test.go`
  now asserts it explicitly.
- **Behavior-preserving, confirmed by data.** Every
  `internal/command/testdata/**` golden compared byte-identical with no
  `-update` run.

## fee-f3u8 — feedwatch.Config, App skeleton, and the options constructor

- **The default store path had to become lazy, not just move.** The ticket makes
  the CLI pass `--db` through verbatim, empty included, so nothing resolves the
  XDG default at flag-parse time any more. Both the App and the CLI's surviving
  `openStore` need the same resolution plus first-run directory creation, so it
  landed as `Config.StorePath()`: a non-empty `Store` is returned untouched, an
  empty one resolves `DefaultStorePath()` and creates its parent at `0o700`.
  Keeping `DefaultStorePath()` pure (no I/O) is what makes it table-testable
  against `XDG_STATE_HOME` and `HOME`.
- **Dropping `ensureStoreDir` from the `Before` hook is a visible behavior
  change, in the right direction.** Previously every command created
  `$XDG_STATE_HOME/feedwatch/` even when it never opened a store, so `discover`
  provisioned a database directory it had no use for. Creation now happens on
  first store open. `TestDefaultStoreDirAutoCreated` still passes because
  `migrate --status` does open the store.
- **`backendName` became `Config.Backend()` to avoid a duplicated scheme
  check.** The App picks a driver and `migrate --status` reports a name from the
  same rule; with `Config` public, one method serves both and there is nothing
  to drift. `BackendSQLite`/`BackendPostgres` are named constants because the
  value is contract output, not an internal label.
- **An injected store is migrated but never closed.** Those two halves of
  ownership pull in opposite directions and are easy to conflate: the App must
  bring any backend to the schema version its use cases expect (so `resolveStore`
  calls `Migrate` once per App, injected or not), while `Close` releases only
  what the App opened. `InMemoryStore.Migrate` is idempotent and
  `FailingUpsertStore` delegates through its embedded store, so the doubles
  absorbed the new `Migrate` call with no changes.
- **`warnf` exists ahead of its callers deliberately, and is tested for it.**
  The `unused` linter flags an unexported method nothing calls, and no use case
  raises a warning yet, so `TestWarnerReceivesAdvisories` is what keeps the port
  wired and the build green until the poll use case lands.
- **A repo-wide AST walk is a cheap way to pin the no-leak rule.**
  `imports_test.go` parses every `.go` file in the module imports-only and fails
  any `urfave/cli` import outside `internal/command`, which covers ADR 0003's
  rule for the library, the domain packages, and every frontend added later, not
  just the two contract files `run_test.go` already checked.
- **Behavior-preserving, confirmed by data.** Every
  `internal/command/testdata/**` golden compared byte-identical with no
  `-update` run.

## fee-rzwl — App use cases: store-only commands

- **`App.Migrate` needed a second store-resolution seam, not a flag.**
  `resolveStore` applies pending migrations once per `App`, which is exactly
  what makes `applied` untruthful for the one use case whose job is to report
  that count. Splitting the open from the migrate (`storeLocked`, plus
  `resolveStoreUnmigrated` and `markMigrated`) keeps the guard intact for the
  other seven while letting `Migrate` do the work itself and then record it, so
  a later use case on the same `App` still does not repeat it.
- **`PruneRequest`'s pointer fields replace `cmd.IsSet`, and that is the whole
  reason they are pointers.** The framework's set-ness check was the only thing
  distinguishing `--keep-days 0` (prune everything older than now) from the flag
  being absent. Moving the policy into the library meant carrying that
  distinction in the request type, which a plain `int` cannot express. The CLI
  now decodes set-ness into the pointer once, in `pruneRequest`.
- **`Validate` delegating to the resolver is what keeps the two honest.**
  `PruneRequest.Validate` and `ItemsRequest.Validate` both resolve against a
  fixed instant and discard the result. The parsing and enum rules are therefore
  stated once, in `policy`/`query`, and a validation pass cannot accept
  something the use case then rejects.
- **`dashIfEmpty` is duplicated across the package boundary on purpose, for one
  ticket.** `discover` still renders its own text table from `internal/command`
  and moves in `fee-kj8z`; copying four lines beats exporting a formatting
  helper from the library or having the CLI import a library internal.
- **The `--fields` field-name validation moved with `suggest.go` intact.**
  `unknownFieldMessage` and the Levenshtein suggester came across whole, with
  their test, because the exact message (did-you-mean plus the full valid list)
  is pinned by `internal/command/testdata/err/` goldens.
- **Behavior-preserving, confirmed by data.** Every
  `internal/command/testdata/**` golden compared byte-identical with no
  `-update` run, and the reflected `output_schema` for all seven commands is
  unchanged: `jsonschema.Reflect` emits structural JSON Schema only, with no
  package or type name in it, so moving the result structs to the root package
  could not shift the emitted contract.

## fee-kj8z — App use cases: network commands

- **`App.Poll` is the one use case whose result and error are both meaningful.**
  `poll.Run` can commit some feeds' writes and then fail, so the method returns
  a populated `PollResult` alongside a non-nil error. The contract is stated on
  the method rather than left implicit in the CLI: `res.Polled > 0` with a
  non-nil error is the truthful partial envelope a caller should still render,
  and `res.Polled == 0` is an early failure whose result must be discarded.
  `Poll` now returns the zero `PollResult` explicitly in that early case, so a
  frontend cannot render a misleading all-zeros envelope with an OK head.
- **The auto-disable advisory only survived the move because of `WithWarner`.**
  `poll.Deps.Warn` used to be wired straight to the renderer inside the action.
  With the loop in the library, the App's `Warner` is the only path back to
  stderr, so `Deps.app` gained `WithWarner(rendererFrom(ctx).Warn)`. Until that
  wiring landed the `auto_disable` golden's stderr was empty, which is exactly
  the regression the golden exists to catch.
- **`PollResult.ExitCode` duplicates `poll.Result.ExitCode` on purpose, pinned
  by a table test.** The envelope already carries `Polled` and `Failed`, so the
  CLI no longer needs to import `internal/poll` at all; the shared test over a
  grid of polled/failed pairs is what keeps the two derivations from drifting.
- **`Add` validates the network before opening the store.** The old action
  opened the store first because the resolver did; the library's lazy store
  resolution means an unfetchable URL is now rejected without provisioning
  anything, which matches the documented three-step shape (syntax, then proof it
  parses as a feed, then the `GetFeed` probe that decides `created`).
- **`Discover` is the test that proves lazy store resolution works.** Running it
  against a config whose store path points into a `t.TempDir()` leaves that
  directory empty, which is only true because `New` performs no I/O and
  `Discover` never calls `resolveStore`.
- **`internal/command/import.go` carries a temporary copy of the two add-time
  helpers.** `isAbsoluteHTTPURL` and `validateParsesAsFeed` moved into the root
  package unexported, so the OPML import action (still in the CLI until
  `fee-gvuo`) got its own `importURLIsAbsoluteHTTP` and
  `importEntryParsesAsFeed` with identical messages. Duplicating ten lines for
  one ticket beats exporting library internals or blocking on the OPML move.
- **Behavior-preserving, confirmed by data.** Every
  `internal/command/testdata/**` golden compared byte-identical with no
  `-update` run, `make build` passes, and `make test-race` is clean across both
  fan-out use cases.

## fee-gvuo — App use cases: OPML import and export

- **The filesystem boundary belongs to the frontend, so the use cases exchange
  bytes.** `App.Import` takes the OPML document as `[]byte` and `App.Export`
  returns it as a string; opening the file, reading stdin, and creating the `-o`
  destination all stay in `internal/command`. That is what lets an HTTP handler
  reuse the same two methods with a request and a response body.
- **`ExportResult` is deliberately headless.** Every other result envelope
  carries the schema head, but export's payload _is_ the OPML document; wrapping
  it would contradict the `schemaRegistry` entry that already describes export as
  a `string` scalar rather than a JSON envelope.
- **`ImportRequest.Validate` is a field, so that request type has no
  `Validate() error` method.** Go forbids a field and a method sharing a name.
  Import has nothing to validate syntactically (an unparseable document is
  reported by `Import` itself), so the field wins; the reflection ticket that
  walks every request type must not assume the method is universal.
- **Reading the source moved out of `opml.Parse`, which split one error into
  two.** The action used to hand a reader straight to the parser, so an I/O
  failure surfaced as "not a valid OPML document". Now `io.ReadAll` runs first
  and a read failure is its own usage error ("cannot read the OPML source"),
  which is the honest classification.
- **Retiring the resolver replaced three typed test seams with one.**
  `Deps.store`, `Deps.fetch`, and `Deps.parse` became a single
  `opts []feedwatch.Option`, appended by `Deps.app`. The `storeOpts`/`netOpts`
  helpers in `root_test.go` keep the call sites as short as the old struct
  fields while expressing injection in public library types.
- **The CLI's own OPML assertions had to move down a layer.** `export_test.go`
  parsed its output with `internal/opml`, which the CLI may no longer import;
  the document's shape is now asserted in the library's `export_test.go`, and
  the CLI test asserts only that the document reaches stdout or the `-o` file.
  `TestTheCLIHoldsNoDomainCollaborators` in `imports_test.go` is what keeps the
  six banned adapter imports out for good.
- **Behavior-preserving, confirmed by data.** Every
  `internal/command/testdata/**` golden compared byte-identical with no
  `-update` run, `make build` passes, and `make test-race` is clean.

## fee-savm — Derive CLI flags from request-struct tags by reflection

- **The projection uncovered a real contract drift, and fixing it cost the
  byte-identity criterion.** The ticket asked for `feedwatch schema` output to be
  byte-identical before and after, and it is for every flag on every command. It
  is not for two `args` arrays: `poll` and `check` accepted trailing feed
  references through urfave's implicit positional tail while declaring no
  `Arguments`, so `schema poll` reported `"args":[]` even though `ArgsUsage` said
  `[FEED...]` and [cli-design.md](../cli-design.md) documented
  `"args":[{"name":"feed","variadic":true}]`. Declaring the variadic argument, as
  the ticket's own design mandates, makes the introspected surface agree with both
  the design doc and reality. The schema fixture for those two commands was
  regenerated deliberately; every other fixture, and all of
  `internal/command/testdata/**`, compared byte-identical with no `-update` run.
- **A variadic `StringArgs` needs `Max: -1`.** With the zero value the framework
  refuses to parse the argument at all ("args feed has max 0, not parsing
  argument"), and declaring one moves the values out of `cmd.Args().Slice()` into
  `cmd.StringArgs(name)`. Both halves matter: `argsFor` sets `Max: -1` and `bind`
  reads the named accessor, so the previous `cmd.Args().Slice()` call sites had to
  change with the declaration rather than after it.
- **Construction and binding live in one table value.** `flagKinds` maps a
  `reflect.Type` to a `flagKind{newFlag, bind}` pair rather than having `flagsFor`
  and `bind` each carry their own switch. Two switches over the same six types
  would be free to drift; one map entry cannot.
- **A missing tag panics, not just an unmapped type.** The ticket specified a
  panic for a field type absent from the mapping table. The same argument applies
  to a field carrying neither `flag` nor `arg`: without the panic, adding a
  request field would silently produce no CLI surface, which is exactly the drift
  the ticket exists to prevent. `flag:"-"` is the explicit opt-out.
- **One usage string cannot be a struct tag.** `--fields` enumerates the
  projectable item field names from `core.ItemFieldNames()`, and a tag is a
  compile-time constant. Rather than duplicating the list into the tag, `withUsage`
  overrides that one flag's usage after projection. It is a named, panicking
  escape hatch (an unknown flag name is a programming error), so the exception is
  visible instead of hidden.
- **The bare-schema golden had to exclude the error inventory.**
  `TestSchemaNewSentinelAppears` registers a test-only sentinel into the
  process-wide `terr` registry and cannot unregister it, so the `errors` array in
  `schema` output is not stable across a package run: the new fixture passed alone
  and failed in the suite. The golden now normalizes `errors` to a token; the
  inventory is pinned by `TestSchemaErrorInventory` instead, and the flags and
  arguments the fixture exists to pin are untouched.
- **The schema envelope carries no usage text, so a second golden was needed.**
  `FlagSchema` records name, aliases, type, and default but not usage, which means
  a schema-only fixture would not have caught a mangled `usage` tag. A companion
  `help/` golden set pins `--help` for the root and every command, and it compared
  byte-identical throughout the migration; together the two fixtures cover every
  tag the projector reads.
- **`--keep-days 0` is now pinned at the CLI, not just at the projector.** The
  pointer entry in the mapping table is the only place where the Go type and the
  flag type deliberately disagree, so it is covered twice: `bind`'s set-versus-unset
  round trip, and `TestPruneExplicitZeroKeepDays` driving the real boundary.

## fee-vbid — feedwatch/daemon: the embeddable poll scheduler

- **Drop-on-overlap forces the poll off the loop goroutine.** A loop that calls
  `App.Poll` inline cannot drop a tick: with a caller-driven channel an
  unbuffered send blocks until the loop comes back around, and with a
  `time.Ticker` the cap-1 buffer queues exactly one stale tick that fires a
  second poll the instant the first returns. Both are "queued", which the design
  forbids. `pollOnce` therefore runs the poll in a goroutine (its result lands on
  a buffered channel, so the goroutine always exits) while the caller keeps
  selecting on the tick channel and discards whatever arrives. The drop is then a
  property of the code rather than of the channel's buffering, and the test can
  hold a poll open on a gated fetcher, deliver several ticks, and assert one
  event.
- **The "Run twice" test needs proof the first Run is in its loop.** Spawning the
  first `Run` in a goroutine and immediately calling `Run` again races on which
  call wins the guard: when the second call wins, it blocks in the loop forever
  and the test hangs rather than failing. Sending one tick first and receiving
  its event proves a `Run` is already looping, so the direct call is
  unambiguously the loser and returns `ErrAlreadyRunning`.
- **The guard is not reset on exit, deliberately.** `Run` closes `Events` before
  returning, so a second run would publish into a closed channel. The
  single-use guard is a `CompareAndSwap` that is never cleared, which makes a
  re-run an error instead of a panic.
- **A store double, not a fetcher double, produces `Event.Err`.** A feed that
  fails to fetch is result data (it lands in `Result.Failures`, and `Poll`
  returns nil), so driving the error path needs a whole-invocation failure: a
  `store.Store` wrapper whose first `DueFeeds` call fails, embedding the
  interface so only that one method is overridden. The second tick then proves
  the scheduler survived.

## fee-3p3r — Public API documentation and runnable examples

- **The learnings for this epic live here, not in the file the ticket named.**
  The ticket points at `docs/specs/001-initial-implementation/learnings.md`, but
  every other ADR 0007 ticket (`fee-d32a`, `fee-lq28`, `fee-f3u8`, `fee-rzwl`,
  `fee-kj8z`, `fee-gvuo`, `fee-savm`, `fee-vbid`) recorded into
  `docs/specs/learnings.md`. Splitting one epic across two files would be worse
  than following the ticket literally, so the convention won.
- **Three structural findings the epic paid for, restated as the surface's
  rationale.** They are now documented where an embedder reads them rather than
  only where an implementor recorded them:
  - `core` and `store` cannot be merged into the root package. The internal
    adapters import the domain types and the `Store` interface, and the root
    package imports those adapters to build the default store from
    configuration, so declaring either at the root closes an import cycle. The
    argument is stated in `store/doc.go`, which is the package an external
    implementor reads first.
  - `ParsedFeed` had to move from `internal/parse` to `core` for the public
    `Parser` port to be assignable at all. A public interface whose method
    signature names an `internal/` type is uninhabitable from outside the module:
    it compiles, and no external type can ever satisfy it. The same applies to
    `Candidate`, which is why `core/doc_test.go` pins both by constructing them
    from `package core_test`.
  - `New` must not open the store eagerly. `Discover` is read-only and touches
    no store, so an eager constructor would create a database file (and its
    parent directory, under the XDG default) for every `feedwatch discover`
    invocation. Lazy opening also puts the "any command applies pending
    migrations" guarantee on first use rather than on construction, which is why
    the migrate use case deliberately bypasses that guard to report a truthful
    applied count.
- **The repo's `defer func() { _ = x.Close() }()` convention outranks the
  idiomatic doc snippet.** `errcheck` is enabled, and an example is ordinary
  compiled test code, so the conventional `defer app.Close()` a godoc snippet
  would show fails the gate. The examples use the checked form and `doc.go`'s
  snippet was changed to match, so a reader copying either one gets code that
  passes this project's lint.
- **A doc-coverage sweep found only what a linter cannot see.** `revive`'s
  exported rule is not enabled, so the check was a throwaway `go/ast` walk over
  the four public packages asserting every exported identifier carries a comment
  starting with its own name. It reported four gaps: the two port interface
  methods (`Fetcher.Fetch`, `Parser.Parse`), and the members of two grouped
  const blocks (`BackendSQLite`/`BackendPostgres`,
  `SourceAutodiscovery`/`SourceProbe`) that had a block-level comment only. The
  interface-method gap is the one worth noticing: a port's method comment is
  where an implementor learns the contract, and it was the one place with none.
- **Which examples run is a deliberate split, and `go vet` is what keeps it
  honest.** `ExampleNew` and `ExampleApp_Items` carry `// Output:` and run
  against a temp-dir store, which is deterministic because migrations are
  idempotent and an empty store returns an empty projection. `ExampleApp_Add`,
  `ExampleApp_Poll`, `ExampleApp_Poll_errors`, `ExampleWithStore`, and
  `ExampleScheduler` carry none, so they are compiled and never executed: the
  first three would reach the network and write to the default store path, and
  the last two would panic on the stub's embedded nil interface. `go vet`'s
  example check is what catches a misnamed `ExampleApp_Poll` that would
  otherwise silently never compile against a real method.
- **`ExampleNew` asserts the no-I/O rule as data.** It stats the configured
  store path after `New` and prints `store created by New: false`, so the
  lifecycle promise in `doc.go` is pinned by a running test rather than only
  claimed in prose.

## fee-ui25 — Library and thin frontends (epic closeout)

- **The epic's own work was verification, plus one gap the children's criteria
  could not see.** Each of the nine children asserted its own slice; the epic
  asserts the whole. Four of the five acceptance criteria were already
  demonstrable (`go list` shows exactly `feedwatch`, `core`, `store`, `daemon`
  plus two `main` packages as non-internal; `internal/command` imports only
  `output`, `terr`, and `jsonschema`, with the domain-collaborator ban enforced
  as a test by `TestTheCLIHoldsNoDomainCollaborators`; no tracked golden is
  modified; `make build` and `make test-race` pass). The fifth exposed the gap.
- **"Mandatory table test" was satisfied in letter but not in effect.** ADR 0007
  requires a test that walks _every_ request type in the library, because an
  unmapped field type is the one property of the reflection layer that cannot
  fail at compile time. The table was hand-maintained and its comment argued
  that a request type no frontend wires cannot affect any input surface. True of
  wiring, but not of the risk being guarded: declaring the type is what admits
  the unmapped field, so the failure has to land when the type is added, not
  when a frontend first wires it and panics at command-tree construction.
  `TestRequestSurfaceCoverage` closes it by parsing the library's own source for
  exported `*Request` types and requiring each to appear in the table, which
  makes the omission of the next use case a test failure.
- **The library's source is the only enumeration of its exported types.**
  Reflection cannot list a package's declarations, so the guard reads them with
  `go/ast`, following the precedent already set by the module-root
  `imports_test.go` architectural guards. Both take the same two precautions:
  fail when the walk finds nothing, so the guard can never pass vacuously, and
  glob plus `parser.ParseFile` rather than `parser.ParseDir`, which staticcheck
  rejects as deprecated (SA1019) since it ignores build tags.
- **A guard that cannot be seen failing is not yet a guard.** This one passed
  the moment it was written, since the table was complete, so it was checked by
  deleting one row and confirming the failure named `feedwatch.PruneRequest`
  before restoring it. Writing an always-green assertion and trusting it is how
  a vacuous guard enters a suite.

## fee-6ol6: items --fields discoverability

**`jsonschema:"opaque"` suppresses all recursion.** The reflector treats the tag
as a signal that the per-element shape is dynamic and emits a bare
`{"type":"object"}`. Removing it from `ItemsResult.Items` and `PollResult.Items`
is sufficient for the full item shape to appear in `schema items` and
`schema poll`. `ProjectedItemsResult.Items` correctly keeps the tag because its
element shape really is caller-projected.

**`time.Time` is a struct with no exported fields.** Without special-casing it,
the reflector emits `{"type":"object"}` -- correct Go, wrong schema. The fix is
to check `t == timeType` before the struct branch and return
`{"type":"string","format":"date-time"}`. For `*time.Time` (nullable
`published_at`), check `t.Elem() == timeType` in the pointer branch and return
`{"type":["string","null"],"format":"date-time"}`.

**The `schema.Type` field must be `any` (not `string`) to support nullable
types.** Changing it to `any` allows marshaling either a plain string or a JSON
array `["string","null"]` without introducing a separate struct.

**Levenshtein distance 2 misses `published -> published_at` (distance 3).**
The did-you-mean guard was correct as designed; the fix was to append the full
valid field list unconditionally so callers never need a probe round-trip even
when no suggestion fires.

## fee-8klp: poll failures[] message field

**E2e golden files must be updated when the JSON envelope shape changes.**
Adding `message` to `PollFailure` broke two golden files in
`internal/e2e/testdata/`. The fix is straightforward -- update the golden
file content -- but easy to miss if you only run unit tests.

**`Detail()` centralizes the fallback logic that `Error()` also applies.**
Rather than duplicating "prefer Message, fall back to Err.Error()" at every
call site, the `Detail()` method on `*FeedError` owns it once. The `fee-r1kt`
`check` command can reuse it directly for its own `CheckFailure.Message` field.

## fee-r1kt: check command

**A bounded errgroup is sufficient for `check` concurrency.** The ticket notes
that per-host serialization (like poll's `orchestrate`) is nice-to-have for
`check`. Since `check` is a validation pass typically run over imports (mostly
distinct hosts), the bounded errgroup provides adequate politeness without the
complexity of host-keyed worker routing. If per-host fairness becomes important
later, the orchestrate pattern can be extracted as a library.

**Unconditional GET for check: omit ETag/LastModified from FetchRequest.**
Setting `FetchRequest{URL: f.URL}` (no ETag or LastModified) ensures the server
always returns a 200 with a body to parse. A conditional GET that receives 304
would prove the URL is reachable but not that the body is currently parseable.

## fee-8ugz: migrate Go module from src/ to repo root

**Moving go.mod to the VCS root changes VCS stamping on bare `go build`.**
While the module lived at `src/go.mod` (a subdirectory of the git repo), a bare
`go build` reported `debug.ReadBuildInfo().Main.Version` as `(devel)`, so
`version.Current()` fell through to `"dev"`. Once `go.mod` sits at the
repository root (which is also the VCS root), Go stamps `Main.Version` with a
real pseudo-version derived from the git tag (for example
`v0.0.3-0.2026...-<sha>+dirty`). That broke the e2e `version` golden, which
asserts the deterministic dev fallback. Fix: build the e2e binary with
`-buildvcs=false` so `Main.Version` stays `(devel)` and the fallback is
exercised deterministically regardless of git state. The shipped binary is
unaffected because `make` always sets `version.Override` via `-ldflags -X`.

**golangci-lint caches results by absolute path.** After the `git mv`, lint
reported stale gosec/unused findings against nonexistent `/workspace/src/...`
paths. `golangci-lint cache clean` (plus `go clean -cache`) cleared them; the
findings were purely cache artifacts, not real regressions.

## fee-v737: terr coded errors layered onto core.FeedError

**Giving `FeedError` an `ExitCode() int` method silently made it satisfy
`urfave/cli/v3`'s `ExitCoder` interface (`error` plus `ExitCode() int`).** The
exit boundary in `internal/cli/root.go` distinguished the poll/check result
sub-code carrier from hard failures with
`errors.As(err, &coder)` against `cliv3.ExitCoder`. Once `FeedError` carried
`ExitCode()` (required by ADR 0002's `terr.Coded`), every hard `FeedError`
matched that interface, so the boundary treated it as an already-reported
outcome and skipped rendering the JSON error object to stderr entirely (exit code
still set, stderr empty). Fix: match the concrete unexported `exitError` type
(`errors.As(err, &ee)`), which is the only intentional result sub-code carrier,
instead of the now-too-broad `cli.ExitCoder` interface. The `terr.Coded` and
`cli.ExitCoder` interfaces overlap by construction; disambiguate by concrete
type at the boundary.

**Feed-scoped `FeedError` now exits 70 at the boundary, not 0.** The old
`core.ExitCodeFor` returned 0 for feed-scoped categories (http/network/parse/
timeout) because feed outcomes drive the aggregate poll/check codes 2 and 3, not
a returned error. The new `output.ExitCodeFor` resolves `terr.Coded` via
`errors.As` and returns the class sentinel's exit code, which is 70 for every
feed-scoped category. This is intentional (ADR 0002 invariant): a feed-scoped
`FeedError` reaching the whole-invocation boundary is a bug path and must
classify loudly as the internal-error class (70) rather than silently exit 0.
The `internal/cli/exit_conformance_test.go` inputs (whole-invocation classes
only) are unchanged: 64/65/69/78/70.

**Where the sentinels live.** The registered `terr.E` sentinels stay in
`internal/core` (not `internal/terr`) so the ~40 `errors.Is(err, core.ErrUsage)`
call sites keep compiling and `core` gains only a `terr` import (`terr` imports
nothing but `fmt`/`sync`, so no cycle). `ExitCodeFor` moved to `internal/output`
per the ticket, resolving `terr.Coded` at the boundary.

## fee-7hf3: ADR 0005 result envelope head and null-coalescing marshaling

**Two payload-field renames were forced by the head, not just the one the
ticket named.** The head occupies the `schema_version` and `ok` JSON keys. The
ticket flagged the `migrate` collision (its store version also used
`schema_version`, renamed to `store_schema_version`). A second, unflagged
collision existed: `CheckResult` carried the passing-feed count as `ok` (an
integer). Embedding the boolean head would have let the shallower `ok:int` win
under encoding/json's field-depth rule, silently dropping the head's `ok:true`.
Renamed that field to `passed`. Both renames are breaking and are recorded in
the CHANGELOG; the `check` usage prose and manual-QA shapes were updated too.

**The `type alias` trick in each envelope's `MarshalJSON` drops the whole method
set, so the embedded head still marshals normally.** `json.Marshal(alias(e))`
inlines `output.Head` (schema_version, ok) because Head has no marshaler of its
own; only the outer envelope's `MarshalJSON` is shed, which is exactly what
prevents infinite recursion.

**`jsonschema` had to learn to inline an anonymous embedded struct.** The
reflector took a field's property name from the json tag, falling back to the Go
field name, so an embedded `output.Head` (no tag) would have emitted a nested
`Head` property. `structSchema` now detects an anonymous field with no json tag
and merges the embedded struct's properties and required entries into the
parent, matching how encoding/json flattens the field. This keeps the reflected
`output_schema` the single source of truth: every reflected envelope now shows
`schema_version` and `ok` at the top level, both required.

**The generic text fallback skips anonymous embedded fields.** `renderText`
walks exported struct fields; without a guard it would have printed a
`Head: {1 true}` line under `--format text`. Skipping anonymous fields keeps
text output byte-identical to the pre-head shape (the e2e goldens are all
`.stdout`; no `.stderr` or text golden changed).

**ADR 0006 evidence.** Regenerating the e2e goldens with `-update` produced
exactly the reviewed diff: every JSON `.stdout` gained the two leading keys,
`migrate_status.stdout` also gained the `store_schema_version` rename, and no
`.stderr` file and no exit code changed.

## fee-z3bm: ADR 0005 stderr error envelope, batch form removed

**The reference `EmitError(w, err)` message assumed a bare `Error()` string.**
The go-cookiecutter reference sets `Message: err.Error()`, which is correct
there because its coded errors render a bare message. feedwatch's
`core.FeedError.Error()` prepends a `<category> <url> (status):` head for text
output, so using it verbatim would have leaked that prefix into the envelope
message (the ticket's target shape shows a bare `"server returned HTTP 404"`).
`EmitError` now resolves the message through an optional `detailer` interface
(`Detail() string`), which `FeedError` already implements, falling back to
`err.Error()` for everything else. The structured `code` and `details` already
carry the classification the prefix duplicated.

**`details` must be omitted, not rendered as `{}`, for a whole-invocation
error.** `FeedError.ErrorDetails()` returned a non-nil `feedErrorDetails{}` even
when the error had no feed scope (empty URL, zero status), so every usage or
config error leaked `"details":{}`. `ErrorDetails()` now returns nil in that
case, so `EmitError`'s `errors.As(err, &terr.Detailed)` still matches but the
`omitempty` on the envelope's `details any` field drops it. The acceptance
criterion is "details present only when populated"; an empty object is not that.

**Per-feed failures are stdout-only result data now.** Removing the stderr batch
`{"errors":[...]}` had no information cost: the poll and check `failures` arrays
on stdout already carry `{feed_url, category, status, message}`, and exit codes 2
and 3 are driven by the aggregate result, not by a returned error. The
`all_failed` and `partial` e2e `poll.stderr` goldens regenerated to empty; the
matching `poll.stdout` and exits (2 and 3) were unchanged.

**Context cancellation stays non-internal without coercing to FeedError.** The
old `feedErrorFor` coerced every boundary error into a `*core.FeedError`; its one
load-bearing behavior was mapping `context.Canceled`/`DeadlineExceeded` to the
timeout category so a graceful interrupt never rendered as `internal_error`. The
replacement `boundaryError` passes any `terr.Coded` error through untouched (so
`EmitError` classifies it) and only wraps an uncoded residual cancellation as a
timeout-category error. Pinned by `TestBoundaryErrorContextCancellationIsNotInternal`.

## fee-q120: consolidate the exit boundary into Run(args, deps) int

**The package rename `internal/cli` to `internal/command` created a stutter.**
`cli.CommandSchema` was fine; `command.CommandSchema` trips revive's
`exported: ... stutters`. Renamed the type to `command.Schema` (the aggregate
stays `command.SchemaResult`). The type is package-internal, so no external
caller moved; the schema goldens are unaffected because the JSON keys never
mentioned the Go type name.

**The boundary needs a format-aware error renderer, so the reference's
JSON-only `output.EmitError(deps.Err, err)` in `Run` does not port verbatim.**
feedwatch renders `--format text` errors as a symbol-marked line, pinned by
`TestTextErrorNoColorOnNonTTY` and used by the real CLI. `Run` cannot resolve
the format itself without importing the framework (the resolved global flags
live on the parsed `*cli.Command`). The interior `runRoot` therefore returns the
boundary renderer (`*output.Renderer`, a framework-free type) alongside the
error, and `finish` emits through it. This keeps `run.go` framework-free while
preserving format-aware error output.

**`--version` moved off the framework's `VersionPrinter` global.** ADR 0003
forbids mutating framework package globals. Rather than reinstalling
`cliv3.VersionPrinter` from the construction point, the root sets
`HideVersion: true` and defines a plain `--version`/`-v` bool flag; the Before
hook detects it, writes the version envelope, and returns a zero-code
`exitError` so the action never runs and no store setup happens. This also
short-circuits before the store-dir creation that a real command triggers.

**Void framework callbacks surface errors through a captured pointer.**
`CommandNotFound` (unknown command, unsupported completion shell) returns
nothing in urfave v3, so it cannot hand an error to the neutralized boundary.
`runCustom` passes a `*error` that the callbacks set; after `cmd.Run` returns,
the interior prefers that captured error. This replaces the old
render-then-`OsExiter` path so those usage errors flow through the single
boundary like any other.

**The signal override is deterministically unit-testable by pre-filling the
channel and blocking the action on `ctx.Done()`.** `watchSignal` records the
caught signal into a buffered channel _before_ cancelling the context; an action
that blocks on `<-ctx.Done()` cannot return until the cancel fires, which cannot
happen until the record is done, so `finish` observes the signal without a race.
`TestRunSignalOverridesExitCode` feeds SIGINT/SIGTERM and asserts 130/143 win
over the action's would-be config error (78). The e2e `signal_test.go` still
drives the real binary unchanged.

**Dropping `Store`/`Fetch`/`Parse` from the exported `Deps` moved the test seam
to unexported same-package fields.** `Deps` is now `{In, Out, Err, Clock,
Version, Signal}` plus unexported `store`/`fetch`/`parse` that only
`package command` tests set; `resolve.go` reads them and builds production
collaborators when nil. Every per-command test helper funnels through one
`drive(t, d, args...)` that runs the real `Run` with temp-file streams (which
still satisfy the `Fd()`/`Stat()` terminal probe, so color stays off on a
non-terminal). The `runResult.exited` bool is gone: `Run` returns the code
directly, and `exited` was exactly `code != 0`.

## fee-nkdl: ADR 0005 NDJSON warning channel with auto-disable producer

**The warning envelope carries `level:"warning"` instead of `ok`, so a consumer
disambiguates it from the error envelope by presence, not by value.**
`output.EmitWarning` mirrors `EmitError`'s best-effort discipline (marshal
failure drops the line rather than escalating), since a warning never changes
the outcome it advises about. `Renderer.Warn` gates on format exactly like
`Result` and `Error`, and text mode uses a new `SymbolWarn` (⚠) with
`ansiYellow`, deliberately not `SymbolFail`: a warning is not a failure, and
color is never the sole carrier of the marker.

**The poll layer stays stream-blind through an injected `WarnFunc` on `Deps`,
not by importing `output`.** `RecordFailure` gained a trailing
`warn WarnFunc` parameter and raises the advisory only on the exact crossing
(`count == threshold`), not `>=`, so a feed that keeps failing past the
threshold does not re-warn. The `command` layer wires `poll.Deps.Warn` to the
renderer's `Warn` method value, whose signature already matches `WarnFunc`. A
nil callback is a no-op, so unit tests that build `Deps` without a warner keep
working; the direct `RecordFailure` lifecycle tests pass `nil` explicitly.

**`--quiet` suppresses logs, never warnings, which the e2e golden pins for
free.** The suite already runs with `--quiet`, so under it a per-feed failure
leaves stderr empty (the failure info log is suppressed) and the only line on
the crossing poll's stderr is the warning object. The `auto_disable` scenario
drives 10 forced polls: nine warm-ups assert only the unchanged exit 2 via
`runJSON`, and the tenth is golden-pinned. `poll --force` re-targets the
still-active feed each round (it selects active feeds, ignoring backoff), and a
further forced poll after the disable targets nothing (exit 0, no warning).

**Final advisory shape:** code `feed_auto_disabled`, message
`feed disabled after N consecutive failures`, hint
`re-enable with: feedwatch enable <feed>`, details
`{feed_url, failures}`. The `<feed>` in the hint is HTML-escaped in the JSON
line by `encoding/json` (`<feed>`), consistent with the error
envelope.

## fee-e4tl: grow the schema command with the error inventory and tool-level exit codes

**Uniform reference core plus additive enrichment.** `SchemaResult` now carries
the ADR 0005 reference core (`tool`, `version`, `commands`, a tool-level
`exit_codes` as `[]int`, and an `errors` inventory of `{code, exit_code, hint}`)
alongside the pre-existing feedwatch enrichment (`global_flags`, the per-command
`exit_codes` map, and the derived per-command `output_schema`). Neither replaces
the other: a reference-written consumer reads the core, and a feedwatch-aware one
also gets the richer detail.

**Both new collections are projections, not literals.** `errorInventory()` maps
`terr.All()` element-wise, and `exitCodeUnion()` reads the same per-command
`ExitCodes` maps the schema already reports (parsing the string keys to int and
sorting). Because the union reads the reported `Schema.ExitCodes` rather than the
registry tables directly, it can never disagree with what each command entry
shows. The tests assert against the registry rather than a hard-coded list, so a
newly registered sentinel (`TestSchemaNewSentinelAppears`) or a new command exit
code appears with no test edit.

**`SchemaError` lives in `internal/command`, not `internal/output`.** The
reference template puts it in the output package, but feedwatch's schema types
(`Schema`, `SchemaResult`) already live in `internal/command/schema.go` and the
projection happens there, so co-locating avoids a cross-package type with no
other consumer.

**The `schema` command's own registry entry stayed a described `Scalar`, not a
reflected `oneOf`.** Both `Schema` and `SchemaResult` carry a `json.RawMessage`
`output_schema` field; `json.RawMessage` is `[]byte`, which the reflector renders
as an array of integers, so `OneOf(Reflect(Schema{}), Reflect(SchemaResult{}))`
would misdescribe `output_schema`. The prose was corrected to name the new
envelope keys instead.

**Reciprocal conformance guard.** `exit_conformance_test.go` gained
`TestRegisteredCodesDeclaredInTables`: every code in `terr.All()` maps to an exit
code declared in the command tables (all registered codes map to 64/65/69/70/78,
which `defaultExitCodes` declares and every command inherits). Combined with the
original `TestExitCodeTablesCoverExitCodeFor`, the tables and the error registry
cannot drift apart in either direction.

**No `schema` e2e golden added.** `schema` is not in the exec golden set, and the
follow-up harness ticket (fee-hlu7) builds the in-process golden triple where a
schema scenario belongs; adding an exec golden here would only be deleted there.
The shape is covered by the `internal/command` unit tests.

## fee-hlu7: ADR 0006 in-process golden-triple harness

**The in-process harness drives real collaborators, not fakes.** Scenarios call
`Run(args, deps)` with `bytes.Buffer` streams and a real store on a temp `--db`
path, a real fetcher, a real parser, and a real `httptest` feed server, exactly
as a user would. Only the clock (`testsupport.FixedClock`) and the version
string are injected, so `published_at`, backoff, and due calculations are
deterministic. This keeps the goldens honest: they pin the same bytes the binary
emits, and every shared golden came out byte-identical to the exec suite it
replaced.

**The exit code is a scenario-table value, not a third golden file.** Each
scenario declares its expected exit as the `wantExit` parameter and the harness
asserts it, which reads clearer than pinning a third artifact per scenario and
matches the exec suite's existing shape.

**Every observed exit is checked against the declared tables at run time.**
`assertDeclared` looks up each observed exit in the union of
`defaultExitCodes`/`pollExitCodes`/`checkExitCodes`, so a scenario that produced
an undeclared code would fail the suite, not just a mismatched golden. Coverage
in the other direction (every declared code has a scenario) is
`TestGoldenExitCodeConformance`.

**Exit 70 (EX_SOFTWARE, internal/unclassified) has no reachable scenario.** It is
produced only by an unclassified Go error or a recovered panic in `main`,
neither of which a real command emits deterministically. Rather than fake it,
the conformance test names it as intentionally uncovered (the explicit note ADR
0006 asks for) and still asserts a table declares it, so the note cannot go
stale.

**Schema-too-new (65) is set up by stamping the store directly.** After a real
`migrate` brings the store to the current version, the test opens the sqlite
file with a second `sql.Open("sqlite", path)` connection and inserts
`MAX(version)+1` into `schema_migrations`, mimicking a db written by a newer
binary. The preceding `Run` has already closed the store, so the two connections
do not contend.

**The one legitimate byte difference from the exec goldens is the version
value.** In-process, `--version` reports the injected `Deps.Version` ("1.2.3");
the exec suite builds with `-buildvcs=false`, so its binary reports "dev". This
is a difference in the version injection path, not a contract change; every
other shared golden is byte-identical, and there is no stream-interleaving
difference because both harnesses keep stdout and stderr as separate sinks.

**Exec suite reduced to its conditional half.** `internal/e2e` now carries only
`signal_test.go` (SIGINT to 130, SIGTERM to 143, which need a real process to
receive a signal) plus the shared `TestMain`/`binPath`/`exitCodeOf`/`rssFeed` it
depends on. Every stream-contract scenario moved in-process and its exec goldens
were deleted (the whole `internal/e2e/testdata` tree) rather than kept as a
drifting second copy. The `fee-udsl` first-poll regression moved in-process too,
as a JSON-decoding scenario (`TestGoldenFirstPollReportsAllNewItems`), since it
never needed a process.

**Per-command tests now partly redundant (for a follow-up to retire).** The
JSON-shape assertions in `add_test.go`, `list_test.go`, `items_test.go`,
`poll_test.go`, `migrate_test.go`, `discover_test.go`, `export_test.go`,
`import_test.go`, and `prune_test.go` that decode stdout to check the envelope
shape are largely superseded by the golden scenarios, which pin the exact bytes.
The error-code assertions scattered across `root_test.go` and the per-command
usage tests are likewise covered by the `err/*` golden scenarios plus
`assertDeclared`. Retiring them is out of scope here (the acceptance only
requires the old tests keep passing, which they do).

## fee-by04: final documentation pass for the ADR adoption

This ticket carried no code; its value is recording the six adoption decisions
that were resolved by owner decision rather than derivable from the code, so a
future reader understands why the adopted shape looks as it does. The
per-ticket implementation discoveries live under the sibling headings above;
these are the design rationale behind them.

**`terr` layers onto `core.FeedError` rather than replacing it.** `FeedError`
survives for its per-instance structure (`FeedURL`, `Status`, `Category`) and
gains `terr.Coded`/`terr.Detailed` by delegating to a per-category class
sentinel (ADR 0002's "per-instance structure delegates to its class sentinel"
pattern). Replacing it outright would have churned the ~40 `errors.Is(err,
core.Err...)` call sites for no contract gain. The sentinels stay in
`internal/core` for the same reason (see fee-v737).

**The stderr per-feed batch `{"errors":[...]}` form was dropped, not migrated.**
Per-feed failures are result data: they already appear in the stdout `failures`
array, and the poll and check exit codes (2 and 3) are driven by the aggregate
result, not by a returned error. Keeping a second stderr copy would only invite
drift and mislabel a partial run as `ok:false`. stderr now carries only
whole-invocation error envelopes, warnings, and logs (see fee-z3bm).

**The migrate store version moved to `store_schema_version`.** The envelope head
owns the `schema_version` key for every command, so the migrate payload's own
store-version field could not keep that name without colliding into two
`schema_version` keys in one object. Renaming the payload field, not the head
key, keeps the head uniform across the fleet (see fee-7hf3, which also caught
the parallel `check` `ok`-to-`passed` rename).

**The schema command is uniform core plus additive enrichment.** The `commands`
list, the tool-level `exit_codes` union, and the `errors` inventory of
`{code, exit_code, hint}` are the reference core every fleet tool shares, so a
reference-written consumer works unchanged against feedwatch; the per-command
`exit_codes` map and derived `output_schema` stay as additive feedwatch detail.
The floor is uniform, the ceiling is per-tool (see fee-e4tl).

**Exactly one warning producer was wired.** Only the auto-disable-after-threshold
advisory (`feed_auto_disabled`) is a state change a caller cannot otherwise
observe from a single invocation's output, so it is the one warning raised. The
three other candidates the plan considered were deliberately left unwired: the
permanent-redirect rename is already in the `renamed` result array, and the
lossy-charset fallback and the guessed-versus-autodiscovered candidate are
observable from their own result fields. A warning earns its place only when the
result stream does not already carry the signal (see fee-nkdl).

**Warnings are not suppressed by `--quiet`.** A warning is contract output, not a
log; `--quiet` raises the log floor only. The e2e suite runs with `--quiet` and
still sees the `feed_auto_disabled` line, which pins the distinction for free
(see fee-nkdl).

**`strconv.Quote` escapes the value a "does the message quote the input" test
looks for.** A tag-validation test asserting `strings.Contains(err.Error(),
"a\tb")` failed even though the message did quote the tag, because the message
carried the escaped `"a\\tb"`. The assertion has to be written against
`strconv.Quote(tag)`, not the raw tag. This matters for any message that quotes
user input containing control characters (see fee-zt9x).

**`core` builds its own usage errors rather than reaching for `usageErr`.** The
root package's `usageErr` helper lives in `prune.go` and is not importable from
`core`, so `core/tags.go` has a small file-local `tagUsageErr` constructing
`&FeedError{Category: CatUsage, Err: ErrUsage}` directly. The duplication is
deliberate: promoting the helper into `core` would invert the dependency the
package layout keeps acyclic (see fee-zt9x).

**Tag filter fields were purely additive at every construction site.** Adding
`Tags`/`Match` to `ListFilter`, `ItemQuery`, and `PrunePolicy` needed no call-site
change, because the zero `TagMatch` is `MatchAll` and an empty tag set matches
every feed, so today's "match everything" behavior survives untouched. That is
what let the value layer land ahead of the store and command lanes without a
behavior change (see fee-zt9x).

**Every migration-count assertion was already written against the derived
maximum.** Adding `0002_feed_tags.sql` bumped the schema to version 2 without
touching a single existing test: `Pending` is compared to
`len(loadMigrations())`, the too-new guard derives `codeMax` at runtime, and the
golden harness stamps `MAX(version)+1`. Only two golden files moved, and both
diffs were the version integer alone. Writing migration tests against the
derived maximum rather than a literal is what makes each new migration a
one-file change (see fee-c6fa).

**Proving an in-place upgrade needs the unexported applier, not `Migrate`.**
`Migrate` always runs the full set, so a test that seeds a v1-shaped row has to
call `applyMigrations(ctx, ms[:1])` first, insert through `s.db`, then finish
the migration. That is why the migration suite is white-box in
`migrate_internal_test.go`, and why `openTestStore` opens a temp _file_ rather
than `:memory:`, where each pooled connection would see its own empty database
(see fee-c6fa).

## fee-pfpz: sqlite feed tags read, write, and filtering

**Omitting one column from `DO UPDATE SET` implements the whole preserve rule.**
`AddFeed` writes `tags` in the INSERT list but deliberately not in the conflict
clause, so creation takes the caller's tags and a re-add leaves the stored set
untouched. That is the plan's "omitted preserves, given replaces" semantics with
no need for the store to distinguish an empty tag set from an absent one, and no
`*[]string` in `core.Feed`. Explicit replacement has its own path, `SetTags`.

**`json_each` is the first SQL-side JSON use in the store.** Everything to date
marshals Go-side and treats the column as opaque text, so `tagPredicate` in the
new `internal/store/sqlite/tags.go` carries a comment saying the JSON1 functions
ship with `modernc.org/sqlite` and need no build tag. The predicate takes the
column name as a parameter precisely so the `items` half (fee-2lbg) can reuse it
inside a `feed_url IN (SELECT ...)` subquery without a second copy.

**Canonicalizing before building the `match=all` SQL is what makes it correct.**
The all-match form compares `count(DISTINCT value)` against the number of
requested tags, so `--tag AI --tag ai` would demand two matches from a feed that
can only ever supply one. Running `core.CanonicalTags` first collapses the
request to one tag and one expected count.

**`ListFeeds` had to stop growing its WHERE incrementally.** The old code
appended `" WHERE status = ?"` inside the status branch, which silently assumed
status was the only possible predicate. A second optional clause makes that
assumption a bug, so both `ListFeeds` and `DueFeeds` now build a `[]string` of
clauses and hand them to a shared `feedQuery` helper that emits `WHERE` only
when there is something to filter on (and `strings.Builder`, never `+`, to keep
`gosec` G202 quiet).

**The `DueFeeds` signature break cost eight call sites and no behavior.** Every
existing caller passes `core.ListFilter{}`, whose zero value matches every feed,
so the change is purely mechanical outside the new lane path in the store and
the test double. `internal/testsupport` got the feeds-half parity in the same
pass (a shared `matchesTags` helper used by both `ListFeeds` and `DueFeeds`,
plus `SetTags` and `TagCounts`), leaving fee-o5uq the items and prune half.

## fee-2lbg: sqlite item query and prune filtering by tag

**One shared helper carried the clause to all three statements.** `tagPredicate`
from fee-pfpz already took the column name as a parameter, so the items half
only needed a thin wrapper, `feedTagScope` in `internal/store/sqlite/tags.go`,
that wraps it as `feed_url IN (SELECT url FROM feeds WHERE <pred>)`. That one
form drops into the item query's `nonDateFilters`, the age prune's `WHERE`, and
both halves of the max-per-feed prune, so the SQL exists once.

**A subquery beats a JOIN here for two independent reasons.** `feeds` and
`items` both have an `updated_at` column, so `JOIN feeds ON feeds.url =
items.feed_url` would make every reference in `itemColumns`, `alwaysColumns`,
and `scanItem` ambiguous and force qualification across the file. Separately,
the subquery composes into any existing `WHERE`, which is what let one clause
serve statements with three different shapes.

**Putting the clause in `nonDateFilters` is what keeps `OmittedNoDate` honest.**
`countOmittedNoDate` rebuilds its own SQL from the same helper, so adding the
tag clause one level up gave the dateless-exclusion count the same scope for
free. The behavior is only observable by test: an undated item on an out-of-lane
feed must not inflate the count.

**Pagination is the reason the filter had to be in SQL.** `QueryItems` applies
`LIMIT`/`OFFSET` in SQL, so a Go-side tag filter would page over the unfiltered
set and then discard rows, returning short and wrongly-offset pages. The fixture
interleaves publication times across in-lane and out-of-lane feeds precisely so
that `Limit: 2, Offset: 2` lands on a different pair each way (`k03 k04`
filtered versus `k02 k03` unfiltered), which makes the test a real
discriminator rather than a tautology.

**The max-per-feed prune needs the scope twice, and the inner one matters
most.** `ROW_NUMBER() OVER (PARTITION BY feed_url ...)` ranks whatever its
source yields; leaving `FROM items WHERE tombstoned = 0` unscoped keeps
out-of-lane rows in the window, so the `rn > N` cutoff falls in the wrong place
even though the outer `WHERE` would then tombstone only in-lane rows. Both
statements were converted from hardcoded strings to `strings.Builder` plus an
accumulating `[]any` (also what keeps `gosec` G202 quiet), and the scope's
arguments are appended once per textual occurrence, in clause order.

## fee-o5uq: testsupport InMemoryStore tag parity

**Half the parity work was already there, and only reading both files revealed
which half.** `SetTags`, `TagCounts`, the `DueFeeds` signature, and tag
filtering in `ListFeeds` had landed with the feeds-half tickets; the items and
prune halves had not. There is no conformance suite to point at the gap, so the
gap was found by listing the SQLite tag test names and checking each for a twin.
That listing is the actual verification step for a hand-maintained double, and
it is worth doing before writing any code rather than after.

**Two nil-means-all URL sets are clearer than intersecting them.** `QueryItems`
already had `feedURLSetLocked`, whose nil return means "match all". Rather than
merging the lane set into it, the lane resolves to a second set and both are
tested in the per-feed loop. Nil-as-match-all does not survive intersection
cleanly (nil ∩ set is the set, not empty), so keeping them separate avoids a
helper whose correctness depends on remembering that asymmetry.

**A feed the double has no row for is out of every lane.** The lane set is built
from `s.feeds`, so items upserted for an unsubscribed URL (which several older
tests do) vanish under any tag filter. That is exactly what the SQL subquery over
`feeds` does, and it is the kind of place where an in-memory double could
trivially be more permissive than the store and thereby lie to command tests.

**Canonicalizing the requested tags at the call site is not just a
micro-optimization.** Hoisting `core.CanonicalTags(filter.Tags)` above each loop
made `matchesTags(feedTags, want, m)` take two plain slices, which is what let
the same helper serve the two feed loops and the lane-set builder. The earlier
shape took a whole `core.Feed` and could not be reused for a set built from a
map iteration.

**The re-add-preserves-tags behavior is an omission, so it needs a comment.**
The double's existing-feed branch copies `Alias`, `Interval`, and `UpdatedAt` and
says nothing about `Tags`. That silence is the whole implementation of "omitted
preserves, given replaces", mirroring the SQLite upsert's `DO UPDATE SET`, and
without a doc comment on `AddFeed` it reads as a field someone forgot.

## fee-47yv: FeedView.tags and add --tag

**A comma in a tag cannot be rejected through the CLI, only through the
library.** The ticket asked for `add URL --tag "a,b"` to exit 64. It cannot:
urfave splits a `[]string` flag on commas before `AddRequest.Validate` ever
runs, so the request carries two well-formed tags and the add succeeds. That is
not a bug to route around, it is the documented equivalence of the two `--tag`
spellings in the feed-tags plan. `core.ValidateTags`'s comma rule therefore
guards library embedders, who can pass a string the flag parser never saw. The
CLI test pins the equivalence instead of the rejection, and the library test
keeps the rejection; asserting the ticket's literal wording would have meant
breaking the documented spelling rule to satisfy a test.

**A non-omitempty slice field makes its struct non-comparable, which breaks
`==` in tests, not in production.** Adding `Tags []string` to `FeedView` made
`enable_test.go`'s `first.Feed != second.Feed` idempotency check a compile
error. `reflect.DeepEqual` is the fix. Worth grepping for `==` on any struct
before adding a slice field to it, since the failure is a build break in a test
file rather than anything the type checker flags at the definition.

**`tags` is required on `FeedView` and optional on `AddResult`, and that
asymmetry is deliberate.** The reflected schema shows `tags` in `required` for
the three `FeedView` envelopes and absent from `AddResult`'s. `FeedView` is the
feed projection an agent parses uniformly, so the key is always present;
`AddResult` already omits `alias` and `interval` when unset, and staying
internally consistent matters more there than cross-command uniformity.

**Two goldens were stale before this ticket and only passed via the test
cache.** `migrate_status.stdout` and `err/schema_too_new.stderr` still carried
store schema version 1 after the tags migration bumped it to 2. The opening
`make build` passed because `go test` served a cached result; the first
uncached run failed both. Regenerating them is part of this ticket only because
it ran `-update`; the lesson is that a green `make build` on an inherited tree
is not evidence the goldens match until something forces an uncached run.

## fee-frus: list --tag and the --match pattern

**The `bind` trap was real, and the tests caught it in the intended order.**
`internal/command/list.go`'s action passed a literal `feedwatch.ListRequest{}`
instead of calling `bind`, a habit that was harmless only while the request had
no fields. Writing the CLI tag test first made the failure exact and immediate:
`flag provided but not defined: -tag`, because `flagsFor` and the action are two
independent halves and only the former is derived from the struct. `export` is
now the last command with a literal-request action; the same fix is owed there
the moment `ExportRequest` grows a field.

**`TestRequestSurfaceMapping` is the guard that makes this class of drift
visible.** Its hand-maintained table asserts a flag count per request type, so
adding two flags to `ListRequest` failed it (`want 0`) before any golden did.
It does not, however, prove the flags reach the request — only that they are
declared. That gap is exactly what the missing `bind` lived in, so a
behavioral test that asserts a filtered result is still required per command.

**`tagFilter` deliberately validates `--match` even when no tag is named.**
`list --match bogus` with no `--tag` is a usage error rather than a silent
no-op, because a caller that misspells the match value has a bug whether or not
the selection is currently empty. The alternative (skip validation when
`Tags` is empty) would make the same typo fail loudly or silently depending on
an unrelated flag.

**`ListRequest.Validate` and `App.List` both route through `filter()`, so the
use case never calls `Validate` directly.** `App.List` calls `req.filter()`
once and uses its result, which validates as a side effect; calling `Validate`
first would parse the same selection twice. This mirrors how `ItemsRequest`
resolves through `query`, and it is the shape T10-T12 should copy: one resolver
method, `Validate` discarding its result, the use case keeping it.

**The comma and repeated spellings are equivalent for free, and that is worth a
test anyway.** urfave's string-slice flag splits on commas, so `--tag a,b` and
`--tag a --tag b` produce the same `[]string` without any parsing code. The
test that runs both spellings and compares raw stdout pins that as contract
rather than as an accident of the framework, which matters because `--match` is
then provably the only knob that changes the answer.

## fee-9ajb: tag command: read and edit a feed's tags

**The write-skip needed a spy, not a timestamp.** The acceptance rule "a read
and a no-op edit do not bump `updated_at`" cannot be asserted through
`updated_at` in the use-case tests: `newTestApp` runs on a `FixedClock`, so the
column is byte-identical whether or not the row was rewritten, and the
assertion passes vacuously against an implementation that always writes. A
small `store.Store` decorator embedding `*testsupport.InMemoryStore` and
counting `SetTags` calls observes the skip directly. The same trap applies to
any future "this path performs no write" claim tested on a fixed clock.

**The delta is a set difference over canonical sets, which makes the write-skip
free.** `apply` returns the canonical set the feed should carry, so comparing it
with the canonical current set decides both what changed and whether to write.
Computing `added`/`removed` from the requested flags instead would have needed
separate no-op detection and would have reported a requested-but-already-present
tag as added.

**`--add` composing with `--remove` needs a stated order.** Applying additions
before removals means a tag named in both ends up removed; the opposite order is
equally defensible, so the choice is pinned by `TestTagAddBeforeRemove` and
stated in the `App.Tag` doc comment rather than left to the reader.

**Adding a command touches six places, and only two of them fail loudly.** The
tree registration and the schema registry are silent when missed (`registryFor`
falls back to `{"type":"object"}`), while `TestRequestSurfaceCoverage` and the
golden enumeration fail immediately. `TestRequestSurfaceCoverage` was the one
not named in the ticket's registration checklist: it parses the library source
for every `*Request` type, so a new request type fails it until a row is added
to `requestSurfaceCases`. Expect it on T7 and any later use case.

**The comma rule in `core.ValidateTags` is unreachable from the CLI.** A
string-slice flag splits on commas before the request is built, so `--add a,b`
arrives as two valid tags and `--add a,,b` arrives as an empty tag. The comma
rejection still matters for library callers, which is where its test lives.

## fee-7pg3: tags command: report the lane vocabulary

**A zero-field request still needs its row in `requestSurfaceCases`.** The
previous ticket predicted `TestRequestSurfaceCoverage` would fire on T7, and it
did: `TagsRequest{}` is an empty struct, so `flagsFor`/`argsFor` return empty
slices and nothing about the command's behavior depends on the table, but the
guard parses the library source for `*Request` types and fails until the row is
added. The row reads `{"tags", feedwatch.TagsRequest{}, 0, 0}`, which is the
point of the table: zero flags and zero arguments is a declared surface, not an
unmapped one.

**A stray positional on a zero-argument command is silently ignored by the
framework, so the rejection has to be written.** urfave/cli v3 leaves unclaimed
words in `cmd.Args()` when `Arguments` is empty, so `feedwatch tags extra`
exited 0 and reported the whole vocabulary until `tagsAction` checked
`cmd.Args().First()` and returned a usage-category error. The root's own
`rootAction` already treats a leftover positional as a usage error, so this is
the subcommand-level version of an established rule rather than a new one.

**Every other zero-argument command still ignores extras.** `list`, `export`,
`migrate`, and `import` (with a file flag) all accept and discard a stray word;
only `tags` now rejects it. Making the rule uniform is a tree-wide contract
change with golden fallout across the whole surface, so it belongs in its own
ticket rather than riding along here. Until then, `tags` is deliberately the
strict one, matching what its ticket asked for.

**The `tags` count is over feeds of any status, and that is the only reason the
result differs from unioning `list --tag`.** A lane whose feeds have all been
auto-disabled stays visible in the vocabulary, which is what makes `tags` a
discovery command rather than a summary of what would poll next. The
active-only count is `list --tag X`, and the doc comment on `App.Tags` says so
rather than leaving the difference to be discovered from a surprising count.

## fee-5wk4: poll --tag and check --tag

**The lane filter had to be threaded into `skippedCount`, not just the
selection.** `skippedCount` measured the polled set against every active feed in
the store, so a lane-scoped poll of a two-feed lane in a three-feed store
reported `skipped` for a feed that was never a candidate. Both the force branch
of `selectFeeds` and `skippedCount` now narrow through one `activeIn(filter)`
helper, which is the structural guarantee that the selection and the count draw
from the same universe rather than two hand-written `core.ListFilter` literals
that agree today.

**`--tag` with named feeds is a usage error, and the check has to live in the
resolution path both `Validate` and the use case share.** `PollRequest.filter`
and `CheckRequest.filter` mirror `ListRequest.filter`: they own the rule, and
`Validate` calls them and discards the result. `App.Poll` and `App.Check` call
`filter` directly rather than calling `Validate` and then re-resolving, so an
invalid selection cannot reach the store and the rejection cannot drift from the
validation.

**Rejecting early is what keeps the destructive-adjacent guarantee testable.**
The CLI tests assert on the fetcher's per-URL request count, not only on exit
64. A validation that ran after `resolveStore` would still exit 64 while having
dialed the network, and only the call-count assertion catches that.

**`poll --tag` deliberately does not imply `--force`.** It narrows the _due_
selection, which is what makes a lane runnable on its own cron cadence; the
force branch narrows the active selection instead. Both paths are covered
separately because a single test of `--force --tag` would pass against an
implementation that quietly forced every lane poll.

## fee-0fcw: items --tag

**`--feed` and `--tag` intersect here, where `poll` and `check` reject the
combination.** The divergence is deliberate and worth stating, because both
readings are defensible. `poll --tag X --feed Y` is ambiguous about which set to
run and is a usage error; `items --tag X --feed Y` narrows a read, so the two
clauses simply AND, and a feed outside the lane returns an empty list rather
than an error. The intersection is the store's natural behavior (two independent
WHERE clauses), so the risk was never a wrong implementation but an untested
assumption; both the library and CLI tables pin it explicitly, including the
empty-intersection case.

**Pagination is the test that proves where the filtering lives.** The lane
fixture titles every item with its age in hours, so `--tag ai --limit 2
--offset 2` has one hand-computable answer over the filtered set. A Go-side
filter applied after the store paged would return a different pair while every
other lane assertion still passed, which is why that subtest is worth its
arithmetic.

**Request field order is the CLI surface.** `flagsFor` walks the struct in
declaration order, so placing `Tags`/`Match` next to `Feeds` rather than at the
end is what keeps `--tag` beside `--feed` in `--help` and in `schema`. Adding
them also moves `TestRequestSurfaceMapping`'s expected flag count, which is the
guard's way of asking that a new flag be acknowledged rather than absorbed.

**The output contract did not move.** Only the flag lists in
`schema/items.stdout`, `help/items.stdout` and `schema/all.stdout` changed;
`output_schema` and `lifecycle/items.stdout` are byte-identical, which is the
evidence that no item-level tag field crept in. Items carry no tags of their
own: a lane is a property of the subscription.

## fee-7emy: prune --tag and rm --tag

**`--tag` narrows a destructive operation; it never authorizes one.** Adding
tags to `PruneRequest.policy` after the bound check, not before it, is what
keeps a bare `prune --tag ai` a usage error. The rule is worth a test of its
own because the natural reading of "I selected a lane" is "I said what to
prune", and a lane is not a bound: `prune --tag ai` with no `--keep-days` or
`--max-items` would otherwise silently mean "delete this lane's entire
history".

**`rm` needed an explicit "no selector" rule that no other command needs.**
Every other command reads an empty selector as "everything" and is harmless.
For `rm`, an unguarded empty selector is the difference between removing
nothing and removing every subscription, so the request rejects it up front
rather than letting the store resolve it. Both selector usage errors are tested
against **store state**, not just the exit code: a destructive command that
validates after it resolves would still exit 64 while having already deleted.

**Validating in the request, not the action, is what makes that guarantee
cheap.** `RemoveRequest.filter` is called before `resolveStore`, so a rejected
invocation never opens the store, and the store-state assertion is a
consequence of where the check lives rather than of extra care in the action.

**`removed` became a list on every path, and the head deliberately did not
move.** ADR 0005 bumps `schema_version` on a breaking shape change, and this is
one, but bumping a whole-contract integer over one field on one of sixteen
commands would break agents pinning `schema_version == 1` for unrelated
reasons. The pre-1.0 policy makes `CHANGELOG.md` the mechanism instead, so the
entry has to say explicitly that an unchanged head is not an unchanged shape.
Revisit at 1.0.

**A golden regenerated with `-update` is only as trustworthy as the tree it
ran in.** The working tree carried unrelated, uncommitted golden updates from
earlier tickets in this epic, so `git diff` on `testdata/` showed far more than
this ticket touched. Read the diff against the working tree's own prior state,
not against `HEAD`, before concluding a regeneration went wrong;
`testdata/opml/prune.stdout` staying byte-identical to `HEAD` is the check that
the untagged `prune` path did not move.

## fee-igmb: OPML tags round-trip

**`,omitempty` on the `category` attribute is what kept the diff to this
feature.** `encoding/xml` writes every attribute field, so without it each
untagged outline would have gained `category=""` and
`testdata/opml/export.stdout` would have moved for a scenario feed that has no
tags. Placing `Category` after `XMLURL` in `exportOutline` matters for the same
reason: attribute order follows struct field order, so appending keeps the
existing attributes in their recorded positions. The check that both went right
is `testdata/opml/export.stdout` remaining byte-identical after `-update`.

**`export` and `list` were the two commands whose action never called `bind`,
because their request structs were empty.** Adding fields to `ExportRequest`
alone would have produced flags that appear in `--help` and `schema`, parse
without error, and are silently discarded. The CLI-level `--tag` test exists
specifically to catch that: a library-only test would have passed against the
broken wiring. When a use case's request goes from empty to non-empty, the bind
call is part of the change, not a follow-up.

**`TestRequestSurfaceMapping`'s flag-count table is the tripwire for exactly
that mistake.** It failed the moment `ExportRequest` grew fields, before any
golden did, which is the ADR 0007 guard doing its job.

**An OPML tag is dropped, not fatal.** OPML arrives from foreign tools that
know nothing of feedwatch's tag rules, so `importTags` keeps the names that
pass `core.ValidateTags` and discards the rest, leaving the feed subscribed
with its usable lanes. The alternative (failing the outline) would make one
space-bearing category cost the whole subscription. This is the one place tag
validation is advisory rather than a usage error, and it is a property of the
foreign source, not of tags.

**Tag inheritance from folder outlines is deliberately not implemented.**
`walk` threads no parent context, and making a folder imply tags raises a real
semantic question (does a nested folder append or replace?) that this ticket
does not answer. `TestParseDoesNotInheritFolderTags` pins the current behavior
so a future implementation is a conscious decision rather than a silent one.

**Import assigns tags only on create, which falls out of the store's upsert.**
Re-importing a backup that names an already-subscribed feed leaves its lanes
alone, consistent with `add`'s omitted-preserves rule and with import already
reporting such a feed as `skipped`. An `import --tag` applying one lane to
every entry is a natural follow-up but was left out to keep the round-trip the
whole diff.

## fee-zs6b: feed tags and lane filtering (epic)

The per-ticket sections above record the local decisions. These are the ones
that only make sense across the whole epic, and that a reader of any single
ticket would not reconstruct.

**Two numbers are called a schema version, and only one moved.** The database
schema version went 1 to 2 (`migrate` reports it as `store_schema_version`);
the output-contract version in the envelope head (`schema_version`, ADR 0005)
stayed 1. They are independent by design: the first says what the store's
columns look like, the second says what an agent's parser can expect. Holding
the head at 1 across a genuinely breaking `rm` change is a deliberate
deviation from ADR 0005, justified by the pre-1.0 policy and by the cost of
signalling a whole-contract generation change to consumers of sixteen commands
over one field on one. The consequence is that the changelog, not the head, is
the mechanism recording the break, which is why its entry has to say in so many
words that an unchanged head is not an unchanged shape. Anyone renaming or
reusing either number should read this paragraph first.

**This is the codebase's first SQL-side use of JSON.** Tags are stored as a
JSON array string in a `TEXT` column, matching `items.categories` and
`items.enclosures`, but unlike those it is also _queried_: matching pushes a
`json_each(tags)` predicate into SQLite rather than reading every feed and
filtering in Go. That choice is what keeps `--limit`/`--offset` correct on
`items --tag` (paging after filtering, not before) and what makes a lane query
cheap on a large subscription list. Its price is a `store.Store` contract that
now names tags explicitly, so the predicate lives once per backend rather than
once per call site. Postgres will need its own spelling of the same predicate
when that backend lands; the Go-side filtering shortcut is deliberately not
available as a fallback.

**`AddFeed` omits `tags` from its `DO UPDATE SET` on purpose.** That single
omission is the whole implementation of "omitted preserves, given replaces":
the upsert writes tags on insert and never on the conflict path, and `Add`
calls `SetTags` afterwards only when the request named tags. The alternative,
having the store distinguish an absent tag set from an empty one, would have
required a nullable or pointer-typed field threaded through `core.Feed` for a
distinction only one call site cares about. The behavior it buys is worth
stating plainly: a routine re-add, which agents do idempotently, never drops a
feed out of its lanes.

**`list` and `export` were the two commands whose actions never called
`bind`,** because their request structs had been empty. Adding fields produced
flags that appeared in `--help` and in `schema`, parsed without error, and were
silently discarded. A library-level test passes against that wiring; only a
CLI-level test catches it. The general rule: when a use case's request goes
from empty to non-empty, wiring `bind` is part of that change, not a follow-up.
`TestRequestSurfaceMapping`'s flag-count table is the tripwire and fired before
any golden did.

**`--match` carries the AND/OR semantics, not the spelling of `--tag`.** The
CLI's `[]string` flags already accept both `--tag a --tag b` and `--tag a,b`,
and urfave parses them to the same value, so overloading the spelling with
intersection-versus-union would have made two identical invocations mean
different things. Putting the semantics in an explicit `--match all|any` also
made the comma illegal inside a tag name (the flag parser consumes it before
validation ever sees it), which in turn is what makes the OPML `category`
encoding unambiguous. `--match` is a per-request field with a default rather
than a root global, because it is meaningless for `add`, `tag`, `discover`,
`migrate`, and `schema`, and keeping it on the request types preserves the
ADR 0007 projection of the CLI surface from the library API.

**The daemon needed no new poll path.** `WithTags` and `WithMatch` populate the
`PollRequest` the scheduler already issues, so lane scoping cost two options
and no control flow. Two schedulers over one `App` watch two lanes at two
cadences against one store, which is the same property the per-lane cron
pattern relies on: splitting lanes across databases is what would break global
deduplication, and `--tag` exists so that never has to happen.

## fee-fxl2: docs, changelog, and manual QA for feed tags

**The stale `learnings.md` link had been copied into three places.** `AGENTS.md`
carried it twice and `docs/build.md` once, all pointing at
`docs/specs/001-initial-implementation/learnings.md`, which never existed; the
real file is `docs/specs/learnings.md`, shared across specs. `CLAUDE.md` is a
symlink to `AGENTS.md`, so fixing the one file fixed both names.

**Every JSON line in `docs/usage.md` was regenerated from a real binary.** The
examples are hand-written comments, so an edit-by-eye pass would have missed
that the poll fences were already stale before this feature: they omitted
`renamed`, which has been in the envelope since the rename-reporting work.
Running each documented invocation against `make build`'s binary and pasting
the output is what surfaced that, and it is cheaper than any review.

## fee-zs6b: epic gate for feed tags

**A migration downgrade beat a historical build for the legacy-store test.** The
epic's acceptance criteria demand that a store created before migration `0002`
migrate forward cleanly and that a binary predating `0002` refuse a migrated
store. Deleting the migration file from a scratch copy of the tree yields a
binary for the second half, but not the first: the rest of the code queries
`feeds.tags`, so that binary cannot populate a v1 store at all. Producing the
fixture with SQL instead (`ALTER TABLE feeds DROP COLUMN tags` plus
`DELETE FROM schema_migrations WHERE version = 2` on a copy of a fully populated
store) gives a genuine v1 database that still holds feeds and items, which is
the case that matters: it proves the `DEFAULT '[]'` backfill and that item rows
survive.

**The comma rule is enforced at two layers with two different reaches.** A
comma-bearing tag on the command line never reaches `core.ValidateTags`, because
the urfave slice flag splits on it first: `tag REF --add "a,b"` succeeds and
stores two tags. That is correct under the settled decision that the flag
spelling carries no semantics, but it means the comma branch of `ValidateTags`
is reachable only through the library API. Worth knowing before reading an
exit 0 there as a gap in validation.

**Leading and trailing whitespace is rejected, not trimmed.** `CanonicalizeTags`
trims, yet `ValidateTags` runs on the raw input and rejects any tag containing
whitespace, so `--add "  ZED "` exits 64 while `--add ZED` stores `zed`. The two
functions serve different callers (validation guards the request, canonicalization
normalizes what is already valid), and the stricter surface is the documented
one.
