# Test Plan: feedwatch CLI (Full Surface)

Manual testing plan derived from [requirements.md](requirements.md). It exercises
every command, global flag, exit code, and the non-functional constraints. For
the behavioral contract behind each case see [usage.md](../../usage.md) and
[cli-design.md](../../cli-design.md); requirement IDs below (for example `REQ 9`) refer
to the numbered sections of [requirements.md](requirements.md).

## Executive Summary

- **Under test:** the `feedwatch` command-line tool, an agent-first RSS/Atom feed
  watcher that fetches, parses, normalizes, stores, deduplicates, and queries
  feed content as discrete one-shot invocations.
- **Objective:** confirm the observable contract (structured JSON output, exit
  codes, idempotence, persisted state, discoverability) matches the requirements
  across the full command surface, on both a fresh machine and with accumulated
  state.
- **Key risks:** stdout/stderr separation leaking diagnostics into result JSON;
  dedup or conditional-GET regressions re-emitting seen items; SSRF guard gaps;
  non-deterministic ordering; schema migration corrupting persisted state.
- **Test type:** manual, black-box, one-shot CLI invocations, asserting on
  stdout JSON, stderr JSON, and process exit code.

## Test Scope

**In Scope:**

- All subcommands: `add`, `rm`, `list`, `poll`, `items`, `prune`, `discover`,
  `enable`, `disable`, `import`, `export`, `migrate`, `schema`, plus
  `--version`, `--help`, and shell completion.
- Global flags and environment variables and their precedence (REQ 5).
- Output contract: JSON envelope on stdout, structured logs/errors on stderr,
  `--format text`, color gating (REQ 2).
- Exit-code signaling for whole-invocation and per-feed outcomes (REQ 3).
- State persistence, schema migration, and the SQLite file backend (REQ 6).
- Polling, deduplication, conditional requests, concurrency and politeness
  (REQ 9, 10, 11).
- Item normalization, history querying, parsing robustness (REQ 12, 13, 14).
- Failure lifecycle, retention, OPML interoperability (REQ 15, 16, 17).
- Non-functional constraints: determinism, no network credentials, no config
  file, fresh-machine operation (REQ 20).

**Out of Scope:**

- The PostgreSQL remote backend (deferred in the codebase; only the
  config-rejection path is tested here).
- Authenticated feeds (HTTP auth, tokens, private cookies) - explicitly out of
  scope per REQ 20.
- LLM/content-intelligence behavior - feedwatch performs none (REQ 20).
- Internal unit/race coverage, which is owned by `make test` / `make test-race`.

## Test Strategy

**Test Types:** manual functional, negative testing, boundary analysis,
idempotence checks, and lightweight exploratory passes around parsing and SSRF.

**Test Approach:**

- Black box: assert only on stdout JSON, stderr JSON, and exit code; never on
  internal state except via `list`, `items`, and `migrate --status`.
- Positive and negative: each command tested for success and for its documented
  failure modes (usage, config, per-feed network/http/parse/timeout).
- Determinism and idempotence: repeat invocations must produce stable output and
  no additional observable effects (REQ 1).
- Stream isolation: every case verifies stdout carries only result JSON and
  diagnostics go to stderr (REQ 2).

## Test Environment

- **OS:** macOS (darwin) and Linux. No platform-specific behavior expected;
  spot-check both where signal handling (REQ 1) is exercised.
- **Binary:** built with `make build`, which produces `bin/app`. Alias it for
  the session so cases read naturally:

  ```sh
  make build
  alias feedwatch="$(pwd)/bin/app"
  ```

- **Store:** use a throwaway DB per case via `--db "$(mktemp -d)/fw.db"` or
  `FEEDWATCH_DB`, so cases are independent and a "fresh machine" is reproducible.
- **Fixture server:** fetch, poll, conditional-GET, redirect, failure, and parse
  cases need a controllable HTTP endpoint. The repo ships one at
  `src/cmd/qafixtures` with the feed fixtures embedded under
  `src/cmd/qafixtures/feeds`. Start it on loopback:

  ```sh
  make qa-server                         # listens on 127.0.0.1:8099
  make qa-server QA_ARGS="--addr 127.0.0.1:9000"   # custom address
  ```

  It serves the fixtures over HTTP and also produces scripted responses (status
  codes, redirects, latency, conditional GET) and logs each request's
  conditional headers to stderr. The bundled fixtures cover RSS 0.9x/1.0/2.0,
  Atom 1.0, JSON Feed, a malformed XML feed, feeds in ISO-8859-1 and UTF-16 (with
  BOM), a feed declaring `<ttl>`, a non-feed sitemap, an OPML outline, and an HTML
  page with `<link rel="alternate">` autodiscovery. It also serves a feed at the
  discover probe path `/feed.xml` and a non-feed at `/index.xml`, so the
  path-probing cases (TC-DISC-002/003) have something to find. Set
  `FIX=http://127.0.0.1:8099` for the commands below.
- **Public-origin alias (SSRF case only):** TC-FETCH-006 needs a *public*-classified
  origin that still routes locally. The fetch SSRF guard classifies by resolved
  IP, so a documentation-range (TEST-NET, RFC 5737) address aliased onto loopback
  dials locally yet counts as public. Configure it once and bind the fixture
  server on all interfaces so it answers on both the alias and loopback:

  ```sh
  PUBLIC_ALIAS=203.0.113.10                        # TEST-NET-3, never a real host
  sudo ifconfig lo0 alias "${PUBLIC_ALIAS}" up     # macOS
  sudo ip addr add "${PUBLIC_ALIAS}/24" dev lo      # Linux
  make qa-server QA_ARGS="--addr 0.0.0.0:8099"
  export PUB="http://${PUBLIC_ALIAS}:8099"
  ```

  Remove the alias when done: `sudo ifconfig lo0 -alias "${PUBLIC_ALIAS}"` (macOS)
  or `sudo ip addr del "${PUBLIC_ALIAS}/24" dev lo` (Linux).
