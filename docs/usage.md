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
| `rm [<url\|alias>]` | Unsubscribe a feed, or a whole lane with `--tag`, removing stored items. |
| `list`             | List subscriptions with status, alias, tags, failure count, last error. |
| `poll [feed...]`   | Poll due feeds (or the named feeds), report new items, update state.     |
| `check [feed...]`  | Validate reachability and parseability without storing or updating state. |
| `items`            | Query stored item history with filters, ordering, and pagination.        |
| `prune`            | Trim stored item history by age and/or per-feed count, preserving dedup. |
| `discover <url>`   | Read-only: list candidate feeds autodiscovered or probed from a URL.     |
| `enable <feed>`    | Re-enable a disabled feed and reset its failure lifecycle.               |
| `disable <feed>`   | Disable a feed so `poll` skips it until re-enabled.                       |
| `tag <feed>`       | Read or edit the tags on one subscription.                              |
| `tags`             | List every tag in use with the number of feeds carrying it.             |
| `import <file\|->` | Add subscriptions from an OPML outline read from a file or stdin.        |
| `export`           | Export subscriptions as OPML 2.0 to a file or stdout.                    |
| `migrate`          | Apply or inspect schema migrations (`--status`).                         |
| `schema [command]` | Emit the machine-readable interface contract.                            |

### Lanes

A **lane** is a set of feeds sharing a tag. Tags are assigned with `add --tag` or
the `tag` command, and every selecting command (`list`, `poll`, `check`, `items`,
`prune`, `rm`, `export`) narrows to a lane with a repeatable `--tag` plus a
`--match`:

| Flag              | Meaning                                                                     |
| ----------------- | --------------------------------------------------------------------------- |
| `--tag <name>`    | Tag to select (repeatable, or comma-separated); all feeds when omitted.     |
| `--match <mode>`  | `all` (default): the feed carries every named tag. `any`: at least one.     |

`[]string` flags accept both the repeated and the comma-separated spelling, and
the two parse to identical values, so `--tag ai --tag agents` and
`--tag ai,agents` are the same selection. **`--match` is the only carrier of
AND/OR semantics**; the spelling of `--tag` carries none.

Tags are canonicalized on every write: trimmed, lowercased, deduplicated, and
stored sorted, so `--tag AI` and `--tag ai` name the same lane. A tag that is
empty, or that contains a comma or whitespace, is a usage error (exit 64):
commas would collide with the comma-separated flag spelling, and whitespace
makes a tag unquotable in cron scripts.

A feed carrying no tags is matched by no `--tag` selection and is included in
every command run without `--tag`. A `--tag` naming a tag no feed carries is an
empty result and exit 0, not an error.

```sh
feedwatch list --tag ai --tag agents        # feeds carrying both tags
feedwatch list --tag ai,agents              # identical to the line above
feedwatch list --tag ai,agents --match any  # feeds carrying either tag
```

## Output contract

- stdout carries exactly one newline-terminated result envelope per invocation,
  and nothing else: never a log line, a warning, or a human-facing banner. Every
  result envelope opens with the same head: `schema_version` (the output-contract
  version, an integer bumped on breaking shape changes) and `ok` (a boolean),
  followed by the command-specific payload. Collections in the payload never
  serialize as `null`: an absent list is always `[]`. `--format text` switches to
  terminal-friendly tables (which omit the head); `--format json` is the default
  and may be stated explicitly.
- stderr carries three distinguishable kinds of JSON object, and a consumer tells
  them apart by which key is present, without inspecting a value:
  - the **error envelope** (`ok: false` plus an `error` object), emitted at most
    once, for a whole-invocation failure;
  - **NDJSON warning lines** (`level: "warning"`), one JSON object per line, for
    non-fatal advisories;
  - **slog diagnostic records**, the ordinary logs.

  The rule is: an `ok` key present means the error envelope; a `level` key
  present means a warning; neither present means a log record. Keeping results on
  stdout and everything else on stderr means piping stdout into `jq` never trips
  over a diagnostic.
- A hard, whole-invocation failure (bad arguments, unreachable store) writes a
  single JSON error object to stderr and nothing to stdout. An exception is a
  `poll` that fails partway through persisting fetched feeds: the envelope for
  the feeds already persisted is still written to stdout (see `poll` below),
  since that work is durable and would otherwise never be reported.
