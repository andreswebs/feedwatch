package daemon

import (
	"time"

	"github.com/andreswebs/feedwatch/core"
)

// Option overrides one of a Scheduler's settings.
type Option func(*Scheduler)

// WithInterval sets how often the scheduler wakes to poll due feeds. It is a
// wake cadence, not a poll interval: which feeds are actually fetched stays with
// the store's per-feed schedule. A non-positive duration is ignored, so the
// default stands.
func WithInterval(d time.Duration) Option {
	return func(s *Scheduler) {
		if d > 0 {
			s.interval = d
		}
	}
}

// WithClock sets the time source used to stamp events. A nil clock is ignored,
// so the system clock stays in place.
func WithClock(c core.Clock) Option {
	return func(s *Scheduler) {
		if c != nil {
			s.clock = c
		}
	}
}

// WithTicks replaces the internal ticker with a caller-driven channel, so a test
// can advance the schedule without sleeping. A nil channel is ignored.
func WithTicks(ch <-chan time.Time) Option {
	return func(s *Scheduler) {
		if ch != nil {
			s.ticks = ch
		}
	}
}

// WithPollOnStart polls immediately on Run rather than waiting for the first
// tick.
func WithPollOnStart(b bool) Option {
	return func(s *Scheduler) { s.pollOnStart = b }
}