- **JSON tooling:** `jq` available, to assert envelope shape and confirm stdout
  parses cleanly.
- **Network:** outbound HTTP allowed for a small number of real-feed smoke cases;
  most cases use loopback fixtures for determinism.

### Fixture server routes

The server (`src/cmd/qafixtures`) exposes these routes. `<name>` is a file under
`src/cmd/qafixtures/feeds` (for example `rss20.xml`, `atom.xml`, `jsonfeed.json`,
`ttl.xml`, `malformed.xml`, `latin1.xml`, `utf16le-bom.xml`, `sitemap.xml`,
`subs.opml`, `autodiscovery.html`).

| Route                                | Behavior                                                                 | Used by                          |
| ------------------------------------ | ------------------------------------------------------------------------ | -------------------------------- |
| `GET /feeds/<name>`                  | Serve fixture with `ETag` + `Last-Modified`; honors `If-None-Match` and `If-Modified-Since` (304). | TC-SUB, TC-POLL, TC-FETCH-001/002, TC-PARSE, TC-DISC |
| `GET /feeds/<name>?ct=<type>`        | Override the `Content-Type` (URL-encode `;` as `%3B` for a charset). | TC-PARSE-003 (charset precedence) |
| `GET /status/<code>`                 | Respond with that status; `?retry-after=N` sets `Retry-After` on 429. | TC-EXIT-003/004, TC-FAIL-003/004 |
| `GET /flaky/<name>?n=<k>`            | Respond 503 for the first `k` requests to `<name>`, then serve it. | TC-FAIL-001 (transient retry)    |
| `GET /slow/<name>?ms=<n>`           | Wait `n` ms (honoring cancellation), then serve `<name>`. | TC-FETCH-004, TC-EXEC-004/005, TC-CONC-003 |
| `GET /redirect?to=<url>&code=<3xx>`  | Redirect to an arbitrary target (default 302). | TC-FETCH-006 (SSRF), TC-FETCH-007 (301/308 rewrite) |
| `GET /feed.xml`                      | Serve a real feed at a discover probe path. | TC-DISC-002 (probe source) |
| `GET /index.xml`                     | Serve a non-feed (sitemap) at a probe path, for the validator to drop. | TC-DISC-003 (validation) |
| `GET /`                              | List routes and bundled fixtures. | smoke check                      |

For the SSRF case (TC-FETCH-006), reach the `/redirect` route through the
public-origin alias `${PUB}` and point it at a loopback target: a public origin
redirecting into private space is what the guard blocks. The direct-loopback
allowance is covered separately by TC-FETCH-005, and the public-origin block is
also unit-tested in `internal/fetch` (`TestCheckRedirectBlocksPublicToPrivate`).

## Entry Criteria

- [ ] `make build` passes (full quality gate green).
- [ ] `feedwatch --version` returns JSON and exit 0.
- [ ] Fixture HTTP server reachable on loopback.
- [ ] `jq` and a writable temp directory available.

## Exit Criteria

- [ ] All P0 and P1 cases executed.
- [ ] 100% pass on P0 cases; 95%+ overall pass rate.
- [ ] No open P0/P1 defect in output contract, exit codes, dedup, or SSRF.
- [ ] Every command verified to keep stdout free of diagnostics.

## Risk Assessment

| Risk                                                | Probability | Impact | Mitigation                                                              |
| --------------------------------------------------- | ----------- | ------ | ---------------------------------------------------------------------- |
| Diagnostics leak into stdout, breaking `jq` parsing | M           | H      | Every case pipes stdout through `jq -e`; redirect stderr separately.   |
| Dedup regression re-emits seen items                | L           | H      | Double-poll idempotence checks (TC-POLL-002) on every feed format.     |
| SSRF guard allows public-to-private redirect        | L           | H      | Dedicated redirect cases (TC-FETCH-005/006) with loopback target.      |
| Non-deterministic multi-feed ordering               | M           | M      | Stable-order assertion across repeated polls (TC-CONC-003).            |
| Migration corrupts or downgrades persisted state    | L           | H      | Fresh-DB, re-run, and too-new guard cases (TC-MIGRATE-001..003).       |
| Date parsing fabricates values                      | L           | M      | Null-date and coalesce cases (TC-ITEM-003).                            |

## Test Deliverables

- This plan, the test cases below, an execution report (pass/fail per case), and
  bug reports for any failures (`BUG-NNN`, severity and priority assigned).

---

## Test Cases

Conventions: each case is a one-shot invocation. Unless stated, **Precondition**
includes a fresh `--db` path and the fixture server running. "stdout is valid
JSON" means `feedwatch ... | jq -e .` exits 0. Capture stderr with `2>err.json`
and the exit code with `; echo $?`.

### Module: Execution and Invocation Model (REQ 1)

#### TC-EXEC-001: Each command is one-shot and returns control (P0)

- **Preconditions:** fresh DB.
- **Steps:**
  1. Run `feedwatch list` -> command exits promptly, prints JSON, returns to the
     shell prompt without awaiting input.
  2. Run `time feedwatch poll` with no subscriptions -> completes in well under a
     second and exits; no daemon process remains (`pgrep -f feedwatch` empty).
- **Expected:** no blocking, no background process, exit 0.

#### TC-EXEC-002: State persists across invocations (P0)

- **Steps:**
  1. `feedwatch add <fixture-feed-url> --alias f1` -> `created:true`.
  2. In a new shell invocation, `feedwatch list` -> the feed `f1` is present.
