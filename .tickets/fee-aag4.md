---
id: fee-aag4
status: closed
deps: [fee-ah78]
links: []
created: 2026-06-30T22:54:52Z
type: feature
priority: 2
assignee: Andre Silva
parent: fee-bqyy
tags: [beta, cli, items]
---
# Honest handling of missing publication dates

Implements Req 3 of the 002-beta spec
([requirements.md](../docs/specs/002-beta/requirements.md), section 3). Amends
baseline Req 12 (Item Model) and Req 13 (Querying History).

**Breaking change.** This supersedes the baseline behavior that coalesced a null
publication time to the fetch time for filtering and ordering. Publication-axis
`--since`/`--until` queries that previously returned dateless items will no
longer return them; those items remain reachable through the fetch-time axis
added by Req 2 (`fee-ah78`), which is why this ticket depends on it. See
[Appendix A](../docs/specs/002-beta/requirements.md) of the spec.

Today `COALESCE(published_at, fetched_at)` makes a dateless item look "freshly
published" inside a `--since 7d` window (observed: ~21 hashnode-style items
leaked into a 7-day window in a real session). That is dishonest: the publisher
never declared those dates. After this change, a publication-axis window
includes only items that actually carry a publication date, and the count of
items dropped for being dateless is surfaced so the exclusion is never silent.

## Design

- An item with no parseable publication date stores `published_at` as null
  (already the case); a feed omitting a date is valid, not an error.
- On the publication axis, `--since`/`--until` exclude null-`published_at`
  items. The fetch time is never substituted.
- Publication-axis ordering places null items last under `desc` and first under
  `asc`.
- When a publication-axis date filter drops one or more null-date items, the
  envelope reports `omitted_no_date` (the count) and an informational line is
  written to stderr stating the count and axis.
- The fetch axis (Req 2) is unaffected: `fetched_at` is never null.

```sh
feedwatch items --since 7d 2>items.log
# stdout: {"items":[...],"omitted_no_date":21}
# items.log: ... level=INFO msg="excluded items with no publication date" count=21 axis=published
```

## Acceptance Criteria

- An item with no parseable publication date stores `published_at` as null.
- A publication-axis `--since`/`--until` filter excludes null-publication items.
- The fetch time is never substituted for a null publication time when filtering
  or ordering on the publication axis.
- Publication-axis ordering places null-publication items last under `desc`,
  first under `asc`.
- When a publication-axis filter excludes one or more null-publication items,
  the envelope reports the count as `omitted_no_date`.
- That exclusion also emits an informational stderr log line stating the count
  and the axis.
- The fetch-time axis is unaffected.
- The machine-readable `schema` and usage docs reflect the publication axis
  excluding dateless items.

## Implementation Plan

Builds directly on Req 2 (`fee-ah78`), which added `q.TimeField` and switched
`itemFilters` to pick the filter column by axis. Here the publication branch
stops coalescing, and the query reports what it dropped.

1. Stop coalescing on the publication axis. In
   `src/internal/store/sqlite/items.go`, the publication branch of `itemFilters`
   (post-Req-2) filters on `COALESCE(published_at, fetched_at)`; change it to
   filter on `published_at` directly:

   ```go
   axis := "published_at"          // publication axis: nulls excluded by SQL
   if q.TimeField == "fetched" {
       axis = "fetched_at"
   }
   ```

   SQL three-valued logic does the exclusion for free: `published_at >= ?` and
   `published_at <= ?` are NULL (not true) for a null row, so dateless items
   drop out of a publication-axis window automatically.

2. Fix publication-axis ordering. In `itemOrder`, the publication branch is
   `COALESCE(published_at, fetched_at)`; change it to `published_at`:

   ```go
   expr := "published_at"
   if o.Field == "fetched" {
       expr = "fetched_at"
   }
   ```

   SQLite sorts NULL as smaller than any value, so `published_at DESC` already
   puts nulls last and `published_at ASC` puts them first, exactly as required;
   no explicit `NULLS LAST/FIRST` is needed. Note for the deferred Postgres
   backend: Postgres defaults the opposite way and will need explicit
   `NULLS LAST`/`NULLS FIRST`, so keep this null-ordering rule behind the
   `Store` seam.

3. Report the omitted count from the query. The count is intrinsic to the
   filter context, so compute it where the filtering happens rather than adding
   a second `Store` method that would duplicate the filter logic. Add a small
   result type in `src/internal/core/query.go`:

   ```go
   // ItemQueryResult carries the matched items and the number of items excluded
   // from a publication-axis date window solely because their publication time
   // was null. OmittedNoDate is zero on the fetch axis and when no date filter
   // is active.
   type ItemQueryResult struct {
       Items         []Item
       OmittedNoDate int
   }
   ```

   Change the `Store` interface method in `src/internal/store/store.go` to:

   ```go
   QueryItems(ctx context.Context, q core.ItemQuery) (core.ItemQueryResult, error)
   ```

