# feedwatch CLI Design

## Overview

feedwatch is an agent-first command-line tool for watching RSS and Atom
feeds. Its primary user is an AI agent, not a human at a terminal. Every design
decision favors structured output, meaningful exit codes, deterministic and
idempotent behavior, schema discoverability, and one-shot invocations over
interactive prompts or long-running processes.

## Philosophy

feedwatch is pure, deterministic plumbing: fetch, parse, normalize, store,
deduplicate, and query. It is a reliable sensor. The calling agent is the brain.

- No LLM calls, no API keys, no network credentials, no nondeterminism.
- All content intelligence (summarizing, ranking, relevance, triage) is the
  agent's job, performed by reasoning over the clean JSON feedwatch returns.
- No daemon and no blocking loop. "Watch" describes the purpose (subscription
  state persists across invocations), not a running process. The agent, cron, or
  a systemd timer decides when to poll.

This keeps the tool reproducible, testable, dependency-light, and free of cost
or rate-limit concerns.

## Execution Model

feedwatch is stateful and one-shot. Each command is a short-lived invocation
that reads and writes persistent state, then exits. State persists between
calls, so the tool reports only what is new since the previous poll and handles
deduplication and conditional-GET bookkeeping internally.

```sh
feedwatch add https://blog.example/feed.xml
feedwatch poll        # reports new items, marks them seen
feedwatch poll        # immediately again: nothing new
```

## State and Storage

State (subscriptions, seen-item IDs, ETag and Last-Modified per feed, failure
counters) lives behind a `Store` interface with two interchangeable backends:

- Default: a single SQLite file at `$XDG_STATE_HOME/feedwatch/feedwatch.db`.
- Optional: a remote PostgreSQL database via a `postgres://` DSN.

The backend is selected by a single setting that holds either a filesystem path
or a connection string; the URL scheme determines the driver.

```sh
FEEDWATCH_DB=~/.local/state/feedwatch/feedwatch.db   # default (SQLite)
FEEDWATCH_DB=postgres://user@host/feedwatch          # remote (Postgres)
feedwatch --db ./test.db poll                        # per-call override
```

SQLite uses a pure-Go driver to avoid CGO. It is opened with WAL journaling, a
`busy_timeout`, and `foreign_keys = ON`, and never `synchronous = OFF`, so the
store stays durable and crash-safe under the concurrent writes of a worker-pool
poll. Each fetch worker takes its own connection from the pool rather than
sharing one. These pragmas are SQLite-specific and live behind the `Store`
interface; PostgreSQL is transactional and durable by default.

### Schema Lifecycle

Versioned SQL migrations are embedded in the binary. On any command, feedwatch
checks a stored schema version and applies pending migrations idempotently
inside a transaction. A fresh machine needs no manual setup. Migration SQL is
kept portable across SQLite and PostgreSQL, maintained behind the `Store`
interface where the dialects differ.

A migration that fails aborts the command and surfaces a JSON error on stderr
(exit 1); a failure is never logged and skipped. If the stored schema version is
newer than the running binary understands, feedwatch refuses to operate rather
than risk corrupting data written by a future version.

```sh
feedwatch migrate --status
# {"schema_version":4,"pending":0,"backend":"sqlite"}
```

## Configuration

Configuration is supplied by flags and environment variables, with XDG-based
defaults. There is no config file, so an agent never has to read hidden state to
understand current behavior. Precedence is: flags, then environment variables,
then compiled-in defaults.

Common settings include the store location (`FEEDWATCH_DB` / `--db`), the HTTP
user agent (`FEEDWATCH_USER_AGENT`), the default fetch interval, the worker
concurrency, the connect and overall per-feed timeouts, an outbound HTTP proxy
(`--proxy`, also honoring the standard `HTTP_PROXY` / `HTTPS_PROXY` / `NO_PROXY`
variables), a custom CA bundle (`--ca-bundle`), a minimum TLS version
(`--min-tls`), whether redirects into private address space are allowed
(`--allow-private`), and the log level.