- Per-feed failures during a poll are result data, carried on stdout only. The
  stdout envelope carries `succeeded` and `failed` counts and a `failures` list
  whose entries hold the feed URL, an error `category` (`network`, `http`,
  `parse`, `timeout`), a `message` with the underlying error detail (always
  present), and an HTTP `status` (present only for `http` failures, omitted
  otherwise), so a partial failure is fully triageable from stdout alone.
  `timeout` is its own `category`, so an agent need not inspect `message` to
  distinguish a timeout from other network errors. stderr carries no per-feed
  batch object; it is reserved for whole-invocation error envelopes and logs.
- A whole-invocation error envelope on stderr is a single JSON object of the
  form `{"schema_version", "ok": false, "error": {"code", "message", "hint"?,
  "details"?}}`. `code` is the stable machine code (for example `usage_error`,
  `config_error`, `store_unavailable`, `schema_too_new`, `internal_error`, and
  the feed-scoped `http_error`, `feed_unreachable`, `parse_error`,
  `timeout_error`); `hint` is a remediation string, present only when the code
  carries one; and `details` carries per-instance structure such as a feed
  failure's `feed_url` and `status`, present only when populated. An
  unclassified error renders `code` as `internal_error`.
- Non-fatal advisories that do not change the exit code are written to stderr as
  NDJSON warning objects, one per line, distinct from both logs and the error
  envelope. A warning is a single JSON object of the form `{"schema_version",
  "level": "warning", "code", "message", "hint"?, "details"?}`; it carries
  `"level": "warning"` instead of an `ok` field, so a consumer can tell it apart
  from the error envelope unambiguously. Warnings are contract output, not logs,
  so `--quiet` does not suppress them. The one advisory raised today is
  `feed_auto_disabled` (see `poll` and `disable`).

```sh
feedwatch poll 2>err.json; echo "exit=$?"
# stdout: {"schema_version":1,"ok":true,"polled":3,"succeeded":2,"failed":1,"skipped":0,"fetched":10,"new_items":2,"deduped":8,
#          "items":[...],
#          "failures":[{"feed_url":"...","category":"http","status":404,"message":"server returned HTTP 404"},
#                      {"feed_url":"...","category":"network","message":"dial tcp: connection refused"}],
#          "renamed":[]}
# err.json: empty (a partial poll is not a whole-invocation failure; the
#           per-feed detail is on stdout in the failures array)
# exit=3
```

Color appears only under `--format text`, only on a stream that is a terminal,
and never as the sole carrier of meaning. It is disabled by `--no-color`, by the
`NO_COLOR` environment variable, or when `TERM=dumb`.

## Exit codes

Distinct exit codes let an agent branch on the outcome without parsing output.
They follow the taxonomy in
[ADR 0001](adr/0001-exit-code-taxonomy.md): whole-invocation failures use the
BSD `sysexits.h` range, while codes 2 and 3 are result sub-codes (the command
completed and the code summarizes the poll outcome), not failures.

| Code | Meaning                                                          |
| ---- | ---------------------------------------------------------------- |
| 0    | Full success.                                                    |
| 2    | Result: all targeted feeds failed (command completed).           |
| 3    | Result: some feeds failed and some succeeded (command completed). |
| 64   | Usage error: the CLI surface was misused (`EX_USAGE`).           |
| 65   | Data error: the stored schema is newer than this binary supports (`EX_DATAERR`). |
| 69   | Store unavailable: the store could not be opened or reached (`EX_UNAVAILABLE`). |
| 70   | Internal error, a bug (`EX_SOFTWARE`).                           |
| 78   | Configuration error: invalid configuration (`EX_CONFIG`).       |
| 130  | Interrupted by `SIGINT`.                                         |
| 143  | Terminated by `SIGTERM`.                                         |

Codes 2 and 3 are produced only by commands that target feeds (notably `poll`
and `check`). Commands without a per-feed outcome use 0 for success and a
`sysexits.h` failure code otherwise. No whole-invocation failure exits 1; exit 1
and the 2-63 range are reserved for result classes.

Lane selection adds no new codes; its misuses are ordinary usage errors (exit
64), raised before any work is done:

- A tag that is empty, or that contains a comma or whitespace.
- A `--match` value other than `all` or `any`.
- `--tag` combined with positional feed refs on `poll`, `check`, or `rm`.
  Naming feeds and naming a lane are two different selections, so neither side
  is silently ignored.