- **Expected:** subscription survives between separate process runs.

#### TC-EXEC-003: Idempotent repeat invocation (P1)

- **Steps:** run the same `feedwatch add <url>` twice.
- **Expected:** second run succeeds without creating a duplicate (REQ 7); no
  additional observable effect. See TC-SUB-005.

#### TC-EXEC-004: SIGINT during poll exits 130 with partial persistence (P1)

- **Preconditions:** subscribe two feeds; point one at a fixture endpoint that
  delays its response (slow handler), one that responds fast.
- **Steps:**
  1. Start `feedwatch poll --all`, send `SIGINT` (Ctrl-C) while the slow fetch is
     in flight.
- **Expected:** process stops starting new fetches, persists the completed feed,
  emits the envelope for completed work on stdout, exits **130**. A subsequent
  `feedwatch items` shows items from the completed feed only.

#### TC-EXEC-005: SIGTERM during poll exits 143 (P2)

- **Steps:** as TC-EXEC-004 but send `SIGTERM` (`kill -TERM <pid>`).
- **Expected:** exit **143**; completed work persisted and emitted.

---

### Module: Output Contract (REQ 2)

#### TC-OUT-001: stdout is pure result JSON in a consistent envelope (P0)

- **Steps:** `feedwatch list 2>/dev/null | jq -e .` for several commands
  (`list`, `poll`, `items`, `discover`).
- **Expected:** stdout parses as JSON for every command; a consistent top-level
  envelope shape per command; exit 0.

#### TC-OUT-002: Logs and errors go to stderr only (P0)

- **Steps:** `feedwatch --log-level debug poll 1>out.json 2>err.json`.
- **Expected:** `out.json` is valid result JSON only; `err.json` holds structured
  JSON log lines. No log or error text appears in `out.json`.

#### TC-OUT-003: Hard failure writes nothing to stdout (P0)

- **Steps:** `feedwatch add not-a-url 1>out.json 2>err.json; echo $?`.
- **Expected:** `out.json` empty; `err.json` contains a single JSON error object;
  exit 1.

#### TC-OUT-004: `--format text` renders friendly output on both streams (P1)

- **Steps:** `feedwatch --format text list` and a failing command with
  `--format text`.
- **Expected:** stdout is terminal-friendly text (tables), stderr friendly text;
  no JSON envelope; no color codes embedded in any JSON anywhere.

#### TC-OUT-005: `--format json` is the default and may be stated (P2)

- **Steps:** compare `feedwatch list` and `feedwatch --format json list`.
- **Expected:** identical JSON output.

#### TC-OUT-006: Color only on a TTY, never in JSON (P1)

- **Steps:**
  1. `feedwatch --format text list` piped to a file (not a TTY) -> no ANSI codes.
  2. `feedwatch --format text list` in an interactive terminal -> color may
     appear, and every colored status is also marked by a symbol or word.
  3. `feedwatch list | cat` (JSON) -> never any ANSI codes.
- **Expected:** color gated by TTY per stream; meaning never conveyed by color
  alone.

#### TC-OUT-007: Color disabled by `--no-color`, `NO_COLOR`, `TERM=dumb` (P1)

- **Steps:** in a TTY, run `--format text` three ways: with `--no-color`, with
  `NO_COLOR=1`, with `TERM=dumb`.
- **Expected:** no color in any of the three; stdout and stderr evaluated
  independently.

#### TC-OUT-008: `--log-level` floor and `--quiet` (P2)

- **Steps:** run `poll` at `--log-level error`, `warn`, `info`, `debug`, and with
  `--quiet`.
- **Expected:** only messages at or above the selected level appear on stderr;
  `--quiet` suppresses all non-error logs.

---

### Module: Exit Codes and Per-Feed Errors (REQ 3)

#### TC-EXIT-001: Full success exits 0 (P0)

- **Steps:** `feedwatch poll <healthy-feed>; echo $?`.
- **Expected:** exit 0.

#### TC-EXIT-002: Usage/config error exits 1 (P0)

- **Steps:** `feedwatch --concurrency 0 poll; echo $?` and
  `feedwatch bogus-command; echo $?`.
- **Expected:** exit 1 in both; one JSON error object on stderr with category
  `config` and `usage` respectively.

#### TC-EXIT-003: All targeted feeds fail exits 2 (P1)

- **Preconditions:** subscribe two feeds whose fixture endpoints both return 404.
- **Steps:** `feedwatch poll --all 2>err.json; echo $?`.
- **Expected:** exit **2**; stderr lists each feed as a structured error object
  with `feed_url`, `category` (`http`), `status` 404, and a message; stdout
  envelope reports outcome counts.

#### TC-EXIT-004: Partial success exits 3 (P1)

- **Preconditions:** one healthy fixture feed, one returning 500.
- **Steps:** `feedwatch poll --all 2>err.json; echo $?`.
- **Expected:** exit **3**; stdout reports the new items from the healthy feed;
  stderr reports the failed feed.

#### TC-EXIT-005: Per-feed error categories (P1)

- **Steps:** trigger one feed each of: connection refused (network), 404 (http),
  malformed body served as a feed (parse), and a non-responding socket (timeout,
  with a short `--timeout`).
- **Expected:** each stderr error object carries the correct `category` of
  `network`, `http`, `parse`, or `timeout`.

---

### Module: Discoverability (REQ 4)

#### TC-SCHEMA-001: `schema` describes every command (P0)

- **Steps:** `feedwatch schema | jq -e .`.
- **Expected:** machine-readable JSON listing every command with its args, flags
  (name, type, default), exit codes, and an output schema; valid JSON; exit 0.

#### TC-SCHEMA-002: `schema <command>` narrows to one command (P1)

