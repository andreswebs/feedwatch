package feedwatch_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/andreswebs/feedwatch"
	"github.com/andreswebs/feedwatch/core"
	"github.com/andreswebs/feedwatch/store"
)

// TestRemoveUnknownRefIsUsageError pins why the use case resolves before
// removing: the store's RemoveFeed is a no-op on a missing feed, so without the
// lookup an unknown ref would be reported as a successful removal.
func TestRemoveUnknownRefIsUsageError(t *testing.T) {
	app, _, _ := newTestApp(t)

	res, err := app.Remove(context.Background(), feedwatch.RemoveRequest{Ref: "https://nope.example/feed.xml"})
	wantUsageError(t, err, "feed not found")
	if len(res.Removed) != 0 {
		t.Errorf("Removed = %v, want empty on failure", res.Removed)
	}
}

// TestRemoveByAliasReportsCanonicalURL covers that the reported identity is the
// canonical URL even when the caller named the feed by its alias, and that the
// single-ref path reports it as a one-element list like every other path.
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
	if want := []string{url}; !reflect.DeepEqual(res.Removed, want) {
		t.Errorf("Removed = %v, want the canonical URL %v", res.Removed, want)
	}

	feeds, err := st.ListFeeds(ctx, core.ListFilter{})
	if err != nil {
		t.Fatalf("ListFeeds: %v", err)
	}
	if len(feeds) != 0 {
		t.Errorf("store still holds %d feed(s) after Remove, want 0", len(feeds))
	}
}

// TestRemoveRequestValidate pins the wording of the two selector usage errors,
// which the CLI surfaces byte for byte. Both exist because a mistyped bulk rm
// otherwise fails by unsubscribing nothing or everything.
func TestRemoveRequestValidate(t *testing.T) {
	cases := []struct {
		name string
		req  feedwatch.RemoveRequest
		want string
	}{
		{
			"ref and tags",
			feedwatch.RemoveRequest{Ref: "blog", Tags: []string{"ai"}},
			"--tag cannot be combined with a named feed; name a feed to unsubscribe exactly that one, or use --tag to unsubscribe a lane",
		},
		{
			"neither selector",
			feedwatch.RemoveRequest{},
			"rm requires a feed reference or --tag",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wantUsageError(t, tc.req.Validate(), tc.want)
		})
	}
}

// removeLaneFixture subscribes three feeds: two carrying "ai" (one of which also
// carries "go") and one carrying nothing, so a tag selection has both an in-lane
// set and an out-of-lane survivor.
func removeLaneFixture(t *testing.T, st store.Store) (both, ai, none string) {
	t.Helper()
	ctx := context.Background()

	both = "https://a.example/feed.xml"
	ai = "https://b.example/feed.xml"
	none = "https://c.example/feed.xml"
	for _, f := range []core.Feed{
		{URL: both, Tags: []string{"ai", "go"}, Status: core.FeedActive},
		{URL: ai, Tags: []string{"ai"}, Status: core.FeedActive},
		{URL: none, Status: core.FeedActive},
	} {
		if _, err := st.AddFeed(ctx, f); err != nil {
			t.Fatalf("AddFeed(%s): %v", f.URL, err)
		}
	}
	return both, ai, none
}

// remainingFeeds reads back the URLs the store still holds.
func remainingFeeds(t *testing.T, st store.Store) []string {
	t.Helper()

	feeds, err := st.ListFeeds(context.Background(), core.ListFilter{})
	if err != nil {
		t.Fatalf("ListFeeds: %v", err)
	}
	out := make([]string, 0, len(feeds))
	for _, f := range feeds {
		out = append(out, f.URL)
	}
	return out
}

// TestRemoveByTagUnsubscribesLane covers the bulk path: every in-lane feed is
// removed and reported in URL order, and the out-of-lane feed survives.
func TestRemoveByTagUnsubscribesLane(t *testing.T) {
	app, st, _ := newTestApp(t)
	both, ai, none := removeLaneFixture(t, st)

	res, err := app.Remove(context.Background(), feedwatch.RemoveRequest{Tags: []string{"ai"}})
	if err != nil {
		t.Fatalf("Remove = %v, want nil", err)
	}
	if want := []string{both, ai}; !reflect.DeepEqual(res.Removed, want) {
		t.Errorf("Removed = %v, want %v in URL order", res.Removed, want)
	}
	if got, want := remainingFeeds(t, st), []string{none}; !reflect.DeepEqual(got, want) {
		t.Errorf("remaining = %v, want %v", got, want)
	}
}

// TestRemoveByTagMatchSemantics covers that --match all and --match any select
// different sets when two tags are named.
func TestRemoveByTagMatchSemantics(t *testing.T) {
	t.Run("all removes only the feed carrying both", func(t *testing.T) {
		app, st, _ := newTestApp(t)
		both, ai, none := removeLaneFixture(t, st)

		res, err := app.Remove(context.Background(), feedwatch.RemoveRequest{Tags: []string{"ai", "go"}})
		if err != nil {
			t.Fatalf("Remove = %v, want nil", err)
		}
		if want := []string{both}; !reflect.DeepEqual(res.Removed, want) {
			t.Errorf("Removed = %v, want %v", res.Removed, want)
		}
		if got, want := remainingFeeds(t, st), []string{ai, none}; !reflect.DeepEqual(got, want) {
			t.Errorf("remaining = %v, want %v", got, want)
		}
	})

	t.Run("any removes every feed carrying either", func(t *testing.T) {
		app, st, _ := newTestApp(t)
		both, ai, none := removeLaneFixture(t, st)

		res, err := app.Remove(context.Background(),
			feedwatch.RemoveRequest{Tags: []string{"ai", "go"}, Match: "any"})
		if err != nil {
			t.Fatalf("Remove = %v, want nil", err)
		}
		if want := []string{both, ai}; !reflect.DeepEqual(res.Removed, want) {
			t.Errorf("Removed = %v, want %v", res.Removed, want)
		}
		if got, want := remainingFeeds(t, st), []string{none}; !reflect.DeepEqual(got, want) {
			t.Errorf("remaining = %v, want %v", got, want)
		}
	})
}

// TestRemoveEmptyLaneRemovesNothing covers the "empty lane is not an error" rule:
// a lane no feed carries succeeds having removed nothing.
func TestRemoveEmptyLaneRemovesNothing(t *testing.T) {
	app, st, _ := newTestApp(t)
	both, ai, none := removeLaneFixture(t, st)

	res, err := app.Remove(context.Background(), feedwatch.RemoveRequest{Tags: []string{"nosuchlane"}})
	if err != nil {
		t.Fatalf("Remove = %v, want nil for an empty lane", err)
	}
	if len(res.Removed) != 0 {
		t.Errorf("Removed = %v, want empty", res.Removed)
	}
	if got, want := remainingFeeds(t, st), []string{both, ai, none}; !reflect.DeepEqual(got, want) {
		t.Errorf("remaining = %v, want every feed %v", got, want)
	}
}
