---
id: fee-oyw2
status: closed
deps: [fee-udsl]
links: []
created: 2026-07-04T12:21:07Z
type: bug
priority: 1
assignee: Andre Silva
tags: [poll, cli]
---
# poll: emit partial result envelope when persistence fails mid-run

## Context

First-customer feedback (v0.0.1): items were durably stored while the poll
JSON contract reported nothing (`new_items: 0`, empty `items[]`). The root
cause (an unconditional 5s persistence deadline) is fixed by fee-udsl, but the
failure mode it exposed is general: whenever a store write fails partway
through the persistence stage, the work already persisted becomes invisible.

Today, `consume` (`src/internal/poll/consume.go`) persists feeds one at a
time, each feed's writes committing independently. On the first failed write
it returns a hard error; `Run` (`src/internal/poll/run.go`) then discards the
accumulated totals and returns a zero `Result`:

```go
totals, feedErrs, err := consume(persistCtx, d, outcomes)
if err != nil {
    return Result{}, feedErrs, err
}
```

`pollAction` (`src/internal/cli/poll.go`) returns that error before rendering,
so the process exits 1 with empty stdout. Every feed persisted before the
failure is in the database, but no envelope ever reported its items. An agent
pipeline (cron, poll, summarize, deliver) permanently misses that content: the
next poll dedups those items and does not report them as new either.

## Design

A hard mid-persist failure should still report what was durably persisted.

1. `poll.Run` returns the partial result alongside the error. `consume`
   already returns its accumulated `pollTotals` on error; `Run` must shape
   them into a `Result` instead of dropping them:

    ```go
    totals, feedErrs, err := consume(persistCtx, d, outcomes)
    res := Result{
        Polled:   totals.polled,
        Skipped:  skipped,
        NewItems: totals.newItems,
        Failed:   totals.failed,
        Items:    orderedItems(feeds, totals),
        Renamed:  totals.renames,
    }
    if err != nil {
        return res, feedErrs, err
    }
    return res, feedErrs, nil
    ```

    Extract the existing items-ordering loop into `orderedItems` so both paths
    share it.

2. `pollAction` renders the envelope before propagating the hard error, only
   when a partial result exists (`result.Polled > 0` distinguishes a
   mid-persist failure from an early failure such as an unreachable store,
   where stdout must stay empty):

    ```go
    result, feedErrs, err := poll.Run(ctx, pd, cmd.Args().Slice(), cmd.Bool("force"))
    if err != nil {
        if result.Polled > 0 {
            _ = r.Result(shapePollResult(result, feedErrs))
        }
        return err
    }
    ```

    Extract the current `PollResult` construction into `shapePollResult` so
    the success and failure paths cannot drift. The exit code stays 1 (hard
    failure), from the existing error boundary; stderr still carries the
    structured error.

3. Semantics to preserve: on a hard mid-persist failure the envelope's
   `new_items`/`items[]` reflect only feeds whose writes committed before the
   failure. Feeds not yet persisted are absent; a retry will report them as
   new. This keeps the invariant the customer asked for: an item is reported
   as new in exactly one successful-or-partial envelope.

4. Documentation: update `docs/usage.md` (poll section) and the exit-code
   notes in `docs/cli-design.md` to state that exit 1 may now carry a partial
   envelope on stdout describing work persisted before the failure, and that
   consumers should process it.

## TDD plan

In `src/internal/poll` (unit) and `src/internal/cli` (envelope):

1. A store wrapper that fails `UpsertItems` for the Nth feed: `Run` returns an
   error and a `Result` whose `NewItems`/`Items` cover exactly the feeds
   persisted before the failure.
2. CLI-level: with the same failing store injected, `pollAction` writes a
   parseable envelope to stdout and the invocation maps to exit 1.
3. Early hard failure (store fails on `selectFeeds`/`ListFeeds`): stdout stays
   empty, exit 1, unchanged behavior.
4. Success path unchanged: envelope identical to today's (`shapePollResult`
   refactor is behavior-neutral).

## Acceptance criteria

- A mid-persist hard failure exits 1 with a partial envelope on stdout listing
  the items persisted before the failure; stderr still carries the error.
- Early hard failures (store open, feed selection) leave stdout empty.
- `docs/usage.md` and `docs/cli-design.md` document the partial-envelope
  contract.
- `make build` passes.

## Notes

**2026-07-04T15:26:52Z**

Implemented: poll.Run now returns a partial Result alongside a mid-persist error, built via the new orderedItems() helper shared with the success path. pollAction writes the stdout envelope (via new shapePollResult()) before propagating the hard error when result.Polled > 0, distinguishing a mid-persist failure from an early hard failure (store open, unknown named feed) where stdout stays empty. Exit code remains 1 in both hard-failure cases. Added testsupport.FailingUpsertStore (wraps store.Store, fails UpsertItems for a chosen feed URL) to simulate the failure in poll and cli package tests. Updated docs/usage.md and docs/cli-design.md to document that poll's exit 1 may carry a partial envelope. make build passes.
