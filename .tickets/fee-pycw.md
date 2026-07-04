---
id: fee-pycw
status: closed
deps: [fee-udsl]
links: []
created: 2026-07-04T12:21:07Z
type: feature
priority: 2
assignee: Andre Silva
tags: [poll, cli]
---
# poll: add fetched/deduped counters to the poll envelope

## Context

First-customer feedback (v0.0.1): the poll envelope
(`{"polled":141,"succeeded":133,"failed":8,"skipped":0,"new_items":0}`) gives
no visibility into how many items were seen on the wire versus how many were
new versus how many were dedup hits. On subsequent polls this matters: "if
`new_items` is 3 but I don't know how many were fetched total, I can't tell if
feeds are quiet or if dedup is eating everything." The customer suggested
`fetched`, `stored`, and `deduped` counters.

## Design

Add two counters to the envelope; keep `new_items` as the stored count (no
`stored` alias, to avoid two names for one number):

```json
{
  "polled": 141,
  "succeeded": 133,
  "failed": 8,
  "skipped": 0,
  "fetched": 5335,
  "new_items": 5335,
  "deduped": 0,
  "items": [],
  "failures": [],
  "renamed": []
}
```

Definitions:

- `fetched`: total items parsed across all successful 200 responses in this
  run. A 304 (not modified) response carries no body and contributes 0.
  Failed feeds contribute 0.
- `new_items`: unchanged, the count of newly stored items (what
  `UpsertItems` returned).
- `deduped`: `fetched - new_items`, items seen on the wire that were already
  known (refreshed live rows plus tombstoned rows that are never
  resurrected).

Implementation points:

1. `pollTotals` in `src/internal/poll/consume.go` gains a `fetched int`
   field. In `consume`, on the success path, add `len(oc.parsed.Items)` when
   `!oc.result.NotModified` (mirror the guard in `consumeSuccess`; simplest is
   to count inside the loop before calling `consumeSuccess`).
2. `Result` in `src/internal/poll/run.go` gains `Fetched int` and
   `Deduped int` (computed as `totals.fetched - totals.newItems`); `Run` fills
   them.
3. `PollResult` in `src/internal/cli/poll.go` gains, between `Skipped` and
   `NewItems` (struct declaration order controls JSON key order and the
   reflected schema):

    ```go
    Fetched int `json:"fetched"`
    ```

    and after `NewItems`:

    ```go
    Deduped int `json:"deduped"`
    ```

    `pollAction` maps them through. The output schema updates automatically:
    `schemaRegistry` (`src/internal/cli/schema_registry.go`) reflects
    `PollResult{}`.
4. Docs: update the poll envelope examples and field descriptions in
   `docs/usage.md`, and note in the poll section that
   `deduped = fetched - new_items` and that 304 responses contribute nothing
   to `fetched`.

## TDD plan

1. `src/internal/poll/run_test.go`: first poll over the in-memory store, two
   feeds with 2 and 3 items: `Fetched == 5`, `NewItems == 5`, `Deduped == 0`.
2. Second run with unchanged bodies (no validators, so re-fetched and
   re-parsed): `Fetched == 5`, `NewItems == 0`, `Deduped == 5`.
3. A 304 outcome (`NotModified: true`): contributes 0 to `Fetched`.
4. A run mixing one new item into a known feed: `Fetched == n`,
   `NewItems == 1`, `Deduped == n - 1`.
5. CLI-level (`src/internal/cli/poll_test.go`): envelope JSON contains
   `fetched` and `deduped` keys; `schema poll` output schema lists both as
   required properties.

## Acceptance criteria

- Poll envelope carries `fetched` and `deduped` with the definitions above;
  `new_items` semantics unchanged.
- `feedwatch schema poll` documents the new fields (via struct reflection).
- `docs/usage.md` updated.
- `make build` passes.

## Notes

**2026-07-04T15:36:46Z**

Added fetched and deduped counters to the poll envelope. fetched counts items parsed across 200 responses (304s contribute 0); deduped = fetched - new_items. Changes: pollTotals gains fetched field (incremented in consume loop before consumeSuccess for non-NotModified outcomes); Result gains Fetched/Deduped; PollResult gains Fetched/Deduped between Skipped and NewItems; shapePollResult maps them through. Updated 5 e2e golden stdout files and schema_test contract. docs/usage.md poll section updated with field definitions and examples.
