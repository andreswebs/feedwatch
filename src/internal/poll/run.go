package poll

import (
	"context"
	"sync"
	"time"

	"github.com/andreswebs/feedwatch/internal/core"
)

// persistGrace bounds how long persistence may continue *after* an interrupt
// before it is aborted, not the persistence stage as a whole. An uninterrupted
// run's persistence has no deadline; the grace starts ticking only once the
// poll context is cancelled, so an interrupted poll cannot hang on an
// unresponsive store while completed work is flushed. It is a var, not a
// const, so tests can shrink it and avoid a multi-second sleep.
var persistGrace = 5 * time.Second

// graceAfterCancel returns a context detached from parent's cancellation, plus
// a stop func that must be deferred. While parent is live the returned context
// has no deadline. Once parent is cancelled (an interrupt), the returned
// context is cancelled grace later, so persistence of already-completed work
// still cannot hang on an unresponsive store.
func graceAfterCancel(parent context.Context, grace time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.WithoutCancel(parent))
	stopped := make(chan struct{})
	var once sync.Once
	stop := func() { once.Do(func() { close(stopped) }); cancel() }
	go func() {
		select {
		case <-parent.Done():
			t := time.NewTimer(grace)
			defer t.Stop()
			select {
			case <-t.C:
				cancel()
			case <-stopped:
			}
		case <-stopped:
		}
	}()
	return ctx, stop
}

// Result is the outcome summary of a poll run, ready for the output stage.
// Polled counts the feeds fetched (successes and failures); Skipped counts the
// active feeds left unpolled because they were not due; Failed is the subset of
// Polled that errored. Fetched is the total items parsed across all successful
// 200 responses (304s contribute 0); NewItems is the count stored for the first
// time; Deduped is Fetched minus NewItems. Items holds the newly-seen items in
// feed-selection order.
// Failed is not part of the stdout envelope: the exit code reports whether feeds
// failed and stderr reports which, so the caller drops it when shaping output.
type Result struct {
	Polled   int
	Skipped  int
	Fetched  int
	NewItems int
	Deduped  int
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
	// context detached from the signal, unbounded unless that signal fires, so a
	// successful uninterrupted run is never aborted by an arbitrary deadline and
	// an interrupt mid-poll still durably records the completed work instead of
	// aborting it with a context-canceled store write.
	outcomes := orchestrate(ctx, d, feeds)
	persistCtx, stop := graceAfterCancel(ctx, persistGrace)
	defer stop()
	totals, feedErrs, err := consume(persistCtx, d, outcomes)
	res := Result{
		Polled:   totals.polled,
		Skipped:  skipped,
		Fetched:  totals.fetched,
		NewItems: totals.newItems,
		Deduped:  totals.fetched - totals.newItems,
		Failed:   totals.failed,
		Items:    orderedItems(feeds, totals),
		Renamed:  totals.renames,
	}
	if err != nil {
		return res, feedErrs, err
	}
	return res, feedErrs, nil
}

// orderedItems flattens the per-feed new items accumulated in totals into
// feed-selection order, so a partial result (consume stopped early on a hard
// failure) reports exactly the feeds it reached.
func orderedItems(feeds []core.Feed, totals pollTotals) []core.Item {
	items := make([]core.Item, 0, totals.newItems)
	for _, f := range feeds {
		items = append(items, totals.newByFeed[f.URL]...)
	}
	return items
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
