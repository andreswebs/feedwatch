# feedwatch usage reference

This document covers every command, the global flags, exit codes, environment
variables, and the scheduling recipes. For the design rationale behind these
choices, see [cli-design.md](cli-design.md).

`feedwatch <command> --help` prints the same information for a single command,
and `feedwatch schema` emits it as machine-readable JSON.

## Synopsis

```text
feedwatch [global options] <command> [command options] [arguments]
```

feedwatch exposes a flat set of verb subcommands with no nesting:

| Command            | Purpose                                                                  |
| ------------------ | ------------------------------------------------------------------------ |
| `add <url>`        | Subscribe to an explicit feed URL after validating it parses as a feed.  |
| `rm <url\|alias>`  | Unsubscribe a feed and remove its stored items.                          |
| `list`             | List subscriptions with status, alias, failure count, and last error.   |
| `poll [feed...]`   | Poll due feeds (or the named feeds), report new items, update state.     |
| `items`            | Query stored item history with filters, ordering, and pagination.        |
| `prune`            | Trim stored item history by age and/or per-feed count, preserving dedup. |
| `discover <url>`   | Read-only: list candidate feeds autodiscovered or probed from a URL.     |
| `enable <feed>`    | Re-enable a disabled feed and reset its failure lifecycle.               |
| `disable <feed>`   | Disable a feed so `poll` skips it until re-enabled.                       |
| `import <file\|->` | Add subscriptions from an OPML outline read from a file or stdin.        |
| `export`           | Export subscriptions as OPML 2.0 to a file or stdout.                    |
| `migrate`          | Apply or inspect schema migrations (`--status`).                         |
| `schema [command]` | Emit the machine-readable interface contract.                            |

## Output contract

- stdout carries pure result JSON by default, in a consistent envelope per
  command. `--format text` switches to terminal-friendly tables; `--format
  json` is the default and may be stated explicitly.
- stderr carries structured JSON log lines and structured error objects. This
  keeps stdout clean, so piping it into `jq` never trips over a diagnostic.
- A hard, whole-invocation failure (bad arguments, unreachable store) writes a
  single JSON error object to stderr and nothing to stdout. An exception is a
  `poll` that fails partway through persisting fetched feeds: the envelope for
  the feeds already persisted is still written to stdout (see `poll` below),
  since that work is durable and would otherwise never be reported.
- Per-feed failures during a poll are reported on both streams. The stdout
  envelope carries `succeeded` and `failed` counts and a `failures` list whose
  entries hold the feed URL, an error `category` (`network`, `http`, `parse`,
  `timeout`), a `message` with the underlying error detail (always present), and
  an HTTP `status` (present only for `http` failures, omitted otherwise), so a
  partial failure is fully triageable from stdout alone without parsing stderr.
  `timeout` is its own `category`, so an agent need not inspect `message` to
  distinguish a timeout from other network errors. stderr adds structured
  per-feed objects with the same detail.

```sh
feedwatch poll 2>err.json; echo "exit=$?"
# stdout: {"polled":3,"succeeded":2,"failed":1,"skipped":0,"fetched":10,"new_items":2,"deduped":8,
#          "items":[...],
#          "failures":[{"feed_url":"...","category":"http","status":404,"message":"server returned HTTP 404"},
#                      {"feed_url":"...","category":"network","message":"dial tcp: connection refused"}],
#          "renamed":[]}
# err.json: {"errors":[{"feed_url":"...","category":"http","status":404,
#                       "message":"server returned HTTP 404"},
#                      {"feed_url":"...","category":"network",
#                       "message":"dial tcp: connection refused"}]}
# exit=3
```

Color appears only under `--format text`, only on a stream that is a terminal,
and never as the sole carrier of meaning. It is disabled by `--no-color`, by the
`NO_COLOR` environment variable, or when `TERM=dumb`.

## Exit codes

Distinct exit codes let an agent branch on the outcome without parsing output.

