package daemon

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/andreswebs/feedwatch"
	"github.com/andreswebs/feedwatch/core"
)

// DefaultInterval is the wake cadence a Scheduler uses when WithInterval is not
// given.
const DefaultInterval = time.Minute

// ErrAlreadyRunning is returned by a second call to Run on the same Scheduler. A
// Scheduler is single-use: Run closes the event channel before returning, so a
// second run has nowhere to publish.
var ErrAlreadyRunning = errors.New("daemon: scheduler already running")

// Scheduler polls due feeds on a cadence and publishes each run's outcome.
type Scheduler struct {
	app         *feedwatch.App
	clock       core.Clock
	interval    time.Duration
	ticks       <-chan time.Time
	pollOnStart bool

	events  chan Event
	started atomic.Bool
}

// Event is one completed scheduler run: when it was published, the poll result,
// and the error the poll returned, if any. Result can be a truthful partial
// envelope alongside a non-nil Err, exactly as App.Poll returns it.
type Event struct {
	At     time.Time
	Result feedwatch.PollResult
	Err    error
}

// New builds a Scheduler over app. The App belongs to the caller: the Scheduler
// never closes it, exactly as an App never closes an injected store.
func New(app *feedwatch.App, opts ...Option) *Scheduler {
	s := &Scheduler{
		app:      app,
		clock:    core.SystemClock,
		interval: DefaultInterval,
		events:   make(chan Event),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Events returns the channel on which each run's outcome is published. Run
// closes it before returning, so a consumer ranging over it terminates.
func (s *Scheduler) Events() <-chan Event { return s.events }

// Run polls until ctx is done, then returns ctx.Err(). It closes the event
// channel before returning.
func (s *Scheduler) Run(ctx context.Context) error {
	if !s.started.CompareAndSwap(false, true) {
		return ErrAlreadyRunning
	}
	defer close(s.events)

	ticks := s.ticks
	if ticks == nil {
		t := time.NewTicker(s.interval)
		defer t.Stop()
		ticks = t.C
	}

	if s.pollOnStart && !s.pollOnce(ctx, ticks) {
		return ctx.Err()
	}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticks:
			if !s.pollOnce(ctx, ticks) {
				return ctx.Err()
			}
		}
	}
}

// pollOnce runs one poll and publishes its outcome, reporting whether the loop
// should continue. Ticks arriving while the poll is in flight are received and
// dropped rather than queued, so a poll slower than the wake cadence cannot
// stack runs behind itself.
//
// Publishing blocks until the consumer receives, or until ctx is done, in which
// case the event is still offered without blocking so a final partial envelope is
// not lost on a consumer that is still listening.
func (s *Scheduler) pollOnce(ctx context.Context, ticks <-chan time.Time) bool {
	done := make(chan Event, 1)
	go func() {
		res, err := s.app.Poll(ctx, feedwatch.PollRequest{})
		done <- Event{At: s.clock(), Result: res, Err: err}
	}()

	var ev Event
	for waiting := true; waiting; {
		select {
		case <-ticks:
		case ev = <-done:
			waiting = false
		}
	}

	select {
	case s.events <- ev:
		return ctx.Err() == nil
	case <-ctx.Done():
		select {
		case s.events <- ev:
		default:
		}
		return false
	}
}