- Conflicting write flags on `tag`: `--set`, `--clear`, and the
  `--add`/`--remove` pair are mutually exclusive.
- `rm` with neither a feed ref nor `--tag`.

Selecting a lane that matches no feed is not an error: the command completes
with an empty result and exits 0.

A failing `poll` can carry a partial envelope on stdout: when a store write
fails partway through persisting fetched feeds, the feeds already persisted
before the failure are still reported (`new_items`/`items` cover exactly that
subset), and the process still exits with a failure code (70, an internal
error, for the unclassified write failure). An early hard failure (unreachable
store, unknown named feed) leaves stdout empty, as before. A consumer of `poll`
should process stdout even on a failure exit, since it is not necessarily
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
- `--tag <name>` - tag to assign (repeatable). On the idempotent re-add path an
  omitted `--tag` preserves the feed's existing tags and a given `--tag`
  replaces the whole set, so a routine re-add never silently drops a feed out of
  its lanes. Use `tag --clear` to remove every tag.

```sh
feedwatch add http://127.0.0.1:8099/feeds/rss20.xml --alias qarss --interval 30m --tag ai --tag agents
# {"schema_version":1,"ok":true,"url":"http://127.0.0.1:8099/feeds/rss20.xml","alias":"qarss","interval":"30m0s","tags":["agents","ai"],"created":true}
```

### `rm [<url|alias>]`

Unsubscribe a feed by URL or unique alias, or every feed in a lane with
`--tag`, removing their stored items. A ref and `--tag` together, and neither of
them, are both usage errors (exit 64).

`removed` is an array of canonical URLs on every path, including a single-ref
`rm`, so a caller never parses two shapes for one command. It is `[]` when a
lane matched no feed.

Options:

- `--tag <name>` - unsubscribe every feed carrying this tag (repeatable).
- `--match <all|any>` - multi-tag semantics; `all` by default.

```sh
feedwatch rm qarss
# {"schema_version":1,"ok":true,"removed":["http://127.0.0.1:8099/feeds/rss20.xml"]}
feedwatch rm --tag security
# {"schema_version":1,"ok":true,"removed":["http://127.0.0.1:8099/feeds/atom.xml"]}
```

### `list`

List subscriptions with their health. Every feed view carries a `tags` array,
always present and `[]` when the feed is untagged.

Options:

- `--tag <name>` - tag to filter by (repeatable); all feeds when omitted.
- `--match <all|any>` - multi-tag semantics; `all` by default.

