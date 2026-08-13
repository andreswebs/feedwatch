---
id: fee-kj8z
status: closed
deps: [fee-rzwl]
links: []
created: 2026-08-13T14:03:47Z
type: task
priority: 1
assignee: Andre Silva
parent: fee-ui25
tags: [lib, app]
---

# App use cases: network commands

Fifth step of
[docs/adr/0007-library-and-frontends.md](../docs/adr/0007-library-and-frontends.md).
Moves the four use cases that touch the network into `App`: `poll`, `check`,
`add`, and `discover`. These carry the most logic still living in CLI actions,
including `check`'s concurrent fetch loop and `add`'s feed validation.

## Design

### 1. Method set

In the root `feedwatch` package, one file per use case (`poll.go`, `check.go`,
`add.go`, `discover.go`):

```go
func (a *App) Poll(ctx context.Context, req PollRequest) (PollResult, error)
func (a *App) Check(ctx context.Context, req CheckRequest) (CheckResult, error)
func (a *App) Add(ctx context.Context, req AddRequest) (AddResult, error)
func (a *App) Discover(ctx context.Context, req DiscoverRequest) (DiscoverResult, error)
```

```go
type PollRequest struct {
    Feeds []string // feed URLs or aliases; all due feeds when empty
    Force bool     // poll every active feed, ignoring the schedule
}

type CheckRequest struct {
    Feeds []string // feed URLs or aliases; all active feeds when empty
}

type AddRequest struct {
    URL      string
    Alias    string
    Interval time.Duration
}

type DiscoverRequest struct {
    URL string
}
```

`PollResult`, `PollFailure`, `CheckResult`, `CheckFailure`, `AddResult`, and
`DiscoverResult` move verbatim from `internal/command`, tags, `MarshalJSON`,
`RenderText`, and `CheckResult.ExitCode` included.

### 2. Poll

`App.Poll` absorbs what `pollAction` does today: assembling `poll.Deps` from the
App's config, ports, and clock, calling `poll.Run`, and running
`shapePollResult` over the outcome and its `[]*core.FeedError`.

Two behaviors must survive intact, both currently expressed in the action:

- **Partial results on a mid-persist failure.** `poll.Run` can return a
  populated `Result` *and* a non-nil error when some feeds' writes already
  committed. `App.Poll` returns both, and its doc comment states the contract:
  when the error is non-nil and `res.Polled > 0`, the result is a truthful
  partial envelope the caller should still render; when `res.Polled == 0` the
  failure was early (an unreachable store) and the result must not be rendered.
  This is what keeps stdout empty on an early hard failure and populated on a
  mid-persist one.
- **The warning channel.** `poll.Deps.Warn` is wired to the App's `Warner`
  (`WithWarner`), which the CLI sets to `output.Renderer.Warn`. The
  auto-disable advisory is golden-pinned in
  `internal/command/testdata/auto_disable/`, so its code, message, hint, and
  details must be byte-identical after the move.

Add the exit-code derivation to the envelope, mirroring `CheckResult.ExitCode`,
so the CLI no longer needs `poll.Result`:

```go
// ExitCode derives the process exit code from the outcome: 0 when nothing was
// polled or every polled feed succeeded, 2 when every polled feed failed, and 3
// when some succeeded and some failed.
func (r PollResult) ExitCode() int
```

It computes from `Polled` and `Failed`, which the envelope already carries, and
must agree with `poll.Result.ExitCode` for every input. `internal/poll` keeps
its own method; the two are pinned against each other by a test.

### 3. Check

`App.Check` absorbs `checkAction` whole: target selection (named refs via
`GetFeed`, else all active feeds via `ListFeeds`), the bounded `errgroup` fetch
and parse loop at `Config.Concurrency`, the position-indexed failure slots, and
`checkFeedError`'s classification (extract an existing `*core.FeedError` from
the chain, else `core.NetworkErr`). No store writes.

Note the existing semantics to preserve: an unknown named ref is a hard error
that aborts the whole command, while a fetch or parse failure is a per-feed
outcome that never cancels its siblings (each worker returns nil).

