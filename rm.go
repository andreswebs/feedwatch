package feedwatch

import (
	"context"
	"encoding/json"

	"github.com/andreswebs/feedwatch/core"
	"github.com/andreswebs/feedwatch/store"
)

// RemoveRequest names what to unsubscribe: either one subscription, by its
// exact URL or unique alias, or every subscription in a lane. The two are
// alternatives rather than refinements of each other, so exactly one must be
// given. Removing a feed cascades to its stored items, which makes the bulk
// path the only place one flag deletes many feeds' history; that is why the
// selector rules are explicit here rather than left to the store.
type RemoveRequest struct {
	Ref   string   `arg:"ref"`
	Tags  []string `flag:"tag" usage:"unsubscribe every feed carrying this tag (repeatable), deleting their stored items too; cannot be combined with a named feed"`
	Match string   `flag:"match" default:"all" usage:"multi-tag semantics: 'all' (default) or 'any'"`
}

// Validate reports whether the request names exactly one selector. An unknown
// feed reference is a store-resolution failure rather than a syntactic one, so
// it surfaces from Remove instead.
func (r RemoveRequest) Validate() error {
	_, err := r.filter()
	return err
}

// filter resolves the request into the store filter that selects the lane, so
// Validate and Remove state the rules once and cannot drift. Naming neither
// selector is rejected rather than resolved as "every feed": the failure mode of
// a mistyped bulk rm would otherwise be unsubscribing everything.
func (r RemoveRequest) filter() (core.ListFilter, error) {
	switch {
	case r.Ref != "" && len(r.Tags) > 0:
		return core.ListFilter{}, usageErr("--tag cannot be combined with a named feed; " +
			"name a feed to unsubscribe exactly that one, or use --tag to unsubscribe a lane")
	case r.Ref == "" && len(r.Tags) == 0:
		return core.ListFilter{}, usageErr("rm requires a feed reference or --tag")
	}
	return tagFilter(r.Tags, r.Match)
}

// RmResult is the rm result envelope: the canonical URLs unsubscribed. It is a
// list rather than a single value because --tag removes a lane, and a caller
// that deletes in bulk needs to know exactly what went.
type RmResult struct {
	Head
	Removed []string `json:"removed"`
}

// MarshalJSON coalesces removed so it always serializes as [] rather than null.
func (r RmResult) MarshalJSON() ([]byte, error) {
	type alias RmResult
	a := alias(r)
	if a.Removed == nil {
		a.Removed = []string{}
	}
	return json.Marshal(a)
}

// Remove unsubscribes the feed the request names, or every feed in the lane it
// selects, cascading to their stored items, and reports the canonical URLs
// removed in URL order. A named reference is resolved first, so the reported URL
// is canonical even when the caller passed an alias and an unknown reference is
// a usage-category failure rather than a silent success (the store's RemoveFeed
// is a no-op on a missing feed). A lane no feed carries removes nothing and
// succeeds, matching the empty-lane rule everywhere else.
func (a *App) Remove(ctx context.Context, req RemoveRequest) (RmResult, error) {
	filter, err := req.filter()
	if err != nil {
		return RmResult{}, err
	}
	st, err := a.resolveStore(ctx)
	if err != nil {
		return RmResult{}, err
	}

	targets, err := removeTargets(ctx, st, req.Ref, filter)
	if err != nil {
		return RmResult{}, err
	}

	removed := make([]string, 0, len(targets))
	for _, url := range targets {
		if err := st.RemoveFeed(ctx, url); err != nil {
			return RmResult{}, err
		}
		removed = append(removed, url)
	}
	return RmResult{Head: OKHead(), Removed: removed}, nil
}

// removeTargets resolves the request's selector into the canonical URLs to
// unsubscribe: exactly one for a named reference, or the lane's feeds in URL
// order, which is the order ListFeeds guarantees.
func removeTargets(ctx context.Context, st store.Store, ref string, filter core.ListFilter) ([]string, error) {
	if ref != "" {
		feed, err := st.GetFeed(ctx, ref)
		if err != nil {
			return nil, err
		}
		return []string{feed.URL}, nil
	}

	feeds, err := st.ListFeeds(ctx, filter)
	if err != nil {
		return nil, err
	}
	urls := make([]string, 0, len(feeds))
	for _, f := range feeds {
		urls = append(urls, f.URL)
	}
	return urls, nil
}
