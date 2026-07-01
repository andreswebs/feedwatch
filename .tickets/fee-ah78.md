---
id: fee-ah78
status: closed
deps: []
links: []
created: 2026-06-30T22:54:52Z
type: feature
priority: 2
assignee: Andre Silva
parent: fee-bqyy
tags: [beta, cli, items]
---
# Fetch-time query axis (fetched_at field and --time-field)

Implements Req 2 of the 002-beta spec
([requirements.md](../docs/specs/002-beta/requirements.md), section 2). Amends
baseline Req 12 (Item Model) and Req 13 (Querying History). Purely additive.

feedwatch already records the moment it first stored each item, but
`Item.FetchedAt` is tagged `json:"-"` (hidden) and there is no way to filter on
it: `--since`/`--until` always filter on the publication time. An agent cannot
reliably answer "what newly arrived" for feeds that omit or mis-format
publication dates. This change exposes `fetched_at` as a selectable field and
adds `--time-field` to choose whether the time window filters on publication or
fetch time. It is the foundation the honest-null-date change (Req 3, `fee-aag4`)
builds on, which is why this ticket lands first.

## Design

- `fetched_at` becomes part of the item JSON (RFC3339 UTC), present on every
  item and never null, alongside `published_at`. It is returned by default and
  is selectable via `--fields fetched_at`.
- A new `--time-field published|fetched` flag (default `published`) chooses
  which time `--since`/`--until` filter on.
- `--order published|fetched asc|desc` is unchanged and remains independent of
  `--time-field`: an agent can window on fetch time but sort by publication
  time, or vice versa.

```sh
feedwatch items --since 7d --time-field fetched --order fetched desc
feedwatch items --feed godev --fields title,link,published_at,fetched_at
```

## Acceptance Criteria

- `fetched_at` is exposed as a selectable item field and is populated for every
  stored item (never null).
- `--time-field` selects the publication (`published`) or fetch (`fetched`)
  axis for `--since`/`--until`, defaulting to `published`.
- Selecting the fetch axis matches items by their fetch time.
- Ordering by publication or fetch time still works independently of the
  selected filter axis.

## Implementation Plan

No schema migration is needed: the `items` table already has a populated
`fetched_at` column (see `insertItem` in
`src/internal/store/sqlite/items.go`), and `scanItem` already reads it into
`Item.FetchedAt`. The work is exposing the field and threading a filter-axis
choice through the query.

1. Expose the field. In `src/internal/core/types.go` change the tag:

   ```go
   FetchedAt time.Time `json:"fetched_at"`
   ```

   This makes `fetched_at` appear in every item-bearing envelope, including
   `poll` items and the default (unprojected) `items` output. Expect golden
   fixtures for `poll` and `items` to change.

2. Make it selectable and projectable. In `src/internal/core/query.go`:
   - add `"fetched_at": true` to `ValidItemFields`;
   - add a `case "fetched_at": out["fetched_at"] = it.FetchedAt` to
     `ProjectItem`.

   In `src/internal/store/sqlite/items.go` add `"fetched_at": "fetched_at"` to
   `fieldColumns` for completeness (the column is already in `alwaysColumns`, so
   it is selected regardless, but the mapping keeps the two tables consistent).
   Mirror the field in the `testsupport` double: add `"fetched_at": true` to
   `projectedFields` and copy it in `project` (it is already always retained
   there).

3. Add the filter-axis to the query model. In `src/internal/core/query.go` add
   to `ItemQuery`:

   ```go
   TimeField string // "published" | "fetched"; "" means published
   ```

4. Parse the flag. In `src/internal/cli/items.go`:
   - register `&cliv3.StringFlag{Name: "time-field", Value: "published",
     Usage: "axis for --since/--until: 'published' or 'fetched'"}`;
   - in `buildItemQuery`, read and validate it, defaulting to `published` and
     rejecting anything else as a usage error (exit 1), mirroring
     `parseItemOrder`:

     ```go
     tf := cmd.String("time-field")
     switch tf {
     case "", "published":
         q.TimeField = "published"
     case "fetched":
         q.TimeField = "fetched"
     default:
         return core.ItemQuery{}, usageErr("--time-field must be 'published' or 'fetched', got " + strconv.Quote(tf))
     }
     ```

