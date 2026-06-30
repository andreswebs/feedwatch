---
id: fee-u0i4
status: closed
deps: [fee-8cau, fee-2heq, fee-vzu8]
links: []
created: 2026-06-29T19:28:37Z
type: task
priority: 2
assignee: Andre Silva
parent: fee-5d25
tags: [cmd]
---

# poll: fetch-orchestration

Implement the fetch-orchestration stage of poll: select due/named/forced feeds and fetch+parse them concurrently via an errgroup bounded by --concurrency with per-host politeness, capturing per-feed outcomes without cross-feed cancellation, conditional GET and 304 handling. Refs: docs/cli-design.md (Poll Semantics, Concurrency and Politeness); docs/research/urfave-cli.reference.md.

## Design

The fetch-orchestration stage of `poll`: select due feeds and fetch+parse them
concurrently, recording per-feed outcomes. Package `src/internal/poll`.

```go
&cli.Command{ Name:"poll", ArgsUsage:"[FEED...]",
  Arguments: []cli.Argument{ &cli.StringArgs{Name:"feeds", Max:-1} },
  Flags: []cli.Flag{ &cli.BoolFlag{Name:"force"}, &cli.BoolFlag{Name:"all"} },
  Action: pollAction }

type feedOutcome struct {
  feed core.Feed; result core.FetchResult; parsed parse.ParsedFeed
  err *core.FeedError // nil on success or 304
}
func orchestrate(ctx, deps, feeds []core.Feed) []feedOutcome
```

- Target selection: named feeds, else `store.DueFeeds(now)`. `--force`/`--all`
  override due-ness.
- Concurrency: an `errgroup` with `SetLimit(concurrency)`. Each feed's failure is
  captured into its `feedOutcome` and never cancels siblings; the group context
  is cancelled only by SIGINT/SIGTERM.
- Per-host politeness: group feeds by host onto one worker; small per-host delay.
- For each feed: build `core.FetchRequest` from stored validators, `Fetch`
  (conditional GET), and on a 200 `Parse` the decoded body. 304 -> outcome with
  no items.
- Honor a parsed `<ttl>` when computing the next due time downstream.

TDD plan (httptest servers + real store + fixed clock):

1. (tracer) polling two due feeds fetches both and returns two outcomes; a
   non-due feed is skipped unless `--force`.
2. one feed failing (500 after retries) does not prevent the other from
   succeeding; both outcomes present.
3. a 304 feed yields an outcome with no items.
4. concurrency is bounded by `--concurrency` (observe max in-flight via a barrier
   handler).
5. cancellation: a cancelled context stops scheduling new feeds.

Deep-module note: orchestration returns outcomes; consuming/persisting and output
are the next two tickets. The signal-aware context comes from `main`.

## Acceptance Criteria

- `poll` selects due (or named/`--force`) feeds and fetches+parses them via an
  `errgroup` bounded by `--concurrency`, with per-host politeness; a feed failure
  never cancels siblings; 304 yields no items.
- Behaviors 1-5 covered against httptest + real store.
- Supports Req 9 (due-only, conditional GET) and 11. `make validate` passes.

## Notes

**2026-06-29T22:49:51Z**

Implemented fetch-orchestration stage in internal/poll (orchestrate.go, select.go). selectFeeds: named refs resolved via GetFeed regardless of due-ness (unknown ref -> CatUsage error); no names + force/all -> ListFeeds(active); else DueFeeds(now). orchestrate: groups feeds by host (url.Host) onto one worker each, serialized with PerHostDelay between same-host requests; host groups run under errgroup with SetLimit(Concurrency). Per-feed failure captured into feedOutcome.err and never cancels siblings (task funcs always return nil, so errgroup ctx is cancelled only by the parent/signal). 304 -> outcome with NotModified result and no items, Parse skipped. Outcomes written into position-indexed slots and returned in input order; cancelled/unscheduled feeds are dropped from the result. feedOutcome{feed,result,parsed,err} and orchestrate/fetchAndParse are unexported for the sibling poll tickets (q6t3 dedup-and-consume, 12gs output) to consume; Deps{Store,Fetcher,Parser,Clock,Concurrency,PerHostDelay} is exported for cli wiring in 12gs. No persistence here (RecordSuccess/Failure/UpsertItems/SetValidators are q6t3). IMPORTANT side-fix: gofeed.Parser is NOT concurrency-safe (mutates shared translator state); parse.GofeedParser now constructs a fresh gofeed.NewParser() per Parse call instead of holding one, so concurrent workers are race-free (verified with go test -race). Added golang.org/x/sync as a direct require. Tests: behaviors 1-3 use the testsupport InMemoryStore + FakeFetcher/FakeParser; behaviors 4 (concurrency bounded, peak==2) and 5 (cancellation stops scheduling) use real httptest servers + real fetch.Client + real parse.New(). make build and make test-race both clean.
