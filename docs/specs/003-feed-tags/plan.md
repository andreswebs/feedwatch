# feedwatch: Feed Tags & Lane Filtering

**Feature:** Native tagging on feeds to support interest-group "lanes"

**Author:** Hermes (agent) — primary feedwatch user

**Status:** Spec, decisions resolved, ready for ticket breakdown

**Target version:** v0.0.5+

## Problem

The `feeds` table has no grouping concept. `alias` is a single unique label per
feed; `items.categories` comes from the feed source (not user-assigned). There is
no way to:

- Poll a subset of feeds by interest group
- Query items scoped to a lane
- List/export feeds per lane
- Produce per-lane digests

The current workaround (multiple databases) breaks global dedup. An external
manifest is invisible to feedwatch and rots independently of the DB.

## Design

### Schema migration

A new migration `0002_feed_tags.sql` adds a `tags` column to `feeds`:

```sql
ALTER TABLE feeds ADD COLUMN tags TEXT NOT NULL DEFAULT '[]';
```

Stored as a JSON array string, consistent with `items.categories` and
`items.enclosures`:

```text
tags: '["agents","ai","security"]'
```

Existing feeds get `'[]'`. No data backfill: users add tags after upgrading.

Dedup is unaffected. Tags live on `feeds`; the item dedup key is
`(feed_url, dedup_key)`. A feed with multiple tags still stores each item once.

#### Two version numbers, only one moves

The DB schema version goes 1 → 2. The **output contract** version
(`feedwatch.SchemaVersion`, ADR 0005) stays **1**.

Every envelope change here is additive except one: `rm`'s `removed` becomes an
array (see "`prune` and `rm`" below). ADR 0005 says the head integer is bumped
on breaking shape changes, so holding it at 1 is a deliberate deviation, not an
oversight. The pre-1.0 policy stated at the top of `CHANGELOG.md` already
permits breaking changes in minor releases, and bumping the head would signal a
whole-contract generation change to consumers of all sixteen commands over one
field on one command. Revisit at 1.0, when the head becomes a real
compatibility promise. The deviation is recorded in the changelog entry.

No new guard code is needed for old binaries. `Migrate` already returns
`core.ErrSchemaTooNew` when the stored version exceeds the binary's highest
migration, which the error boundary maps to exit 65.

### Tag names

Tags are canonicalized on every write: trimmed, lowercased, deduplicated, and
stored sorted. So `--tag AI` and `--tag ai` name the same lane, and the stored
JSON is byte-stable for golden tests.

A tag that is empty, or that contains a comma or whitespace, is a usage error
(exit 64). Commas are illegal because `[]string` flags accept a comma-separated
spelling (see below); whitespace is illegal because it makes tags unquotable in
the shell and in cron scripts. No other content restriction applies.

### Tag selection syntax

Every tag-filtered command takes a repeatable `--tag` and a `--match`:

```bash
feedwatch list --tag ai --tag agents
feedwatch list --tag ai,agents            # identical to the line above
feedwatch list --tag ai,agents --match any
```

`[]string` flags in this CLI already accept both the repeated and the
comma-separated spelling (as `items --fields` documents), and the two parse to
the same value. **`--match` is therefore the only carrier of AND/OR semantics**;
the spelling of `--tag` carries none.

| `--match`       | Meaning                           |
| --------------- | --------------------------------- |
| `all` (default) | Feed must have ALL specified tags |
| `any`           | Feed must have AT LEAST ONE       |

`--match` is a `Match string` field with `default:"all"` on each filtering
request struct, not a root-level global flag: it is meaningless for `add`,
`tag`, `migrate`, `discover`, and `schema`, and keeping it in the request types
preserves the ADR 0007 projection (the library API carries the option; the CLI
surface is derived from it). An unrecognized value is a usage error (exit 64),
raised from the request's `Validate`.

SQLite has no array type, so matching uses `json_each(tags)`:

```sql
-- match=all: feed has all requested tags
SELECT f.url FROM feeds f
WHERE (
  SELECT count(DISTINCT value) FROM json_each(f.tags)
  WHERE value IN ('ai', 'agents')
) = 2;

-- match=any: feed has at least one requested tag
SELECT f.url FROM feeds f
WHERE EXISTS (
  SELECT 1 FROM json_each(f.tags)
  WHERE value IN ('ai', 'agents')
);
```

### Where filtering happens

Filtering is **store-side**, pushed into SQL rather than applied in Go after a
full read. This keeps `--limit`/`--offset` correct on `items` and avoids loading
every feed to answer a lane query. It costs a change to the public,
embedder-facing store API:

