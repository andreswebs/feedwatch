package poll

import (
	"context"

	"github.com/andreswebs/feedwatch/core"
)

// selectFeeds resolves the feeds a poll run should target. Named refs are
// resolved exactly (by URL or unique alias) and fetched regardless of due-ness;
// an unknown ref is a usage error. With no names, force selects every active
// feed (overriding scheduling), otherwise only feeds whose next-due time has
// elapsed are returned. Disabled feeds are excluded from the unnamed paths.
//
// filter narrows both unnamed paths to a lane. It is not applied to the named
// path: naming feeds and naming a lane are mutually exclusive selections, which
// the caller rejects before reaching here.
func selectFeeds(ctx context.Context, d Deps, names []string, force bool, filter core.ListFilter) ([]core.Feed, error) {
	if len(names) > 0 {
		feeds := make([]core.Feed, 0, len(names))
		for _, ref := range names {
			f, err := d.Store.GetFeed(ctx, ref)
			if err != nil {
				return nil, err
			}
			feeds = append(feeds, f)
		}
		return feeds, nil
	}

	if force {
		return d.Store.ListFeeds(ctx, activeIn(filter))
	}
	return d.Store.DueFeeds(ctx, d.Clock(), filter)
}

// activeIn narrows filter to active feeds, which is the universe every unnamed
// poll path draws from. It is shared by the force selection and the skipped
// count so the two always measure the same set.
func activeIn(filter core.ListFilter) core.ListFilter {
	filter.Status = core.FeedActive
	return filter
}