| Code | Meaning                                                |
| ---- | ------------------------------------------------------ |
| 0    | Full success.                                          |
| 1    | Usage or configuration error.                          |
| 2    | All targeted feeds failed.                             |
| 3    | Partial success (some feeds failed, others succeeded). |
| 130  | Interrupted by `SIGINT`.                               |
| 143  | Terminated by `SIGTERM`.                               |

Codes 2 and 3 are produced only by commands that target feeds (notably `poll`).
Commands without a per-feed outcome use 0 for success and 1 for a usage or
configuration error.

Exit 1 from `poll` can carry a partial envelope on stdout: when a store write
fails partway through persisting fetched feeds, the feeds already persisted
before the failure are still reported (`new_items`/`items` cover exactly that
subset), and the process still exits 1. An early hard failure (unreachable
store, unknown named feed) leaves stdout empty, as before. A consumer of
`poll` should process stdout even on exit 1, since it is not necessarily
empty.

## Global flags

Global flags are defined on the root command and inherited by every subcommand.
Configuration precedence is flags, then environment variables, then compiled-in
defaults.

| Flag                | Argument   | Env                     | Default      | Purpose                                                  |
| ------------------- | ---------- | ----------------------- | ------------ | -------------------------------------------------------- |
| `--db`              | `PATH`     | `FEEDWATCH_DB`          | XDG path     | Store location: a filesystem path or a `postgres://` DSN. |
| `--format`          | `FORMAT`   | `FEEDWATCH_FORMAT`      | `json`       | Output format: `json` or `text`.                         |
| `--log-level`       | `LEVEL`    |                         | `info`       | Log level: `error`, `warn`, `info`, or `debug`.          |
| `--quiet`           |            |                         | `false`      | Raise the log floor to errors only.                      |
| `--no-color`        |            |                         | `false`      | Disable color in text output.                            |
| `--user-agent`      | `string`   | `FEEDWATCH_USER_AGENT`  | `feedwatch`  | HTTP `User-Agent` header.                                |
| `--concurrency`     | `int`      | `FEEDWATCH_CONCURRENCY` | `8`          | Worker pool size for concurrent polling.                 |
| `--connect-timeout` | `duration` |                         | `5s`         | Dial deadline per feed.                                  |
| `--timeout`         | `duration` |                         | `30s`        | Overall deadline per feed.                               |
| `--proxy`           | `URL`      |                         |              | Outbound HTTP proxy URL.                                 |
| `--ca-bundle`       | `FILE`     |                         |              | Path to a custom CA bundle.                              |
| `--min-tls`         | `VERSION`  |                         | `1.2`        | Minimum TLS version: `1.2` or `1.3`.                     |
| `--allow-private`   |            |                         | `false`      | Allow redirects into private address space.              |
| `--help`, `-h`      |            |                         |              | Show help (unaffected by `--format`).                    |
| `--version`, `-v`   |            |                         |              | Print version information as JSON.                       |

The default store location is `$XDG_STATE_HOME/feedwatch/feedwatch.db`, falling
back to `~/.local/state/feedwatch/feedwatch.db` when `XDG_STATE_HOME` is unset.

## Environment variables

| Variable                                  | Effect                                                                   |
| ----------------------------------------- | ------------------------------------------------------------------------ |
| `FEEDWATCH_DB`                            | Default store location (overridden by `--db`).                           |
| `FEEDWATCH_FORMAT`                        | Default output format (overridden by `--format`).                        |
| `FEEDWATCH_USER_AGENT`                    | Default HTTP `User-Agent` (overridden by `--user-agent`).                |
| `FEEDWATCH_CONCURRENCY`                   | Default worker pool size (overridden by `--concurrency`).                |
| `XDG_STATE_HOME`                          | Base directory for the default store path.                               |
| `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY`   | Standard outbound proxy configuration, honored when `--proxy` is unset.  |
| `NO_COLOR`                                | When present (any value), disables color in text output.                 |
| `TERM`                                    | A value of `dumb` disables color in text output.                         |

## Commands

### `add <url>`