## Input and Output Contract

The output contract is the core of the agent-first interface.

- stdout carries pure result JSON by default, using a consistent top-level
  envelope so an agent can parse every command uniformly. `--format text`
  switches to terminal-friendly tables and color for the occasional human;
  `--format json` is the default and may be stated explicitly. The enum leaves
  room for additional formats later without adding new flags.
- stderr carries structured JSON log lines and structured JSON error objects.
  This keeps stdout clean: piping stdout into `jq` never chokes on an error or a
  diagnostic. `--log-level` (error, warn, info, debug) and `--quiet` control
  verbosity; `--format text` makes stderr friendly text too.

Color appears only in `--format text`; JSON output is never colorized on either
stream. In text mode, color is enabled for a stream only when that stream is a
terminal, and is disabled by `--no-color`, by the `NO_COLOR` environment
variable, or when `TERM=dumb`; stdout and stderr are evaluated independently.
Color is never the sole carrier of meaning, so stripping it loses no
information: every color-coded status is also marked by a symbol or word.

### Exit Codes

Distinct exit codes let an agent branch on the outcome without parsing output.

| Code | Meaning                                               |
| ---- | ----------------------------------------------------- |
| 0    | Full success                                          |
| 1    | Usage or configuration error                          |
| 2    | All targeted feeds failed                             |
| 3    | Partial success (some feeds failed, others succeeded) |
| 130  | Interrupted by SIGINT                                 |
| 143  | Terminated by SIGTERM                                 |

Per-feed failures are reported as structured objects on stderr with the feed
URL, an error category (network, http, parse, timeout), and a message. Hard
failures (bad arguments, unreachable store) also emit a JSON error object on
stderr.

```sh
feedwatch poll 2>err.json; echo "exit=$?"
# stdout: {"polled":3,"succeeded":2,"failed":1,"new_items":2,"items":[...],
#          "failures":[{"feed_url":"...","category":"http","status":404}],
#          "renamed":[]}
# err.json: {"errors":[{"feed_url":"...","category":"http","status":404,
#                       "message":"404 Not Found"}]}
# exit=3
```

The stdout envelope enumerates which feeds failed (URL, category, and HTTP
status where applicable) so an agent can triage a partial failure from the
result stream alone; stderr still carries the full per-feed detail, including
the human-readable message. The two streams are redundant by design, not
substitutes.

### Discoverability

An agent discovers the full contract without guessing:

- `feedwatch schema` emits a machine-readable JSON description of every command:
  its arguments and flags (name, type, default), exit codes, and a JSON Schema
  for its output envelope. `feedwatch schema <command>` narrows to one command.
- Every command supports conventional `--help` and `-h`, which print human
  usage to stdout and are unaffected by `--format`. `--version` and `-v` print
  version information as JSON.

```sh
feedwatch schema poll
# {"command":"poll","args":[{"name":"feed","variadic":true}],
#  "flags":[{"name":"--force","type":"bool"}, ...],
#  "exit_codes":{"0":"ok","2":"all failed","3":"partial"},
#  "output_schema":{ ... JSON Schema ... }}
```

## Architecture

feedwatch is organized into small, single-responsibility packages under `src/`,
with a deliberately acyclic dependency graph. Pure domain types sit at the
bottom and are imported everywhere; behavior lives behind narrow interfaces that
their consumers depend on.

```text
src/cmd/feedwatch/             main: signal-aware context, command tree, exit
src/internal/core/            domain types: Feed, Item, Enclosure, Category,
                              FeedError, sentinel errors (no internal deps)
src/internal/store/           Store interface over core types
src/internal/store/sqlite/    SQLite implementation + embedded migrations
src/internal/store/postgres/  PostgreSQL implementation (deferred)
src/internal/fetch/           HTTP: conditional GET, SSRF guard, retry, charset;
                              Fetcher interface
src/internal/parse/           Parser interface + gofeed impl + normalization
src/internal/poll/            orchestration: fetch, parse, dedup, persist
src/internal/discover/        autodiscovery and common-path probing
src/internal/opml/            OPML import and export
src/internal/output/          result envelope types, JSON/text renderers, color
src/internal/cli/             command tree, flag definitions, Before hook, schema
src/internal/config/          resolved configuration
```

