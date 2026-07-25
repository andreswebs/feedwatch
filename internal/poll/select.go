package poll

import (
	"context"

	"github.com/andreswebs/feedwatch/internal/core"
)

// selectFeeds resolves the feeds a poll run should target. Named refs are
// resolved exactly (by URL or unique alias) and fetched regardless of due-ness;
// an unknown ref is a usage error. With no names, force selects every active
// feed (overriding scheduling), otherwise only feeds whose next-due time has
// elapsed are returned. Disabled feeds are excluded from the unnamed paths.
func selectFeeds(ctx context.Context, d Deps, names []string, force bool) ([]core.Feed, error) {
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
		return d.Store.ListFeeds(ctx, core.ListFilter{Status: core.FeedActive})
	}
	return d.Store.DueFeeds(ctx, d.Clock())
}
