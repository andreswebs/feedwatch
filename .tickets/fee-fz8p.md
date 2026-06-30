---
id: fee-fz8p
status: closed
deps: [fee-n6j6]
links: []
created: 2026-06-30T03:42:16Z
type: bug
priority: 1
tags: [cli]
---

# poll: SIGINT/SIGTERM do not exit 130/143 or persist completed work

SIGINT and SIGTERM during `poll` do not honor the documented graceful-shutdown contract. Found during manual QA (TC-EXEC-004 / TC-EXEC-005); see `docs/qa.result.bak.md` BUG-001.

## Design

Expected (REQ 1, `docs/usage.md` exit codes): on `SIGINT` stop starting new fetches, persist already-completed feeds, emit the result envelope for completed work on stdout, and exit `130`; on `SIGTERM` exit `143`.

Observed (5/5 runs each signal): exit `1`; empty stdout; a single error object on stderr with category `internal`:

```json
{
  "error": {
    "category": "internal",
    "message": "persist validators for \"...\": set validators \"...\": context canceled"
  }
}
```

A subsequent `feedwatch items` returns 0 rows, so no completed-feed work is persisted.

Repro:

```sh
feedwatch --db "$DB" import two-feeds.opml   # one fast, one /slow/atom.xml?ms=8000
feedwatch --db "$DB" poll --all & PID=$!
sleep 2; kill -INT $PID; wait $PID; echo "exit=$?"   # -> 1, expected 130
feedwatch --db "$DB" items | jq ".items|length"      # -> 0, expected fast-feed items
```

Likely cause: the signal cancels the shared context, which propagates into the persistence layer and aborts the whole poll (poll appears to commit at the end rather than per completed feed), so the cancel-path is treated as an internal error instead of a graceful interrupt.

## Acceptance Criteria

- `SIGINT` during `poll` exits `130`; `SIGTERM` exits `143`.
- Feeds that completed before the signal are persisted and appear in `items`.
- The result envelope for completed work is written to stdout.
- No `internal`-category "context canceled" error leaks to stderr on interrupt.

## Implementation Plan

Decision (confirmed): signal handling is process-wide. `main` owns the signal-to-exit-code mapping (`128 + signum`, i.e. SIGINT -> `130`, SIGTERM -> `143`); the poll layer persists already-completed work on a non-cancelled context. Stopping new fetches already happens in `orchestrate` (it returns `nil` on `gctx.Done()`); the bug is that `consume` runs on the cancelled context and its store writes fail with `context.Canceled`, which escalates to an `internal` error and discards the partial result.

1. Capture the signal and map the exit code in `src/cmd/feedwatch/main.go`. Replace `signal.NotifyContext` with explicit notification so the actual signal is known:

   ```go
   ctx, cancel := context.WithCancel(context.Background())
   defer cancel()

   sigCh := make(chan os.Signal, 1)
   signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
   defer signal.Stop(sigCh)

   caught := make(chan os.Signal, 1)
   go func() {
       if s, ok := <-sigCh; ok {
           caught <- s // buffered; recorded before cancel propagates
           cancel()
       }
   }()

   err := cli.NewRootCommand(deps).Run(ctx, os.Args)

   select {
   case s := <-caught:
       if sig, ok := s.(syscall.Signal); ok {
           os.Exit(128 + int(sig)) // SIGINT -> 130, SIGTERM -> 143
       }
       os.Exit(1)
   default:
   }
   cliv3.HandleExitCoder(err)
   ```

   The buffered `caught` send happens before `cancel()`, so by the time `Run` returns (which requires the cancellation to have propagated) the signal is already recorded; the post-`Run` non-blocking read observes it.

2. Persist completed work on a non-cancelled context. In `Run` (`src/internal/poll/run.go`), run the persistence stage on a context detached from the signal so the writes for feeds `orchestrate` already fetched complete:

   ```go
   outcomes := orchestrate(ctx, d, feeds)
   persistCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), persistGrace)
   defer stop()
   totals, feedErrs, err := consume(persistCtx, d, outcomes)
   ```

   `orchestrate` still receives the cancellable `ctx`, so it stops starting new fetches on the signal. `consume` persists every outcome it was given. No `ctx.Done()` guard is added to `consume`: that would skip persisting outcomes when the signal arrived during the fetch phase (the common case and the source of the "0 rows" symptom). `persistGrace` is a small fixed bound (a few seconds).

3. Backstop in the error boundary. In `src/internal/core/errors.go` (`ExitCodeFor`) and/or `feedErrorFor` (`src/internal/cli/root.go`), treat `errors.Is(err, context.Canceled)` as a non-`internal` outcome, so any residual cancellation cannot leak as an `internal` error. With step 2 this path should be unreachable from poll, but it hardens every other store write site.

Result: `pollAction` writes the partial envelope to stdout as usual and returns normally; `main` overrides the exit code to `130`/`143` when a signal was caught; stderr carries no `internal` error.

Verification:

- Tests driving `Run` with a context that cancels mid-fetch (one fast feed, one slow): assert completed feeds are persisted and queryable, the envelope reflects completed work, and `consume` returns no error.
- An end-to-end signal test (if the harness supports it): exit `130` on SIGINT and `143` on SIGTERM, with completed-feed items present and empty stderr.
- `make build` green; learnings entry.

## Notes

**2026-06-30T03:42:16Z**

Source: manual QA execution report docs/qa.result.bak.md, defect BUG-001 (TC-EXEC-004, TC-EXEC-005). Severity High, Priority P1.

**2026-06-30T14:10:03Z**

Fixed in three parts. (1) poll.Run now persists completed feeds on a context detached from the signal: orchestrate keeps the cancellable ctx (stops scheduling new fetches), consume runs on context.WithTimeout(context.WithoutCancel(ctx), persistGrace=5s). No ctx.Done guard in consume — that would drop the common case (signal during fetch) and reproduce the 0-rows symptom. (2) main wraps cliv3.OsExiter to override the exit code with 128+signum when a signal was caught; this is required because urfave/cli's ExitErrHandler exits from INSIDE Run via OsExiter (a post-Run check is dead code). Happens-before: handler does buffered 'caught<-s' before cancel(), and only cancel() unwinds Run into OsExiter, so the signal is observable race-free. (3) feedErrorFor maps context.Canceled/DeadlineExceeded to CatTimeout (was falling through to CatInternal) as a backstop for other store sites. Tests: poll unit test with a ctx-checking store double + signalling fetcher (deterministic, no sleep); e2e signal test spawns the real binary with two feeds on different httptest hosts, SIGINT->130 and SIGTERM->143, asserts items persisted and no internal error on stderr. make build green.