`Store`, `Parser`, and `Fetcher` are narrow interfaces defined for their
consumers (`poll`, `cli`), with concrete implementations in their own packages,
so each can be replaced by a test double. Time is obtained through an injected
`Clock` (a `func() time.Time` or a small interface) rather than calling
`time.Now` directly, which keeps polling, backoff, and due calculations
deterministic under test. Domain types and the error taxonomy live in `core`, so
`output`, `parse`, and `store` depend on it without depending on one another.

Components with several optional settings (notably `fetch.New` and `store.Open`)
use functional options rather than wide parameter lists, so call sites name only
what they override.

The resolved configuration and the `*slog.Logger` are passed explicitly to the
components that need them; `context.Context` carries cancellation and deadlines
only, never optional parameters. The root `Before` hook may place the resolved
config and logger in the context solely to hand them to an action, which then
wires explicit dependencies. The code avoids mutable package-level state apart
from the framework's controlled `OsExiter` and `ErrWriter` overrides.

## Error Handling and Logging

feedwatch follows idiomatic Go error handling, with a single boundary that owns
all error emission and exit-code mapping.

Errors are values, handled once. Internal layers (fetch, parse, store) wrap
errors with `%w` to preserve the chain and return them; they never log and
return. Each command's logic is invoked through one top-level `run` function
that `main` calls, and that boundary is the only place that writes error output
to stderr and selects the process exit code.

Errors that callers must distinguish carry structure. A `*FeedError` value
records the feed URL, an error `Category`, an optional HTTP status, and the
wrapped cause, and it implements `Unwrap`. `Category` is an enumeration covering
`network`, `http`, `parse`, `timeout`, `usage`, `config`, and `store`. Static
whole-invocation failures are sentinels (`ErrUsage`, `ErrConfig`,
`ErrStoreUnavailable`, `ErrSchemaTooNew`). The boundary classifies with
`errors.As` and `errors.Is`; categories are never matched by string.

The boundary's `run` function returns a result envelope and an error, from which
the exit code is derived:

- A non-nil error is a hard, whole-invocation failure (usage, configuration, or
  an unreachable or too-new store) and maps to exit 1. No result is written to
  stdout; a single structured error object is written to stderr.
- A nil error means the envelope is written to stdout, and the exit code is
  derived from the envelope's per-feed outcome summary: 0 when all targeted
  feeds succeeded, 2 when all failed, and 3 when some succeeded and some failed.

Per-feed failures during a poll are recorded into the result and persisted as
feed failure state (count, last error, time), and the same failures are emitted
to stderr as structured `*FeedError` objects. The stdout envelope also reports
the outcome on the result stream: a `succeeded` count, a `failed` count, and a
`failures` list whose entries carry the feed URL, the error category, and, where
applicable, the HTTP status (the `status` field is omitted for network, parse,
and timeout failures that have no status). The attempted-feed count `polled`
satisfies the invariant `polled == succeeded + failed`, and `failures` is always
present as a list, empty when no feed failed. The exit code still reports
whether anything failed; the envelope reports which feeds and their category;
stderr adds the human-readable message and full diagnostic detail.

Logging uses the standard library `log/slog`. The default handler writes JSON
log lines to stderr; under `--format text` a text handler writes friendly lines
instead. The active level comes from `--log-level`, and `--quiet` raises the
floor to errors only. Logs are never written to stdout, so a result stream piped
into `jq` is never polluted by diagnostics.

Ordinary failures never panic. `main` installs a top-level `recover` that
converts an unexpected panic into a structured error object with category
`internal` on stderr and exits 1, so a crash never breaks the stdout JSON
contract.

## CLI Framework

