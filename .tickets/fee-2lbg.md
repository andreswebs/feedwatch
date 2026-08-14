---
id: fee-2lbg
status: open
deps: [fee-zt9x, fee-c6fa]
links: []
created: 2026-08-14T02:43:46Z
type: task
priority: 1
assignee: Andre Silva
parent: fee-zs6b
tags: [store, tags]
---
# sqlite: item query and prune filtering by tag

Honor ItemQuery.Tags/Match in QueryItems via a subquery inside nonDateFilters (so countOmittedNoDate stays in sync and LIMIT/OFFSET pagination stays correct), and honor PrunePolicy.Tags/Match on both prune statements including the inner ROW_NUMBER source.

## Design

Teach the SQLite store to narrow item queries and prune passes by feed tag.
This is the `items`-table half of the store work, split from the `feeds` half
(T3) because the interaction between a tag filter and `LIMIT`/`OFFSET` is the
risky part of the feature and deserves its own test pass.

Spec: [docs/specs/003-feed-tags/plan.md](docs/specs/003-feed-tags/plan.md),
sections "Where filtering happens" and the `items` / `prune` command sections.

## 1. `QueryItems` — a subquery, not a JOIN

`internal/store/sqlite/items.go` assembles the query in `QueryItems` as
`SELECT <projectedColumns> FROM items <where> <order> [LIMIT ? OFFSET ?]`, with
the WHERE built by `itemFilters` on top of `nonDateFilters`. `LIMIT`/`OFFSET`
are applied **in SQL** (only when `q.Limit > 0`), so the tag filter must be
part of the WHERE clause, not applied afterwards in Go, or pagination silently
returns short pages.

Add the tag clause **inside `nonDateFilters`**, next to the existing feeds
filter, as a subquery rather than a JOIN:

```sql
feed_url IN (SELECT url FROM feeds WHERE <tagPredicate("feeds.tags", …)>)
```

Two reasons this beats a real JOIN, both worth keeping in a comment:

- `nonDateFilters` is shared by `QueryItems` and `countOmittedNoDate`, so one
  edit keeps the row query and the omitted-count query in sync. That sharing is
  the file's stated design intent.
- A `JOIN feeds ON feeds.url = items.feed_url` makes `updated_at` ambiguous
  (both tables have it) and would force every column reference in
  `itemColumns`, `alwaysColumns`, and `scanItem` to be qualified.

The existing feeds filter and the tag filter compose with `AND`, so
`items --feed X --tag ai` means "items from feed X, which must also be in lane
ai" — an intersection. That is the correct reading and needs no special case.

Reuse `tagPredicate` from T3. If it lives in `feeds.go`, either move it to a
new `internal/store/sqlite/tags.go` in this ticket or leave it where T3 put it
and import it in-package; do not write a second copy.

## 2. `PruneItems` — scope both statements

`internal/store/sqlite/prune.go` runs two independent `UPDATE`s that tombstone
rather than delete. A tag scope means an extra
`AND feed_url IN (SELECT url FROM feeds WHERE <tagPredicate>)` on:

- the age-prune `WHERE`, and
- **both** the outer `WHERE` and the inner `FROM items WHERE tombstoned = 0` of
  the max-per-feed `ROW_NUMBER()` statement.

The inner one is the subtle part: leaving the window's source unscoped would
rank a feed's rows against every other feed's rows, so the `rn > N` cutoff
would tombstone the wrong items. Call this out in a comment.

Both statements are currently hardcoded strings with a single bound parameter.
Converting them to `strings.Builder` plus an accumulating `[]any` is required;
follow the `SetValidators` pattern in `feeds.go`.

## TDD plan

External `package sqlite_test` in `sqlite_test.go`, reusing `newStore(t)`,
`addTestFeed`, `fixedClock`, and `ptrTime`. Fixture shape for the query tests:
three feeds — one tagged `["ai","agents"]`, one tagged `["ai"]`, one untagged —
each with several items at distinct publication times, so ordering and
pagination are observable.

1. **(tracer)** `QueryItems` with `Tags: ["ai"]` returns only items from the two
   tagged feeds; the untagged feed's items are absent.
2. `MatchAll` with `["ai","agents"]` returns only the first feed's items;
   `MatchAny` returns both tagged feeds' items.
3. A tag filter composes with `Feeds` (intersection), with `Since`/`Until`, and
   with `Contains`.
4. **Pagination correctness**: with a tag filter plus `Limit: 2, Offset: 2`, the
   returned page is the third and fourth items *of the filtered set*, not of the
   unfiltered set. This is the behavior a Go-side filter would get wrong; assert
   it explicitly against a hand-computed expectation.
5. `OmittedNoDate` counts only items inside the tag filter — an undated item on
   an out-of-lane feed does not inflate the count.
6. A tag matching no feed returns an empty result and no error.
7. `PruneItems` with `KeepBefore` and a tag filter tombstones only in-lane
   items; an equally old out-of-lane item survives.
8. `PruneItems` with `MaxPerFeed` and a tag filter keeps N per **in-lane** feed
   and leaves out-of-lane feeds entirely untouched — the test that catches an
   unscoped inner `ROW_NUMBER()` source.
9. A pruned in-lane item keeps its `(feed_url, dedup_key)` fingerprint, so a
   re-poll does not resurrect it (the existing dedup invariant must survive tag
   scoping).

## Gotchas

- `gosec` G202: the two prune statements must be assembled with
  `strings.Builder`, not `+`.
- Argument order must follow clause order exactly; the placeholder list appears
  once per predicate instance, and the max-per-feed statement now has the
  predicate **twice**, so its arguments must be appended twice.
- `countOmittedNoDate` rebuilds its own SQL from `nonDateFilters`; verify by
  test (behavior 5), not by inspection, that it picked the tag clause up.
- Items themselves carry no tags. Do not add a tag column to `items`, do not
  extend `core.ValidItemFields`, and do not touch `core.ProjectItem` — a tag is
  a property of the subscription, and `items.categories` (feed-supplied) is a
  different concept that must not be conflated with it.

## Acceptance Criteria

- `QueryItems` honors `ItemQuery.Tags`/`Match` via a `feed_url IN (SELECT url
  FROM feeds WHERE ...)` clause added inside `nonDateFilters`, so
  `countOmittedNoDate` picks it up with no second edit.
- The tag filter is applied in SQL before `LIMIT`/`OFFSET`, proven by an
  explicit pagination test.
- `PruneItems` honors `PrunePolicy.Tags`/`Match` on the age pass and on both
  the outer and inner statements of the max-per-feed pass.
- Pruned items keep their dedup fingerprint under tag scoping.
- No JOIN is introduced and no column reference needs qualifying.
- No change to `items` schema, `core.ValidItemFields`, or `core.ProjectItem`.
- Behaviors 1-9 covered in `internal/store/sqlite/sqlite_test.go`.
- `make build` passes.
