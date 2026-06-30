---
id: fee-4q22
status: closed
deps: [fee-gyos, fee-8cau, fee-2heq]
links: []
created: 2026-06-29T19:28:36Z
type: task
priority: 2
assignee: Andre Silva
parent: fee-171q
tags: [cmd]
---

# add command

Implement the add command: validate an explicit http(s) feed URL parses as a feed (fetch + parse), reject HTML pointing to discover, then upsert the subscription with optional alias/interval (idempotent). Refs: docs/cli-design.md (Commands, Feed Identity, Feed Discovery); docs/research/urfave-cli.reference.md.

## Design

The `add` command: subscribe to an explicit feed URL after validating it parses
as a feed.

```go
&cli.Command{
  Name: "add", ArgsUsage: "URL",
  Arguments: []cli.Argument{ &cli.StringArg{Name: "url"} },
  Flags: []cli.Flag{ &cli.StringFlag{Name:"alias"}, &cli.DurationFlag{Name:"interval"} },
  Action: addAction,
}
```

(urfave ref: cli.Command, StringArg, StringFlag/DurationFlag, Action.) Action:

- Validate the URL is http(s) and not a bare host (reject otherwise -> usage
  error, exit 1).
- Fetch it with the `fetch.Fetcher` and parse with the `parse.Parser`; if it does
  not parse as a feed, reject and point to `discover` (`CatUsage`).
- On success, `store.AddFeed` (upsert): create, or if already subscribed, update
  alias/interval and report the existing subscription (idempotent success).
- Output the resulting feed as the JSON envelope.

```go
type AddResult struct {
  URL string `json:"url"`; Alias string `json:"alias,omitempty"`
  Interval string `json:"interval,omitempty"`; Created bool `json:"created"`
}
```

TDD plan (cmd.Run with stub or real Fetcher/Parser/Store via httptest + temp db):

1. (tracer) adding a valid feed URL stores it and outputs `created:true`, exit 0.
2. a URL that fetches HTML (not a feed) is rejected, exit 1, message points to
   `discover`.
3. a non-http scheme or bare host is rejected as a usage error, exit 1.
4. re-adding an existing URL with a new `--alias` updates it, `created:false`,
   exit 0 (idempotent upsert).

Deep-module note: the command wires Fetcher+Parser+Store; validation logic is
testable through `cmd.Run`.

## Acceptance Criteria

- `add` validates http(s) + feed-parseability before subscribing; rejects HTML
  and points to `discover`; idempotent upsert of alias/interval.
- Behaviors 1-4 covered.
- Supports Req 7. `make validate` passes.

## Notes

**2026-06-29T23:12:41Z**

Implemented the add command (internal/cli/add.go + add_test.go), registered in commands(). Validates absolute http(s) URL (bare host/non-http scheme -> CatUsage exit 1, no fetch), then fetches+parses to confirm it is a feed (fetch failure or parse failure -> CatUsage exit 1 pointing to discover), then upserts via store.AddFeed. created flag is derived by a pre-AddFeed GetFeed: a not-found (CatUsage) FeedError means new (created:true), nil means existing (created:false), any other error propagates as a hard store failure. AddResult envelope: {url, alias?, interval?, created}. All 4 TDD behaviors covered; make build green. NOTE: 301/308 URL rewrite on add not handled (no Store.RenameFeed yet, same deferral as poll).
