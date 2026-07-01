---
id: fee-9otc
status: closed
deps: [fee-e1s2]
links: []
created: 2026-06-30T22:54:52Z
type: feature
priority: 2
assignee: Andre Silva
parent: fee-bqyy
tags: [beta, cli, poll]
---
# Permanent-redirect rename visibility

Implements Req 6 of the 002-beta spec
([requirements.md](../docs/specs/002-beta/requirements.md), section 6). Amends
baseline Req 9 (Polling) and Req 10 (Conditional Requests and Fetching). Purely
additive. Depends on Req 1 (`fee-e1s2`) because both reshape the same poll
result envelope; Req 1 lands the envelope expansion first.

`fee-n6j6` already made `poll` rename a feed to its canonical URL on a permanent
redirect (HTTP 301/308) and cascade the rename to that feed's items. But the
rename is silent: the agent only discovers it when a later `items --feed
<old-url>` returns nothing (observed: `https://aihero.dev/rss.xml` became
`https://www.aihero.dev/rss.xml` with no signal). This change reports each
rename in the poll envelope and on stderr, so the new identity is learned at
rename time.

## Design

- The poll envelope gains a `renamed` list, each entry `{from, to}` naming the
  prior and new canonical URL. It is always present, empty (`[]`, never `null`)
  when nothing was renamed.
- When one or more feeds are renamed, an informational line is written to
  stderr.
- The rename and item cascade themselves are unchanged (already implemented in
  `fee-n6j6`).

```sh
feedwatch poll 2>poll.log
# stdout: {"polled":1,"succeeded":1,"failed":0,"skipped":0,"new_items":3,
#          "items":[...],"failures":[],
#          "renamed":[{"from":"https://aihero.dev/rss.xml","to":"https://www.aihero.dev/rss.xml"}]}
# poll.log: ... level=INFO msg="renamed feeds after permanent redirect" count=1
```

## Acceptance Criteria

- `poll` continues to rename a feed to its canonical URL on a 301/308, cascading
  to its items (unchanged from `fee-n6j6`).
- When a poll renames a feed, the envelope reports the rename with both the prior
  URL and the new URL.
- When a poll renames one or more feeds, an informational stderr log line is
  emitted.
- `renamed` is an empty list (not absent, not `null`) when no feed was renamed.

## Implementation Plan

The rewrite already happens inside `consumeSuccess`
(`src/internal/poll/consume.go`), which computes a `finalURL` for a permanent
redirect and passes it to `RecordSuccess`. But the store may decline the rename
(when the redirect target is already subscribed, it keeps the original URL and
does not merge). So reporting must reflect what actually happened, not just the
intent: the store needs to tell the caller the URL it landed on. Reporting the
intended target would falsely claim a rename the store skipped, which is exactly
the kind of dishonesty this beta set is removing.

1. Make `RecordSuccess` report the resulting URL. Change the `Store` interface
   in `src/internal/store/store.go`:

   ```go
   // RecordSuccess ... returns renamedTo, the new canonical URL when a
   // permanent-redirect rewrite was applied, or "" when the feed URL was
   // unchanged (including when a rename was declined because the target was
   // already subscribed).
   RecordSuccess(ctx context.Context, url string, fetchedAt, nextDue time.Time, finalURL string) (renamedTo string, err error)
   ```

   - SQLite (`src/internal/store/sqlite/feeds.go`): the rename branch already
     computes `target` (either `finalURL` or, when the target is taken, `url`).
     Return `finalURL` when `target != url` (an actual rename), else `""`. The
     no-rename fast path returns `("", nil)`.
   - In-memory double (`src/internal/testsupport/store.go`): `RecordSuccess`
     already computes `target`; return `target` when `target != url`, else `""`.

2. Thread it through the poll helper. In `src/internal/poll/lifecycle.go`,
   `RecordSuccess` wraps the store call; change it to return
   `(renamedTo string, err error)` and pass the store's return through.

3. Capture the rename in `consumeSuccess` (`src/internal/poll/consume.go`).
   `consumeSuccess` currently returns `([]core.Item, error)`; widen it to also
   report the rename, e.g. return `([]core.Item, core.FeedRename, error)` where
   a zero `FeedRename` means none, or set it on the outcome. When the store
   reports `renamedTo != ""`, the rename is `{From: oc.feed.URL, To: renamedTo}`.

