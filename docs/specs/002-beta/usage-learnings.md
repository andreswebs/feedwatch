# feedwatch Beta - Usage Learnings

## Purpose

This is a practical field guide distilled from operating feedwatch end-to-end
against a real subscription set of 137 feeds: bulk subscribing, seeding history,
querying for "what's new," and diagnosing failures. It is written to seed a
later agent skill document, so it favors concrete commands, the gotchas that
actually bit, and the mental model an agent needs.

It describes the tool's behavior **as it exists today**. Where a behavior is
slated to change by the beta requirements in
[requirements.md](requirements.md), that is flagged inline as
`[changes in 002-beta: Req N]` so the skill author knows what to update once the
beta work lands.

## Mental model

- feedwatch is a one-shot sensor, not a daemon. Each invocation reads persistent
  state, does its work, and exits. The agent (or cron) drives cadence; the tool
  never loops or fetches on its own.
- **stdout carries pure JSON results; stderr carries structured logs and
  structured error objects.** Treat them as two separate channels and capture
  both. Piping stdout into `jq` is always safe because diagnostics never land
  there.
- **Exit codes are the fast outcome signal.** Branch on them before parsing:

  | Code      | Meaning                             |
  | --------- | ----------------------------------- |
  | 0         | full success                        |
  | 1         | usage or configuration error        |
  | 2         | all targeted feeds failed           |
  | 3         | partial success (some feeds failed) |
  | 130 / 143 | interrupted by `SIGINT` / `SIGTERM` |

  Codes 2 and 3 come only from feed-targeting commands, in practice `poll`.

## The single most important gotcha: never discard stderr

Twice during the session, a command looked like it returned "nothing" when it
had actually written a perfectly good error to stderr that had been redirected
to `/dev/null`. The clearest case: an `items --fields title,published_at,feed_url`
call exited non-zero with `{"error":{"category":"usage","message":"--fields:
unknown field \"feed_url\""}}` on stderr and empty stdout. With stderr
discarded, it read as a silent empty result.

Rules that follow from this:

- Capture stderr to a file or keep it visible: `feedwatch poll 2>err.log`.
- An empty or surprising stdout plus a non-zero exit almost always means an
  error object is waiting on stderr. Read it before concluding "no results."
- Per-feed `poll` failures live **only on stderr** today as
  `{"errors":[{feed_url, category, status?, message}]}`; the stdout envelope
  looks clean even when feeds failed. `[changes in 002-beta: Req 1 adds
succeeded/failed counts and a failures[] list to the stdout envelope]`

## Core workflows

### Bulk subscribe via OPML import

For more than a handful of feeds, do not loop `add`. Generate an OPML 2.0
outline from the URL list and import it once:

```sh
OPML="${TMPDIR:-/tmp}/feeds.opml"
{
  printf '<?xml version="1.0" encoding="UTF-8"?>\n<opml version="2.0">\n  <head><title>import</title></head>\n  <body>\n'
  while IFS= read -r url; do
    [ -z "$url" ] && continue
    printf '    <outline type="rss" xmlUrl="%s"/>\n' "$url"
  done < "${FEEDS_TXT}"
  printf '  </body>\n</opml>\n'
} > "${OPML}"

feedwatch import "${OPML}" > import.json 2> import.log
jq '{added, skipped, failed: (.failed|length)}' import.json
```

- import walks the outline recursively, deduplicates against existing
  subscriptions, and reports per-entry failures without aborting on one bad
  entry.
- It uses an outline's `text`/`title` as the alias when that name is free.
  Outlines with no title (like the minimal ones generated above) get **no
  alias**. See "Feed identity churn" for why that matters.
- **Today import does not fetch-validate**: `added` counts subscriptions
  created, not feeds proven reachable. An OPML full of dead or non-feed URLs
  still reports them as added. Always follow an import with a `poll` to learn
  which feeds actually resolve. `[changes in 002-beta: Req 4 makes import
validate by default, with --no-validate to opt out]`

### Seed history, then poll incrementally

```sh
feedwatch poll --force > poll.json 2> poll.log   # seed: every feed, ignore schedule
feedwatch poll          > poll.json 2> poll.log   # incremental: only due feeds, only new items
```