```sh
feedwatch list
# {"schema_version":1,"ok":true,"feeds":[{"url":"http://127.0.0.1:8099/feeds/atom.xml","alias":"qaatom","tags":["security"],"status":"active","failures":0},
#           {"url":"http://127.0.0.1:8099/feeds/rss20.xml","alias":"qarss","interval":"30m0s","tags":["agents","ai","research"],"status":"active","failures":0}]}
feedwatch list --tag ai
# {"schema_version":1,"ok":true,"feeds":[{"url":"http://127.0.0.1:8099/feeds/rss20.xml","alias":"qarss","interval":"30m0s","tags":["agents","ai","research"],"status":"active","failures":0}]}
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
- `--tag <name>` - poll only feeds carrying this tag (repeatable). Alone it
  narrows the scheduled selection, so a lane runs on its own cadence; with
  `--force` it narrows the forced selection to that lane's active feeds. It
  cannot be combined with named feeds (exit 64).
- `--match <all|any>` - multi-tag semantics; `all` by default.
- `--fields <list>` - project the reported new items to a subset of item fields
  (repeatable or comma-separated), with the same names and rules as `items
  --fields`. Every other envelope key (the counts, `failures`, and `renamed`) is
  unchanged. Projection shapes output only: every new item is still stored and
  marked seen in full, so a later `items` query can read its content. An unknown
  field name is a usage error (exit 64) raised before any feed is fetched. Use
  it to keep scheduled runs small, since full items carry `content_html` and
  `content_text`.

A hard failure while persisting a fetched feed (a store write error) aborts the
run and exits with a failure code (70, an internal error, for the unclassified
write failure), but the envelope for feeds already persisted before the failure
is still written to stdout, since that work is durable; a retry reports the
remaining feeds as new. An early hard failure (before any feed is fetched, such
as an unreachable store, exit 69, or an unknown named feed, a usage error, exit
64) leaves stdout empty.

```sh
feedwatch poll          # only due feeds
# {"schema_version":1,"ok":true,"polled":2,"succeeded":2,"failed":0,"skipped":1,"fetched":4,"new_items":4,"deduped":0,"items":[...],"failures":[],"renamed":[]}
feedwatch poll          # immediately again
# {"schema_version":1,"ok":true,"polled":0,"succeeded":0,"failed":0,"skipped":3,"fetched":0,"new_items":0,"deduped":0,"items":[],"failures":[],"renamed":[]}
feedwatch poll --tag ai                       # due feeds in the lane only
# {"schema_version":1,"ok":true,"polled":1,"succeeded":1,"failed":0,"skipped":0,"fetched":3,"new_items":3,"deduped":0,"items":[...],"failures":[],"renamed":[]}
feedwatch poll --tag ai                       # immediately again: nothing due
# {"schema_version":1,"ok":true,"polled":0,"succeeded":0,"failed":0,"skipped":1,"fetched":0,"new_items":0,"deduped":0,"items":[],"failures":[],"renamed":[]}
feedwatch poll --force --tag ai,security --match any
# {"schema_version":1,"ok":true,"polled":2,"succeeded":2,"failed":0,"skipped":0,"fetched":2,"new_items":2,"deduped":0,"items":[...],"failures":[],"renamed":[]}
feedwatch poll --fields title,link             # titles and links only
# {"schema_version":1,"ok":true,"polled":1,"succeeded":1,"failed":0,"skipped":0,"fetched":3,"new_items":3,"deduped":0,
#   "items":[{"feed_url":"http://127.0.0.1:8099/feeds/rss20.xml","title":"First post","link":"http://example.com/posts/1"},...],
#   "failures":[],"renamed":[]}
```

`skipped` counts feeds that were in the selection but not due, so under `--tag`
it counts against the lane rather than against the whole subscription list.

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

The envelope reports `checked` feeds attempted, `passed` feeds that fetched and
parsed cleanly, `failed` feeds that did not, and a `failures` list (always
present, empty when no feed failed) with one `{feed_url, category, message,
status?}` entry per failed feed -- the same shape as the poll failures list.

Exit codes mirror `poll`:

- 0: all checked feeds passed (or nothing to check)
- 2: all checked feeds failed (a result sub-code, the command completed)
- 3: partial -- some feeds passed and some failed (a result sub-code)
- 64/65/69/70/78: whole-invocation failures (usage, too-new data, store
  unavailable, internal, config) per the exit-code taxonomy above

Options:

- `--tag <name>` - check only feeds carrying this tag (repeatable). The same
  selection rules as `poll --tag`, including that it cannot be combined with
  named feeds (exit 64).
- `--match <all|any>` - multi-tag semantics; `all` by default.

```sh
feedwatch check
# {"schema_version":1,"ok":true,"checked":3,"passed":3,"failed":0,"failures":[]}
feedwatch check --tag ai
# {"schema_version":1,"ok":true,"checked":1,"passed":1,"failed":0,"failures":[]}
feedwatch check https://dead.example/feed.xml
# {"schema_version":1,"ok":true,"checked":1,"passed":0,"failed":1,"failures":[{"feed_url":"...","category":"network","message":"..."}]}
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
- `--tag <name>` - restrict to items from feeds carrying this tag (repeatable);
  all feeds when omitted. The output shape is unchanged, just filtered.
- `--match <all|any>` - multi-tag semantics; `all` by default.
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
  emitted regardless. An unknown field name is a usage error (exit 64) that
  returns no partial results; the error message lists all valid field names, and
  when the unknown name closely resembles a valid field, the error also includes
  a did-you-mean suggestion. `published_at` and `updated_at` are nullable
  (`null` when unparseable); `fetched_at` is always present.