### 4. Add

`App.Add` absorbs `addAction` and takes `validateFeedURL`,
`validateParsesAsFeed`, `feedIsNew`, and `isAbsoluteHTTPURL` with it.
`validateFeedURL` becomes `AddRequest.Validate`, keeping the message verbatim:

```text
add requires an absolute http(s) feed URL; run 'feedwatch discover <url>' to find a feed from a homepage
```

`isAbsoluteHTTPURL` and `validateParsesAsFeed` stay unexported in the root
package and are reused by the OPML import use case in `fee-gvuo`, which is why
they move here rather than being duplicated.

Preserve the three-step shape: validate the URL syntactically, prove it fetches
and parses as a feed (a failure of either is usage-category, pointing at
`discover`), then determine `created` by a `GetFeed` probe before the upsert.
`feedIsNew` treats a usage-category `*core.FeedError` from `GetFeed` as "not
subscribed" and propagates anything else, which is exactly the store contract
documented in `fee-lq28`.

### 5. Discover

`App.Discover` absorbs `discoverAction`: `validateDiscoverURL` becomes
`DiscoverRequest.Validate` (message `discover requires an absolute http(s)
URL`), and the method builds `discover.Deps` from the App's fetcher and parser.

It must not touch the store. The lazy store resolution from `fee-f3u8`
guarantees that, and it is worth an explicit test: running `Discover` against a
config whose store path points into a `t.TempDir()` must leave no file there.

### 6. CLI rewiring

The four actions reduce to flag decoding, one `App` call, and rendering. What
stays in the CLI:

- `poll`: emitting the partial envelope when `err != nil && res.Polled > 0`,
  the "renamed feeds after permanent redirect" info log driven by
  `len(res.Renamed)`, and returning `exitError{code: res.ExitCode()}`.
- `check`: returning `exitError{code: res.ExitCode()}`.
- `Deps.app` (added in `fee-rzwl`) gains `WithWarner(rendererFrom(ctx).Warn)`.

`internal/command/resolve.go` is now used only by the OPML actions and is
deleted in `fee-gvuo`.

## TDD notes

Vertical slices, one use case at a time, in the order `discover`, `add`,
`check`, `poll` (cheapest collaborators first). For each: write the library
test red, move the logic green, then rewire the CLI action and delete the dead
helper.

Drive the tests with the existing doubles: `testsupport.NewFeedServer` for real
HTTP behavior including conditional GET and hit counts, the programmable
`Fetcher` and `Parser` doubles for error injection, `NewInMemoryStore` and
`FailingStore` for store behavior, and `FixedClock` for scheduling. Inject them
through the public options (`WithStore`, `WithFetcher`, `WithParser`,
`WithClock`, `WithWarner`), which is the first real exercise of the extension
point.

Behaviors worth a test each:

- `DiscoverRequest.Validate` rejects a bare host and a non-http scheme with the
  current message.
- `Discover` returns an empty, non-nil `Candidates` slice for a page with no
  feeds, and opens no store.
- `AddRequest.Validate` rejects a scheme-less URL with the discover-pointing
  message.
- `Add` on a URL that fetches but does not parse as a feed returns a
  usage-category error naming `discover`, and stores nothing.
- `Add` on an unreachable URL returns a usage-category error, not a network
  one.
- `Add` reports `created: true` on a fresh subscription and `created: false` on
  an idempotent re-add that updates alias and interval.
- `Check` with no named feeds targets active feeds only.
- `Check` with an unknown named ref returns a hard error and no envelope.
- `Check` reports one failure per failed feed while the others still pass, and
  performs no store writes (assert feed state is unchanged afterwards).
- `Check.ExitCode` is 0 for an empty target set, 2 for all-failed, 3 for
  partial.
- `Poll` returns `Failures` as an empty slice, never null, when nothing failed.
- `Poll` reports `polled == succeeded + failed`, and `deduped == fetched -
  new_items`.
- `Poll` on a second run of an unchanged feed reports zero new items and exit
  code 0.