feedwatch is built on `github.com/urfave/cli/v3` rather than a hand-rolled
parser. The program is a single root `*cli.Command`, and each verb is a
subcommand in its `Commands`.

Global flags (`--db`, `--format`, `--log-level`, `--quiet`, `--no-color`,
`--user-agent`, `--concurrency`, the connect and overall timeouts, `--proxy`,
`--ca-bundle`, `--min-tls`, and `--allow-private`) are defined on the root and
inherited by every subcommand; command-specific flags are local. Configuration
precedence (flags, then environment, then defaults) is the framework's native
model: a flag's `Value` is the default, `Sources` of `cli.EnvVars("FEEDWATCH_*")`
supply the environment layer, and the command line overrides both. `NO_COLOR`
(presence disables, whatever its value), the proxy variables, and `TERM` are
handled outside the framework rather than as flag sources.

The root `Before` hook configures the `slog` logger, the output format, and
color from the global flags and passes a derived `context.Context` to actions.

`cmd.Run` returns an error and never calls `os.Exit`, so `main` owns the exit
code (see Error Handling and Logging). Each action writes the stdout envelope
and then returns either nil (exit 0) or a value implementing `cli.ExitCoder` for
exit 1, 2, or 3; `main` passes the result to `cli.HandleExitCoder`. A custom
`ExitErrHandler` emits the structured JSON error object to stderr in place of
the framework's default text. Usage errors and unknown commands are intercepted
through `OnUsageError` and `CommandNotFound` to emit the same JSON error shape
with exit 1.

The `schema` command is generated by introspecting the live command tree (its
subcommands, flags, types, and defaults), augmented by a per-command registry
for the exit codes and output JSON Schema that the framework does not track.
This keeps `schema` from drifting out of sync with the real flags.

`--help` and `-h` render conventional human help to stdout and are unaffected by
`--format`; `schema` is the machine-readable counterpart. `--version` and `-v`
print version information as JSON through an overridden version printer; there
is no separate `version` subcommand. Shell completion is enabled, exposing a
hidden `completion <shell>` subcommand for bash, zsh, fish, and powershell.

## Commands

feedwatch uses a flat set of verb subcommands (no nesting), kept shallow and
predictable.

| Command            | Purpose                                                                                                                                                 |
| ------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `add <url>`        | Subscribe to an explicit feed URL. Validates the URL parses as a feed; rejects HTML pages and points to `discover`. Accepts `--alias` and `--interval`. |
| `rm <url\|alias>`  | Unsubscribe.                                                                                                                                            |
| `list`             | List subscriptions with status, alias, failure count, last error.                                                                                       |
| `poll [feed...]`   | Poll due feeds (or the named feeds), report new items, update state.                                                                                    |
| `items`            | Query stored item history with filters, sorting, and pagination.                                                                                        |
| `prune`            | Delete stored item history by age or per-feed count, preserving dedup state.                                                                            |
| `discover <url>`   | Read-only: list candidate feeds autodiscovered or probed from a URL.                                                                                    |
| `enable <feed>`    | Re-enable an auto-disabled feed.                                                                                                                        |
| `disable <feed>`   | Manually disable a feed (skipped by poll).                                                                                                              |
| `import <file\|->` | Import subscriptions from an OPML outline; validates each feed by default (`--no-validate` to skip).                                                     |
| `export [-o file]` | Export subscriptions as OPML 2.0.                                                                                                                       |
| `migrate`          | Apply or inspect schema migrations (`--status`).                                                                                                        |
| `schema [command]` | Emit the machine-readable interface contract.                                                                                                           |

## Feed Identity

A feed URL is the canonical, stable identity and the natural deduplication key.
`add` optionally takes `--alias <name>`; any command that accepts a feed
resolves either the exact URL or a unique alias. There are no opaque generated
IDs to track.

```sh
feedwatch add https://blog.go.dev/feed.atom --alias godev
feedwatch poll godev
feedwatch items --feed godev --since 7d
feedwatch rm https://blog.go.dev/feed.atom   # URL also works
```

