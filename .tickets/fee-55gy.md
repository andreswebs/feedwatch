---
id: fee-55gy
status: closed
deps: [fee-gyos]
links: []
created: 2026-06-29T19:28:36Z
type: task
priority: 2
assignee: Andre Silva
parent: fee-171q
tags: [cmd]
---

# rm command

Implement the rm command: resolve a feed by URL or unique alias and remove it (cascading items); unknown ref is a usage error. Refs: docs/cli-design.md (Commands, Feed Identity); docs/research/urfave-cli.reference.md.

## Design

The `rm` command: unsubscribe by URL or alias.

```go
&cli.Command{ Name:"rm", ArgsUsage:"URL|ALIAS",
  Arguments: []cli.Argument{ &cli.StringArg{Name:"ref"} }, Action: rmAction }
```

Action resolves the ref (exact URL or unique alias) and calls
`store.RemoveFeed`, which cascades items (live and tombstoned). Reports
`{removed: <url>}`. A missing ref is a usage error (exit 1).

TDD plan (cmd.Run + temp db):

1. (tracer) `rm` by URL removes the subscription and its items; output
   `removed`, exit 0.
2. `rm` by alias resolves and removes.
3. `rm` of an unknown ref -> usage error, exit 1.

Deep-module note: thin command over `store.RemoveFeed`.

## Acceptance Criteria

- `rm` resolves URL or unique alias and removes the subscription (cascading
  items); unknown ref -> exit 1.
- Behaviors 1-3 covered.
- Supports Req 7. `make validate` passes.

## Notes

**2026-06-29T23:21:23Z**

Implemented rm command (internal/cli/rm.go + rm_test.go), registered in root.go commands(). Thin store-only command following the list/fee-on7r store-only deps pattern: rmStore prefers injected Deps.Store else openStoreMigrated. Key design point: GetFeed(ref) FIRST to (a) resolve alias->canonical URL for the `{removed:<url>}` envelope and (b) turn an unknown ref into the CatUsage 'feed not found' *FeedError (exit 1). RemoveFeed alone could not do this: both stores treat RemoveFeed of a missing feed as a no-op, so calling it directly would wrongly exit 0 on an unknown ref. RemoveFeed cascades items (live+tombstoned) per store contract. Behaviors 1-3 covered: rm by URL (verifies feed gone + items cascade), rm by alias (verifies canonical URL reported), unknown ref (exit 1, CatUsage error JSON on stderr, empty stdout). make build green.
