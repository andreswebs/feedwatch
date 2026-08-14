package feedwatch_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/andreswebs/feedwatch"
	"github.com/andreswebs/feedwatch/core"
	"github.com/andreswebs/feedwatch/internal/testsupport"
)

// TestListEmptyStore pins the collection-coalescing rule of the output
// contract: no subscriptions yields an empty, non-nil list that serializes as
// [] rather than null.
func TestListEmptyStore(t *testing.T) {
	app, _, _ := newTestApp(t)

	res, err := app.List(context.Background(), feedwatch.ListRequest{})
	if err != nil {
		t.Fatalf("List = %v, want nil", err)
	}
	if res.Feeds == nil {
		t.Error("Feeds is nil, want an empty slice")
	}
	if len(res.Feeds) != 0 {
		t.Errorf("Feeds = %v, want empty", res.Feeds)
	}

	b, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("Marshal = %v, want nil", err)
	}
	if !strings.Contains(string(b), `"feeds":[]`) {
		t.Errorf("marshaled result = %s, want feeds as []", b)
	}
}

// TestListMapsFeedFields covers the view mapping, including the zero interval
// that must be omitted rather than rendered as "0s".
func TestListMapsFeedFields(t *testing.T) {
	app, st, _ := newTestApp(t)
	ctx := context.Background()

	if _, err := st.AddFeed(ctx, core.Feed{
		URL: "https://a.example/feed.xml", Alias: "a", Status: core.FeedActive,
	}); err != nil {
		t.Fatalf("AddFeed: %v", err)
	}
	if _, err := st.AddFeed(ctx, core.Feed{
		URL: "https://b.example/feed.xml", Interval: 30 * time.Minute,
		Status: core.FeedDisabled, FailureCount: 4, LastError: "dns: no such host",
	}); err != nil {
		t.Fatalf("AddFeed: %v", err)
	}

	res, err := app.List(ctx, feedwatch.ListRequest{})
	if err != nil {
		t.Fatalf("List = %v, want nil", err)
	}
	if len(res.Feeds) != 2 {
		t.Fatalf("Feeds has %d entries, want 2", len(res.Feeds))
	}

	a, b := res.Feeds[0], res.Feeds[1]
	if a.URL != "https://a.example/feed.xml" || a.Alias != "a" || a.Status != string(core.FeedActive) {
		t.Errorf("first feed = %+v, want the active aliased feed", a)
	}
	if a.Interval != "" {
		t.Errorf("zero interval rendered as %q, want it omitted", a.Interval)
	}
	if b.Interval != "30m0s" {
		t.Errorf("interval = %q, want %q", b.Interval, "30m0s")
	}
	if b.Failures != 4 || b.LastError != "dns: no such host" || b.Status != string(core.FeedDisabled) {
		t.Errorf("second feed = %+v, want the disabled failing feed", b)
	}
}

// TestListReportsTagsAlwaysAsArray covers behaviors 2 and 3: a tagged feed
// reports its tags in canonical order and an untagged one reports [], asserted
// on the marshaled bytes because decoding hides a null.
func TestListReportsTagsAlwaysAsArray(t *testing.T) {
	app, st, _ := newTestApp(t)
	ctx := context.Background()

	if _, err := st.AddFeed(ctx, core.Feed{
		URL: "https://a.example/feed.xml", Tags: []string{"AI", "agents"}, Status: core.FeedActive,
	}); err != nil {
		t.Fatalf("AddFeed: %v", err)
	}
	if _, err := st.AddFeed(ctx, core.Feed{
		URL: "https://b.example/feed.xml", Status: core.FeedActive,
	}); err != nil {
		t.Fatalf("AddFeed: %v", err)
	}

	res, err := app.List(ctx, feedwatch.ListRequest{})
	if err != nil {
		t.Fatalf("List = %v, want nil", err)
	}

	b, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("Marshal = %v, want nil", err)
	}
	if !strings.Contains(string(b), `"tags":["agents","ai"]`) {
		t.Errorf("marshaled result = %s, want the tagged feed's tags in canonical order", b)
	}
	if !strings.Contains(string(b), `"tags":[]`) {
		t.Errorf("marshaled result = %s, want the untagged feed's tags as []", b)
	}
}