Subscribe to an explicit feed URL. The URL is validated by actually parsing it
as a feed; HTML pages are rejected with a pointer to `discover`.

Options:

- `--alias <name>` - a short, unique name to reference the feed.
- `--interval <duration>` - minimum poll interval; `0` uses the configured
  default.

```sh
feedwatch add https://blog.go.dev/feed.atom --alias godev --interval 30m
# {"url":"https://blog.go.dev/feed.atom","alias":"godev","interval":"30m0s","created":true}
```

### `rm <url|alias>`

Unsubscribe a feed by URL or unique alias, removing its stored items.

```sh
feedwatch rm godev
# {"removed":"https://blog.go.dev/feed.atom"}
```

### `list`

List subscriptions with their health.

```sh
feedwatch list
# {"feeds":[{"url":"...","alias":"godev","interval":"30m0s","status":"active","failures":0},
#           {"url":"...","status":"disabled","failures":12,"last_error":"dns: no such host"}]}
```

### `poll [feed...]`

Poll feeds and report new items. With no arguments, only feeds whose interval
has elapsed are polled (respecting a feed's declared `<ttl>`); named feeds (by
URL or alias) restrict the run to those feeds.

An item is new when its `(feed_url, dedup_key)` identity has not been recorded
before; new items are returned and marked seen as part of a successful poll. A
second immediate poll returns an empty set. Conditional GET (`If-None-Match` and
`If-Modified-Since`) is always sent, and parsing is skipped on `304 Not
Modified`.

The envelope reports the per-feed outcome: `polled` feeds attempted (with the
invariant `polled == succeeded + failed`), `skipped` feeds left unpolled because
they were not due, `fetched` items parsed across all successful 200 responses
(304 Not Modified responses contribute 0), `new_items` items stored for the
first time, `deduped` items already known (`deduped = fetched - new_items`), the
`items` themselves, and a `failures` list (always present, empty when no feed
failed) with one `{feed_url, category, message, status?}` entry per failed feed.
`message` carries the underlying error detail and is always present. `status` is
only present for `http` failures. `timeout` is a distinct `category`; no
`message` parsing is needed to distinguish a timeout from other network errors.

Options:

- `--force`, `--all` - poll every active feed, ignoring the schedule.

A hard failure while persisting a fetched feed (a store write error) aborts the
run and exits 1, but the envelope for feeds already persisted before the
failure is still written to stdout, since that work is durable; a retry
reports the remaining feeds as new. An early hard failure (before any feed is
fetched, such as an unreachable store or an unknown named feed) leaves stdout
empty.

```sh
feedwatch poll          # only due feeds
# {"polled":2,"succeeded":2,"failed":0,"skipped":1,"fetched":4,"new_items":4,"deduped":0,"items":[...],"failures":[]}
feedwatch poll          # immediately again
# {"polled":0,"succeeded":0,"failed":0,"skipped":3,"fetched":0,"new_items":0,"deduped":0,"items":[],"failures":[]}
```

### `check [feed...]`

Validate feed reachability and parseability without storing items or updating
any state. With no arguments every active feed is checked; named feeds (by URL
or alias) restrict the run to those feeds. Disabled feeds can be checked when
named explicitly.

Each feed is fetched with an unconditional GET (no `If-None-Match` or
`If-Modified-Since` validators; a 304 would prove nothing about parseability)
and the response body is parsed with the shared parser. No items are stored, no
ETags or schedule timestamps are written, and the failure-lifecycle counters are
not updated. The command is read-only from the store's perspective.

The envelope reports `checked` feeds attempted, `ok` feeds that fetched and
parsed cleanly, `failed` feeds that did not, and a `failures` list (always
present, empty when no feed failed) with one `{feed_url, category, message,
status?}` entry per failed feed -- the same shape as the poll failures list.

Exit codes mirror `poll`:

- 0: all checked feeds passed (or nothing to check)
- 1: usage or configuration error (unknown ref, unreachable store)
- 2: all checked feeds failed
- 3: partial -- some feeds passed and some failed

