---
id: fee-vbid
status: closed
deps: [fee-kj8z]
links: []
created: 2026-08-13T14:03:47Z
type: feature
priority: 2
assignee: Andre Silva
parent: fee-ui25
tags: [lib, daemon]
---

# feedwatch/daemon: the embeddable poll scheduler

Eighth step of
[docs/adr/0007-library-and-frontends.md](../docs/adr/0007-library-and-frontends.md),
and the fourth public package. The daemon is **not** a layer beneath the
frontends: it is another consumer of `App`, a loop that calls `App.Poll` on a
cadence and publishes the outcomes. A TUI embeds it for live updates, an HTTP
server embeds it next to its handlers, and neither becomes the core.

Depends on `fee-kj8z`, which is where `App.Poll` lands.

## Design

### 1. Surface

Package `daemon`, import path `github.com/andreswebs/feedwatch/daemon`. It
imports the root package; the root package never imports it, so there is no
cycle.

```go
// Scheduler polls due feeds on a cadence and publishes each run's outcome.
type Scheduler struct { /* ... */ }

func New(app *feedwatch.App, opts ...Option) *Scheduler

// Run polls until ctx is done, then returns ctx.Err(). It closes the event
// channel before returning, so a consumer ranging over Events terminates.
func (s *Scheduler) Run(ctx context.Context) error

// Events returns the channel on which each run's outcome is published.
func (s *Scheduler) Events() <-chan Event

// Event is one completed scheduler run.
type Event struct {
    At     time.Time
    Result feedwatch.PollResult
    Err    error
}

type Option func(*Scheduler)

// WithInterval sets how often the scheduler wakes to poll due feeds.
// Default: one minute.
func WithInterval(d time.Duration) Option

// WithClock sets the time source used to stamp events. Default: the system clock.
func WithClock(c core.Clock) Option

// WithTicks replaces the internal ticker with a caller-driven channel, so a
// test can advance the schedule without sleeping.
func WithTicks(ch <-chan time.Time) Option

// WithPollOnStart polls immediately on Run rather than waiting for the first
// tick. Default: false.
func WithPollOnStart(b bool) Option
```

### 2. The interval is a wake cadence, not a poll interval

feedwatch already schedules per feed: `Store.DueFeeds` selects feeds whose
next-due time has passed, and per-feed intervals and failure backoff decide
that time. The scheduler therefore calls `App.Poll` with an empty
`PollRequest` (no feeds named, `Force: false`) and lets the store decide what is
actually due. `WithInterval` controls only how often the scheduler asks. This
must be stated in the package doc, because "interval" invites the wrong reading,
and a scheduler that forced every feed on every tick would hammer publishers and
defeat the politeness and backoff machinery.

### 3. Concurrency and lifecycle

Follow the invariant that every goroutine has a documented exit condition:

- `Run` owns the loop; it does not spawn a background goroutine that outlives
  the call. The caller decides whether to run it in a goroutine.
- Runs never overlap. A tick arriving while a poll is in flight is dropped, not
  queued; a poll that takes longer than the interval must not stack. Record the
  drop in the doc comment as deliberate.
- Publishing is a blocking send that also selects on `ctx.Done()`, so a slow
  consumer slows the scheduler rather than losing events, and a cancelled
  context never deadlocks the loop. Document that a consumer must drain
  `Events()`, and offer a buffered channel via the constructor if a buffer
  proves necessary.
- On `ctx` cancellation mid-poll, `App.Poll` already stops promptly and returns
  what it persisted; publish that final event if it can be sent without
  blocking, then close the channel and return `ctx.Err()`.
- `Run` is single-use per `Scheduler` and returns an error if called twice
  concurrently. Guard it explicitly rather than leaving it undefined.

The scheduler never closes the `App`: the embedder owns it, exactly as the App
never closes an injected store.

### 4. Errors are published, not fatal