5. Apply the axis in the filter. In `src/internal/store/sqlite/items.go`,
   `itemFilters` currently filters on `COALESCE(published_at, fetched_at)` for
   both bounds. Pick the column from `q.TimeField`:

   ```go
   axis := "COALESCE(published_at, fetched_at)" // publication axis (unchanged here)
   if q.TimeField == "fetched" {
       axis = "fetched_at"
   }
   ```

   Use `axis` for the `>= ?` (since) and `<= ?` (until) clauses. Leave
   `itemOrder` as it is; `--order` already supports both fields. The publication
   branch keeps coalescing for now; Req 3 (`fee-aag4`) removes that.

6. Mirror in the double. In `src/internal/testsupport/store.go`,
   `matchesItemFilters` uses `coalesce(it)` for the date comparison; switch the
   compared value on `q.TimeField` (use `it.FetchedAt` when `fetched`, else
   `coalesce(it)`) so the in-memory store and SQLite stay behaviorally
   identical.

7. Docs and schema. The item JSON shape is `jsonschema:"opaque"`, so the
   reflected schema does not enumerate item fields; update the prose and the
   `items` flag list in [usage.md](../docs/usage.md) and the schema command's
   documented surface to describe `fetched_at` and `--time-field`.

## Verification

- Tests (cli + both store implementations): `--fields fetched_at` returns
  `feed_url` + `fetched_at`; default `items` output includes `fetched_at`;
  `--time-field fetched --since <t>` matches on fetch time while
  `--time-field published` (default) matches on publication time; an invalid
  `--time-field` value exits 1 with a usage error; `--order` still sorts
  independently of `--time-field`.
- Add a SQLite-vs-double parity case so both back the same axis behavior.
- Regenerate golden fixtures touched by the newly visible `fetched_at`.
- `make build` green; learnings entry under the Req 2 heading in
  [learnings.md](../docs/specs/001-initial-implementation/learnings.md).

## References

- Spec: [requirements.md](../docs/specs/002-beta/requirements.md) section 2.
- Design: [cli-design.md](../docs/cli-design.md), "Item Model and Content" and
  "Querying History".
- Field notes: [usage-learnings.md](../docs/specs/002-beta/usage-learnings.md),
  "Null dates silently coalesce into recent windows".
- Blocks: `fee-aag4` (Req 3) and `fee-n4p6` (Req 5) depend on this ticket.

## Notes

**2026-06-30T23:27:57Z**

Implemented Req 2: exposed Item.FetchedAt as json:"fetched_at" (selectable via --fields, present on every item, never null) and added --time-field published|fetched (default published) selecting the --since/--until axis, independent of --order. Changes: core/types.go (tag), core/query.go (ValidItemFields+ProjectItem+ItemQuery.TimeField), cli/items.go (flag+validation, usage err on bad value), store/sqlite/items.go (axis switch in itemFilters; fieldColumns), testsupport/store.go (matchesItemFilters axis + projectedFields). FIXED a surfaced SQLite/double parity bug: SQLite UpsertItems did not write the resolved fetch time back into returned items, so poll reported fetched_at as Go zero time; now resolved once at the UpsertItems entry point (regression test TestUpsertItemsResolvesFetchedAt). Also corrected pre-existing stale e2e poll goldens (missing succeeded/failed/failures from fee-e1s2; suite was green only via test cache) and added a reFetchedAt normalizer to the e2e harness for the volatile timestamp. Tests added in core/cli/sqlite/testsupport; usage.md updated; learnings appended. make build green. Unblocks fee-aag4 and fee-n4p6.
