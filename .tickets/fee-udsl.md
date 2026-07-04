---
id: fee-udsl
status: open
deps: []
links: []
created: 2026-07-04T12:21:07Z
type: bug
priority: 0
assignee: Andre Silva
tags: [poll, store]
---
# poll: unconditional 5s persistence deadline can abort a successful poll

## Context

First-customer feedback (feedwatch v0.0.1, 141 feeds imported via OPML,
`poll --force`): the poll envelope reported
`polled=141 succeeded=133 failed=8 skipped=0 new_items=0` with an empty
`items[]` array, yet 5,335 items were stored in the database (confirmed via
`feedwatch items`). The customer's summary: "the entire first batch of content
is silently invisible" to an agent consuming the JSON contract, because
`new_items: 0` tells it nothing new arrived.

## Investigation findings

The happy path is correct. Reproduced locally at the `v0.0.1` tag (whose
`src/` tree is identical to `main`): `add` plus `poll --force`, and OPML
`import --no-validate` plus `poll --force`, both report correct `new_items`
and a populated `items[]` on first poll. `UpsertItems`
(`src/internal/store/sqlite/items.go`) correctly returns newly inserted rows,
and `consume` (`src/internal/poll/consume.go`) counts them.

The defect is in `src/internal/poll/run.go`:

```go
const persistGrace = 5 * time.Second

// in Run:
outcomes := orchestrate(ctx, d, feeds)
persistCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), persistGrace)
defer stop()
totals, feedErrs, err := consume(persistCtx, d, outcomes)
```

The comment (and `TestRunInterruptPersistsCompletedFeeds` in
`run_interrupt_test.go`) says the 5-second grace exists so an interrupted poll
can still persist completed work without hanging on an unresponsive store. But
the deadline is applied unconditionally: every poll run, even a fully
successful uninterrupted one, must persist all fetched feeds within 5 seconds
total.

At the customer's scale (133 successful feeds, 5,335 items, one transaction
per feed, one `SELECT` plus `INSERT` round-trip per item, per-transaction
fsync) persistence on slow storage can exceed 5 seconds. When the deadline
expires mid-`consume`:

1. The in-flight store call fails with `context.DeadlineExceeded`.
2. `consumeSuccess` wraps it, `consume` returns a hard error, `Run` returns a
   zero `Result` plus the error.
3. `pollAction` (`src/internal/cli/poll.go`) returns the error before
   rendering: exit 1, empty stdout, while every feed processed before the
   expiry is already durably persisted (each feed commits independently).
4. Any retry then finds those items already stored: `UpsertItems` dedups them,
   so `new_items` under-reports or reports 0 and `items[]` is empty, exactly
   as observed.

This is the only mechanism found in the code by which items end up stored
while the envelope reports zero. Reporting partial results on a hard
mid-persist failure is tracked separately in fee-oyw2; this ticket removes the
arbitrary deadline from normal operation.

## Design

Keep the interrupt semantics (persistence survives cancellation, bounded by a
grace period) but make the grace start at interrupt time, not at persist
start, and make an uninterrupted run unbounded:

```go
// graceAfterCancel returns a context detached from parent's cancellation.
// While parent is live the returned context has no deadline. Once parent is
// cancelled (an interrupt), the returned context is cancelled grace later, so
// persistence of completed work still cannot hang on an unresponsive store.
// The returned stop func releases the watcher goroutine and must be deferred.
func graceAfterCancel(parent context.Context, grace time.Duration) (context.Context, context.CancelFunc) {
    ctx, cancel := context.WithCancel(context.WithoutCancel(parent))
    stopped := make(chan struct{})
    var once sync.Once
    stop := func() { once.Do(func() { close(stopped) }); cancel() }
    go func() {
        select {
        case <-parent.Done():
            t := time.NewTimer(grace)
            defer t.Stop()
            select {
            case <-t.C:
                cancel()
            case <-stopped:
            }
        case <-stopped:
        }
    }()
    return ctx, stop
}
```

`Run` becomes:

```go
persistCtx, stop := graceAfterCancel(ctx, persistGrace)
defer stop()
totals, feedErrs, err := consume(persistCtx, d, outcomes)
```

Keep `persistGrace = 5 * time.Second` and update its doc comment to state the
grace is measured from the interrupt, not from persist start.

## TDD plan

In `src/internal/poll`:

1. Regression: a normal (uncancelled) run persists with no deadline. Wrap the
   in-memory store so `UpsertItems` records `_, ok := ctx.Deadline()`; run
   `Run` with a background context and assert no store write carried a
   deadline. With the current code this fails: every write carries the 5s
   deadline.
2. Existing `TestRunInterruptPersistsCompletedFeeds` must stay green: after
   `cancel()`, completed feeds persist and `Run` returns no hard error.
3. New: after parent cancellation, the persist context is cancelled once the
   grace elapses. Make the grace injectable (a `Deps` field defaulted to 5s,
   or a package variable overridden in the test) so the test waits
   milliseconds, not seconds. Do not sleep 5 seconds in tests.

End-to-end regression for the customer scenario, in `src/internal/e2e`:

1. Serve at least 20 feeds (a few items each) from `httptest`, import them,
   run a first `poll --force`, and assert `new_items` equals the total item
   count, `len(items)` equals `new_items`, and a subsequent `feedwatch items`
   returns the same count. There is currently no e2e assertion on `new_items`
   at all.

## Acceptance criteria

- An uninterrupted poll's persistence stage has no deadline: store writes
  observe a context without a deadline.
- An interrupted poll still persists completed feeds and is bounded by the
  grace measured from the interrupt.
- First-poll `new_items` and `items[]` are covered by an e2e test.
- `make build` passes.
