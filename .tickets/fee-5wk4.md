---
id: fee-5wk4
status: closed
deps: [fee-pfpz, fee-o5uq, fee-frus]
links: []
created: 2026-08-14T02:48:06Z
type: task
priority: 2
assignee: Andre Silva
parent: fee-zs6b
tags: [cmd, tags]
---
# poll --tag and check --tag

Lane-scoped poll and check: --tag narrows the due selection (via the new DueFeeds filter), --force --tag narrows the active selection, skippedCount counts against the lane, and --tag combined with positional feed refs is a usage error.

## Design

Add `--tag`/`--match` to `poll` and `check`. One ticket because the two share
their selection rules verbatim, and implementing them separately would produce
two subtly different answers to "what does `--tag` plus a named feed mean".

Reuse the `tagFilter` helper and the `--match` validation established by the
`list` ticket.

Spec: [docs/specs/003-feed-tags/plan.md](docs/specs/003-feed-tags/plan.md),
sections "`poll` — poll a lane" and "`check` — validate a lane".

## Selection rules (identical for both commands)

| Invocation                 | Selection                                  |
| -------------------------- | ------------------------------------------ |
| `poll`                     | due active feeds (unchanged)               |
| `poll --force`             | every active feed (unchanged)              |
| `poll REF...`              | exactly those feeds (unchanged)            |
| `poll --tag ai`            | **due** active feeds in the lane           |
| `poll --force --tag ai`    | every active feed in the lane              |
| `poll --tag ai REF...`     | **usage error, exit 64**                   |

`check` maps the same way onto its own targets: no args means every active
feed, so `check --tag ai` means every active feed in the lane, and
`check --tag ai REF` is a usage error.

The `--tag`-plus-positional case is an error rather than a warning with one
side ignored. Naming feeds and naming a lane are two different selections, and
the tree already treats an unresolvable ref as a hard failure rather than a
silent narrowing; silently discarding half a user's input would be the only
place in the CLI that does so.

`poll --tag` narrowing the **due** selection (not implying `--force`) is what
makes a lane runnable on its own cadence from cron. It is why the store ticket
changed `DueFeeds` to take a `core.ListFilter`.

## Request changes

```go
type PollRequest struct {
	Feeds []string `arg:"feed" variadic:"true"`
	Force bool     `flag:"force" alias:"all" usage:"poll every active feed, ignoring the schedule"`
	Tags  []string `flag:"tag" usage:"poll only feeds carrying this tag (repeatable); cannot be combined with named feeds"`
	Match string   `flag:"match" default:"all" usage:"multi-tag semantics: 'all' (default) or 'any'"`
}
```

`CheckRequest` gains the same two fields with `check`-appropriate usage text.

Both `Validate` methods stop being trivial:

```go
func (r PollRequest) Validate() error {
	if len(r.Tags) > 0 && len(r.Feeds) > 0 {
		return usageErr("--tag cannot be combined with named feeds; " +
			"name feeds to poll exactly those, or use --tag to poll a lane")
	}
	_, err := tagFilter(r.Tags, r.Match)
	return err
}
```

The message must say what to do instead, matching the house style set by
`AddRequest.Validate`, which tells the user to run
`feedwatch discover <url>` to find a feed from a homepage.

## Threading the filter into `internal/poll`

`poll.Run(ctx, d Deps, names []string, force bool)` calls `selectFeeds`, then
`skippedCount`. Add a filter parameter rather than a new `Deps` field —
selection is per-invocation, while `Deps` holds collaborators and tuning:

```go
func Run(ctx context.Context, d Deps, names []string, force bool, filter core.ListFilter) (Result, []*core.FeedError, error)
```

In `selectFeeds` (`internal/poll/select.go`):

- named-refs branch: unchanged (`Validate` has already rejected the
  combination, so no filter applies here).
- `force` branch: `d.Store.ListFeeds(ctx, core.ListFilter{Status:
  core.FeedActive, Tags: filter.Tags, Match: filter.Match})`.
- due branch: `d.Store.DueFeeds(ctx, d.Clock(), filter)`.

**Also update `skippedCount`** in `internal/poll/run.go`, which calls
`ListFeeds(ctx, core.ListFilter{Status: core.FeedActive})` to compute the
total. With a lane filter it must count against the **same** universe, or
`skipped` reports feeds that were never candidates. This is the easiest thing
in the ticket to miss and the hardest to notice in review.

`App.Poll` builds the filter from the request and passes it through.

## `check`

`checkTargets` in `check.go` (root package) takes the filter and passes it to
`ListFeeds` on the no-names path. Status filtering stays first: a disabled feed
in the lane is still skipped by `check`, matching the plan's edge-case table.

## TDD plan

