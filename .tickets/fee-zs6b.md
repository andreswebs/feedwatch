---
id: fee-zs6b
status: open
deps: [fee-zt9x, fee-c6fa, fee-pfpz, fee-2lbg, fee-o5uq, fee-9ajb, fee-7pg3, fee-47yv, fee-frus, fee-5wk4, fee-0fcw, fee-7emy, fee-igmb, fee-93m0, fee-fxl2]
links: []
created: 2026-08-14T02:40:46Z
type: epic
priority: 1
assignee: Andre Silva
tags: [feature, tags]
---
# epic: feed tags and lane filtering

Dependency gate for the feed-tags feature: a tags column on feeds plus lane-scoped filtering across list, poll, check, items, export, import, prune, and rm, two new commands (tag, tags), and daemon lane scoping. Spec: docs/specs/003-feed-tags/plan.md.

## Design

This epic is a **dependency gate, not a code ticket**, in the pattern of
`fee-63n9`, `fee-8cau`, and `fee-gyos`. Its substantive scope is delivered by
its children; closing it requires verifying they integrate, not writing new
code.

Full specification: [docs/specs/003-feed-tags/plan.md](docs/specs/003-feed-tags/plan.md).
Proposed decomposition and rationale:
[docs/specs/003-feed-tags/tickets.md](docs/specs/003-feed-tags/tickets.md).

## Feature summary

Feeds gain a `tags` column so an agent can address an interest-group "lane":
poll it, query its items, list it, export it, prune it. Today `alias` is a
single unique label and `items.categories` comes from the feed source, so there
is no user-assigned grouping. The workaround (one database per lane) breaks
global dedup.

## Decisions already settled (do not relitigate)

- Storage is a JSON array string in a new `feeds.tags TEXT NOT NULL DEFAULT
  '[]'` column, matching `items.categories`. Migration `0002_feed_tags.sql`.
- The **DB** schema version goes 1 to 2. The existing `core.ErrSchemaTooNew`
  guard already covers an old binary against a migrated store; no new guard
  code.
- The **output contract** version (`feedwatch.SchemaVersion`, ADR 0005) stays
  **1**. Every envelope change is additive except `rm`'s `removed`, which
  becomes an array of URLs on every path (the feature's one breaking CLI
  change, settled). Holding the head at 1 despite ADR 0005's bump rule is a
  deliberate deviation under the pre-1.0 policy, recorded in `CHANGELOG.md`
  and revisited at 1.0.
- Tags are canonicalized on write: trim, lowercase, dedupe, sort. Empty,
  comma-bearing, and whitespace-bearing tags are usage errors (exit 64).
- `--tag` is a `[]string` request field. urfave/cli v3 slice flags accept both
  `--tag a --tag b` and `--tag a,b` and produce the identical value, so the
  spelling carries no semantics: **`--match` is the sole carrier of AND/OR**.
- `--match` is a per-request `Match string` field with `default:"all"`, not a
  root global flag. Invalid value is a usage error.
- Filtering is store-side (SQL `json_each`), which breaks the public
  `store.Store` interface (`DueFeeds` gains a filter). Acceptable pre-1.0;
  must be recorded in `CHANGELOG.md`.
- `poll --tag`/`check --tag` combined with positional feed refs is a usage
  error (exit 64), not a warning.
- `add --tag`: omitted preserves existing tags, given replaces the set.

## Children

| Ticket | Scope                                                |
| ------ | ---------------------------------------------------- |
| T1     | `core` tag type, canonicalization, filter fields     |
| T2     | Migration `0002_feed_tags.sql`                       |
| T3     | SQLite: feed tags read/write and filtering           |
| T4     | SQLite: item and prune filtering by tag              |
| T5     | `InMemoryStore` parity                               |
| T6     | `tag` command                                        |
| T7     | `tags` command                                       |
| T8     | `FeedView.tags` and `add --tag`                      |
| T9     | `list --tag` and the `--match` pattern               |
| T10    | `poll --tag` and `check --tag`                       |
| T11    | `items --tag`                                        |
| T12    | `prune --tag` and `rm --tag`                         |
| T13    | OPML tags round-trip                                 |
| T14    | Daemon lane scoping                                  |
| T15    | Docs, changelog, manual QA                           |

## Closing procedure

No new code. Verify, on a binary from `make build`:

```sh
DB="$(mktemp -d)/fw.db"
feedwatch --db "${DB}" migrate --status        # store_schema_version 2, pending 0
feedwatch --db "${DB}" add "${FEED}" --tag ai --tag agents
feedwatch --db "${DB}" tag "${FEED}"           # {"tags":["agents","ai"]}
feedwatch --db "${DB}" tags                    # counts per lane
feedwatch --db "${DB}" list --tag ai
feedwatch --db "${DB}" poll --force --tag ai
feedwatch --db "${DB}" items --tag ai --since 24h
feedwatch --db "${DB}" export --tag ai         # outline carries category="agents,ai"
feedwatch --db "${DB}" poll --tag ai "${FEED}" # exit 64
```

Then confirm the export/import round-trip preserves tags, and that every
command without `--tag` behaves exactly as before on a store with no tags.

## Acceptance Criteria

- Every child ticket is closed.
- `make build` is green (`fmt-check`, `vet`, `lint`, `test`, then compile).
- The smoke sequence in the design section runs end to end on a native binary
  with the documented outputs.
- `feedwatch migrate --status` on a store created before this feature reports
  `store_schema_version: 2, pending: 0` and every pre-existing feed reports
  `"tags":[]`.
- Every command invoked without `--tag` produces byte-identical output to the
  pre-feature binary for an untagged store, except for the additive `tags` key
  on `FeedView` and `rm`'s `removed` becoming an array.
- `feedwatch.SchemaVersion` is still 1, and `CHANGELOG.md` states both the
  `rm` break and why the head did not move.
- A binary built before migration 0002 exits 65 against a migrated store.
