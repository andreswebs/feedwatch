---
id: fee-pfpz
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
# sqlite: feed tags read, write, and filtering

Read and write the feeds.tags column, add SetTags and TagCounts to store.Store, add a json_each tag predicate, honor ListFilter.Tags in ListFeeds, and change DueFeeds to take a core.ListFilter so a lane can be polled on its own schedule (breaking store.Store change).

## Design

Teach the SQLite store to read, write, and filter feed tags. This is the
`feeds`-table half of the store work; the `items` join and prune scoping are
T4 (`fee-` sibling, see the epic).

Depends on the `core` vocabulary (`TagMatch`, `CanonicalTags`) and on migration
0002 having added the column.

Spec: [docs/specs/003-feed-tags/plan.md](docs/specs/003-feed-tags/plan.md),
sections "Where filtering happens" and "Tag selection syntax".

## 1. Column plumbing — `internal/store/sqlite/feeds.go`

`feedColumns` is the single shared SELECT list driving `GetFeed`, `ListFeeds`,
and `DueFeeds`; `scanFeed(row rowScanner)` scans positionally against it. Add
`tags` to **both**, in the same position.

```go
const feedColumns = `url, alias, interval_seconds, status, etag, last_modified,
	failure_count, last_error, last_error_at, last_fetch_at, next_due_at,
	tags, created_at, updated_at`
```

`tags` is `NOT NULL DEFAULT '[]'`, so scan it into a plain `string` (not a
`sql.NullString`, unlike `alias`) and `json.Unmarshal` it into `f.Tags` when
non-empty — exactly the pattern `scanItem` uses for `categories` and
`enclosures` in `items.go`. Guard the unmarshal with `!= ""` the same way.

## 2. `AddFeed` — create sets tags, re-add preserves them

The existing upsert is `INSERT ... ON CONFLICT(url) DO UPDATE SET alias,
interval_seconds, updated_at`. Add `tags` to the **INSERT column list only**,
and deliberately **not** to the `DO UPDATE SET` clause:

```go
	`INSERT INTO feeds (url, alias, interval_seconds, status, tags, created_at, updated_at)
	 VALUES (?, ?, ?, ?, ?, ?, ?)
	 ON CONFLICT(url) DO UPDATE SET
		alias = excluded.alias,
		interval_seconds = excluded.interval_seconds,
		updated_at = excluded.updated_at`
```

That single choice implements the plan's "omitted preserves, given replaces"
rule without the store needing to distinguish an empty slice from an absent
one: creation takes `f.Tags`, re-add leaves stored tags untouched, and an
explicit replacement goes through `SetTags`. Marshal with
`json.Marshal(core.CanonicalTags(f.Tags))` so the stored bytes are canonical
and never `null`.

## 3. New method: `SetTags`

Add to the `store.Store` interface in `store/store.go` and implement in
`feeds.go`:

```go
	// SetTags replaces a feed's tag set with the canonical form of tags,
	// writing an empty array when tags is empty.
	SetTags(ctx context.Context, url string, tags []string) error
```

Keyed by exact URL, not a ref — same convention as `SetStatus` and
`SetValidators`. It also bumps `updated_at`. Add/remove/clear semantics belong
to the `tag` command (T6), which reads the feed, computes the new set in Go,
and calls this once; the store stays a dumb setter.

## 4. The tag predicate

New unexported helper in `feeds.go` (or a new `tags.go` in the same package if
T4 will share it — coordinate, but duplication across the two files is
acceptable if it keeps the diffs independent):

```go
// tagPredicate builds a SQL fragment matching feeds carrying the requested
// tags, with its bound arguments. It returns an empty string for no tags, so a
// zero-valued filter adds no clause. The column argument names the feeds-table
// alias to test, so both the feeds query and the items subquery can use it.
func tagPredicate(col string, tags []string, m core.TagMatch) (string, []any)
```

`match=any`:

```sql
EXISTS (SELECT 1 FROM json_each(<col>) WHERE value IN (?, ?))
```

`match=all`:

```sql
(SELECT count(DISTINCT value) FROM json_each(<col>) WHERE value IN (?, ?)) = ?
```

with the trailing bound argument being `len(tags)` after canonicalization.
Canonicalize the requested tags with `core.CanonicalTags` before building, so
`--tag AI --tag ai` counts as one tag and the `= N` comparison is correct.

This is the codebase's **first SQL-side JSON usage**; everything to date
marshals Go-side. `modernc.org/sqlite` ships the JSON1 functions, so
`json_each` is available with no build tag. Say so in a comment above the
helper so the next reader does not assume it was an oversight.

Build the placeholder list with the existing precedent in `items.go`:

```go
placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(tags)), ", ")
```

and assemble with a `strings.Builder`, never `+` concatenation of a
non-constant, or `gosec` G202 fails the build.

## 5. `ListFeeds` — clause list instead of an incremental WHERE

Today:

```go
	query := `SELECT ` + feedColumns + ` FROM feeds`
	var args []any
	if filter.Status != "" {
		query += ` WHERE status = ?`
		args = append(args, string(filter.Status))
	}
	query += ` ORDER BY url`
```

`WHERE` can no longer be assumed to be introduced by the status branch.
Refactor to a `clauses []string` plus `args []any`, joined with `" AND "` and
prefixed with `" WHERE "` only when non-empty, assembled through a
`strings.Builder`. Status stays first so the emitted SQL is stable and
reviewable.