// seedLaneFeeds stores the three-feed lane fixture every tag-filter test shares:
// one feed in both lanes, one in a single lane, and one untagged.
func seedLaneFeeds(t *testing.T, st *testsupport.InMemoryStore) (both, one, none string) {
	t.Helper()

	both, one, none = "https://both.example/feed.xml", "https://one.example/feed.xml", "https://none.example/feed.xml"
	ctx := context.Background()
	for _, f := range []core.Feed{
		{URL: both, Tags: []string{"ai", "agents"}, Status: core.FeedActive},
		{URL: one, Tags: []string{"ai"}, Status: core.FeedActive},
		{URL: none, Status: core.FeedActive},
	} {
		if _, err := st.AddFeed(ctx, f); err != nil {
			t.Fatalf("AddFeed(%s): %v", f.URL, err)
		}
	}
	return both, one, none
}

// listedURLs projects a list result onto its feed URLs, which is what every
// tag-filter assertion compares.
func listedURLs(res feedwatch.ListResult) []string {
	urls := make([]string, 0, len(res.Feeds))
	for _, f := range res.Feeds {
		urls = append(urls, f.URL)
	}
	return urls
}

// TestListFiltersByTag covers behaviors 1, 2, 3, and 7 on the library side: a
// tag narrows the listing to a lane, the default match is all, an omitted tag
// set reports every subscription, and an empty lane is an empty list rather
// than an error.
func TestListFiltersByTag(t *testing.T) {
	app, st, _ := newTestApp(t)
	both, one, none := seedLaneFeeds(t, st)

	tests := []struct {
		name string
		req  feedwatch.ListRequest
		want []string
	}{
		{"no tags reports every subscription", feedwatch.ListRequest{}, []string{both, none, one}},
		{"one tag narrows to the lane", feedwatch.ListRequest{Tags: []string{"ai"}}, []string{both, one}},
		{"two tags default to match all", feedwatch.ListRequest{Tags: []string{"ai", "agents"}}, []string{both}},
		{"match any unions the lanes", feedwatch.ListRequest{Tags: []string{"ai", "agents"}, Match: "any"}, []string{both, one}},
		{"match all is spelled explicitly", feedwatch.ListRequest{Tags: []string{"ai", "agents"}, Match: "all"}, []string{both}},
		{"an empty lane is not an error", feedwatch.ListRequest{Tags: []string{"nosuchlane"}}, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := app.List(context.Background(), tt.req)
			if err != nil {
				t.Fatalf("List = %v, want nil", err)
			}
			got := listedURLs(res)
			if len(got) != len(tt.want) {
				t.Fatalf("urls = %v, want %v", got, tt.want)
			}
			for i, u := range tt.want {
				if got[i] != u {
					t.Errorf("urls = %v, want %v", got, tt.want)
					break
				}
			}
		})
	}
}

// TestListRejectsInvalidTagSelection covers behaviors 5 and 6: an unknown
// --match value and an unstorable tag name are usage errors, reported by both
// Validate and the use case so a frontend can reject before dialing the store.
func TestListRejectsInvalidTagSelection(t *testing.T) {
	tests := []struct {
		name string
		req  feedwatch.ListRequest
		msg  string
	}{
		{"unknown match", feedwatch.ListRequest{Tags: []string{"ai"}, Match: "bogus"}, `match must be 'all' or 'any', got "bogus"`},
		{"empty tag", feedwatch.ListRequest{Tags: []string{""}}, `tag must not be empty, got ""`},
		{"tag with whitespace", feedwatch.ListRequest{Tags: []string{"a b"}}, `tag must not contain whitespace, got "a b"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app, st, _ := newTestApp(t)
			seedLaneFeeds(t, st)

			wantUsageError(t, tt.req.Validate(), tt.msg)

			_, err := app.List(context.Background(), tt.req)
			wantUsageError(t, err, tt.msg)
		})
	}
}
