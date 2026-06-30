---
id: fee-ydl6
status: closed
deps: [fee-gyos]
links: []
created: 2026-06-29T19:28:37Z
type: task
priority: 2
assignee: Andre Silva
parent: fee-5d25
tags: [cmd]
---

# items command

Implement the items query command: translate --feed/--since/--until/--limit/--offset/--order/--contains/--fields into a core.ItemQuery and render the stored items (full by default, --fields projects). Refs: docs/cli-design.md (Querying History); docs/research/urfave-cli.reference.md.

## Design

The `items` command: query stored item history.

```go
&cli.Command{ Name:"items", Action: itemsAction, Flags: []cli.Flag{
  &cli.StringSliceFlag{Name:"feed"},
  &cli.StringFlag{Name:"since"}, &cli.StringFlag{Name:"until"},
  &cli.IntFlag{Name:"limit"}, &cli.IntFlag{Name:"offset"},
  &cli.StringFlag{Name:"order", Value:"published desc"},
  &cli.StringFlag{Name:"contains"},
  &cli.StringSliceFlag{Name:"fields"},
}}
```

(urfave ref: StringSliceFlag, IntFlag, StringFlag.) Action builds a
`core.ItemQuery` (resolving `--since`/`--until` as RFC3339 or relative durations
like `24h`/`7d`; parsing `--order` into field+direction) and calls
`store.QueryItems`. Full items by default; `--fields` projects to a subset.
Renders the result list.

```go
type ItemsResult struct { Items []core.Item `json:"items"` }
```

TDD plan (cmd.Run + temp db seeded with items):

1. (tracer) `items --feed X` returns that feed's items as JSON.
2. `--since 7d` / `--until` filter by time window; null published_at coalesces to
   fetched.
3. `--limit`/`--offset` paginate; `--order published asc|desc` orders.
4. `--contains` filters by substring over title/content.
5. `--fields title,link` returns only those fields.

Deep-module note: thin command translating flags to `core.ItemQuery`; the query
semantics live in the store.

## Acceptance Criteria

- `items` supports `--feed` (repeatable), `--since`/`--until` (RFC3339 or
  relative), `--limit`/`--offset`, `--order`, `--contains`, and `--fields`
  projection; full items by default.
- Behaviors 1-5 covered.
- Supports Req 13. `make validate` passes.

## Notes

**2026-06-29T23:35:32Z**

Implemented items command (internal/cli/items.go + items_test.go), registered in root commands(). Thin command: translates --feed/--since/--until/--limit/--offset/--order/--contains/--fields into a core.ItemQuery and calls store.QueryItems; all query semantics (coalesce null published_at to fetched, contains, projection, ordering, pagination) live in the store, reused unchanged. Notable decisions: (1) --order is a single space-separated specifier ('published desc'); field must be published|fetched, direction asc|desc, defaults to desc; bad values are CatUsage/ErrUsage -> exit 1, empty stdout. (2) --since/--until accept RFC3339 OR relative durations; relative parsing extends time.ParseDuration with 'd'(days)/'w'(weeks) since Go rejects '7d', and a relative value means now-dur (resolved via d.Clock for determinism). (3) Test seam mirrors list_test/poll_test (injected InMemoryStore, FixedClock, OsExiter capture); seedItem must AddFeed first because both the SQLite store and the double resolve --feed via the feeds table (feed_url IN (SELECT url FROM feeds WHERE url/alias IN ...)). 6 tests cover behaviors 1-5 plus the usage-error path. make build green; smoke-tested against a real sqlite db. Remaining E6 child before the epic can close: prune (fee-7r0v).
