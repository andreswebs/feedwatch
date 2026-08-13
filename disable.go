package feedwatch

import (
	"context"

	"github.com/andreswebs/feedwatch/core"
)

// DisableRequest names the subscription to disable, by its exact URL or its
// unique alias.
type DisableRequest struct {
	Ref string `arg:"ref"`
}

// Validate reports whether the request is usable. An empty ref is left to the
// store's resolution, which reports it as a usage-category "feed not found".
func (r DisableRequest) Validate() error { return nil }

// DisableResult is the disable result envelope: the feed's state after it has
// been manually disabled so poll skips it.
type DisableResult struct {
	Head
	Feed FeedView `json:"feed"`
}

// Disable marks a feed disabled so poll's due selection excludes it. Unlike
// auto-disable, which the failure lifecycle drives, this is a manual switch and
// leaves the failure count untouched; Enable reverses it. Disabling an already
// disabled feed is idempotent. An unknown reference is a usage-category failure.
func (a *App) Disable(ctx context.Context, req DisableRequest) (DisableResult, error) {
	if err := req.Validate(); err != nil {
		return DisableResult{}, err
	}
	st, err := a.resolveStore(ctx)
	if err != nil {
		return DisableResult{}, err
	}

	feed, err := st.GetFeed(ctx, req.Ref)
	if err != nil {
		return DisableResult{}, err
	}
	if err := st.SetStatus(ctx, feed.URL, core.FeedDisabled); err != nil {
		return DisableResult{}, err
	}

	updated, err := st.GetFeed(ctx, feed.URL)
	if err != nil {
		return DisableResult{}, err
	}
	return DisableResult{Head: OKHead(), Feed: feedView(updated)}, nil
}