Library tests in `poll_test.go` and `check_test.go` (external, `newNetworkApp`
with `FakeFetcher`/`FakeParser` registered per feed URL); CLI tests in
`internal/command/poll_test.go` and `check_test.go` (`runPoll`/`runCheck`
helpers, `netOpts`). Fixed clock via `pollFixedTime()`.

Fixture: three active feeds — `["ai","agents"]`, `["ai"]`, untagged — with
controllable due times via `NextDueAt`.

1. **(tracer)** `poll --force --tag ai` polls exactly the two tagged feeds;
   `polled == 2` and the untagged feed's fetcher was never called.
2. `poll --tag ai` with only one in-lane feed due polls exactly that one, and
   an out-of-lane due feed is not polled.
3. `skipped` is computed against the lane, not the whole store: with a lane of
   two feeds of which one is due, `polled == 1 && skipped == 1`.
4. `poll --tag ai --tag agents` (default match) polls only the both-tagged
   feed; `--match any` polls both.
5. `poll --tag ai REF` is a usage error, exit 64, and **nothing is fetched or
   written** — assert on the fetcher call count, not just the exit code.
6. `poll` with no `--tag` is unchanged (regression guard over the existing
   scheduled-poll behavior).
7. `check --tag ai` checks only in-lane feeds; `checked` reflects the lane.
8. `check --tag ai REF` is a usage error, exit 64.
9. A disabled feed carrying the tag is skipped by both `poll --force --tag`
   and `check --tag`.
10. `--match bogus` is a usage error on both commands.

## Golden fallout

`testdata/schema/{poll,check,all}.stdout` and
`testdata/help/{poll,check}.stdout` (new flags). The poll behavioral scenarios
(`lifecycle/`, `all_failed/`, `partial/`, `auto_disable/`, `opml/poll`) do not
use `--tag` and should be unchanged; verify rather than assume, since the
`skippedCount` change touches the `skipped` field they all carry.

## Gotchas

- `poll.Run`'s signature change touches `App.Poll` only (the daemon reaches it
  through `App.Poll`), but grep to confirm before assuming.
- The named-refs branch of `selectFeeds` fetches regardless of due-ness by
  design; do not accidentally apply the lane filter there while "being
  consistent".
- `poll` returns a populated result alongside a non-nil error in the
  mid-persist case. The new usage error is an early failure with
  `res.Polled == 0`, so it must not render a partial envelope.

## Acceptance Criteria

- `PollRequest` and `CheckRequest` carry `Tags` and `Match`, validated through
  the shared `tagFilter` helper.
- `poll --tag` narrows the **due** selection; `poll --force --tag` narrows the
  active selection; `check --tag` narrows the active selection.
- `--tag` combined with positional feed refs is a usage error (exit 64) on both
  commands, with nothing fetched and nothing written.
- `poll.Run` takes a `core.ListFilter`, applied in both the force and due
  branches of `selectFeeds` **and** in `skippedCount`, so `skipped` counts
  against the lane.
- Disabled feeds in a lane are still skipped by `poll` and `check`.
- Untagged invocations of both commands are unchanged.
- Behaviors 1-10 covered across the library and command test files.
- `schema/{poll,check,all}.stdout` and `help/{poll,check}.stdout` are
  regenerated; the poll behavioral goldens are confirmed unchanged.
- `make build` passes.

## Notes

**2026-08-14T20:07:16Z**

Implemented --tag/--match on poll and check.

Library: PollRequest and CheckRequest carry Tags/Match, resolved through an unexported filter() method (mirroring ListRequest.filter) that both Validate and the use case call, so the rules are stated once. --tag combined with positional feed refs is a usage error on both commands, rejected before the store is resolved so nothing is fetched.

internal/poll: Run and selectFeeds take a core.ListFilter. It applies to the force branch (via a new activeIn helper that pins Status=active) and the due branch, and crucially to skippedCount, which now counts against the lane instead of every active feed in the store. Named-refs branch is deliberately unfiltered. poll --tag narrows the due selection and does not imply --force.

check: checkTargets takes the filter and sets Status=active on it, so a disabled feed carrying the lane's tag is still skipped.

Tests: behaviors 1-10 covered across poll_test.go, check_test.go, internal/command/poll_test.go, internal/command/check_test.go. The CLI rejection tests assert on the fetcher's per-URL request count, not just exit 64, so a validation that ran after the store was dialed would fail. newPollApp now returns the fetcher and pollFeed gained tags/notDue/disabled fields; existing callers updated.

Goldens: schema/{poll,check,all}.stdout and help/{poll,check}.stdout regenerated. The poll behavioral goldens (lifecycle, all_failed, partial, auto_disable, opml/poll) are unchanged, confirmed via git status despite the skippedCount change touching the skipped field they all carry. reflectflags_test.go requestSurfaceCases updated: poll 1->3 flags, check 0->2.

make build passes.
