package poll

import (
	"context"
	"fmt"
	"time"

	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/parse"
)

// pollTotals aggregates the consumed outcomes for the output stage. polled is
// the number of feeds fetched (including those that failed); failed is the
// subset that errored; newItems is the total count of newly-seen items, indexed
// per feed in newByFeed. The count of feeds skipped as not-due is not derivable
// from outcomes (those feeds are never fetched); the output-shaping stage adds
// it from the selection result.
type pollTotals struct {
	polled    int
	newItems  int
	failed    int
	newByFeed map[string][]core.Item
}

// consume turns fetch/parse outcomes into persisted state, the dedup-and-consume
// stage of poll. For each successful outcome it persists changed conditional-GET
// validators, assigns dedup keys and upserts items (returning only the unseen
// ones), and records a success that reschedules the feed honoring its <ttl> or
// interval. A failed outcome drives the failure lifecycle (count, backoff,
// auto-disable) and is collected into the returned per-feed errors.
//
// The returned error is a hard, whole-invocation failure: a store write that
// fails maps to exit 1 and aborts the run. Feeds processed before it remain
// persisted, since each feed's writes are independent. Per-feed fetch/parse
// failures are not errors here; they are the *core.FeedError slice the boundary
// reports on stderr and maps to exit 2 or 3.
func consume(ctx context.Context, d Deps, outcomes []feedOutcome) (pollTotals, []*core.FeedError, error) {
	totals := pollTotals{polled: len(outcomes), newByFeed: make(map[string][]core.Item)}
	var feedErrs []*core.FeedError

	for _, oc := range outcomes {
		if oc.err != nil {
			totals.failed++
			feedErrs = append(feedErrs, oc.err)
			if err := d.recordFailure(ctx, oc); err != nil {
				return totals, feedErrs, err
			}
			continue
		}

		newItems, err := d.consumeSuccess(ctx, oc)
		if err != nil {
			return totals, feedErrs, err
		}
		if len(newItems) > 0 {
			totals.newByFeed[oc.feed.URL] = newItems
			totals.newItems += len(newItems)
		}
	}

	return totals, feedErrs, nil
}

// consumeSuccess persists a successful (200 or 304) outcome: it writes any
// changed validators, upserts the parsed items (skipped on a 304, which carries
// none), and records the success with the next-due schedule. It returns the
// newly-seen items.
func (d Deps) consumeSuccess(ctx context.Context, oc feedOutcome) ([]core.Item, error) {
	if err := d.Store.SetValidators(ctx, oc.feed.URL, oc.result.ETag, oc.result.LastModified); err != nil {
		return nil, fmt.Errorf("persist validators for %q: %w", oc.feed.URL, err)
	}

	var newItems []core.Item
	if !oc.result.NotModified {
		items := oc.parsed.Items
		for i := range items {
			items[i].DedupKey = parse.DedupKey(items[i])
		}
		n, err := d.Store.UpsertItems(ctx, oc.feed.URL, items)
		if err != nil {
			return nil, fmt.Errorf("upsert items for %q: %w", oc.feed.URL, err)
		}
		newItems = n
	}

	// A permanent (301/308) redirect rewrites the stored feed URL; the SSRF
	// guard already failed the fetch on a redirect into blocked private space,
	// and 302/307 leave Permanent false, so the rewrite is safe here. The
	// earlier SetValidators/UpsertItems writes stay keyed on the original URL;
	// RecordSuccess performs the rename atomically as its final step.
	finalURL := ""
	if oc.result.Permanent && oc.result.FinalURL != "" && oc.result.FinalURL != oc.feed.URL {
		finalURL = oc.result.FinalURL
	}

	interval := effectiveInterval(oc.feed.Interval, oc.parsed.TTL, d.DefaultInterval)
	if err := RecordSuccess(ctx, d.Store, d.Clock, oc.feed.URL, interval, d.DefaultInterval, finalURL); err != nil {
		return nil, fmt.Errorf("record success for %q: %w", oc.feed.URL, err)
	}
	return newItems, nil
}

// recordFailure drives the failure lifecycle for a failed outcome, using the
// feed's effective interval (no parsed <ttl> is available on a failure) as the
// backoff base.
func (d Deps) recordFailure(ctx context.Context, oc feedOutcome) error {
	base := effectiveInterval(oc.feed.Interval, 0, d.DefaultInterval)
	if err := RecordFailure(ctx, d.Store, d.Clock, oc.feed.URL, oc.err.Category, oc.err.Error(),
		d.FailureThreshold, base, d.MaxBackoff); err != nil {
		return fmt.Errorf("record failure for %q: %w", oc.feed.URL, err)
	}
	return nil
}

// effectiveInterval picks a feed's poll interval: its own configured interval
// when set, else a declared feed <ttl>, else the configured default.
func effectiveInterval(feedInterval, ttl, def time.Duration) time.Duration {
	if feedInterval > 0 {
		return feedInterval
	}
	if ttl > 0 {
		return ttl
	}
	return def
}