A poll that fails does not stop the scheduler. `Event.Err` carries the failure
and the loop continues to the next tick, since a transient store or network
failure must not silently kill background polling. The one exception worth
considering is a config-category error, which cannot resolve itself; keep it
simple and publish it too, leaving the stop decision to the embedder.

## TDD notes

The whole point of `WithTicks` is that this package is testable without
sleeping. No test in this ticket may call `time.Sleep` to wait for a schedule.

Vertical slices:

1. **RED**: a scheduler over an `App` with an injected store and one due feed,
   driven by one manual tick, publishes exactly one `Event` whose `Result`
   reports that feed polled. **GREEN**: minimal loop.
2. `Run` returns `ctx.Err()` after cancellation and closes `Events()`, so a
   `range` over the channel terminates.
3. `WithPollOnStart` produces an event with no tick delivered.
4. Without `WithPollOnStart`, no event is produced before the first tick.
5. A poll error is published in `Event.Err` and the scheduler survives to poll
   again on the next tick (drive with `FailingStore`, then a healthy store, or
   a fetcher double that fails once).
6. Ticks arriving during an in-flight poll are dropped rather than queued:
   block a poll on a gated fetcher double, deliver several ticks, release, and
   assert exactly one event.
7. `Event.At` comes from the injected clock (`WithClock` plus `FixedClock`).
8. The scheduler polls due feeds only: a feed that is not due is not polled,
   proven by the poll result's `skipped` count, which confirms the scheduler
   does not force.
9. Calling `Run` twice concurrently returns an error from the second call.

Run the package under `make test-race`; a scheduler with a gated fetcher and a
manual tick channel is exactly the shape that surfaces races.

## Acceptance Criteria

- The `daemon` package exists at the repository root with `Scheduler`, `New`,
  `Run`, `Events`, `Event`, and the four options.
- The package doc states that the interval is a wake cadence and that per-feed
  scheduling stays with the store, and documents the drop-on-overlap and
  blocking-publish policies.
- `Run` closes the event channel before returning and returns `ctx.Err()` on
  cancellation.
- A failed poll is published as an event and does not stop the scheduler.
- The scheduler never forces a poll and never closes the `App`.
- No test sleeps to advance the schedule.
- The root package does not import `daemon`.
- `make build` and `make test-race` pass.

## Files

```text
daemon/daemon.go       (new: Scheduler, Run, Events, Event)
daemon/options.go      (new)
daemon/doc.go          (new)
daemon/daemon_test.go  (new, package daemon_test)
```

## Notes

**2026-08-13T15:38:25Z**

Added the public daemon package: Scheduler, New, Run, Events, Event, and the four options (WithInterval, WithClock, WithTicks, WithPollOnStart), plus doc.go stating the wake-cadence, drop-on-overlap, and blocking-publish policies.

Implementation notes for the next person:
- pollOnce runs App.Poll on its own goroutine (result on a buffered channel, so it always exits) while the loop keeps receiving and discarding ticks. That is what makes drop-on-overlap real: an inline poll cannot drop a tick, because an unbuffered caller channel blocks the sender and time.Ticker's cap-1 buffer queues one stale tick.
- The scheduler polls with an empty PollRequest (Force false), so per-feed due-ness, politeness, and backoff stay with the store. Pinned by the skipped-count test.
- Run is single-use: the CompareAndSwap guard is never reset, since Run closes Events, so a re-run returns ErrAlreadyRunning (exported) rather than panicking on a closed channel.
- Publishing is a blocking send that also selects on ctx.Done(); on cancellation the final (possibly partial) event is offered non-blockingly, then the channel is closed and ctx.Err() returned.
- Nine test slices cover the ticket's TDD list; no test sleeps. The error path is driven by a store.Store wrapper whose first DueFeeds call fails (a fetch failure is result data, not Event.Err).
- The root package does not import daemon (verified by grep); make build and make test-race pass, the daemon package also under -race -count=3.
- Still open in fee-3p3r: public API docs and runnable examples, which is where a daemon example belongs.
