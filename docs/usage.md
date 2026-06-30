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
  single JSON error object to stderr and nothing to stdout.
- Per-feed failures during a poll are reported as structured objects on stderr
  with the feed URL, an error `category` (`network`, `http`, `parse`,
  `timeout`), an optional HTTP `status`, and a message. The stdout envelope
  reports outcome counts; stderr reports which feeds failed and why.

```sh
feedwatch poll 2>err.json; echo "exit=$?"
# stdout: {"polled":3,"skipped":0,"new_items":2,"items":[...]}
# err.json: {"errors":[{"feed_url":"...","category":"http","status":404}]}
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

Options:

- `--force`, `--all` - poll every active feed, ignoring the schedule.

```sh
feedwatch poll          # only due feeds
# {"polled":2,"skipped":1,"new_items":4,"items":[...]}
feedwatch poll          # immediately again
# {"polled":0,"skipped":3,"new_items":0,"items":[]}
```

### `items`

Re-query stored item history. By default the full normalized item is returned;
`--fields` narrows the projection to keep large triage queries cheap.

Options:

- `--feed <url|alias>` - feed to query (repeatable); all feeds when omitted.
- `--since <when>`, `--until <when>` - time bounds, RFC3339 or relative such as
  `24h` or `7d`.
- `--limit <int>` - maximum items to return; `0` returns all.
- `--offset <int>` - items to skip before returning results.
- `--order <spec>` - sort: `published|fetched` and `asc|desc` (default
  `published desc`).
- `--contains <text>` - substring matched over title and content.
- `--fields <list>` - project to a subset of item fields (repeatable or
  comma-separated). Valid fields: `id`, `title`, `link`, `summary`,
  `content_html`, `content_text`, `content_mime_type`, `base_url`, `author`,
  `categories`, `enclosures`, `published_at`, `updated_at`. The result carries
  exactly the requested fields plus the always-on `feed_url` identity field; an
  unknown field name is a usage error (exit 1).

```sh
feedwatch items --feed godev --since 7d --limit 50
feedwatch items --contains release --order published desc
feedwatch items --feed godev --fields title,link,published_at
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
  "updated_at": "2026-06-27T10:00:00Z"
}
```

All dates are normalized to RFC3339 UTC. A date that cannot be parsed is stored
as null; for ordering and date filters a null `published_at` coalesces to the
fetch time.

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
per-entry results without failing the whole import on one bad entry. `export`
writes the current subscriptions and aliases as valid OPML 2.0.

Options:

- `export -o <file>` - write OPML to this file instead of stdout.

```sh
feedwatch import subs.opml
# {"added":42,"skipped":3,"failed":[{"xmlUrl":"...","reason":"..."}]}
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