## Feed Discovery

`add` accepts explicit feed URLs only, so it never guesses over the network. To
turn a site's homepage into a feed URL, the agent calls `discover` first, a
pure, read-only lister of candidate feeds. `discover` works in two stages:
`<link rel="alternate">` autodiscovery, then a bounded probe of common feed
paths (`/feed`, `/rss`, `/feed.xml`, `/atom.xml`, `/index.xml`). Every candidate
is validated by actually parsing it, with a content-type short-circuit that
rejects generic XML so a sitemap is not mistaken for a feed. Each candidate is
returned with its title, URL, type, and a `source` of `autodiscovery` or `probe`
so the agent can tell declared feeds from guessed ones. Probing uses the same
per-host politeness and timeouts as a normal fetch. The agent then passes the
chosen URL to `add`.

```sh
feedwatch discover https://example.com
# {"candidates":[{"title":"Blog","url":".../feed.xml",
#   "type":"application/atom+xml","source":"autodiscovery"}]}
feedwatch add https://example.com/feed.xml
```

## Poll Semantics

- Due-only: `poll` fetches only feeds whose interval has elapsed, respecting a
  feed's declared `<ttl>`. `--force` or `--all` overrides scheduling.
- Auto-consume: an item is identified by `(feed_url, dedup_key)`, where
  `dedup_key` is the RSS `guid` or Atom `<id>` when present, falling back to the
  item link, then the title. The store enforces this with a `UNIQUE` constraint
  and an upsert, so dedup is atomic rather than a racy select-then-insert. Items
  whose key was never recorded before are returned as new and marked seen as
  part of a successful poll. A second immediate poll returns an empty set.
  Because items are persisted, the `items` command can always re-query them;
  this is the re-query and crash-safety net.
- Conditional GET is mandatory: feedwatch sends `If-None-Match` and
  `If-Modified-Since`, and skips parsing entirely on `304 Not Modified`. Only a
  validator that actually changed is written back, an empty value never
  overwrites a stored one, and the write is skipped when there is nothing to
  update.
- Rename visibility: when a poll follows a permanent redirect (HTTP `301` or
  `308`) and renames a feed to its canonical URL, it reports the change in the
  envelope's `renamed` list as a `{from, to}` pair and emits a corresponding
  informational log line on stderr, so the agent learns the new identity at
  rename time instead of discovering it through a later query that silently
  returns nothing. `renamed` is always present as a list, empty when no feed was
  renamed.

```sh
feedwatch poll          # only due feeds, marks new items seen
# {"polled":2,"succeeded":2,"failed":0,"skipped":1,"new_items":7,
#  "items":[...],"failures":[],"renamed":[]}
feedwatch poll          # immediately again
# {"polled":0,"succeeded":0,"failed":0,"skipped":3,"new_items":0,
#  "items":[],"failures":[],"renamed":[]}
```

## Fetching and HTTP

Every feed fetch is governed by two deadlines: a short connect timeout for the
dial and a longer overall deadline covering connect, TLS, headers, and body
read. A dead host fails fast without starving a healthy but slow feed of the
overall budget. Both are configurable.

Because feedwatch fetches arbitrary, agent-supplied URLs, it guards against
server-side request forgery. A URL the caller supplies directly may resolve to a
private, loopback, or link-local address, since watching a self-hosted reader on
the LAN is legitimate, but a public URL is never allowed to redirect into
private address space; the resolved address is re-checked after every redirect
hop. `--allow-private` lifts the redirect restriction. Permanent redirects (301,
308) update the stored feed URL, cascading the rename to that feed's stored
items, and that rewrite is subject to the same check; each such rename is
surfaced on the poll result stream and on stderr (see Poll Semantics).

An outbound proxy (`--proxy`, or the standard proxy environment variables), a
custom CA bundle (`--ca-bundle`), and a minimum TLS version (`--min-tls`) are
all configurable for constrained network environments.

## Concurrency and Politeness