```sh
feedwatch check
# {"checked":3,"ok":3,"failed":0,"failures":[]}
feedwatch check https://dead.example/feed.xml
# {"checked":1,"ok":0,"failed":1,"failures":[{"feed_url":"...","category":"network","message":"..."}]}
```

Typical use as a cron health check after `import --no-validate`:

```sh
*/60 * * * * feedwatch check >> ~/check-results.jsonl 2>> ~/feedwatch.log
```

### `items`

Re-query stored item history. By default the full normalized item is returned;
`--fields` narrows the projection to keep large triage queries cheap.

Options:

- `--feed <url|alias>` - feed to query (repeatable); all feeds when omitted.
- `--since <when>`, `--until <when>` - time bounds, RFC3339 or relative such as
  `24h` or `7d`.
- `--time-field <published|fetched>` - which time the `--since`/`--until` window
  filters on (default `published`). `published` filters on the publication time,
  excluding items whose `published_at` is null (the fetch time is never
  substituted); `fetched` filters on the always-present fetch time, which is the
  reliable axis for "what newly arrived" when a feed omits or mis-formats
  publication dates. Independent of `--order`: you can window on one axis and
  sort by the other.
- `--limit <int>` - maximum items to return; `0` returns all.
- `--offset <int>` - items to skip before returning results.
- `--order <spec>` - sort: `published|fetched` and `asc|desc` (default
  `published desc`).
- `--contains <text>` - substring matched over title and content.
- `--fields <list>` - project to a subset of item fields (repeatable or
  comma-separated). Valid fields: `id`, `title`, `link`, `summary`,
  `content_html`, `content_text`, `content_mime_type`, `base_url`, `author`,
  `categories`, `enclosures`, `published_at`, `updated_at`, `fetched_at`. The
  result carries exactly the requested fields plus the always-on `feed_url`
  identity field. Naming `feed_url` itself is accepted as a no-op, since it is
  emitted regardless. An unknown field name is a usage error (exit 1) that
  returns no partial results; the error message lists all valid field names, and
  when the unknown name closely resembles a valid field, the error also includes
  a did-you-mean suggestion. `published_at` and `updated_at` are nullable
  (`null` when unparseable); `fetched_at` is always present.

```sh
feedwatch items --feed godev --since 7d --limit 50
feedwatch items --contains release --order published desc
feedwatch items --since 7d --time-field fetched --order fetched desc
feedwatch items --feed godev --fields title,link,published_at,fetched_at
```

Each normalized item has this shape (optional fields are omitted when empty):

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

All dates are normalized to RFC3339 UTC. The publication time `published_at` is
what the feed declares; a date that cannot be parsed is stored as null and is
never fabricated. On the publication axis a null `published_at` is excluded from
`--since`/`--until` windows (it is not coalesced to the fetch time), and it is
ordered last under `desc` and first under `asc`. When such a window drops one or
more dateless items, the result envelope reports the count as `omitted_no_date`
(absent when zero) and an informational line is logged to stderr. The fetch time
`fetched_at` is the moment feedwatch first recorded the item; it is always
present (never null) and is the reliable freshness signal selected by
`--time-field fetched`, which this exclusion never affects.

### `prune`

Trim stored item history. Pruning deletes item rows but preserves each item's
dedup fingerprint, so a pruned item that a feed still advertises is never
re-emitted as new. There is no automatic pruning.

Options:

- `--keep-days <int>` - tombstone items older than this many days.
- `--max-items <int>` - keep at most this many items per feed, tombstoning the
  rest.

```sh
feedwatch prune --keep-days 90
feedwatch prune --max-items 500
```

### `discover <url>`

Read-only lister of candidate feeds. It performs `<link rel="alternate">`
autodiscovery, then a bounded probe of common feed paths, validating every
candidate by actually parsing it. Each candidate is tagged with a `source` of
`autodiscovery` or `probe`.

```sh
feedwatch discover https://example.com
# {"candidates":[{"title":"Blog","url":".../feed.xml",
#   "type":"application/atom+xml","source":"autodiscovery"}]}
```