```sh
feedwatch items --feed qarss --since 7d --limit 50
feedwatch items --contains release --order published desc
feedwatch items --since 7d --time-field fetched --order fetched desc
feedwatch items --feed qarss --fields title,link,published_at,fetched_at
feedwatch items --tag ai --fields title
# {"schema_version":1,"ok":true,"items":[{"feed_url":"http://127.0.0.1:8099/feeds/rss20.xml","title":"Third post"},
#   {"feed_url":"http://127.0.0.1:8099/feeds/rss20.xml","title":"Second post"},
#   {"feed_url":"http://127.0.0.1:8099/feeds/rss20.xml","title":"First post"}]}
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
- `--tag <name>` - prune only the history of feeds carrying this tag
  (repeatable); all feeds when omitted. `--tag` narrows a prune rather than
  authorizing one, so a bare `prune --tag ai` with neither `--keep-days` nor
  `--max-items` is still a usage error (exit 64).
- `--match <all|any>` - multi-tag semantics; `all` by default.

```sh
feedwatch prune --keep-days 90
feedwatch prune --max-items 500
feedwatch prune --tag ai --keep-days 30
# {"schema_version":1,"ok":true,"pruned":3}
```

### `discover <url>`

Read-only lister of candidate feeds. It performs `<link rel="alternate">`
autodiscovery, then a bounded probe of common feed paths, validating every
candidate by actually parsing it. Each candidate is tagged with a `source` of
`autodiscovery` or `probe`.

```sh
feedwatch discover https://example.com
# {"schema_version":1,"ok":true,"candidates":[{"title":"Blog","url":".../feed.xml",
#   "type":"application/atom+xml","source":"autodiscovery"}]}
```

### `enable <url|alias>` and `disable <url|alias>`

`disable` marks a feed so `poll` skips it; `enable` re-enables a disabled feed
and resets its failure lifecycle so it is due again. A feed that hits the
consecutive-failure threshold (default 10) is auto-disabled and surfaced in
`list`; `enable` resumes it. The poll that crosses the threshold also emits a
`feed_auto_disabled` NDJSON warning on stderr, so an agent learns of the disable
from the invocation that caused it rather than by later noticing the feed gone
quiet:

```json
{"schema_version":1,"level":"warning","code":"feed_auto_disabled","message":"feed disabled after 10 consecutive failures","hint":"re-enable with: feedwatch enable <feed>","details":{"feed_url":"https://flaky.example/feed.xml","failures":10}}
```

The warning does not change the poll exit code (a poll where every targeted feed
failed still exits 2), and it is emitted even under `--quiet`.

```sh
feedwatch disable https://flaky.example/feed.xml
feedwatch enable https://flaky.example/feed.xml
```

### `tag <url|alias>`

Read or edit the tags on one subscription, identified by URL or unique alias.
With no write flag the command is a read. Tags are canonicalized on write
(trimmed, lowercased, deduplicated, stored sorted), so the reported set is the
stored set rather than what was asked for.

Options:

- `--add <name>` - tag to add (repeatable); adding a tag the feed already
  carries is a no-op, not a duplicate.
- `--remove <name>` - tag to remove (repeatable); removing an absent tag is a
  no-op, not an error.
- `--set <name>` - replace the feed's tags with exactly these (repeatable).
- `--clear` - remove every tag from the feed.

`--set`, `--clear`, and the `--add`/`--remove` pair are mutually exclusive;
combining them is a usage error (exit 64).

The result carries the resulting `tags` plus the `added` and `removed` delta, so
a caller sees what actually changed rather than what it requested. All three are
always present, and `added`/`removed` are `[]` on a read or an idempotent write.

```sh
feedwatch tag qarss
# {"schema_version":1,"ok":true,"url":"http://127.0.0.1:8099/feeds/rss20.xml","tags":["agents","ai"],"added":[],"removed":[]}
feedwatch tag qarss --add research
# {"schema_version":1,"ok":true,"url":"http://127.0.0.1:8099/feeds/rss20.xml","tags":["agents","ai","research"],"added":["research"],"removed":[]}
feedwatch tag qarss --remove agents
# {"schema_version":1,"ok":true,"url":"http://127.0.0.1:8099/feeds/rss20.xml","tags":["ai","research"],"added":[],"removed":["agents"]}
feedwatch tag qarss --set ai,agents,research
# {"schema_version":1,"ok":true,"url":"http://127.0.0.1:8099/feeds/rss20.xml","tags":["agents","ai","research"],"added":["agents"],"removed":[]}
```

### `tags`

List every tag in use with the number of subscriptions carrying it, sorted by
tag name and counting feeds of any status, so a disabled feed still contributes
to its lane's count. It takes no flags.

```sh
feedwatch tags
# {"schema_version":1,"ok":true,"tags":[{"tag":"agents","feeds":1},{"tag":"ai","feeds":1},{"tag":"research","feeds":1},{"tag":"security","feeds":1}]}
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

