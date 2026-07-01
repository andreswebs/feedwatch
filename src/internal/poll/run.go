package poll

import (
	"context"
	"time"

	"github.com/andreswebs/feedwatch/internal/core"
)

// persistGrace bounds the persistence stage when the poll context has been
// cancelled by an interrupt. Persisting the feeds that already completed runs on
// a context detached from the signal so the writes are not aborted, but the
// detached context still carries this deadline so an interrupted poll cannot
// hang on an unresponsive store.
const persistGrace = 5 * time.Second

// Result is the outcome summary of a poll run, ready for the output stage.
// Polled counts the feeds fetched (successes and failures); Skipped counts the
// active feeds left unpolled because they were not due; Failed is the subset of
// Polled that errored. Items holds the newly-seen items in feed-selection order.
// Failed is not part of the stdout envelope: the exit code reports whether feeds
// failed and stderr reports which, so the caller drops it when shaping output.
type Result struct {
	Polled   int
	Skipped  int
	NewItems int
	Failed   int
	Items    []core.Item
	Renamed  []core.FeedRename
}

// ExitCode derives the process exit code from the outcome summary, never from a
// returned Go error: 0 when nothing was polled or every polled feed succeeded,
// 2 when every polled feed failed, and 3 when some succeeded and some failed.
func (r Result) ExitCode() int {
	if r.Polled == 0 || r.Failed == 0 {
		return 0
	}
	if r.Failed == r.Polled {
		return 2
	}
	return 3
}

// Run executes a full poll: it selects the target feeds, fetches and parses them
// concurrently, persists the results, and returns the outcome summary alongside
// the per-feed failures. The returned []*core.FeedError is the per-feed outcome
// the caller emits to stderr; a non-nil error is a hard, whole-invocation failure
// (an unreachable store or a store write that failed) that maps to exit 1.
func Run(ctx context.Context, d Deps, names []string, force bool) (Result, []*core.FeedError, error) {
	feeds, err := selectFeeds(ctx, d, names, force)
	if err != nil {
		return Result{}, nil, err
	}

	skipped, err := skippedCount(ctx, d, names, force, len(feeds))
	if err != nil {
		return Result{}, nil, err
	}

	// orchestrate keeps the cancellable ctx so an interrupt stops it from
	// scheduling new fetches. Persisting the feeds it already fetched runs on a
	// context detached from the signal (with a grace deadline), so an interrupt
	// mid-poll still durably records the completed work instead of aborting it
	// with a context-canceled store write.
	outcomes := orchestrate(ctx, d, feeds)
	persistCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), persistGrace)
	defer stop()
	totals, feedErrs, err := consume(persistCtx, d, outcomes)
	if err != nil {
		return Result{}, feedErrs, err
	}

	items := make([]core.Item, 0, totals.newItems)
	for _, f := range feeds {
		items = append(items, totals.newByFeed[f.URL]...)
	}

	return Result{
		Polled:   totals.polled,
		Skipped:  skipped,
		NewItems: totals.newItems,
		Failed:   totals.failed,
		Items:    items,
		Renamed:  totals.renames,
	}, feedErrs, nil
}

// skippedCount reports how many active feeds were left unpolled because they were
// not due. Skipping only happens on the unnamed, unforced due path; named or
// forced runs target their feeds regardless of schedule, so nothing is skipped.
func skippedCount(ctx context.Context, d Deps, names []string, force bool, polled int) (int, error) {
	if len(names) > 0 || force {
		return 0, nil
	}
	active, err := d.Store.ListFeeds(ctx, core.ListFilter{Status: core.FeedActive})
	if err != nil {
		return 0, err
	}
	if skipped := len(active) - polled; skipped > 0 {
		return skipped, nil
	}
	return 0, nil
}