- `Poll` raises exactly one advisory through the injected `Warner` when a feed
  crosses the failure threshold and is auto-disabled.
- `Poll` returns a populated result alongside a non-nil error when persistence
  fails mid-run (use `FailingStore`), and a zero-valued result when the store
  cannot be opened at all.
- `PollResult.ExitCode` agrees with `poll.Result.ExitCode` across a table of
  polled/failed combinations.

The `internal/command` goldens are the contract net, in particular
`testdata/lifecycle/**`, `testdata/all_failed/**`, `testdata/partial/**`, and
`testdata/auto_disable/**`. All must compare byte-identical with no `-update`
run.

## Acceptance Criteria

- `Poll`, `Check`, `Add`, and `Discover` exist on `*App` with the signatures
  given, alongside their request types with `Validate() error`.
- `PollResult`, `PollFailure`, `CheckResult`, `CheckFailure`, `AddResult`, and
  `DiscoverResult` live in the root package and no longer in `internal/command`.
- `PollResult.ExitCode` exists and is pinned against `poll.Result.ExitCode` by
  a test.
- `validateFeedURL`, `validateParsesAsFeed`, `feedIsNew`, `isAbsoluteHTTPURL`,
  `validateDiscoverURL`, `checkFeedError`, and `shapePollResult` no longer exist
  in `internal/command`.
- `App.Discover` opens no store, proven by a test.
- `App.Poll` documents and implements the partial-result-with-error contract.
- The auto-disable warning is raised through the App's `Warner` and its golden
  is byte-identical.
- Every `internal/command/testdata/**` golden compares byte-identical with no
  `-update` run.
- `make build` passes; `make test-race` passes, since `Check` and `Poll` both
  fan out.

## Files

```text
poll.go, check.go, add.go, discover.go              (new, root package)
poll_test.go, check_test.go, add_test.go,
    discover_test.go                                (new, root package)
internal/command/poll.go, check.go, add.go,
    discover.go                                     (reduced to flags + call + render)
internal/command/app.go                             (WithWarner wiring)
internal/command/schema_registry.go                 (imports the library result types)
```

## Notes

**2026-08-13T15:04:30Z**

Moved poll, check, add, and discover into the library as App methods, in the TDD order discover -> add -> check -> poll (cheapest collaborators first).

New root-package files: discover.go, add.go, check.go, poll.go plus their _test.go. PollRequest/CheckRequest/AddRequest/DiscoverRequest each carry Validate(); PollResult, PollFailure, CheckResult, CheckFailure, AddResult, and DiscoverResult moved verbatim (tags, MarshalJSON, RenderText, ExitCode) out of internal/command. PollResult.ExitCode was added and is pinned against poll.Result.ExitCode by a table test, so internal/command no longer imports internal/poll.

Deps.app now wires WithWarner(rendererFrom(ctx).Warn); without it the auto_disable golden's stderr went empty, which is the regression that golden exists to catch. The four CLI actions are now flag decode + one App call + render; poll keeps the partial-envelope emission (err != nil && res.Polled > 0), the renamed-feeds info log, and the exitError sub-code, and check keeps its exitError.

Deleted from internal/command: validateFeedURL, validateParsesAsFeed, feedIsNew, isAbsoluteHTTPURL, validateDiscoverURL, checkFeedError, shapePollResult, dashIfEmpty. Note for fee-gvuo: import.go carries a temporary copy of two of them (importURLIsAbsoluteHTTP, importEntryParsesAsFeed) with identical messages, since the OPML use case is still in the CLI; delete both when import moves and reuse the library's unexported helpers. resolve.go is now used only by import and export.

Behavior change worth knowing: Add validates the URL over the network before resolving the store (the documented three-step shape), so an unfetchable URL is rejected without provisioning a store. App.Poll returns the zero PollResult on an early failure rather than an all-zeros envelope with an OK head.

Every internal/command/testdata/** golden compared byte-identical with no -update run; make build and make test-race both pass. Learnings appended to docs/specs/learnings.md.