- The **first** poll floods every item a feed advertises as "new" (4250 items
  across 129 feeds in this session), because nothing has been seen before. This
  is expected, not a digest.
- Subsequent polls return only genuinely new items, identified by a
  `(feed_url, dedup_key)` fingerprint, and send conditional-GET headers so an
  unchanged feed costs a `304` with no re-parse.
- A poll's stdout envelope is `{polled, skipped, new_items, items:[...]}`.
  `polled` currently counts every attempted feed, **including failures**.
  `[changes in 002-beta: Req 1]`

### Query stored history

```sh
feedwatch items --since 7d --order "published desc" \
  --fields title,link,published_at,summary --feed "${ALIAS_OR_URL}" \
  > items.json 2> items.log
```

- **Always pass `--fields` for triage.** The default projection returns full
  `content_html`/`content_text`, which is enormous over hundreds of items.
- `--order` takes a two-token string (`published desc`, `fetched asc`); quote
  it. Default is `published desc`.
- `--since`/`--until` accept RFC3339 or relative forms like `24h`, `7d`.
- `--contains` substring-matches title and content; `--limit`/`--offset`
  paginate.
- Reference a feed by **alias or URL** (`--feed` resolves either). Prefer the
  alias; it is stable across URL churn.
- `feed_url` is an always-on identity field returned in every item, but naming
  it inside `--fields` is currently a hard usage error. Do not list it.
  An unknown field name fails the whole query with exit 1. `[changes in
002-beta: Req 5 accepts feed_url as a no-op and adds a did-you-mean hint]`

### Inspect and manage feed health

```sh
feedwatch list 2>/dev/null | jq -r '.feeds[] | select(.failures>0 or .status!="active")
  | "\(.status)\tfails=\(.failures)\t\(.url)\t\(.last_error)"'

feedwatch disable "${FEED}"   # poll skips it until re-enabled
feedwatch enable  "${FEED}"   # resets the failure lifecycle, makes it due again
```

- A feed that reaches the consecutive-failure threshold (default `10`) is
  auto-disabled and surfaced in `list`. `enable` resumes it.

## Data-quality realities

These are the lessons that most changed how to read the data. Feeds are messy;
the store faithfully reflects that mess.

### `published_at` can be null, and that is usually normal

A null `published_at` almost always means the publisher simply did not provide a
date, **not** that the feed is malformed. In RSS 2.0 the item `<pubDate>` is
optional; some generators omit it entirely (observed: `hashnode.com` emits zero
date elements on any item). In Atom, `<updated>` is required but `<published>`
is optional, so an Atom item can validly have a null publication time. The rare
"actually malformed" case is a date element present but in an unparseable
format.

### Null dates silently coalesce into recent windows (today)

Today, a null `published_at` coalesces to the item's fetch time for `--since`,
`--until`, and `--order`. The field still shows `null` in output, but the filter
already matched it on fetch time. The effect: dateless feeds always look
"freshly published" in a `--since 7d` window. In this session ~21 items
(hashnode and one other) leaked into the 7-day window this way. `[changes in
002-beta: Req 3 excludes null-published items from the publication axis and
adds the fetch-time axis in Req 2 as the reliable freshness signal]`

### Do not assume a timestamp cluster is an artifact: verify against the raw feed

The costliest mistake of the session: seeing 70 `aihero.dev` items stamped at an
identical timestamp near the poll time, I concluded they were null dates
coalescing to fetch time. **They were not.** Fetching the raw feed showed all 70
"AI Coding Dictionary" entries carry a real, valid
`<pubDate>Mon, 29 Jun 2026 15:38:46 GMT</pubDate>`, stable across re-fetches: a
genuine publisher batch-publish that happened to fall in the poll hour. The
lesson: when a distribution looks suspicious, `curl` the feed and inspect the
actual date elements before blaming the tool.

```sh
curl -sL "${FEED_URL}" | grep -oiE '<(pubDate|published|updated)>[^<]*' | head
```

### Other realities

- **Future dates exist.** Feeds publish events or scheduled posts with dates
  ahead of now (observed: an event dated two weeks out). A digest should not
  assume the newest timestamp is "today."
- **Building a real "what's new" digest** means more than `--since 7d`: group by
  feed, look at the per-feed timestamp distribution, identify and explain
  clusters, and once available prefer the fetch-time axis for the reliable
  "arrived recently" question.

