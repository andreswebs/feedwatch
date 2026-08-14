---
id: fee-o5uq
status: closed
deps: [fee-pfpz, fee-2lbg]
links: []
created: 2026-08-14T02:45:14Z
type: task
priority: 1
assignee: Andre Silva
parent: fee-zs6b
tags: [store, tags, test]
---
# testsupport: InMemoryStore tag parity

Mirror every tag semantic from the SQLite store in testsupport.InMemoryStore: SetTags, TagCounts, the DueFeeds signature, tag filtering in ListFeeds/DueFeeds/QueryItems/PruneItems, and canonicalize-on-create with preserve-on-re-add. There is no automated conformance suite, so every SQLite tag test needs a hand-written twin here. Gates every command ticket.

## Design

Mirror every tag semantic added to the SQLite store in
`internal/testsupport.InMemoryStore`. **This is the gate for every command
ticket in the epic**: all command tests drive the double, so a divergence here
produces command tests that pass against a fiction.

There is **no executable conformance suite** running one test set against both
stores. Parity is maintained by three things only: the compile-time
`var _ store.Store = (*testsupport.InMemoryStore)(nil)` assertion in
`store_test.go`, doc comments on each double method naming the SQLite behavior
they mirror, and hand-written twin tests. So every behavior T3 and T4 proved in
`internal/store/sqlite/sqlite_test.go` needs a twin here, written by hand.

## Changes to `internal/testsupport/store.go`

1. **`AddFeed`** — the existing-feed branch copies only `Alias`, `Interval`,
   and `UpdatedAt` onto the stored feed. Leave `Tags` out of that copy: that is
   exactly how the SQLite upsert preserves tags on re-add by omitting them from
   its `DO UPDATE SET`. The insert branch stores `f` verbatim, so canonicalize
   there with `core.CanonicalTags(f.Tags)` to match the store's write-time
   normalization. Add a doc-comment line saying so, since "the field is
   deliberately not copied" reads like an oversight otherwise.

2. **`SetTags(ctx, url string, tags []string) error`** — new method. Keyed by
   exact URL like `SetStatus`; stores `core.CanonicalTags(tags)`; bumps
   `UpdatedAt`; unknown URL is a no-op, matching `SetStatus`.

3. **`TagCounts(ctx) ([]core.TagCount, error)`** — new method. Count over every
   feed of any status, sorted by tag, returning an empty non-nil slice when
   nothing is tagged.

4. **`ListFeeds`** — add the tag predicate beside the existing status
   predicate.

5. **`DueFeeds(ctx, now, f core.ListFilter)`** — signature change; apply the
   tag predicate on top of the existing active-and-due test. The filter's
   `Status` is ignored, matching the SQLite contract.

6. **`QueryItems`** — items carry no tags, so the filter resolves tags to a
   feed-URL set and intersects with the existing set from `feedURLSetLocked`.
   Extend `feedURLSetLocked` (which today returns nil for "match all") or add a
   sibling; whichever, the intersection must happen **before** the omitted-no-date
   counting and before `paginate`, or behaviors 4 and 5 of the SQLite ticket
   will not have twins that actually mean anything.

7. **`PruneItems`** — restrict both the age pass and the per-feed `MaxPerFeed`
   pass to in-lane feeds. As in SQL, the per-feed pass must not rank out-of-lane
   items.

8. Add one shared unexported helper rather than repeating the predicate five
   times:

```go
// matchesTags reports whether a feed's tags satisfy the filter, mirroring the
// SQLite json_each predicate: no requested tags matches every feed, MatchAll
// requires every requested tag, MatchAny requires at least one.
func matchesTags(feedTags, want []string, m core.TagMatch) bool
```

Canonicalize `want` once at the call site, not inside the helper, so the
per-feed loop does not re-sort on every iteration.

## Call-site fixes

`DueFeeds`'s new parameter breaks `internal/poll/select.go` and
`internal/poll/run.go` (`skippedCount`). If T3 already passed
`core.ListFilter{}` at those sites, leave them; T10 threads the real filter.

## TDD plan

`internal/testsupport/store_test.go`, external `package testsupport_test`,
reusing `newStore(t)` (fixed clock). Naming follows the file's existing
convention: `TestInMemoryStore<Behavior>`, each with a doc comment naming the
SQLite behavior being mirrored.

Write each test as the twin of its SQLite counterpart, one at a time:

