---
id: fee-vzu8
status: closed
deps: [fee-gyos]
links: []
created: 2026-06-29T19:28:36Z
type: task
priority: 1
assignee: Andre Silva
parent: fee-171q
tags: [cmd]
---

# Failure lifecycle (count, backoff, auto-disable, reset)

Implement the persisted failure lifecycle over the store: reset-on-success, exponential backoff (base the feed interval, cap 24h), and auto-disable at a configurable threshold (default 10), driven by an injected clock. Distinct from the E3 in-call retry. Refs: docs/cli-design.md (Failure Handling).

## Design

The persisted failure lifecycle on top of the store, distinct from the in-call
transient retry (E3). Lives in `src/internal/poll` (or `src/internal/feedstate`)
as pure functions over the `store.Store` plus an injected `core.Clock`.

```go
// after a successful fetch
func RecordSuccess(ctx, s store.Store, clk core.Clock, url string, interval, def time.Duration) error
//   sets failure_count=0, clears last_error, sets last_fetch_at=now,
//   next_due_at = now + effective interval (feed interval or default; honoring
//   a parsed <ttl> is applied by the poll orchestrator).

// after a failure (in-call retries already exhausted)
func RecordFailure(ctx, s store.Store, clk core.Clock, url string, cat core.Category, msg string,
    threshold int, baseBackoff, maxBackoff time.Duration) error
//   increments failure_count; sets last_error/at; computes exponential backoff
//   next_due_at = now + min(maxBackoff, baseBackoff * 2^(failure_count-1));
//   when failure_count >= threshold, sets status=disabled.
```

Backoff defaults from config: threshold 10, base = the feed interval, max 24h.

TDD plan (against the real SQLite store + a fixed `core.Clock`):

1. (tracer) `RecordFailure` increments the count and sets `next_due_at` to
   now + base backoff.
2. consecutive failures grow backoff exponentially, capped at max.
3. reaching the threshold sets `status=disabled`.
4. `RecordSuccess` resets count to 0, clears last_error, and sets `next_due_at`
   to now + interval.
5. a disabled feed is excluded from `DueFeeds`.

Deep-module note: pure functions over the Store interface + Clock; deterministic
under a fixed clock. `enable`/`disable` commands and `poll` call these.

## Acceptance Criteria

- `RecordSuccess`/`RecordFailure` implement reset-on-success, exponential backoff
  (base=interval, cap 24h), and auto-disable at the threshold (default 10).
- Deterministic under an injected `core.Clock`.
- Behaviors 1-5 covered against the real store.
- Supports Req 15 (persisted lifecycle, distinct from in-call retry).
  `make validate` passes.

## Notes

**2026-06-29T22:40:58Z**

Implemented persisted failure lifecycle as pure functions in internal/poll/lifecycle.go (poll.RecordSuccess, poll.RecordFailure) over store.Store + injected core.Clock. RecordSuccess clears failure state and schedules next_due_at = now + effective interval (feed interval, else def). RecordFailure reads current count via GetFeed, increments, schedules next_due_at = now + min(maxBackoff, base*2^(count-1)) computed iteratively with overflow guard, and SetStatus(disabled) once count >= threshold. Defaults come from config (FailureThreshold=10, MaxBackoff=24h, base=effective interval). All 5 ticket behaviors covered against the real SQLite store with a fixed clock (lifecycle_test.go); go test -race clean. NOTE for next person: this layer is distinct from E3 in-call retry. The downstream poll orchestrator (fee-u0i4) and enable/disable commands (fee-as1j) call these; the orchestrator must apply parsed `<ttl>` before choosing the interval it passes in, and pass cfg.FailureThreshold/MaxBackoff and the effective interval as baseBackoff. The store's low-level RecordFailure does NOT disable (no status change); disable is decided in poll.RecordFailure. GetFeed-then-write is safe only because poll serializes per-feed (rows never shared concurrently) -- documented in the function comment.
