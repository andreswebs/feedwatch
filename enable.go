package feedwatch

import (
	"context"

	"github.com/andreswebs/feedwatch/core"
)

// EnableRequest names the subscription to re-enable, by its exact URL or its
// unique alias.
type EnableRequest struct {
	Ref string `arg:"ref"`
}

// Validate reports whether the request is usable. An empty ref is left to the
// store's resolution, which reports it as a usage-category "feed not found".
func (r EnableRequest) Validate() error { return nil }

// EnableResult is the enable result envelope: the feed's state after it has been
// re-enabled and its failure lifecycle reset.
type EnableResult struct {
	Head
	Feed FeedView `json:"feed"`
}

// Enable re-enables a disabled feed and resets its failure lifecycle (failure
// count, last error, and backed-off schedule) so poll treats it as due again.
// Resetting through the store's success path makes Enable idempotent on an
// already-active feed. An unknown reference is a usage-category failure.
func (a *App) Enable(ctx context.Context, req EnableRequest) (EnableResult, error) {
	if err := req.Validate(); err != nil {
		return EnableResult{}, err
	}
	st, err := a.resolveStore(ctx)
	if err != nil {
		return EnableResult{}, err
	}

	feed, err := st.GetFeed(ctx, req.Ref)
	if err != nil {
		return EnableResult{}, err
	}
	if err := st.SetStatus(ctx, feed.URL, core.FeedActive); err != nil {
		return EnableResult{}, err
	}

	// Reset through the same path a successful poll uses: it clears the failure
	// count, last error, and last-error time. The next-due is set to now so the
	// cleared backoff leaves the feed immediately due rather than waiting out the
	// disabled feed's backed-off schedule.
	now := a.clock()
	if _, err := st.RecordSuccess(ctx, feed.URL, now, now, ""); err != nil {
		return EnableResult{}, err
	}

	updated, err := st.GetFeed(ctx, feed.URL)
	if err != nil {
		return EnableResult{}, err
	}
	return EnableResult{Head: OKHead(), Feed: feedView(updated)}, nil
}
