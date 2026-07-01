---
id: fee-e1s2
status: closed
deps: []
links: []
created: 2026-06-30T22:54:52Z
type: feature
priority: 2
assignee: Andre Silva
parent: fee-bqyy
tags: [beta, cli, poll]
---
# Poll failure visibility in the result envelope

Implements Req 1 of the 002-beta spec
([requirements.md](../docs/specs/002-beta/requirements.md), section 1). Amends
baseline Req 3 (Outcome Signaling) and Req 15 (Failure Handling). Purely
additive: no existing field changes meaning.

Today a poll's stdout envelope is `{polled, skipped, new_items, items}` and
deliberately omits any failure detail; per-feed failures live only on stderr as
`{"errors":[...]}`. An agent that discards stderr cannot tell a clean poll from
one where feeds failed. This change reports the outcome on the result stream so
a partial failure is triageable from stdout alone, while stderr keeps the full
human-readable detail.

## Design

The `poll` envelope gains, alongside the existing fields:

- `succeeded`: count of feeds polled without error.
- `failed`: count of feeds that errored.
- `failures`: a list with one entry per failed feed, each
  `{feed_url, category, status?}`. `status` is present only for HTTP failures;
  it is omitted for `network`, `parse`, and `timeout` failures that carry no
  status. The list is always present, empty (`[]`, never `null`) when no feed
  failed.

Invariant: `polled == succeeded + failed`. stderr is unchanged: the full
per-feed `*FeedError` detail (including the human message) is still written
there. Exit-code semantics are unchanged (0 all succeeded, 2 all failed,
3 partial, 1 hard failure).

```sh
feedwatch poll 2>err.json; echo "exit=$?"
# stdout: {"polled":2,"succeeded":1,"failed":1,"skipped":0,"new_items":2,
#          "items":[...],"failures":[{"feed_url":"https://x/feed","category":"http","status":404}]}
# err.json: {"errors":[{"feed_url":"https://x/feed","category":"http","status":404,
#                       "message":"404 Not Found"}]}
# exit=3
```

## Acceptance Criteria

- The `poll` envelope includes `succeeded` and `failed` counts.
- The existing `polled` count is preserved and `polled == succeeded + failed`.
- The envelope includes a `failures` list whose entries carry `feed_url`,
  `category`, and, where applicable, `status`.
- `failures` is an empty list (not absent, not `null`) when no feed failed.
- A failure with no HTTP status (network, parse, timeout) omits `status`.
- The full per-feed error detail, including the message, is still written to
  stderr.
- Poll exit codes are unchanged: full success, total failure, and partial
  failure remain distinguished (0 / 2 / 3).

## Implementation Plan

The per-feed failures are already in hand: `poll.Run` returns
`(Result, []*core.FeedError, error)` and `pollAction` already has the
`feedErrs` slice it writes to stderr. `consume` appends exactly one
`*core.FeedError` per failed outcome, so `len(feedErrs) == result.Failed`. The
envelope's `failures` is a projection of that same slice; nothing new needs to
be computed in the `poll` package.

1. Extend the envelope in `src/internal/cli/poll.go`. Add a failure entry type
   and the new fields, and update the (now stale) doc comment on `PollResult`:

   ```go
   // PollFailure is one failed feed in the poll envelope: the feed URL, its
   // error category, and the HTTP status when the category is http (omitted
   // otherwise).
   type PollFailure struct {
       FeedURL  string        `json:"feed_url"`
       Category core.Category `json:"category"`
       Status   int           `json:"status,omitempty"`
   }

   type PollResult struct {
       Polled    int           `json:"polled"`
       Succeeded int           `json:"succeeded"`
       Failed    int           `json:"failed"`
       Skipped   int           `json:"skipped"`
       NewItems  int           `json:"new_items"`
       Items     []core.Item   `json:"items" jsonschema:"opaque"`
       Failures  []PollFailure `json:"failures"`
   }
   ```

2. Populate it in `pollAction` (`src/internal/cli/poll.go`). Build `failures`
   from `feedErrs` and compute `succeeded`. Initialize the slice with `make` so
   it marshals to `[]`, never `null`, when empty:

   ```go
   failures := make([]PollFailure, 0, len(feedErrs))
   for _, fe := range feedErrs {
       failures = append(failures, PollFailure{
           FeedURL:  fe.FeedURL,
           Category: fe.Category,
           Status:   fe.Status,
       })
   }
   res := PollResult{
       Polled:    result.Polled,
       Succeeded: result.Polled - result.Failed,
       Failed:    result.Failed,
       Skipped:   result.Skipped,
       NewItems:  result.NewItems,
       Items:     result.Items,
       Failures:  failures,
   }
   ```

   The existing `r.Errors(feedErrs)` stderr write and the
   `exitError{code: result.ExitCode()}` return stay exactly as they are, so
   stderr detail and exit codes are untouched.

3. `Status: 0` plus `json:"status,omitempty"` yields the required omission for
   non-HTTP categories. `category` and `feed_url` are always emitted.

4. Schema regenerates itself. `schemaRegistry["poll"]` already uses
   `jsonschema.Reflect(PollResult{})` (`src/internal/cli/schema_registry.go`),
   so adding fields updates the published schema automatically; the golden
   schema fixtures will need regenerating.

5. Text mode (`--format text`): `PollResult` has no `RenderText`, so it falls to
   the generic struct dump in `src/internal/output/renderer.go`, which now also
   prints the new counts. A dedicated `RenderText` is out of scope; the JSON
   contract is what the spec governs.

No change to the `poll` package, the store, or the fetch layer is required.

## Verification

- Tests (`src/internal/cli`, table-driven, injecting a `testsupport` fetcher
  that fails one feed and succeeds another): a one-fail/one-succeed poll yields
  `succeeded:1, failed:1, polled:2` with a single `failures` entry carrying
  `feed_url`/`category`/`status`; an all-success poll yields `failures:[]`
  (asserted as present and empty in the raw JSON, distinct from `null`); a
  network failure entry has no `status` key in the JSON.
- Assert the exit code is still 3 on partial and 2 on total failure, and that
  stderr still carries `{"errors":[...]}` with the message.
- Regenerate and review the golden schema and any poll golden-output fixtures.
- `make build` green; add a learnings entry under the Req 1 heading in
  [learnings.md](../docs/specs/001-initial-implementation/learnings.md).

## References

- Spec: [requirements.md](../docs/specs/002-beta/requirements.md) section 1.
- Design: [cli-design.md](../docs/cli-design.md), "Exit Codes" and "Error
  Handling and Logging".
- Field notes: [usage-learnings.md](../docs/specs/002-beta/usage-learnings.md),
  "never discard stderr".
- Related: `fee-9otc` (Req 6) adds `renamed` to this same envelope and depends
  on this ticket.

## Notes

**2026-06-30T23:17:53Z**

Implemented poll failure visibility (Req 1, 002-beta). Added PollFailure{feed_url,category,status?} and succeeded/failed counts + failures[] list to PollResult in cli/poll.go; failures is a pure projection of the existing feedErrs slice (succeeded = polled - failed). failures built with make(...,0,n) so it marshals to [] never null (asserted on raw JSON); status uses omitempty so non-HTTP failures omit it. stderr detail and exit codes (0/2/3) unchanged. Updated cli poll_test.go (added mixed/all-success/all-failed/network-no-status assertions), regenerated 5 e2e poll.stdout goldens (go test ./internal/e2e -update), updated the hand-pinned poll contract in cli/schema_test.go (schema itself auto-reflects), and updated docs/usage.md. make build green.
