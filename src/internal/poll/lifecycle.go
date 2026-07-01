package poll

import (
	"context"
	"time"

	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/store"
)

// RecordSuccess clears a feed's persisted failure state and schedules its next
// poll at now plus the effective interval: the feed's own interval, or def
// when the feed declares none. Honoring a parsed feed `<ttl>` is the poll
// orchestrator's concern, applied before it picks the interval passed here.
//
// finalURL is the permanent-redirect rewrite target, or "" when the feed URL is
// unchanged; the store applies the rename atomically with the success write. It
// returns the new canonical URL when a rename was applied, else "".
func RecordSuccess(ctx context.Context, s store.Store, clk core.Clock, url string, interval, def time.Duration, finalURL string) (string, error) {
	now := clk()
	eff := interval
	if eff <= 0 {
		eff = def
	}
	return s.RecordSuccess(ctx, url, now, now.Add(eff), finalURL)
}

// RecordFailure records a feed fetch failure whose in-call transient retries
// (the fetch layer's concern) are already exhausted. It increments the
// persisted failure count, schedules an exponentially backed-off next poll
// (baseBackoff doubling per consecutive failure, capped at maxBackoff), and
// auto-disables the feed once the count reaches threshold so poll skips it
// until enable. The injected clock makes the schedule deterministic.
//
// The current count is read before the increment; this is safe because a
// feed's rows are never written concurrently (poll serializes per feed), so
// the stored count lands on the same value used for the backoff and disable
// decision.
func RecordFailure(ctx context.Context, s store.Store, clk core.Clock, url string, cat core.Category, msg string, threshold int, baseBackoff, maxBackoff time.Duration) error {
	f, err := s.GetFeed(ctx, url)
	if err != nil {
		return err
	}
	now := clk()
	count := f.FailureCount + 1
	nextDue := now.Add(backoff(baseBackoff, maxBackoff, count))
	if err := s.RecordFailure(ctx, url, cat, msg, now, nextDue); err != nil {
		return err
	}
	if threshold > 0 && count >= threshold {
		return s.SetStatus(ctx, url, core.FeedDisabled)
	}
	return nil
}

// backoff returns base * 2^(n-1) clamped to [base, max]. The doubling is
// computed iteratively with an overflow guard so a large n can never wrap the
// duration negative; a non-positive base degenerates to max.
func backoff(base, maxBackoff time.Duration, n int) time.Duration {
	if base <= 0 {
		return maxBackoff
	}
	d := base
	for i := 1; i < n; i++ {
		if d >= maxBackoff {
			return maxBackoff
		}
		d *= 2
		if d <= 0 { // overflowed int64 nanoseconds
			return maxBackoff
		}
	}
	if d > maxBackoff {
		return maxBackoff
	}
	return d
}