- **Steps:** `feedwatch schema poll | jq -e '.command == "poll"'`.
- **Expected:** description for `poll` only, including its `--force`/`--all` flag
  and exit-code map.

#### TC-SCHEMA-003: `--help` and `-h` on every command (P1)

- **Steps:** `feedwatch --help`; loop `feedwatch <cmd> --help` and
  `feedwatch <cmd> -h` for all subcommands.
- **Expected:** each prints concise usage with examples to stdout; unaffected by
  `--format`; exit 0.

#### TC-SCHEMA-004: schema does not drift from real flags (P2)

- **Steps:** compare flags reported by `schema poll` against `poll --help`.
- **Expected:** flag names, types, and defaults match (schema is introspected
  from the live command tree).

---

### Module: Configuration and Precedence (REQ 5)

#### TC-CONFIG-001: Flags beat env beat defaults (P0)

- **Steps:**
  1. Default: `feedwatch schema` shows `--concurrency` default 8.
  2. `FEEDWATCH_CONCURRENCY=4 feedwatch ...` then verify 4 is used.
  3. `FEEDWATCH_CONCURRENCY=4 feedwatch --concurrency 2 ...` -> 2 wins.
- **Expected:** precedence is flags, then env, then compiled default.

#### TC-CONFIG-002: No config file is read or required (P1)

- **Steps:** run any command in a directory and HOME with no feedwatch config
  file present; place a decoy `feedwatch.conf`/`.feedwatchrc` and confirm it is
  ignored.
- **Expected:** behavior depends only on flags, env, defaults; decoy has no
  effect.

#### TC-CONFIG-003: XDG default store location (P1)

- **Steps:** unset `FEEDWATCH_DB`; set `XDG_STATE_HOME=$(mktemp -d)`; run
  `feedwatch migrate`.
- **Expected:** DB created at `$XDG_STATE_HOME/feedwatch/feedwatch.db`. With
  `XDG_STATE_HOME` unset, falls back to `~/.local/state/feedwatch/feedwatch.db`.

#### TC-CONFIG-004: All documented settings are configurable (P2)

- **Steps:** for each of `--db`, `--user-agent`, `--interval` (default),
  `--concurrency`, `--connect-timeout`, `--timeout`, `--proxy`, `--ca-bundle`,
  `--min-tls`, `--allow-private`, `--log-level`: set via flag and via its env var
  where one exists, confirm accepted.
- **Expected:** each setting is accepted and reflected (observe via `schema`,
  behavior, or stderr debug logs).

#### TC-CONFIG-005: Appendix A defaults applied (P1)

- **Steps:** with no overrides, confirm defaults via `schema` and behavior:
  concurrency 8, default interval 1h, connect timeout 5s, overall timeout 30s,
  per-host delay 1s, retry attempts 3, failure threshold 10, min TLS 1.2, private
  redirects blocked.
- **Expected:** values match Appendix A of the requirements.

---

### Module: State, Storage, and Migrations (REQ 6)

#### TC-MIGRATE-001: Fresh machine auto-applies schema (P0)

- **Steps:** with a brand-new `--db` path, run `feedwatch migrate`.
- **Expected:** `{"applied":N,"schema_version":V}` with `N >= 1`; exit 0; no
  manual setup required.

#### TC-MIGRATE-002: `migrate --status` reports version, pending, backend (P0)

- **Steps:** on a fresh DB run `feedwatch migrate --status`.
- **Expected:** `{"schema_version":V,"pending":0,"backend":"sqlite"}`; the status
  path ensures schema first, so pending is 0; exit 0.

#### TC-MIGRATE-003: Refuse a newer-than-known schema (P1)

- **Preconditions:** a DB whose recorded schema version exceeds what the binary
  knows (hand-stamp a future version, or use a DB from a newer build).
- **Steps:** run any command against it.
- **Expected:** the command aborts, reports a JSON error (schema-too-new), leaves
  state unchanged, exits 1.

#### TC-MIGRATE-004: Failed upgrade leaves state unchanged (P1)

- **Steps:** simulate a migration failure (for example, a read-only DB file mid
  upgrade).
- **Expected:** command aborts with a JSON error on stderr (exit 1); persisted
  state is not partially modified.

#### TC-STORE-001: Remote DSN selects the (deferred) backend (P1)

