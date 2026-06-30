---
id: fee-q6t3
status: closed
deps: [fee-u0i4, fee-lzyw]
links: []
created: 2026-06-29T19:28:37Z
type: task
priority: 2
assignee: Andre Silva
parent: fee-5d25
tags: [cmd]
---

# poll: dedup-and-consume

Implement the dedup-and-consume stage of poll: assign dedup keys, upsert items (new-only), persist changed-only validators without empty-clobber, and drive the failure lifecycle (success/failure). Refs: docs/cli-design.md (Poll Semantics, Failure Handling).

## Design

The dedup-and-consume stage of `poll`: turn fetch outcomes into persisted state.

For each successful outcome:

- Assign `core.Item.DedupKey` via the dedup-key function to every parsed item.
- `store.UpsertItems(feedURL, items)` -> returns the newly-inserted (unseen)
  items; existing keys (live or tombstoned) are not new.
- Persist conditional-GET validators: write only the changed validator; never
  overwrite a stored one with an empty value; skip the write when both are empty.
- Update the failure lifecycle: `RecordSuccess` on success/304 (sets next due
  honoring `<ttl>` or interval); `RecordFailure` on a feed error (count, backoff,
  auto-disable).

```go
type pollTotals struct { polled, skipped, newItems, failed int; newByFeed map[string][]core.Item }
func consume(ctx, deps, outcomes []feedOutcome, clk core.Clock) (pollTotals, []*core.FeedError)
```

TDD plan (real store + fixed clock):

1. (tracer) a feed's new items are returned and marked seen; an immediate second
   poll yields zero new (dedup).
2. validators from the fetch are persisted; an empty new ETag does not clobber a
   stored one.
3. a successful poll calls `RecordSuccess` (next_due advances; honors ttl).
4. a failed outcome calls `RecordFailure` (count up, backoff set).
5. items get dedup keys before upsert (guid/link/title).

Deep-module note: consumes outcomes into the store; reads back nothing for output
beyond the totals it returns.

## Acceptance Criteria

- Assigns dedup keys, upserts items (new-only detection), persists validators
  (changed-only, never empty-clobber), and drives the failure lifecycle.
- Behaviors 1-5 covered against the real store + fixed clock.
- Supports Req 9 (auto-consume, dedup, conditional-GET persistence) and 15.
  `make validate` passes.

## Notes

**2026-06-29T22:57:44Z**

Implemented the dedup-and-consume stage in internal/poll/consume.go: consume(ctx, Deps, []feedOutcome) (pollTotals, []*core.FeedError, error). Per outcome: success/304 -> SetValidators (changed-only; store already skips empties so an empty ETag never clobbers), assign parse.DedupKey to each item then UpsertItems (new-only), RecordSuccess with the effective interval (feed.Interval -> parsed `<ttl>` -> DefaultInterval); failure -> RecordFailure (count/backoff/auto-disable) and collect a per-feed*core.FeedError. Added 3 lifecycle knobs to poll.Deps (DefaultInterval, FailureThreshold, MaxBackoff). Behaviors 1-5 covered against the REAL sqlite store + fixed clock (consume_test.go, package poll/white-box since consume is unexported). make build + go test -race ./internal/poll green.

Design decisions: (1) consume returns a 3rd 'error' value (not in the ticket sketch's 2-tuple) for HARD store-write failures -> exit 1, keeping the design's split between whole-invocation errors and per-feed *FeedError (exit 2/3); fee-12gs needs this to map exit codes. (2) Dropped the 'skipped' field from the pollTotals sketch: not derivable from outcomes (not-due feeds are never fetched) and staticcheck flags an unwritten field as unused -- fee-12gs (output shaping) adds it from the selection result. (3) Permanent-redirect (301/308) URL rewrite is NOT handled here: out of acceptance scope and there is no store RenameFeed method yet.