## 6. `DueFeeds` — breaking signature change

```go
	// DueFeeds returns active feeds whose next-due time is at or before now,
	// narrowed by the filter's tags. The filter's Status is ignored: a due feed
	// is active by definition.
	DueFeeds(ctx context.Context, now time.Time, f core.ListFilter) ([]core.Feed, error)
```

This is what lets a lane be polled on its own schedule rather than only under
`--force`, and it is a **breaking change to the public `store.Store`
interface**. Update every implementer and caller in this ticket so the tree
compiles:

- `internal/store/sqlite/feeds.go` (the implementation)
- `store/store.go` (the interface and its doc comment)
- `internal/testsupport/store.go` — a **minimal** signature fix only; real
  parity behavior is T5's job. Accept the parameter and apply the tag filter or
  leave a `// TODO(T5)`-free minimal implementation that at least honors the
  filter, coordinating with T5 so the two do not conflict.
- `internal/poll/select.go` and `internal/poll/run.go` (`skippedCount`) —
  pass `core.ListFilter{}` for now; T10 threads the real filter.

Record the break in `CHANGELOG.md` under `### Changed` (the file's house style
opens each bullet with a bolded lede and links the governing ADR); the docs
ticket T15 owns the prose, but the compile-breaking interface change should not
land undocumented.

## 7. Tag inventory query

For the `tags` command (T7), add:

```go
	// TagCounts returns each distinct tag with the number of subscriptions
	// carrying it, sorted by tag, counting feeds of any status.
	TagCounts(ctx context.Context) ([]core.TagCount, error)
```

with `core.TagCount{Tag string; Feeds int}` added to `core/tags.go`. The SQL is
a plain aggregate over the JSON expansion:

```sql
SELECT value AS tag, count(*) AS feeds
FROM feeds, json_each(feeds.tags)
GROUP BY value
ORDER BY value
```

## TDD plan

External black-box `package sqlite_test` in `sqlite_test.go`, reusing
`newStore(t)` (temp-file DB, migrated, fixed clock), `addTestFeed`, and
`fixedClock`. One `TestXxx` per behavior with a doc comment naming the
contract; sub-cases as `t.Run` subtests inside a single fixture, matching
`TestQueryItemsFilters`. Assert errors with `errors.As`/`errors.Is`, never on
strings.

1. **(tracer)** `AddFeed` with `Tags: []string{"AI", "agents", "ai"}` then
   `GetFeed` returns `Tags == ["agents", "ai"]` — proves the column, the
   canonicalization on write, and the scan path end to end.
2. A feed added with no tags reads back with an empty, non-nil `Tags`.
3. Re-adding an existing feed with a new alias and **no** tags preserves the
   stored tags (the `DO UPDATE SET` omission).
4. `SetTags` replaces the set; `SetTags(ctx, url, nil)` clears it to `[]`.
5. `ListFeeds` with `Tags: ["ai"]` and the default match returns only tagged
   feeds; an untagged feed is excluded.
6. `ListFeeds` with two tags and `MatchAll` returns only feeds carrying both;
   with `MatchAny` returns feeds carrying either. Build the fixture with three
   feeds (both, one, neither) so the two modes give different answers.
7. `ListFeeds` combining `Status: FeedDisabled` with a tag filter applies both.
8. `DueFeeds` with a tag filter returns only due feeds in the lane; a due feed
   outside the lane is excluded and a not-due feed inside it is too.
9. `TagCounts` returns tags sorted with correct counts, and an empty slice for
   a store with no tagged feeds.

## Gotchas

- `scanFeed`'s `Scan` argument order must stay positionally aligned with
  `feedColumns`. Inserting `tags` in the middle of one and appending it to the
  other is a silent data-corruption bug that no compiler catches.
- `gosec` G202 on `+`-concatenated SQL; `errorlint` requires
  `errors.Is(err, sql.ErrNoRows)`.
- `sqlclosecheck` is enabled: every `*sql.Rows` needs a `defer rows.Close()`
  and a `rows.Err()` check.
- Do not add tag filtering to `GetFeed`: a ref resolves a single feed by
  identity, and narrowing it by lane would make `tag` and `enable` behave
  surprisingly.

## Acceptance Criteria

- `feedColumns` and `scanFeed` carry `tags`, positionally aligned, and round
  trip a canonical tag set through `AddFeed`/`GetFeed`.
- `AddFeed` sets tags on create and preserves them on re-add.
- `store.Store` gains `SetTags(ctx, url string, tags []string) error` and
  `TagCounts(ctx) ([]core.TagCount, error)`, both implemented in SQLite.
- `store.Store.DueFeeds` takes a `core.ListFilter`; every implementer and
  caller in the tree compiles, and the break is noted in `CHANGELOG.md`.
- `ListFeeds` and `DueFeeds` honor `Tags` with both `MatchAll` and `MatchAny`,
  and combine correctly with `Status`.
- The tag predicate is built with `strings.Builder` and bound `?` parameters
  only; no `//nolint:gosec` is added.
- Behaviors 1-9 covered in `internal/store/sqlite/sqlite_test.go`.
- `make build` passes, and `go test -race ./internal/store/...` is clean.
