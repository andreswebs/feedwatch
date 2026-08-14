---
id: fee-93m0
status: open
deps: [fee-5wk4]
links: []
created: 2026-08-14T02:51:32Z
type: task
priority: 3
assignee: Andre Silva
parent: fee-zs6b
tags: [daemon, tags]
---
# daemon lane scoping

Add daemon.WithTags and daemon.WithMatch options that populate the PollRequest the scheduler already issues, so one long-running process can watch a single lane. No selection logic in the daemon itself.

## Design

Let an embedded daemon watch a single lane. The smallest ticket in the epic,
sequenced last so it builds on a settled poll selection.

Spec: [docs/specs/003-feed-tags/plan.md](docs/specs/003-feed-tags/plan.md),
section "Daemon".

## Current state

`daemon.Scheduler` polls with a zero-valued request from exactly one site, in
`pollOnce`:

```go
	res, err := s.app.Poll(ctx, feedwatch.PollRequest{})
```

Options follow the same pattern as the root package: `type Option
func(*Scheduler)`, with each option ignoring a zero or nil input so the default
stands (`WithInterval` ignores a non-positive duration, `WithClock` and
`WithTicks` ignore nil).

## Changes

Add two fields to `Scheduler` next to `interval`, and two options in
`daemon/options.go`:

```go
// WithTags narrows the scheduler to feeds carrying every listed tag, so one
// process can watch a single lane. It is a selection filter, not a schedule:
// the interval still governs when a poll runs, and only due feeds in the lane
// are polled. An empty list leaves the scheduler watching every feed.
func WithTags(tags ...string) Option

// WithMatch sets the multi-tag semantics for WithTags: core.MatchAll (the
// default) requires every tag, core.MatchAny requires at least one.
func WithMatch(m core.TagMatch) Option
```

`WithTags` takes a variadic `...string` rather than a slice, matching how a
caller naturally writes `WithTags("ai", "agents")`; copy the slice into the
struct rather than retaining the caller's backing array.

`pollOnce` becomes `s.app.Poll(ctx, feedwatch.PollRequest{Tags: s.tags, Match:
string(s.match)})`. Consider a small `s.pollRequest()` accessor so the
construction site stays a single expression, but do not add one if it is used
once.

`PollRequest.Match` is a `string` (it is the CLI projection type), while the
daemon option takes a `core.TagMatch` for type safety at the Go API boundary.
The conversion is the one line above. Do not change `PollRequest.Match` to
`core.TagMatch`: `[]string`, `string`, `bool`, `int`, `*int`, and
`time.Duration` are the only types the CLI's reflection projection accepts, and
a custom string type is not in that table.

Invalid tags supplied to `WithTags` surface where every other request error
does: `App.Poll` calls `req.Validate()`, so the failure arrives on the `Event`
channel as `Event.Err`, not as a panic at option time. That is the right
behavior; a test should pin it.

## TDD plan

`daemon/daemon_test.go` (324 lines today) with the existing helpers; the
package drives the scheduler with `WithTicks` on an injected channel so tests
never sleep.

1. **(tracer)** A scheduler built `WithTags("ai")` over a store with one
   in-lane and one out-of-lane due feed emits an `Event` whose
   `Result.Polled == 1`, and the out-of-lane feed's fetcher was never called.
2. `WithMatch(core.MatchAny)` with two tags widens the selection relative to
   the default.
3. `WithTags()` with no arguments, and a scheduler with no `WithTags` at all,
   both watch every feed (regression guard).
4. An invalid tag arrives as `Event.Err` with a usage category, and the
   scheduler keeps running rather than dying.

Also add an `Example` to `daemon/example_test.go` showing a lane-scoped
scheduler, following the file's existing style.

## Gotchas

- `daemon` is one of the four public packages (ADR 0007), so these options are
  public API. Their doc comments are the documentation an embedder gets.
- Do not add a tag filter to the daemon's own scheduling logic; the lane is a
  selection passed through to `App.Poll`, and duplicating selection logic in
  the daemon would create a second place for it to drift.

## Acceptance Criteria

- `daemon.WithTags(tags ...string)` and `daemon.WithMatch(m core.TagMatch)`
  exist with godoc comments, following the package's ignore-the-zero-value
  option convention.
- `pollOnce` passes the selection through to `App.Poll`; the daemon contains no
  selection logic of its own.
- A lane-scoped scheduler polls only in-lane due feeds; an unscoped one is
  unchanged.
- An invalid tag surfaces as `Event.Err` and does not stop the scheduler.
- Behaviors 1-4 covered in `daemon/daemon_test.go`, plus an example in
  `daemon/example_test.go`.
- `make build` passes.