4. Define the pair type in `src/internal/core/types.go` so both the poll
   `Result` and the cli envelope can share it with the agent-facing JSON names:

   ```go
   // FeedRename records a feed URL changed by a permanent-redirect rewrite
   // during a poll.
   type FeedRename struct {
       From string `json:"from"`
       To   string `json:"to"`
   }
   ```

5. Aggregate in the poll result. In `src/internal/poll/consume.go` add
   `renames []core.FeedRename` to `pollTotals` and append each non-empty rename;
   in `src/internal/poll/run.go` add `Renamed []core.FeedRename` to `Result` and
   carry the aggregate through `Run`.

6. Surface in the envelope. In `src/internal/cli/poll.go` add to `PollResult`
   (which Req 1, `fee-e1s2`, has already expanded):

   ```go
   Renamed []core.FeedRename `json:"renamed"`
   ```

   In `pollAction`, set `Renamed` from `result.Renamed`, initializing with
   `make([]core.FeedRename, 0, ...)` so it marshals to `[]`, never `null`, when
   empty. When `len(renamed) > 0`, emit one info line via the context logger:

   ```go
   if len(res.Renamed) > 0 {
       loggerFrom(ctx).InfoContext(ctx, "renamed feeds after permanent redirect",
           "count", len(res.Renamed))
   }
   ```

7. Schema regenerates: `schemaRegistry["poll"]` uses
   `jsonschema.Reflect(PollResult{})`, so `renamed` appears automatically;
   regenerate the golden schema fixtures.

## Verification

- Tests (`src/internal/poll` and `src/internal/cli`, using the `testsupport`
  fetcher to return a permanent redirect via `core.FetchResult{Permanent:true,
  FinalURL:...}`): a 301/308 to a fresh URL yields a `renamed` entry with the
  right `{from,to}` and one stderr info line; a redirect whose target is already
  subscribed is declined and yields no `renamed` entry (honest reporting); a
  temporary redirect (302/307) yields none; a poll with no renames yields
  `renamed:[]` (asserted present and empty, distinct from `null`).
- Store-level tests for the new `RecordSuccess` return value (rename applied vs
  declined) in both the SQLite store and the in-memory double.
- Regenerate the golden schema and any poll golden-output fixtures.
- `make build` green; learnings entry under the Req 6 heading in
  [learnings.md](../docs/specs/001-initial-implementation/learnings.md).

## References

- Spec: [requirements.md](../docs/specs/002-beta/requirements.md) section 6.
- Design: [cli-design.md](../docs/cli-design.md), "Poll Semantics" (Rename
  visibility) and "Fetching and HTTP".
- Field notes: [usage-learnings.md](../docs/specs/002-beta/usage-learnings.md),
  "Feed identity churn".
- Builds on: `fee-n6j6` (the 301/308 rewrite and item cascade). Depends on:
  `fee-e1s2` (Req 1) for the expanded poll envelope.

## Notes

**2026-07-01T00:03:36Z**

Implemented Req 6 (permanent-redirect rename visibility). Store.RecordSuccess now returns (renamedTo string, err error): the actual landing URL when a 301/308 rename was applied, or "" when unchanged (including a rename declined because the target was already subscribed) — so poll reports what the store did, not what it intended. Threaded through poll.RecordSuccess helper, consumeSuccess, pollTotals.renames, poll.Result.Renamed, into cli PollResult.Renamed (json "renamed", always [] never null). Added core.FeedRename{From,To}. Emits one INFO stderr line 'renamed feeds after permanent redirect' with count when >=1 rename (default log level is info). Updated all RecordSuccess implementers/callers (sqlite, in-memory double, fakeStore mock, run_interrupt wrapper, enable.go). Tests: poll.Run rename/declined/temporary cases, cli envelope+log-line, in-memory + sqlite store return-value tests. Regenerated e2e poll goldens (+"renamed":[]) and updated TestOutputSchemaContractPreserved. make build green.
