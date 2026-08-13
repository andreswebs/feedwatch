package feedwatch_test

import (
	"context"
	"testing"

	"github.com/andreswebs/feedwatch"
	"github.com/andreswebs/feedwatch/core"
)

// TestRemoveUnknownRefIsUsageError pins why the use case resolves before
// removing: the store's RemoveFeed is a no-op on a missing feed, so without the
// lookup an unknown ref would be reported as a successful removal.
func TestRemoveUnknownRefIsUsageError(t *testing.T) {
	app, _, _ := newTestApp(t)

	res, err := app.Remove(context.Background(), feedwatch.RemoveRequest{Ref: "https://nope.example/feed.xml"})
	wantUsageError(t, err, "feed not found")
	if res.Removed != "" {
		t.Errorf("Removed = %q, want empty on failure", res.Removed)
	}
}

// TestRemoveByAliasReportsCanonicalURL covers that the reported identity is the
// canonical URL even when the caller named the feed by its alias.
func TestRemoveByAliasReportsCanonicalURL(t *testing.T) {
	app, st, _ := newTestApp(t)
	ctx := context.Background()

	const url = "https://blog.example/feed.xml"
	if _, err := st.AddFeed(ctx, core.Feed{URL: url, Alias: "blog", Status: core.FeedActive}); err != nil {
		t.Fatalf("AddFeed: %v", err)
	}

	res, err := app.Remove(ctx, feedwatch.RemoveRequest{Ref: "blog"})
	if err != nil {
		t.Fatalf("Remove = %v, want nil", err)
	}
	if res.Removed != url {
		t.Errorf("Removed = %q, want the canonical URL %q", res.Removed, url)
	}

	feeds, err := st.ListFeeds(ctx, core.ListFilter{})
	if err != nil {
		t.Fatalf("ListFeeds: %v", err)
	}
	if len(feeds) != 0 {
		t.Errorf("store still holds %d feed(s) after Remove, want 0", len(feeds))
	}
}