When `poll` targets multiple due feeds, it fetches them concurrently with a
worker pool capped by `--concurrency` (environment-configurable, default around
8). Feeds are grouped by host so that same-host requests land on one worker,
reuse a connection, and are serialized with a small per-host delay to stay
polite. Each worker uses its own store connection, and results are written into
position-indexed slots so the output stays in a stable order regardless of
completion order.

Concurrency is bounded with an `errgroup` whose limit is `--concurrency`. A
feed's failure is recorded as its own per-feed outcome and never cancels the
others; the group's context is cancelled only by an interrupt signal. The
`Store` is safe for concurrent use across distinct feeds, which never share rows
(per-worker connection plus WAL).

feedwatch installs a signal-aware context for SIGINT and SIGTERM. On
interruption a poll stops scheduling new feeds and lets in-flight fetches abort;
feeds that already completed are persisted (each feed's write is its own
transaction), the envelope for the completed work is still emitted, and the
process exits 130 for SIGINT or 143 for SIGTERM.

## Item Model and Content

Items carry a single internal state: `seen`, which drives poll deduplication and
is never touched by the agent. There is no read/unread layer; an agent that
needs a processing watermark tracks it itself (for example, by the timestamp of
its last successful processing) and re-queries with `items`.

Each item is normalized into a stable schema. Both the original content and a
derived plaintext rendition are kept: agents usually reason over clean text,
while the raw HTML stays available for link extraction or rendering. The item's
base URL (from `xml:base`, falling back to the item link or the feed URL) and
the source content's MIME type are stored so relative links can be resolved and
plaintext-versus-markup decided after the fact.

All dates are normalized on write to fixed-width RFC3339 UTC regardless of the
source format (RFC 822 for RSS, RFC 3339 for Atom). Writing one uniform zone and
width is what makes the lexicographic string comparison behind `--since`,
`--until`, and `--order` correct.

Each item carries two distinct times. Its publication time `published_at` is the
date the feed declares. A feed legitimately omitting a publication date is valid,
not an error: when an item carries no parseable publication date its
`published_at` is stored as null and never fabricated. Its fetch time
`fetched_at` is the moment feedwatch first recorded the item; feedwatch always
sets it, so it is never null. These are independent: `published_at` answers "when
did the publisher say this was published," `fetched_at` answers "when did this
first arrive here," and the latter is the reliable freshness signal precisely
because it cannot be null or mis-formatted by a publisher.

feedwatch does not substitute the fetch time for a null publication time. On the
publication axis a null `published_at` is excluded from `--since`/`--until` date
filters and ordered last under descending order, first under ascending; on the
fetch axis every item is matched and ordered by its always-present `fetched_at`.
See Querying History for how the axis is selected.

Normalized fields follow an explicit precedence: `content_html` is taken from
`content:encoded` or Atom `content`, falling back to the description when that
is empty; `summary` is the feed's description or iTunes summary; `author`
cascades from the item author to the feed `managingEditor` to Dublin Core
`dc:creator`.

```json
{
  "id": "...",
  "feed_url": "...",
  "title": "...",
  "link": "...",
  "summary": "short description",
  "content_html": "<p>full <a href=\"...\">...</a></p>",
  "content_text": "full readable text",
  "content_mime_type": "text/html",
  "base_url": "https://blog.example/",
  "author": "...",
  "categories": ["go"],
  "enclosures": [{ "url": "...", "type": "audio/mpeg", "length": 5768960 }],
  "published_at": "2026-06-27T10:00:00Z",
  "updated_at": "2026-06-27T10:00:00Z",
  "fetched_at": "2026-06-27T10:05:00Z"
}
```

`content_html` is stored verbatim after CDATA and entity resolution. HTML
sanitization is treated as a render-time concern, not a storage concern.

### Querying History

`items` re-queries the stored history with a practical, agent-friendly filter
set:

- `--feed <url|alias>` (repeatable)
- `--since` and `--until` (RFC3339, or relative such as `24h` or `7d`)
- `--time-field published|fetched` (which time the `--since`/`--until` window
  filters on, default `published`)