4. Compute the count in SQLite (`src/internal/store/sqlite/items.go`). Only when
   `q.TimeField` is the publication axis and `q.Since != nil || q.Until != nil`,
   run a `COUNT(*)` over the same feed/contains/tombstone predicates (i.e.
   `itemFilters` minus the date clauses) with `published_at IS NULL`. That count
   is `OmittedNoDate`; otherwise it is 0. Factor the non-date predicate builder
   out of `itemFilters` so the count query and the row query share it (locality;
   no drift).

5. Mirror in the double. In `src/internal/testsupport/store.go`: stop using
   `coalesce` for the publication axis in `matchesItemFilters` (compare
   `it.PublishedAt`, excluding nil on a publication-axis date filter); make
   `sortItems` order by `published_at` with nil last under desc / first under
   asc on the publication axis; and have `QueryItems` count and return the same
   `OmittedNoDate`. Keep `coalesce` only where it is still correct (e.g.
   `PruneItems` keeps its own age semantics; do not change prune in this
   ticket).

6. Surface the count in the envelope. In `src/internal/cli/items.go`:
   - `itemsAction` receives `core.ItemQueryResult`; add
     `OmittedNoDate int \`json:"omitted_no_date,omitempty"\`` to both
     `ItemsResult` and `ProjectedItemsResult` (the exclusion applies regardless
     of projection). `omitempty` keeps it absent when zero, matching the EARS
     "WHEN ... excludes one or more items" trigger.
   - when `OmittedNoDate > 0`, emit one info line via the context logger:

     ```go
     if res.OmittedNoDate > 0 {
         loggerFrom(ctx).InfoContext(ctx, "excluded items with no publication date",
             "count", res.OmittedNoDate, "axis", "published")
     }
     ```

7. Docs and schema. Update [usage.md](../docs/usage.md) and the `items` schema
   surface to state that a publication-axis window excludes dateless items and
   reports `omitted_no_date`. Update any baseline text that promised coalescing
   (the design doc is already updated; confirm `usage.md` and REQ 13 in
   [001 requirements.md](../docs/specs/001-initial-implementation/requirements.md)
   no longer promise the old behavior).

## Verification

- Tests (cli + both store implementations): given a mix of dated and dateless
  items, `--since 7d` (publication axis) returns only dated items and reports
  `omitted_no_date` equal to the dropped count; the same query on
  `--time-field fetched` returns all matching items and reports no
  `omitted_no_date`; `--order published desc` lists null-date items last and
  `--order published asc` lists them first; no date filter means dateless items
  are still returned and `omitted_no_date` is absent.
- Assert the stderr info line is emitted only when the count is positive, and
  carries `count` and `axis`.
- SQLite-vs-double parity case for the new exclusion, ordering, and count.
- Regenerate golden fixtures; review the breaking change against the manual QA
  plan ([manual-qa.md](../docs/specs/001-initial-implementation/manual-qa.md)).
- `make build` green; learnings entry under the Req 3 heading in
  [learnings.md](../docs/specs/001-initial-implementation/learnings.md).

## References

- Spec: [requirements.md](../docs/specs/002-beta/requirements.md) section 3 and
  Appendix A.
- Design: [cli-design.md](../docs/cli-design.md), "Item Model and Content" and
  "Querying History".
- Field notes: [usage-learnings.md](../docs/specs/002-beta/usage-learnings.md),
  "published_at can be null" and "Null dates silently coalesce".
- Depends on: `fee-ah78` (Req 2, the fetch-time axis).

## Notes

**2026-06-30T23:54:08Z**

Implemented Req 3 (honest null publication dates). Breaking change: publication-axis --since/--until no longer coalesce a null published_at to fetched_at; dateless items are excluded from publication windows, ordered last (desc)/first (asc), and the dropped count is surfaced as omitted_no_date in the items envelope plus an INFO stderr line (count, axis=published). Fetch axis unchanged. Store change: QueryItems now returns core.ItemQueryResult{Items, OmittedNoDate}; nonDateFilters factored out of itemFilters so the row query and COUNT(*) share predicates. SQLite relies on native NULL-low ordering; noted Postgres will need explicit NULLS LAST/FIRST. Mirrored in the in-memory double. Docs (usage.md, 001 Req 13) and schema (omitted_no_date, omitempty) updated. make build green.
