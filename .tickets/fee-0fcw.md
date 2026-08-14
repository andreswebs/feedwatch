---
id: fee-0fcw
status: closed
deps: [fee-2lbg, fee-o5uq, fee-frus]
links: []
created: 2026-08-14T02:49:18Z
type: task
priority: 2
assignee: Andre Silva
parent: fee-zs6b
tags: [cmd, tags]
---
# items --tag

Add Tags/Match to ItemsRequest, resolved in query(now) so validation cannot drift, composing as an intersection with --feed, --since/--until, --contains, and --fields, with pagination and omitted_no_date computed over the filtered set.

## Design

Add `--tag`/`--match` to `items`, so item history can be queried per lane. The
per-lane digest is the feature's primary use case, and this is the command that
produces it.

Reuse the `tagFilter` helper and `--match` validation from the `list` ticket,
and the SQL-side item filtering from the store ticket.

Spec: [docs/specs/003-feed-tags/plan.md](docs/specs/003-feed-tags/plan.md),
section "`items` — query items by lane".

## Request changes

```go
type ItemsRequest struct {
	Feeds     []string `flag:"feed" usage:"feed url or alias to query (repeatable); all feeds when omitted"`
	Tags      []string `flag:"tag" usage:"tag to filter by (repeatable); all feeds when omitted"`
	Match     string   `flag:"match" default:"all" usage:"multi-tag semantics: 'all' (default) or 'any'"`
	// ...existing Since/Until/Limit/Offset/Order/TimeField/Contains/Fields...
}
```

Place `Tags`/`Match` next to `Feeds`, since field order determines flag order
in `--help` and in `schema`, and the two are the same axis (which feeds).

## Resolution

`ItemsRequest.query(now)` is the single place the request becomes a
`core.ItemQuery`, and `Validate` calls it against a fixed instant and discards
the result so the rules cannot drift. Add the tag resolution there:

```go
	filter, err := tagFilter(r.Tags, r.Match)
	if err != nil {
		return core.ItemQuery{}, err
	}
	q.Tags, q.Match = filter.Tags, filter.Match
```

Nothing else in `App.Items` changes: the store does the filtering.

`--feed` and `--tag` **compose with AND**: `items --feed X --tag ai` means
items from feed X, which must also be in lane ai. That is the store's
behavior (two independent WHERE clauses) and it is the correct reading; it
needs no special case, but it does need a test, because a reader could
reasonably expect a union.

## TDD plan

Library tests in `items_test.go` (external, `newTestApp` returning the backing
`InMemoryStore` to seed items); CLI tests in `internal/command/items_test.go`
(internal, `runItems` helper). `internal/command/items_test.go` is the largest
test file in the package — read its existing table-driven subtests before
adding, and follow them.

Fixture: three feeds — `["ai","agents"]`, `["ai"]`, untagged — each with
several items at distinct, fixed publication times.

1. **(tracer)** `items --tag ai` returns only items from the two tagged feeds.
2. `--match all` with two tags returns only the both-tagged feed's items;
   `--match any` returns both tagged feeds' items.
3. `--tag` composes with `--feed` as an intersection.
4. `--tag` composes with `--since`/`--until` and with `--contains`.
5. **Pagination over the filtered set**: `--tag ai --limit 2 --offset 2`
   returns the third and fourth items of the *filtered* result, against a
   hand-computed expectation. This is the behavior a post-query Go-side filter
   would get wrong, and it is why the filtering lives in SQL.
6. `omitted_no_date` counts only in-lane undated items.
7. `--tag` composes with `--fields`: the projected envelope is unchanged in
   shape, only narrowed in rows.
8. `--tag nosuchlane` returns `"items":[]`, exit 0.
9. `--match bogus` and an invalid tag name are usage errors, exit 64.
10. `items` with no `--tag` is unchanged.

## Golden fallout

`testdata/schema/items.stdout`, `testdata/help/items.stdout`,
`testdata/schema/all.stdout` (new flags). `testdata/lifecycle/items.stdout` is
a plain `items` invocation and should be unchanged; verify.

## Gotchas

- Items carry no tags of their own. Do not add `tags` to the item envelope, to
  `core.ValidItemFields`, or to `core.ProjectItem`. `items.categories` is
  feed-supplied metadata and a different concept; conflating the two would be a
  contract break, not a convenience.
- `--fields` usage text is supplied at runtime through `withUsage` in
  `itemsFlags()`, which handles only `*StringFlag` and `*StringSliceFlag`.
  Adding `--tag` needs no `withUsage` call, but do not break the existing one.
- `ItemsRequest.Envelope` chooses the projected or full result based on
  `Fields`; it is unaffected, but the projected path needs behavior 7 to prove
  it.

## Acceptance Criteria

- `ItemsRequest` carries `Tags` and `Match`, resolved in `query(now)` through
  the shared `tagFilter` helper so `Validate` and resolution cannot drift.
- `--tag` composes as an intersection with `--feed`, `--since`/`--until`,
  `--contains`, and `--fields`.
- Pagination and `omitted_no_date` are computed over the filtered set, proven
  by explicit tests.
- An invalid `--match` or tag name exits 64; an empty lane exits 0 with
  `"items":[]`.
- No item-level tag field is introduced anywhere.
- `items` with no `--tag` is unchanged.
- Behaviors 1-10 covered across `items_test.go` and
  `internal/command/items_test.go`.
- `schema/items.stdout`, `help/items.stdout`, and `schema/all.stdout` are
  regenerated and reviewed.
- `make build` passes.

## Notes

**2026-08-14T20:12:45Z**

Implemented items --tag/--match. ItemsRequest gained Tags/Match placed next to Feeds (declaration order drives flag order in --help and schema); both resolve through the shared tagFilter helper inside query(now), which Validate calls and discards, so validation cannot drift from resolution. No other App.Items change was needed: core.ItemQuery, the SQLite store and InMemoryStore already carried Tags/Match from fee-frus.

--tag composes as an intersection with --feed, --since/--until, --contains and --fields; a feed outside the lane yields items:[] and exit 0. This deliberately diverges from poll/check, where --tag plus named feeds is a usage error - items narrows a read rather than choosing a run set. Pagination and omitted_no_date are computed over the filtered set, pinned by a hand-computed --tag ai --limit 2 --offset 2 subtest and by an undated item seeded both inside and outside the lane.

No item-level tag field was introduced anywhere: output_schema in schema/items.stdout and lifecycle/items.stdout are byte-identical. Regenerated goldens are limited to the flag lists in schema/items.stdout, help/items.stdout and schema/all.stdout. TestRequestSurfaceMapping's items flag count moved 9 -> 11.

Tests: TestItemsFiltersByTag, TestItemsTagOmittedNoDateCountsOnlyInLane, TestItemsTagProjects, TestItemsRejectsInvalidTagSelection in items_test.go; TestItemsTagFlagsReachTheRequest, TestItemsTagEmptyLaneSerializesAsList, TestItemsTagOmittedNoDateCountsOnlyInLane, TestItemsTagProjects, TestItemsRejectsInvalidTagSelection in internal/command/items_test.go. A shared seedLaneItems fixture in each package reuses the existing seedLaneFeeds three-feed lane and titles items by age in hours so lane order and pages are hand-computable. Docs and CHANGELOG remain deferred to fee-fxl2. make build passes.