## Feed identity churn

A feed's stored URL is **not stable**. When `poll` follows a permanent redirect
(HTTP `301`/`308`), it renames the feed to the canonical URL and cascades the
rename to that feed's items. Observed: `https://aihero.dev/rss.xml` (a 308) was
silently stored as `https://www.aihero.dev/rss.xml`, after which
`items --feed https://aihero.dev/rss.xml` returned zero with no hint.

What to do about it:

- **Set an alias at subscribe time.** The alias column is untouched by a URL
  rename, so an alias is the durable handle: `items --feed "${ALIAS}"` keeps
  working through redirects. This is the single best mitigation, and it is why
  alias-less bulk imports are fragile.
- If you only have the original URL and a query returns nothing, run `list` and
  match on title/domain to recover the current canonical URL.
- `[changes in 002-beta: Req 6 makes poll report renames as a renamed[] list
plus a stderr line, so the new URL is learned at rename time]`

## Failure taxonomy

Per-feed failures are categorized; use the category to decide whether a feed is
worth keeping:

- `network` - DNS failure (`no such host`), TLS problems (certificate expired,
  or valid for a different host). Often the URL is wrong or the publisher's
  problem.
- `http` - a status like `404` (feed moved or wrong path) or `5xx`.
- `parse` - `Failed to detect feed type`, which in practice means the URL
  returns HTML, not a feed. Use `discover` to find the real feed URL.
- `timeout` - exceeded the per-feed deadline.

Transient outcomes (timeout, `5xx`, `429`, genuine transport errors) are retried
up to 3 attempts within a single invocation (honoring `Retry-After` on `429`);
deterministic ones (`404`, DNS `no such host`, certificate errors, parse
failures) are not retried.

In this session 8 of 137 feeds failed on the first poll: two `404`, one DNS, two
TLS, three "not a feed" (HTML). None were auto-disabled, because each had only a
single failure against the threshold of 10.

## Discovery: turning a broken or HTML URL into a feed

```sh
feedwatch discover "${SITE_OR_FEED_URL}" 2>/dev/null | jq '.candidates'
```

`discover` is read-only. It performs `<link rel="alternate">` autodiscovery,
then a bounded probe of common feed paths, validates each candidate by actually
parsing it, and tags each with a `source` of `autodiscovery` or `probe`. It is
the right tool for the `parse` ("not a feed") and `404` failures above: find the
real URL, then `rm` the broken subscription and `add` the discovered one.

## Use the machine contract, not just prose

`feedwatch schema` (and `feedwatch schema <command>`) emits the machine-readable
interface: each command's arguments and flags with types and defaults, its exit
codes, and a JSON Schema for its output envelope. During this session navigation
leaned mostly on the prose usage docs; for an agent, `schema` is the intended
discovery surface and should be the first stop when wiring up calls
programmatically or validating output shape.

## Scheduling

feedwatch never loops. Drive cadence externally and keep the two streams
separated so results append cleanly while errors collect apart:

```sh
# crontab: poll every 30 minutes
*/30 * * * * feedwatch poll >> "${HOME}/feed-items.jsonl" 2>> "${HOME}/feedwatch.log"
```

A systemd `oneshot` service plus timer works equally well; the agent or operator
owns the cadence, and feedwatch stays a simple externally-driven sensor.

## Condensed checklist for the skill author

- Capture both streams; never `2>/dev/null` when failures matter.
- Branch on exit codes (0/1/2/3) before parsing.
- Bulk subscribe with OPML `import`; then `poll` to learn what is actually
  reachable (import does not validate today).
- First `poll --force` seeds; later `poll` is incremental.
- Always `--fields` for triage; do not name `feed_url`; unknown field = exit 1.
- Treat `published_at: null` as normal (optional attribute), and know it
  coalesces into recent windows today.
- Verify suspicious timestamp clusters against the raw feed before calling them
  artifacts.
- Set aliases at subscribe time; stored URLs change under permanent redirects.
- Use `discover` to repair `parse`/`404` feeds; `enable`/`disable` and the
  10-failure auto-disable govern the lifecycle.
- Prefer `feedwatch schema` as the programmatic contract.