1. **(tracer)** `AddFeed` canonicalizes tags on create; `GetFeed` reads them
   back sorted, lowercased, deduped.
2. Re-adding an existing feed with a new alias and no tags preserves the stored
   tags.
3. `SetTags` replaces the set; `SetTags(ctx, url, nil)` clears it.
4. `ListFeeds` honors `MatchAll` and `MatchAny` over a three-feed fixture
   (both tags, one tag, none), and composes with `Status`.
5. `DueFeeds` with a tag filter returns only due, active, in-lane feeds.
6. `QueryItems` with a tag filter excludes out-of-lane items, and paginates
   over the **filtered** set.
7. `QueryItems` `OmittedNoDate` counts only in-lane undated items.
8. `PruneItems` age and max-per-feed passes are lane-scoped, and an out-of-lane
   feed is untouched by the per-feed pass.
9. `TagCounts` returns sorted counts and an empty slice for an untagged store.

## Verification beyond the tests

After the twins pass, spot-check parity by reading the two test files side by
side: every `Test...Tag...` in `internal/store/sqlite/sqlite_test.go` should
have a same-named-in-spirit twin here. A behavior proved on only one side is
the exact failure mode this ticket exists to prevent, and no tool will catch it.

## Gotchas

- `FailingUpsertStore` in `internal/testsupport/failing_store.go` embeds
  `store.Store` and overrides only `UpsertItems`, so it inherits the new methods
  for free and needs no change — but confirm it still compiles, since embedding
  an interface only satisfies the new methods if the embedded value is non-nil
  at runtime.
- The double must **not** be "better" than SQLite. If a tag filter in SQL
  cannot see a feed, the double must not see it either, even where an
  in-memory implementation could trivially do more.
- `internal/testsupport` is a normal importable package and needs its `doc.go`
  package comment intact for `revive`.

## Acceptance Criteria

- `InMemoryStore` implements `SetTags` and `TagCounts`, takes a
  `core.ListFilter` on `DueFeeds`, and honors `Tags`/`Match` in `ListFeeds`,
  `DueFeeds`, `QueryItems`, and `PruneItems`.
- `AddFeed` canonicalizes tags on create and preserves them on re-add,
  mirroring the SQLite upsert's `DO UPDATE SET` omission, with a doc comment
  saying why.
- Item tag filtering is applied before pagination and before the
  omitted-no-date count.
- One shared `matchesTags` helper expresses the predicate; it is not duplicated
  per method.
- Behaviors 1-9 covered in `internal/testsupport/store_test.go`, each the twin
  of a SQLite test, with a doc comment naming the mirrored behavior.
- The compile-time `var _ store.Store` assertions for `InMemoryStore` and
  `FailingUpsertStore` still hold.
- `make build` passes.

## Notes

**2026-08-14T19:31:14Z**

Tag parity complete in internal/testsupport.InMemoryStore. Feeds-half parity (SetTags, TagCounts, DueFeeds signature, ListFeeds tag filter) had already landed with fee-pfpz; this ticket added the items and prune halves plus every twin test.

Implementation: matchesTags now takes (feedTags, want []string, m core.TagMatch) with the requested set canonicalized once per call site, so the one helper serves both feed loops and the new laneURLSetLocked. laneURLSetLocked resolves a tag filter to the set of in-lane feed URLs (nil = match all), mirroring the SQLite feedTagScope subquery; an item whose feed is not subscribed is in no lane. QueryItems tests it alongside the existing feed-URL set inside the per-feed loop, which puts the lane scope before the omitted-no-date count and before paginate. PruneItems scopes both the age pass and the max-per-feed pass, skipping out-of-lane feeds before collecting live items so out-of-lane items never enter the ranking. AddFeed gained a doc comment explaining that Tags are deliberately not copied onto an existing feed, mirroring the SQLite upsert's DO UPDATE SET omission.

Tests: all 15 tag tests in internal/store/sqlite/sqlite_test.go now have a hand-written twin in internal/testsupport/store_test.go, each with a doc comment naming the mirrored behavior, using a port of the interleaved tagFixture so the pagination twin is a real discriminator. Two extras beyond the SQLite set: SetTags on an unknown URL is a no-op, and a no-tags ListFilter matches every feed.

Note for the next ticket: DueFeeds call sites in internal/poll still pass core.ListFilter{}; threading the real filter is T10's job. FailingUpsertStore needed no change. make build passes.
