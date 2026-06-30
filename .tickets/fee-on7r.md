---
id: fee-on7r
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

# list command

Implement the list command: report each subscription with status, alias, failure count, and last error, as JSON (or a table under --format text). Refs: docs/cli-design.md (Commands); docs/research/urfave-cli.reference.md.

## Design

The `list` command: list subscriptions with status, alias, failure count, and
last error.

```go
&cli.Command{ Name:"list", Action: listAction }
type ListResult struct { Feeds []FeedView `json:"feeds"` }
type FeedView struct {
  URL string `json:"url"`; Alias string `json:"alias,omitempty"`
  Status string `json:"status"`; Failures int `json:"failures"`
  LastError string `json:"last_error,omitempty"`
}
```

Action calls `store.ListFeeds` and renders via `output.Renderer` (JSON default,
table under `--format text`).

TDD plan (cmd.Run + temp db seeded via the store):

1. (tracer) with two feeds, `list` outputs both with status/alias/failures.
2. a disabled feed shows `status:"disabled"` and its last error.
3. empty store outputs `{"feeds":[]}`.

Deep-module note: thin read-only command; output shaping in the renderer.

## Acceptance Criteria

- `list` reports each subscription with status, alias, failure count, last error;
  empty store yields an empty list.
- Behaviors 1-3 covered.
- Supports Req 7. `make validate` passes.

## Notes

**2026-06-29T23:17:36Z**

Implemented the list command (internal/cli/list.go + list_test.go), registered in commands(). Read-only: ListFeeds(core.ListFilter{}) -> ListResult{Feeds []FeedView{url, alias omitempty, status, failures, last_error omitempty}}. Behaviors 1-3 covered (two feeds reported; disabled feed shows status+last_error; empty store yields {"feeds":[]} via make([]FeedView,0,..) so it marshals [] not null). Added a 4th test for --format text: ListResult implements output.TextRenderer with a text/tabwriter aligned table (URL/ALIAS/STATUS/FAILURES/LAST ERROR; dash for empty optional columns; status word carries meaning so no color needed). Store-only wiring via new listStore helper (prefers injected Deps.Store, else openStoreMigrated, no-op closer for injected). make build green; live-smoke verified against real SQLite (JSON, text table, empty).