Tags round-trip through the OPML `category` attribute, per the OPML 2.0
convention: `export` writes each feed's tags comma-separated (and omits the
attribute entirely for an untagged feed), and `import` reads `category` back
into the feed's tags, canonicalizing each entry by the same rules as `--tag`. An
empty or unparseable `category` is ignored rather than failing the outline.
Commas are illegal inside a tag name, so the encoding is unambiguous.

Options:

- `--no-validate` - subscribe every syntactically valid feed without fetching
  it (fast bulk-add). A successful import then does not imply the feeds are
  reachable.
- `export --tag <name>` - export only feeds carrying this tag (repeatable), so
  one lane becomes one OPML document.
- `export --match <all|any>` - multi-tag semantics; `all` by default.
- `export -o <file>` - write OPML to this file instead of stdout.

```sh
feedwatch import subs.opml
# {"schema_version":1,"ok":true,"added":40,"skipped":3,"failed":[{"xmlUrl":"https://dead/feed","reason":"could not fetch ..."}]}
feedwatch import --no-validate subs.opml   # fast bulk-add, no reachability check
feedwatch export -o backup.opml
feedwatch export | curl ...
feedwatch export --tag security
```

```xml
<?xml version="1.0" encoding="UTF-8"?>
<opml version="2.0">
  <head>
    <title>feedwatch subscriptions</title>
  </head>
  <body>
    <outline type="rss" text="qaatom" title="qaatom" xmlUrl="http://127.0.0.1:8099/feeds/atom.xml" category="security"></outline>
  </body>
</opml>
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
# {"schema_version":1,"ok":true,"applied":2,"store_schema_version":2}
feedwatch migrate --status
# {"schema_version":1,"ok":true,"store_schema_version":2,"pending":0,"backend":"sqlite"}
```

`store_schema_version` is the database schema version and is independent of the
envelope head's `schema_version`, which versions the JSON output contract. A
store migrated by a newer binary is refused by an older one, which exits 65
rather than risk corrupting data written by a future version.

### `schema [command]`

Emit the machine-readable interface contract. Bare `feedwatch schema` describes
the whole tool: `tool` and `version`, a `commands` array, a tool-level
`exit_codes` (the sorted union of every command's declared codes), an `errors`
inventory, and the inherited `global_flags`. Each entry in `errors` is a
`{code, exit_code, hint}` triple projected from the error registry, so the
documented error surface cannot drift from the real one. For each command, its
arguments and flags (name, type, default), its per-command `exit_codes` map, and
a JSON Schema for its output envelope are reported. `feedwatch schema <command>`
narrows to that one command.

```sh
feedwatch schema
# {"schema_version":1,"ok":true,"tool":"feedwatch","version":"1.0.0","commands":[...],
#  "exit_codes":[0,2,3,64,65,69,70,78],
#  "errors":[{"code":"usage_error","exit_code":64,"hint":"check the command arguments and flags; run the command with --help"}, ...],
#  "global_flags":[...]}

feedwatch schema poll
# {"schema_version":1,"ok":true,"command":"poll","args":[{"name":"feed","variadic":true}],
#  "flags":[{"name":"--force","aliases":["--all"],"type":"bool"}],
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

### One cron entry per lane

A per-lane digest is one script per lane against **one** database. Splitting
lanes across separate databases would break global deduplication: an item
carried by two feeds in two databases is stored, and reported as new, twice.
`--tag` is the mechanism precisely so a lane can be scheduled independently
without splitting state.

```sh
#!/usr/bin/env bash
# ~/bin/feedwatch-lane-ai
set -o errexit -o nounset -o pipefail
export FEEDWATCH_DB="${FEEDWATCH_DB:-${HOME}/.local/state/feedwatch/feedwatch.db}"

feedwatch poll --force --tag ai --timeout 20s --concurrency 12

feedwatch items --tag ai --since 24h \
  --fields title --fields feed_url --fields link --fields summary \
  --limit 0
```

```sh
# crontab: each lane on its own cadence, all against the same store.
0  */2 * * * "${HOME}/bin/feedwatch-lane-ai"    >> "${HOME}/lane-ai.jsonl"    2>> "${HOME}/feedwatch.log"
15 */6 * * * "${HOME}/bin/feedwatch-lane-infra" >> "${HOME}/lane-infra.jsonl" 2>> "${HOME}/feedwatch.log"
```

Dropping `--force` makes each run honor the per-feed schedule and backoff within
the lane, which is the politer default when the cron cadence is tighter than the
feeds' intervals.

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