```go
// core
type ListFilter struct {
    Status FeedStatus
    Tags   []string   // empty matches every feed
    Match  TagMatch   // "all" (zero value) or "any"
}

type ItemQuery struct {
    // ...existing fields...
    Tags  []string
    Match TagMatch
}

type PrunePolicy struct {
    KeepBefore *time.Time
    MaxPerFeed int
    Tags       []string
    Match      TagMatch
}

// store.Store
DueFeeds(ctx context.Context, now time.Time, f core.ListFilter) ([]core.Feed, error)
```

`DueFeeds` gaining a filter is what lets a lane be polled **on its own
schedule** rather than only under `--force`. This is a breaking change to
`store.Store`; every semantic must be mirrored in
`internal/testsupport.InMemoryStore`, which the store double is required to
track exactly.

`items` filtering joins `items` to `feeds` on `feed_url` and applies the same
`json_each` predicate.

### CLI surface

#### `add` — tag at creation time

```bash
feedwatch add URL --alias NAME --interval 30m --tag ai --tag agents
```

`--tag` is optional and repeatable. On the idempotent re-add path, **an omitted
`--tag` preserves the feed's existing tags; a given `--tag` replaces the whole
set.** A routine re-add therefore never silently drops a feed out of its lanes,
while an explicit `--tag` remains declarative.

#### `tag` — manage tags on existing feeds

A single flat command, consistent with the tree's no-nesting rule:

```bash
feedwatch tag REF                        # read
feedwatch tag REF --add ai --add agents  # idempotent, no duplicates
feedwatch tag REF --remove security
feedwatch tag REF --set ai,agents,research
feedwatch tag REF --clear
```

`REF` is the feed URL or its alias, resolved as every other feed-scoped command
resolves it. `--set`, `--clear`, and the `--add`/`--remove` pair are mutually
exclusive, enforced in `TagRequest.Validate` (exit 64). `--remove` of a tag the
feed does not carry is a no-op, not an error.

Read mode (no write flag):

```json
{
  "schema_version": 1,
  "ok": true,
  "url": "https://example.com/feed",
  "tags": ["agents", "ai"]
}
```

Write mode adds the delta, so a caller sees what actually changed rather than
what it asked for:

```json
{
  "schema_version": 1,
  "ok": true,
  "url": "https://example.com/feed",
  "tags": ["agents", "ai", "research"],
  "added": ["research"],
  "removed": []
}
```

`added` and `removed` are always present, empty (`[]`) when nothing changed.

#### `tags` — the lane vocabulary

```bash
feedwatch tags
```

Reports every distinct tag with the number of subscriptions carrying it, sorted
by tag name, counting feeds of any status. It takes no flags.

```json
{
  "schema_version": 1,
  "ok": true,
  "tags": [{ "tag": "agents", "feeds": 12 }, { "tag": "ai", "feeds": 31 }]
}
```

#### `list` — filter by tag

```bash
feedwatch list --tag ai --tag agents
feedwatch list --tag ai,agents --match any
```

This reverses `ListRequest`'s current "list carries no filters; narrowing is a
query concern `items` covers" stance, which its doc comment must be rewritten to
reflect. A lane is a property of the subscription, not of the item history, so
it belongs on `list`. `--status` is **not** added at the same time; it stays out
of scope.

Every `FeedView` (shared by `list`, `enable`, `disable`, and `rm`) gains a
`tags` array, always present and `[]` when empty, so tags are observable
wherever a feed is reported.

#### `poll` — poll a lane

```bash
feedwatch poll --tag ai                       # due feeds in the lane
feedwatch poll --force --tag ai               # every active feed in the lane
feedwatch poll --tag ai,security --match any
```

`--tag` alone narrows the **scheduled** selection (`DueFeeds` with the filter);
`--force --tag` narrows the force selection (`ListFeeds(active)` with the
filter). A lane can therefore run on its own cadence.

`--tag` combined with positional feed refs is a **usage error, exit 64** — not a
warning with one side silently ignored. Naming feeds and naming a lane are two
different selections, and the tree already treats an unresolvable ref as a hard
failure rather than a silent narrowing.

Feeds with `tags: '[]'` are excluded from any tag-filtered selection and
included in every unfiltered one.

#### `items` — query items by lane

```bash
feedwatch items --tag ai --since 24h
feedwatch items --tag ai --tag agents --limit 50
```

Output shape unchanged; the same `items[]` array, filtered.

#### `check` — validate a lane

```bash
feedwatch check --tag infra
```

Same selection rules as `poll --tag`, including the "`--tag` plus positional
refs is exit 64" rule.

#### `export` — OPML per lane

```bash
feedwatch export --tag ai -o ai-lane.opml
```

Each `<outline>` carries a `category` attribute listing the feed's tags,
comma-separated, per the OPML 2.0 convention. Commas are illegal inside a tag
name, so the encoding is unambiguous.

