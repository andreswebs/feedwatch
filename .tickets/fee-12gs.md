---
id: fee-12gs
status: closed
deps: [fee-q6t3, fee-po72]
links: []
created: 2026-06-29T19:28:37Z
type: task
priority: 2
assignee: Andre Silva
parent: fee-5d25
tags: [cmd]
---

# poll: output-shaping and exit code

Implement the output-shaping stage of poll: build the PollResult stdout envelope, emit per-feed errors to stderr, and derive exit 0/2/3 from the outcome summary via exitError. Refs: docs/cli-design.md (Input and Output Contract, Exit Codes, Error Handling and Logging, Poll Semantics).

## Design

The output-shaping stage of `poll`: build the stdout envelope and derive the exit
code; emit per-feed errors to stderr.

```go
type PollResult struct {
  Polled  int          `json:"polled"`
  Skipped int          `json:"skipped"`
  NewItems int         `json:"new_items"`
  Items   []core.Item  `json:"items"`
}
```

- Build `PollResult` from the consume totals; render to stdout via
  `output.Renderer`.
- Emit each per-feed `*core.FeedError` to stderr (the streams contract: errors on
  stderr, not in the stdout envelope).
- Derive the exit code from the outcome summary (not a returned Go error):
  all succeeded -> 0; all targeted feeds failed -> 2; mixed -> 3. Returned as an
  `exitError` from the action so the boundary maps it.
- SIGINT/SIGTERM during the poll: persist completed feeds, still emit the
  envelope for completed work, exit 130/143 (the signal handling lives in the
  CLI boundary/main; this stage emits what it has).

TDD plan (cmd.Run with stub outcomes + captured Out/Err + OsExiter):

1. (tracer) all-success poll writes the envelope to stdout and exits 0.
2. all-failed poll writes per-feed errors to stderr and exits 2 (envelope still
   on stdout with empty items).
3. mixed poll exits 3; stdout has the successes, stderr has the failures.
4. items appear in the stdout envelope; errors never appear on stdout.

Deep-module note: pure shaping of totals -> envelope + exit code; no I/O beyond
the renderer.

## Acceptance Criteria

- Builds the `PollResult` envelope on stdout, emits per-feed errors on stderr,
  and derives exit 0/2/3 from the outcome summary (via `exitError`).
- Behaviors 1-4 covered through `cmd.Run`.
- Supports Req 2, 3 (exit codes), 9. `make validate` passes.

## Notes

**2026-06-29T23:06:04Z**

Wired the poll command end-to-end (output-shaping + exit code), the capstone of the three poll lanes (orchestrate fee-u0i4, consume fee-q6t3, this).

New code:

- internal/poll/run.go: exported poll.Run(ctx, Deps, names, force) ties select->orchestrate->consume and returns an exported Result{Polled,Skipped,NewItems,Failed,Items} plus the per-feed []*core.FeedError and a hard error. Result.ExitCode() derives 0/2/3 from the outcome summary (polled==0 or failed==0 ->0; failed==polled ->2; mixed ->3) -- never from a Go error. Items are flattened from totals.newByFeed in feed-selection order (the map is keyed per feed precisely so the output stage can order; iterating the selected feeds slice restores stable order). skippedCount runs an extra ListFeeds(active) only on the unnamed/unforced due path (active-not-due = skipped); named/force runs skip nothing.
- internal/cli/poll.go: poll subcommand (--force/--all), PollResult stdout envelope {polled,skipped,new_items,items} -- deliberately NO failed count (exit code says whether, stderr says which, per the design's streams contract). Action writes envelope via renderer.Result, emits per-feed errors via renderer.Errors to stderr, returns exitError{2|3} for the boundary. pollDeps prefers injected Deps.Store/Fetch/Parse (the test seam) and builds production ones otherwise; buildFetcher maps all config knobs and passes WithRetry(cfg.RetryAttempts, 0) so fetch.New keeps its default backoff (config has no RetryBackoff field) while honoring the attempt count (New defaults to 1, config wants 3).
- internal/cli/storeopen.go: openStoreMigrated(ctx,cfg,clock) opens then Migrate()s -- the 'every command except migrate auto-ensures schema on open' resolution flagged in the fee-c66o note. migrate still uses bare openStore (it manages migrations explicitly), so its bare-migrate applied count contract is preserved.

Tests: 3 cli integration behaviors via cmd.Run with injected InMemoryStore/FakeFetcher/FakeParser doubles (all-success->exit0/empty stderr; all-failed->exit2/{errors:[2]}/empty items; mixed->exit3/success on stdout/failure only on stderr) + a poll.Result.ExitCode table test. Race-clean. Production wiring smoke-tested: 'feedwatch --db tmp poll' on an empty store auto-migrates and emits {polled:0,...} exit 0.

Not in scope (left for later tickets): 301/308 URL rewrite (no Store.RenameFeed yet; FetchResult.FinalURL/Permanent carried but unused); SIGINT/SIGTERM partial-persist + 130/143 already lives in main's signal context + orchestrate's cancellation handling -- this stage just emits the completed work.