- `--limit` and `--offset`
- `--order published|fetched asc|desc`
- `--contains <text>` (substring match over title and content)
- `--fields <list>` (project to a subset of item fields)

The filter axis and the order axis are independent: `--time-field` chooses which
time the date window matches, while `--order` chooses which time the results are
sorted by, so an agent can, for example, window on fetch time but sort by
publication time. On the publication axis a `--since`/`--until` window excludes
items whose `published_at` is null; the count of items dropped for that reason
is reported in the result envelope as `omitted_no_date` and noted in an
informational log line on stderr, so a dateless item never silently masquerades
as recently published. The fetch axis is unaffected, since `fetched_at` is never
null. This honest exclusion is reflected in the machine-readable `schema` and
the usage documentation so the documented contract matches the behavior.

By default `items` returns the full normalized item, including `fetched_at`
alongside `published_at`; `--fields` narrows the projection to keep large triage
queries cheap (for example, titles and links without the stored bodies). Naming
the always-present identity field `feed_url` in a projection is accepted as a
no-op rather than an error. An unrecognized field name is a usage error that
returns no partial results; when the name closely resembles a valid field, the
error includes a did-you-mean suggestion.

```sh
feedwatch items --feed godev --since 7d --limit 50
feedwatch items --contains "release" --order published desc
feedwatch items --since 7d --time-field fetched --order fetched desc
feedwatch items --feed godev --fields title,link,published_at,fetched_at
```

## Parsing and Robustness

Real-world feeds are frequently malformed. feedwatch parses with the
`github.com/mmcdole/gofeed` library, wrapped behind an internal `Parser`
interface so a custom or stricter parser could be substituted later. gofeed
handles RSS 0.9x, 1.0, and 2.0, Atom 1.0, and JSON Feed, is lenient on broken
XML, and normalizes into a unified model that feedwatch maps to its own schema.

Before parsing, feedwatch decodes the response body to UTF-8 itself, resolving
the charset by a fixed precedence: a byte-order mark, then the XML declaration,
then the HTTP `Content-Type` charset, then a lossy UTF-8 fallback. Encoding
handling is explicit rather than left implicit in the parser.

### Failure Handling

A transient network error (connection reset, timeout, temporary DNS failure, a
5xx response, or a 429 honoring `Retry-After`) is retried a small bounded number
of times within the same invocation before it counts as a failure; deterministic
errors (4xx other than 429, parse failures, blocked URLs) are never retried.
Only once the in-call retries are exhausted does the persisted lifecycle engage.

For each feed, feedwatch then stores a failure count, the last error, and when
it occurred. Failing feeds are subject to exponential backoff and are not
retried until the backoff elapses, even when their interval is otherwise due.
After a configurable number of consecutive failures (default around 10), a feed
is marked disabled and skipped by poll; it is surfaced in `list` and resumed
with `enable`. All failures are reported on stderr with their category and
count.

```sh
feedwatch list
# {"feeds":[{"url":"...","status":"active","failures":0},
#           {"url":"...","status":"disabled","failures":12,
#            "last_error":"dns: no such host"}]}
feedwatch enable https://flaky.example/feed.xml
```

## Retention

feedwatch persists every item indefinitely by default, which is the re-query and
crash-safety net. For long-running deployments that watch many feeds, `prune`
trims stored history explicitly: by age (`--keep-days`) or by per-feed count
(`--max-items`). Pruning deletes item rows but preserves each item's dedup
fingerprint, so a pruned item that a feed still advertises is never re-emitted
as new. There is no automatic pruning; the agent or a timer decides cadence,
consistent with the externally-driven execution model.

```sh
feedwatch prune --keep-days 90
feedwatch prune --max-items 500
```

## OPML Interoperability

feedwatch imports and exports OPML so subscription lists move cleanly in and out
of other readers.