#### `import` — OPML tags round-trip

`import` reads the `category` attribute back into the feed's tags, canonicalizing
each entry by the same rules as `--tag`. An unparseable or empty `category` is
ignored rather than failing the outline. Without this, `export` would emit an
attribute nothing reads and the documented export/import round-trip would lose
data.

#### `prune` and `rm` — lane-scoped

```bash
feedwatch prune --tag ai --keep-days 30
feedwatch rm --tag stale-lane
```

`prune` narrows which feeds' history is pruned (`PrunePolicy.Tags`); it does
not authorize a prune, so a bare `prune --tag ai` with no `--keep-days` or
`--max-items` is still a usage error.

`rm` unsubscribes every feed in the lane. A ref and `--tag` together, and
neither of them, are both usage errors. **`RmResult.removed` becomes an array
of canonical URLs on every path**, including single-ref `rm`, so a caller never
parses two shapes for one command:

```json
{ "schema_version": 1, "ok": true, "removed": ["https://example.com/feed"] }
```

This is the feature's one breaking output-contract change; `removed` shipped as
a bare string in v0.0.4. Consumers read `.removed[0]` for a single feed.

### Daemon

The daemon gains `WithTags([]string)` and `WithMatch(TagMatch)` options, which
populate the `PollRequest` it already issues, so one long-running process can
watch a single lane. No new poll path.

### Untagged feeds

A feed with `tags: '[]'` is not matched by any `--tag` query. It is included in
every command run without `--tag`. All existing invocations behave identically
for feeds that are never tagged.

### Exit codes

No new exit codes. Tags are a filter, not a new operational mode; the existing
table (0/2/3/64/65/69/70/78) covers every case.

| Condition                                                                 | Exit code |
| ------------------------------------------------------------------------- | --------- |
| `--tag` on a command that does not support it                             | 64        |
| `feedwatch tag` with conflicting write flags                              | 64        |
| Empty tag, or a tag containing a comma or whitespace                      | 64        |
| `--match` with a value other than `all` or `any`                          | 64        |
| `--tag` combined with positional feed refs on `poll` or `check`           | 64        |
| Stored schema newer than the binary (old binary, migrated DB)             | 65        |

### Edge cases

| Case                                          | Behavior                                                                                   |
| --------------------------------------------- | ------------------------------------------------------------------------------------------ |
| Duplicate tags in one invocation              | Canonicalization dedupes; `--add` is idempotent                                            |
| `--tag ""`                                    | Rejected, exit 64                                                                          |
| `--tag "machine learning"` (whitespace)       | Rejected, exit 64                                                                          |
| `--tag AI` versus `--tag ai`                  | Same lane; stored lowercased                                                               |
| Feed has tags but is `disabled`               | Included in `list`/`export`/`tags`; skipped by `poll`/`check` (status filter applies first) |
| `--tag` naming a tag no feed carries          | Empty result, exit 0; not an error                                                          |
| `--remove` of a tag the feed lacks            | No-op; `removed` is `[]`                                                                   |
| Re-add without `--tag`                        | Existing tags preserved                                                                    |

## Cron integration

Per-lane digests become one script per lane against one database, so global
dedup is preserved:

```bash
#!/usr/bin/env bash
set -o errexit -o nounset -o pipefail
export FEEDWATCH_DB="${FEEDWATCH_DB:-${HOME}/.local/state/feedwatch/feedwatch.db}"

feedwatch poll --force --tag ai --timeout 20s --concurrency 12

feedwatch items --tag ai --since 24h \
  --fields title --fields feed_url --fields link --fields summary \
  --limit 0
```

## Migration path

1. `feedwatch migrate` adds the column, automatically and idempotently
2. The user tags existing feeds: `feedwatch tag REF --add ai --add agents`
3. Tag-filtered commands become useful immediately
4. Unfiltered commands are unaffected

No data loss, no mandatory retagging, and no schema downgrade path (an older
binary against a migrated store exits 65, as designed).

Two breaking changes ride along, both acceptable pre-1.0 and both required to
be recorded in `CHANGELOG.md`:

- **CLI contract**: `rm`'s `removed` becomes an array of URLs.
- **Go API**: `store.Store.DueFeeds` takes a `core.ListFilter`, and the
  interface gains `SetTags` and `TagCounts`, so an embedder with a custom
  backend must implement them.

Every other command's output is unchanged for an untagged store apart from the
additive `tags` key on feed views.

## Out of scope

- Bulk tagging by predicate (`tag --all-untagged --add unsorted`); tagging stays
  explicit per feed, and `rm --tag` is the only bulk operation added
- `list --status`
- Tag hierarchies, aliases, or renaming a tag across all feeds
- Postgres backend support for the new predicate, which is deferred with the
  rest of the Postgres backend
