package feedwatch

import "context"

// RemoveRequest names the subscription to unsubscribe, by its exact URL or its
// unique alias.
type RemoveRequest struct {
	Ref string `arg:"ref"`
}

// Validate reports whether the request is usable. An empty ref is left to the
// store's resolution, which reports it as a usage-category "feed not found", so
// one message covers every unresolvable reference.
func (r RemoveRequest) Validate() error { return nil }

// RmResult is the rm result envelope: the canonical URL of the removed
// subscription.
type RmResult struct {
	Head
	Removed string `json:"removed"`
}

// Remove unsubscribes the feed named by the request, cascading to its stored
// items, and reports its canonical URL. It resolves the reference first, so the
// reported URL is canonical even when the caller passed an alias and an unknown
// reference is a usage-category failure rather than a silent success (the
// store's RemoveFeed is a no-op on a missing feed).
func (a *App) Remove(ctx context.Context, req RemoveRequest) (RmResult, error) {
	if err := req.Validate(); err != nil {
		return RmResult{}, err
	}
	st, err := a.resolveStore(ctx)
	if err != nil {
		return RmResult{}, err
	}

	feed, err := st.GetFeed(ctx, req.Ref)
	if err != nil {
		return RmResult{}, err
	}
	if err := st.RemoveFeed(ctx, feed.URL); err != nil {
		return RmResult{}, err
	}
	return RmResult{Head: OKHead(), Removed: feed.URL}, nil
}