- `import` reads an OPML outline from a file or stdin and walks it recursively,
  so feeds nested in folders at any depth are found. It adds each feed's
  `xmlUrl` (falling back to `url` when an exporter omits the standard
  attribute), uses `text` or `title` as the alias when one is free, skips and
  reports duplicates, and never fails the whole import because of one bad entry.
  It reports per-entry results as JSON.
- By default `import` validates each entry the way `add` does, fetching and
  parsing the feed before subscribing, so a reported `added` count means the
  feeds are actually usable rather than merely recorded. Validation runs
  concurrently under the configured concurrency limit and applies the same
  transient-failure retry policy as other fetches. A feed that fails validation
  is not subscribed and is recorded among the per-entry `failed` results with a
  reason; one failing entry never aborts the import. `--no-validate` restores
  the fast bulk-add behavior, subscribing every syntactically valid feed without
  fetching it, in which case a successful import does not imply reachability.
- `export` writes the current subscriptions and their aliases as valid OPML 2.0,
  to a file or stdout. If per-feed tags are ever added, they round-trip through
  the OPML `category` attribute.

```sh
feedwatch import subs.opml
# {"added":42,"skipped":3,"failed":[{"xmlUrl":"...","reason":"404 Not Found"}]}
feedwatch import --no-validate subs.opml   # fast bulk-add, no reachability check
feedwatch export -o backup.opml
feedwatch export | curl ...
```

## Scheduling

feedwatch never loops on its own. Cadence is driven externally by the agent,
cron, or a systemd timer.

```sh
# crontab: poll every 30 minutes, append new items, log errors
*/30 * * * * feedwatch poll >> ~/feed-items.jsonl 2>> ~/feedwatch.log
```

## Design Decisions at a Glance

| Decision             | Choice                                                        |
| -------------------- | ------------------------------------------------------------- |
| Execution model      | Stateful one-shot, no daemon                                  |
| Storage              | SQLite default behind a `Store` interface; optional Postgres  |
| Schema lifecycle     | Embedded migrations, auto-applied, refuse newer version       |
| Configuration        | Flags and environment, XDG defaults, no config file           |
| stdout               | Pure result JSON by default, `--format text` opt-in           |
| stderr               | Structured JSON logs and error objects                        |
| Color                | Text format only; gated by `--no-color`, `NO_COLOR`, and TTY  |
| Exit codes           | 0 success, 1 usage, 2 all failed, 3 partial                   |
| Error model          | Typed `*FeedError` + sentinels, `%w`-wrapped, classified once |
| Logging              | `log/slog` to stderr (JSON; text under `--format text`)       |
| Discoverability      | `schema` command plus conventional `--help`                   |
| Command structure    | Flat verbs                                                    |
| CLI framework        | urfave/cli v3; native flag/env precedence, ExitCoder mapping  |
| Feed identity        | URL canonical, optional alias                                 |
| Discovery            | Explicit `add`; read-only `discover` (autodiscover + probe)   |
| Poll                 | Due-only, auto-consume, conditional GET; reports failures and renames in envelope |
| Deduplication        | `(feed_url, guid)` with link then title fallback, upsert      |
| HTTP fetch           | Connect and overall timeouts, SSRF guard, proxy/CA/TLS knobs  |
| Concurrency          | Bounded parallel (`errgroup`), per-host politeness            |
| Interrupts           | SIGINT/SIGTERM cancel, persist partial, exit 130/143          |
| Package layout       | Small acyclic packages under `src/`; interface + clock seams  |
| Item state           | Single internal `seen` layer                                  |
| Content              | Raw HTML plus plaintext and base URL, dates RFC3339 UTC; null `published_at` honest, `fetched_at` never null |
| Parser               | gofeed behind a `Parser` interface                            |
| Failure handling     | In-call retry, then track, back off, auto-disable, `enable`   |
| Retention            | Optional `prune` by age or count, off by default              |
| OPML                 | Import (validated by default) and export, per-entry reporting |
| Content intelligence | None; pure deterministic plumbing                             |