### `enable <url|alias>` and `disable <url|alias>`

`disable` marks a feed so `poll` skips it; `enable` re-enables a disabled feed
and resets its failure lifecycle so it is due again. A feed that hits the
consecutive-failure threshold (default 10) is auto-disabled and surfaced in
`list`; `enable` resumes it.

```sh
feedwatch disable https://flaky.example/feed.xml
feedwatch enable https://flaky.example/feed.xml
```

### `import <file|->` and `export`

`import` reads an OPML outline from a file or stdin (`-`), walks it recursively,
adds each feed, uses `text` or `title` as the alias when one is free, and reports
per-entry results without failing the whole import on one bad entry. By default
it validates each feed the way `add` does, fetching and parsing it before
subscribing, so a reported `added` count means those feeds actually resolve and
parse; validation runs concurrently under `--concurrency` with the same
transient-retry policy as other fetches, and a feed that fails to fetch or parse
is recorded in `failed` rather than subscribed. `export` writes the current
subscriptions and aliases as valid OPML 2.0.

Options:

- `--no-validate` - subscribe every syntactically valid feed without fetching
  it (fast bulk-add). A successful import then does not imply the feeds are
  reachable.
- `export -o <file>` - write OPML to this file instead of stdout.

```sh
feedwatch import subs.opml
# {"added":40,"skipped":3,"failed":[{"xmlUrl":"https://dead/feed","reason":"could not fetch ..."}]}
feedwatch import --no-validate subs.opml   # fast bulk-add, no reachability check
feedwatch export -o backup.opml
feedwatch export | curl ...
```

### `migrate`

Versioned migrations are embedded in the binary and applied idempotently inside
a transaction. A fresh machine needs no manual setup; any command ensures the
schema on startup. `migrate` makes that step explicit.

Options:

- `--status` - report schema version, pending count, and backend without
  applying.

```sh
feedwatch migrate
# {"applied":1,"schema_version":1}
feedwatch migrate --status
# {"schema_version":1,"pending":0,"backend":"sqlite"}
```

### `schema [command]`

Emit the machine-readable interface contract: for each command, its arguments
and flags (name, type, default), exit codes, and a JSON Schema for its output
envelope. `feedwatch schema <command>` narrows to one command.

```sh
feedwatch schema poll
# {"command":"poll","args":[],"flags":[{"name":"--force","aliases":["--all"],"type":"bool"}],
#  "exit_codes":{"0":"all targeted feeds succeeded", ...},
#  "output_schema":{ ... JSON Schema ... }}
```

## Scheduling

feedwatch never loops on its own. Cadence is driven externally by an agent,
cron, or a systemd timer that invokes `poll` repeatedly. Because each run is a
one-shot whose stdout is clean JSON and whose diagnostics go to stderr, output
appends cleanly to a JSONL log while errors collect separately.

### cron

```sh
# crontab: poll every 30 minutes, append new items, log errors.
*/30 * * * * feedwatch poll >> "${HOME}/feed-items.jsonl" 2>> "${HOME}/feedwatch.log"
```

### systemd timer

A service unit that runs a single poll:

```ini
# ~/.config/systemd/user/feedwatch.service
[Unit]
Description=feedwatch poll

[Service]
Type=oneshot
Environment=FEEDWATCH_DB=%h/.local/state/feedwatch/feedwatch.db
ExecStart=/usr/local/bin/feedwatch poll
StandardOutput=append:%h/feed-items.jsonl
StandardError=append:%h/feedwatch.log
```

A timer that drives it every 30 minutes:

```ini
# ~/.config/systemd/user/feedwatch.timer
[Unit]
Description=Run feedwatch poll every 30 minutes

[Timer]
OnBootSec=5min
OnUnitActiveSec=30min
Persistent=true

[Install]
WantedBy=timers.target
```

Enable it with:

```sh
systemctl --user enable --now feedwatch.timer
```

The agent or operator decides cadence; feedwatch itself stays a simple,
externally-driven sensor.