- **Steps:** `feedwatch --db postgres://user@host/feedwatch migrate; echo $?`.
- **Expected:** JSON error with category `config` ("postgres backend not yet
  implemented"); exit 1. Confirms scheme-based backend selection.

#### TC-STORE-002: Unwritable store reports store error (P1)

- **Steps:** `feedwatch --db /nonexistent-dir/sub/fw.db migrate; echo $?`.
- **Expected:** JSON error category `store` on stderr; exit 1; stdout empty.

#### TC-STORE-003: Crash-safe under concurrent multi-feed poll (P2)

- **Steps:** subscribe many fixture feeds; run `feedwatch poll --all` with default
  concurrency; repeat several times.
- **Expected:** no DB corruption, no "database is locked" failures; `items`
  remains queryable; counts stable.

---

### Module: Subscription Management (REQ 7)

#### TC-SUB-001: `add` validates the URL parses as a feed (P0)

- **Steps:** `feedwatch add <fixture-rss-feed-url> --alias f1 --interval 30m`.
- **Expected:** `{"url":...,"alias":"f1","interval":"30m0s","created":true}`;
  exit 0.

#### TC-SUB-002: `add` rejects a non-feed and points to discover (P0)

- **Steps:** `feedwatch add <html-page-url>; echo $?`.
- **Expected:** rejected with a JSON error directing the caller to `discover`;
  exit 1; nothing subscribed (`list` unchanged).

#### TC-SUB-003: `add` with alias and per-feed interval (P1)

- **Steps:** add with `--alias` and `--interval 30m`; then `list`.
- **Expected:** alias and interval stored and shown.

#### TC-SUB-004: Feed reference resolves by URL or unique alias (P1)

- **Steps:** after adding with alias `f1`, run `feedwatch poll f1` and
  `feedwatch poll <full-url>`.
- **Expected:** both resolve to the same subscription.

#### TC-SUB-005: `add` on an already-subscribed URL is a no-op update (P1)

- **Steps:** `add <url>`; then `add <url> --alias new --interval 15m`.
- **Expected:** second `add` succeeds, creates no duplicate, applies the new
  alias and interval to the existing subscription.

#### TC-SUB-006: `rm` by URL and by alias (P0)

- **Steps:** add a feed; `feedwatch rm <alias>`; re-add; `feedwatch rm <url>`.
- **Expected:** `{"removed":<url>}`; subscription gone from `list`; its stored
  items removed.

#### TC-SUB-007: `list` reports health fields (P0)

- **Steps:** add feeds, force a failure on one (see TC-FAIL-001), then `list`.
- **Expected:** each feed shows `status`, `alias`, `failures`, and `last_error`
  where applicable.

---

### Module: Feed Discovery (REQ 8)

#### TC-DISC-001: `discover` returns candidates without changing state (P0)

- **Steps:** `feedwatch discover <html-page-with-autodiscovery-url>`; then
  `feedwatch list`.
- **Expected:** candidates returned with `title`, `url`, `type`, `source`; `list`
  shows no new subscription (read-only).

#### TC-DISC-002: Autodiscovery and path probing both contribute (P1)

- **Steps:** `feedwatch discover "${FIX}/feeds/autodiscovery.html"`. The page
  declares feeds via `<link rel="alternate">`, and the server also serves a feed
  at the probe path `${FIX}/feed.xml`.
- **Expected:** candidates tagged `source: autodiscovery` (the declared feeds)
  and `source: probe` (`${FIX}/feed.xml`) both appear.

#### TC-DISC-003: Each candidate validated by parsing (P1)

- **Steps:** the same discover run probes `${FIX}/index.xml`, which returns a
  non-feed sitemap.
- **Expected:** the sitemap and other non-feed content are excluded; only
  parseable feeds are returned.

#### TC-DISC-004: `discover` never subscribes (P1)

- **Steps:** run `discover` on several URLs; check `list` after each.
- **Expected:** no subscriptions created at any point.

---

### Module: Polling and Deduplication (REQ 9)

#### TC-POLL-001: Poll reports only genuinely new items (P0)

- **Preconditions:** subscribe a fixture feed with 3 items.
- **Steps:** `feedwatch poll <feed> 2>/dev/null | jq '.new_items, (.items|length)'`.
- **Expected:** `new_items` equals the count of previously unseen items; items
  returned and marked seen; envelope reports `polled`, `skipped`, `new_items`.

#### TC-POLL-002: Immediate second poll returns empty (idempotence) (P0)

- **Steps:** repeat `feedwatch poll <feed>` immediately.
- **Expected:** `new_items: 0`, `items: []`; no item re-emitted as new.

#### TC-POLL-003: New item appearing later is reported once (P1)

- **Steps:** add a 4th item to the fixture feed; `feedwatch poll --force <feed>`;
  then poll again.
- **Expected:** the new item is reported as new exactly once.

#### TC-POLL-004: Dedup key precedence GUID -> link -> title (P1)

- **Steps:** use three fixture feeds: one with GUIDs, one without GUIDs but with
  links, one with neither (titles only). Poll each twice.
- **Expected:** dedup is stable for each; a re-dated item with the same GUID is
  not re-emitted (no `link+date` key).

#### TC-POLL-005: Due-only poll respects interval and `<ttl>` (P1)

- **Steps:** add a feed with `--interval 1h`; poll once (fetches), poll again with
  no args immediately.
- **Expected:** second run skips the not-yet-due feed (`skipped` increments,
  `polled` 0). A feed declaring `<ttl>` is honored when deciding due-ness.

#### TC-POLL-006: `--force` / `--all` ignores schedule (P1)

- **Steps:** with a not-yet-due feed, `feedwatch poll --force <feed>` and
  `feedwatch poll --all`.
- **Expected:** the feed is fetched regardless of due status.

#### TC-POLL-007: Named-feed poll restricts to those feeds (P2)

- **Steps:** with several due feeds, `feedwatch poll f1`.
- **Expected:** only `f1` is polled.

#### TC-POLL-008: Default interval when none specified and no ttl (P2)

- **Steps:** add a feed with no `--interval` and no `<ttl>`; poll, then poll again
  before the default interval (1h) elapses.
- **Expected:** treated as due per the default poll interval; second immediate
  due-only poll skips it.

---

### Module: Conditional Requests and Fetching (REQ 10)

#### TC-FETCH-001: Conditional headers sent when validators stored (P1)

- **Preconditions:** fixture server logs request headers; serve `ETag` and
  `Last-Modified`.
- **Steps:** poll once (stores validators), poll again with `--force`.
- **Expected:** the second request includes `If-None-Match` and
  `If-Modified-Since`.

#### TC-FETCH-002: 304 Not Modified skips parsing, no new items (P1)

- **Steps:** configure the fixture to return 304 on conditional request; poll
  with `--force`.
- **Expected:** feed reported with no new items; no parse occurs; exit 0.

#### TC-FETCH-003: Validator updated only on change; empty never overwrites (P2)

- **Steps:** poll, change the served `ETag`, poll `--force`; then serve an empty
  `ETag`, poll `--force`.
- **Expected:** stored validator updates on a real change; an empty value does
  not overwrite the stored one.

#### TC-FETCH-004: Connect and overall timeouts enforced (P1)

- **Steps:** point a feed at a non-accepting socket with
  `--connect-timeout 1s`; point another at a slow-body endpoint with
  `--timeout 2s`.
- **Expected:** the first fails fast as `timeout`/`network`; the second aborts at
  the overall deadline; both reported as per-feed errors.

#### TC-FETCH-005: Direct private/loopback URL allowed (P1)

- **Steps:** `feedwatch add http://127.0.0.1:8099/feed.xml` (loopback fixture).
- **Expected:** permitted by default (direct private address is allowed).

#### TC-FETCH-006: Public-to-private redirect blocked unless allowed (P0)

- **Preconditions:** the public-origin alias configured and the fixture server
  bound on all interfaces (see Test Environment), so `${PUB}` is a public-classified
  origin and `${FIX}` is loopback. The redirect URL pairs them:
  `REDIR="${PUB}/redirect?to=${FIX}/feeds/rss20.xml"`. The block is enforced at any
  fetch; `add` is used here because `poll` only accepts already-subscribed feeds and
  the block prevents the subscription from being created.
- **Steps:**
  1. `feedwatch add "${REDIR}" 2>err.json; echo $?` -> rejected.
  2. `feedwatch --allow-private add "${REDIR}"; echo $?` -> permitted.
- **Expected:** by default the redirect into private space is blocked (a `network`
  error on stderr citing the blocked redirect; exit 1; `list` unchanged); with
  `--allow-private` the redirect is followed to the loopback feed and the
  subscription is created (exit 0). The resolved address is re-checked on every
  hop. The block is also unit-tested by `TestCheckRedirectBlocksPublicToPrivate`.

#### TC-FETCH-007: Permanent redirect rewrites stored URL (P1)

- **Steps:** serve a 301/308 to a new feed URL; poll; then `list`.
- **Expected:** stored feed URL updated to the redirect target (subject to the
  private-address policy).

#### TC-FETCH-008: Proxy, CA bundle, min TLS applied (P2)

- **Steps:** set `--proxy`, `--ca-bundle`, `--min-tls 1.3` against a fixture that
  can observe them.
- **Expected:** each applied to outbound requests; min TLS defaults to 1.2 when
  unset.

---

### Module: Concurrency and Politeness (REQ 11)

#### TC-CONC-001: Concurrent fetch up to the worker limit (P1)

- **Steps:** subscribe 12 fixture feeds on distinct hosts/ports;
  `feedwatch --concurrency 4 poll --all` while observing the fixture servers.
- **Expected:** at most 4 host groups fetched in parallel.

#### TC-CONC-002: Same-host requests serialized with a delay (P2)

- **Steps:** subscribe several feeds on the same host; poll `--all`.
- **Expected:** same-host requests are serialized with the per-host delay
  (default 1s) between them.

#### TC-CONC-003: Stable output order regardless of completion order (P1)

- **Steps:** poll `--all` several times where feeds complete in varying order
  (vary fixture latencies).
- **Expected:** the order of feeds/items in the envelope is stable across runs.

---

### Module: Item Model and Content (REQ 12)

#### TC-ITEM-001: Stable normalized schema with HTML and text (P0)

- **Steps:** poll a feed; `feedwatch items --feed <feed> | jq '.items[0]'`.
- **Expected:** consistent field names; both `content_html` and `content_text`
  present; `content_mime_type` and `base_url` populated where available.

#### TC-ITEM-002: Dates normalized to RFC3339 UTC (P1)

- **Steps:** use feeds with RSS (RFC 822) and Atom (RFC 3339) dates; inspect
  `published_at`/`updated_at`.
- **Expected:** all dates fixed-width RFC3339 UTC.

#### TC-ITEM-003: Unparseable date stored null, coalesced on order (P1)

- **Steps:** feed an item with an invalid date; query it; order by `published`.
- **Expected:** `published_at` is null (not fabricated); for ordering/date filters
  it falls back to fetch time.

#### TC-ITEM-004: Content and author precedence (P2)

- **Steps:** craft items exercising `content:encoded` vs description fallback;
  item author vs feed `managingEditor` vs `dc:creator`.
- **Expected:** content picks encoded/Atom content, falling back to description;
  author cascades item -> feed -> Dublin Core creator; summary from feed
  description.

#### TC-ITEM-005: No read/unread layer exposed (P2)

- **Steps:** inspect `schema` and `items` output and all flags.
- **Expected:** no command or field lets the agent read or mutate item seen
  state directly.

---

### Module: Querying History (REQ 13)

#### TC-ITEMS-001: Returns stored items by filter (P0)

- **Steps:** `feedwatch items --feed f1`.
- **Expected:** items matching the feed returned; full field set by default.

#### TC-ITEMS-002: Repeatable `--feed` filter (P1)

- **Steps:** `feedwatch items --feed f1 --feed f2`.
- **Expected:** union of both feeds returned.

#### TC-ITEMS-003: `--since` / `--until` as RFC3339 or relative (P1)

- **Steps:** `feedwatch items --since 7d`, `--since 24h`, and an RFC3339
  timestamp; combine with `--until`.
- **Expected:** both absolute and relative bounds accepted and applied.

#### TC-ITEMS-004: `--contains` substring over title and content (P1)

- **Steps:** `feedwatch items --contains release`.
- **Expected:** only items whose title or content contains the substring
  (case-insensitive) returned.

#### TC-ITEMS-005: `--limit` and `--offset` pagination (P1)

- **Steps:** `--limit 5`, then `--limit 5 --offset 5`; `--limit 0`.
- **Expected:** page boundaries correct; `--limit 0` returns all.

#### TC-ITEMS-006: `--order` published/fetched asc/desc (P1)

- **Steps:** `--order published desc` (default), `--order published asc`,
  `--order fetched desc`.
- **Expected:** ordering applied as requested; default is `published desc`.

#### TC-ITEMS-007: `--fields` projection (P1)

- **Steps:** `feedwatch items --fields summary`, then
  `feedwatch items --fields title,link,published_at`, then
  `feedwatch items --fields bogusfield` and `--fields title,bogus`; finally
  `feedwatch items` with `--fields` omitted.
- **Expected:** the projection returns exactly the requested fields plus the
  always-on `feed_url` identity field (e.g. `--fields summary` yields
  `{feed_url, summary}`), and requested fields are always present even when
  empty; an unknown field name (including a partially-valid list like
  `title,bogus`) is rejected with a `usage`-category error, exit 1, empty
  stdout; the full normalized item is returned when `--fields` is omitted (with
  `published_at` present or `null`).

---

### Module: Parsing and Robustness (REQ 14)

#### TC-PARSE-001: All supported formats parse (P0)

- **Steps:** add and poll one feed each of RSS 0.9x, RSS 1.0, RSS 2.0, Atom 1.0,
  JSON Feed.
- **Expected:** each yields normalized items; exit 0.

#### TC-PARSE-002: Malformed XML still yields items (P1)

- **Steps:** poll a fixture with a raw unescaped `&` in element text.
- **Expected:** items extracted despite the malformed XML; no whole-feed failure.

#### TC-PARSE-003: Charset precedence BOM > XML decl > Content-Type > UTF-8 (P1)

- **Steps:** poll fixtures: ISO-8859-1 with matching XML declaration; UTF-16LE
  with a BOM but a conflicting `charset=utf-8` header; a garbage charset.
- **Expected:** correct decoding per precedence; the BOM wins over the
  conflicting header; garbage charset falls back to lossy UTF-8 without crashing.

#### TC-PARSE-004: CDATA and entities resolved (P2)

- **Steps:** poll a feed whose content uses CDATA and character entities.
- **Expected:** stored content has CDATA unwrapped and entities resolved.

#### TC-PARSE-005: Unparseable feed records a parse failure, others continue (P1)

- **Steps:** poll `--all` with one totally unparseable feed among healthy ones.
- **Expected:** the bad feed recorded as a `parse` failure on stderr; healthy
  feeds still processed; exit 3 (partial).

---

### Module: Failure Handling and Lifecycle (REQ 15)

#### TC-FAIL-001: Transient error retried then counted (P1)

- **Preconditions:** fixture returns 503 a few times.
- **Steps:** poll the feed; inspect retry behavior and `list`.
- **Expected:** up to 3 in-call retries before a failure is recorded; failure
  count incremented, `last_error` and time recorded.

#### TC-FAIL-002: Transient classification (P1)

- **Steps:** exercise connection reset, timeout, temporary DNS failure, 5xx, and
  429.
- **Expected:** all classified transient and retried.

#### TC-FAIL-003: 429 honors Retry-After (P2)

- **Steps:** fixture returns 429 with `Retry-After: 1`.
- **Expected:** feedwatch waits accordingly (bounded by the overall deadline).

#### TC-FAIL-004: Deterministic errors not retried (P1)

- **Steps:** fixture returns 404; poll.
- **Expected:** no retry; immediate `http` failure recorded.

#### TC-FAIL-005: Backoff grows; due-only poll skips within window (P1)

- **Steps:** cause consecutive failures; observe that due-only polls skip the
  feed until the (growing) backoff elapses.
- **Expected:** backoff doubles per failure, clamped to the max (24h).

#### TC-FAIL-006: Auto-disable at threshold (P1)

- **Steps:** drive a feed to 10 consecutive failures.
- **Expected:** feed disabled and skipped by poll; surfaced in `list` with
  failure count and last error.

#### TC-FAIL-007: Success resets failure state (P1)

- **Steps:** after some failures, make the feed healthy and poll.
- **Expected:** failure count reset to 0; `last_error` cleared.

#### TC-FAIL-008: `disable` and `enable` (P0)

- **Steps:** `feedwatch disable <feed>`; poll (skipped); `feedwatch enable
  <feed>`; poll (fetched again).
- **Expected:** disable skips the feed; enable clears disabled state and resumes
  polling, resetting the failure lifecycle.

---

### Module: Retention (REQ 16)

#### TC-PRUNE-001: No automatic pruning; items retained by default (P1)

- **Steps:** poll over time; never run `prune`.
- **Expected:** all items retained; nothing auto-deleted.

#### TC-PRUNE-002: `prune --keep-days` deletes older items (P1)

- **Steps:** with items of varying ages, `feedwatch prune --keep-days 90`.
- **Expected:** items older than 90 days removed; newer retained.

#### TC-PRUNE-003: `prune --max-items` keeps N newest per feed (P1)

- **Steps:** `feedwatch prune --max-items 500`.
- **Expected:** oldest items beyond 500 per feed removed.

#### TC-PRUNE-004: Pruned item not re-emitted as new (P0)

- **Steps:** prune an item the feed still advertises, then poll `--force`.
- **Expected:** the still-advertised pruned item is not reported as new (dedup
  fingerprint preserved).

---

### Module: OPML Interoperability (REQ 17)

#### TC-OPML-001: `import` adds feeds at any nesting depth (P1)

- **Steps:** `feedwatch import subs.opml` where the outline has nested folders.
- **Expected:** every feed at any depth added; `{"added":N,"skipped":M,
  "failed":[...]}`.

#### TC-OPML-002: xmlUrl with url fallback; text/title alias (P2)

- **Steps:** import an outline mixing `xmlUrl` and `url`, with `text`/`title`
  present.
- **Expected:** `xmlUrl` used, falling back to `url`; a free `text`/`title`
  assigned as alias.

#### TC-OPML-003: Duplicates skipped, bad entries do not abort (P1)

- **Steps:** import an outline containing an already-subscribed feed and one
  invalid entry.
- **Expected:** duplicate reported as skipped; bad entry reported in `failed`;
  remaining entries imported; per-entry results as JSON.

#### TC-OPML-004: `import` from file and stdin (P2)

- **Steps:** `feedwatch import subs.opml` and `cat subs.opml | feedwatch import -`.
- **Expected:** both work identically.

#### TC-OPML-005: `export` writes valid OPML 2.0 (P1)

- **Steps:** `feedwatch export -o backup.opml`; validate it is well-formed OPML
  2.0; also `feedwatch export | head`.
- **Expected:** current subscriptions and aliases exported as valid OPML 2.0 to
  file or stdout.

#### TC-OPML-006: Round-trip import/export (P2)

- **Steps:** export, wipe DB, import the exported file.
- **Expected:** subscriptions and aliases restored.

---

### Module: Scheduling (REQ 18)

#### TC-SCHED-001: No internal scheduling (P1)

- **Steps:** start `feedwatch poll` and wait.
- **Expected:** it polls once and exits; it never loops or re-polls on its own.

#### TC-SCHED-002: Driven by repeated one-shot invocations (P2)

- **Steps:** run `feedwatch poll >> items.jsonl 2>> err.log` repeatedly (loop or
  cron-style).
- **Expected:** each run appends clean JSON to `items.jsonl`; errors collect in
  `err.log`; output is append-friendly (one envelope per line region).

---

### Module: Command Surface and Meta (REQ 19)

#### TC-CMD-001: Flat verb command set, no nesting (P1)

- **Steps:** `feedwatch --help`; inspect `schema`.
- **Expected:** all commands are flat verbs; no nested subcommand hierarchies.

#### TC-CMD-002: `--version` / `-v` prints JSON (P1)

- **Steps:** `feedwatch --version | jq -e .` and `feedwatch -v`.
- **Expected:** version info as JSON (`version`, `commit`, `go`); exit 0.

#### TC-CMD-003: Shell completion script per shell (P2)

- **Steps:** request completion for bash, zsh, fish, powershell.
- **Expected:** a completion script emitted for each supported shell.

---

### Module: Non-Functional Constraints (REQ 20)

#### TC-NFR-001: No external LLM/AI calls, no credentials (P1)

- **Steps:** run a full poll cycle with no API keys set and network monitoring on.
- **Expected:** no calls to any LLM/AI service; no API keys or network
  credentials required for core operation.

#### TC-NFR-002: Authenticated feeds out of scope (P2)

- **Steps:** `feedwatch add <feed-behind-http-auth>`.
- **Expected:** not fetched/authenticated; treated as a normal fetch failure, not
  a credential prompt.

#### TC-NFR-003: Deterministic given same inputs and state (P1)

- **Steps:** with identical DB state and fixtures, run the same command twice and
  diff stdout.
- **Expected:** byte-identical result envelopes (aside from inherently variable
  fields like fetch timestamps, which should be the only differences).

#### TC-NFR-004: Self-contained binary, fresh-machine operation (P0)

- **Steps:** copy `bin/app` to a clean environment with no Go toolchain; run
  `feedwatch migrate` and `feedwatch add <url>` against a new `--db`.
- **Expected:** works with no external runtime/compiler and no manual setup
  beyond invocation.

#### TC-NFR-005: Queries built without unsanitized interpolation (P2)

- **Steps:** use feed URLs, aliases, and `--contains` values containing SQL
  metacharacters (`'`, `;`, `--`, `%`).
- **Expected:** treated as literal data; no injection, no errors beyond normal
  no-match results.

---

## Traceability Matrix

| Requirement                          | Test Cases                              |
| ------------------------------------ | --------------------------------------- |
| REQ 1 Execution model                | TC-EXEC-001..005                        |
| REQ 2 Output contract                | TC-OUT-001..008                         |
| REQ 3 Exit codes                     | TC-EXIT-001..005                        |
| REQ 4 Discoverability                | TC-SCHEMA-001..004                      |
| REQ 5 Configuration                  | TC-CONFIG-001..005                      |
| REQ 6 State/storage/migrations       | TC-MIGRATE-001..004, TC-STORE-001..003  |
| REQ 7 Subscriptions                  | TC-SUB-001..007                         |
| REQ 8 Discovery                      | TC-DISC-001..004                        |
| REQ 9 Polling and dedup              | TC-POLL-001..008                        |
| REQ 10 Conditional requests/fetch    | TC-FETCH-001..008                       |
| REQ 11 Concurrency and politeness    | TC-CONC-001..003                        |
| REQ 12 Item model                    | TC-ITEM-001..005                        |
| REQ 13 Querying history              | TC-ITEMS-001..007                       |
| REQ 14 Parsing and robustness        | TC-PARSE-001..005                       |
| REQ 15 Failure lifecycle             | TC-FAIL-001..008                        |
| REQ 16 Retention                     | TC-PRUNE-001..004                       |
| REQ 17 OPML                          | TC-OPML-001..006                        |
| REQ 18 Scheduling                    | TC-SCHED-001..002                       |
| REQ 19 Command surface               | TC-CMD-001..003                         |
| REQ 20 Constraints                   | TC-NFR-001..005                         |

## Execution Report Template

Record one row per executed case.

| Test Case   | Result (Pass/Fail/Blocked) | Exit Code | Defect ID | Notes |
| ----------- | -------------------------- | --------- | --------- | ----- |
| TC-EXEC-001 |                            |           |           |       |
