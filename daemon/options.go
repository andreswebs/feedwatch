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

// WithTags narrows the scheduler to feeds carrying every listed tag, so one
// process can watch a single lane. It is a selection filter, not a schedule: the
// interval still governs when a poll runs, and only due feeds in the lane are
// polled. An empty list leaves the scheduler watching every feed.
//
// A tag that cannot be stored (empty, or carrying a comma or whitespace) is not
// rejected here: it surfaces on the Event channel as Event.Err, where every
// other poll request failure arrives.
func WithTags(tags ...string) Option {
	return func(s *Scheduler) {
		if len(tags) > 0 {
			s.tags = append([]string(nil), tags...)
		}
	}
}

// WithMatch sets the multi-tag semantics for WithTags: core.MatchAll (the
// default) requires every tag, core.MatchAny requires at least one. An empty
// match is ignored, so the default stands.
func WithMatch(m core.TagMatch) Option {
	return func(s *Scheduler) {
		if m != "" {
			s.match = m
		}
	}
}
