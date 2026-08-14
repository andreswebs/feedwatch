package feedwatch_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/andreswebs/feedwatch"
	"github.com/andreswebs/feedwatch/core"
	"github.com/andreswebs/feedwatch/internal/testsupport"
)

// seedTaggedFeeds subscribes the lane fixture the tags tests share: one feed in
// two lanes, one in a single lane, and one untagged, so a count is only correct
// if it is per tag rather than per feed.
func seedTaggedFeeds(t *testing.T, st *testsupport.InMemoryStore) {
	t.Helper()

	ctx := context.Background()
	feeds := []core.Feed{
		{URL: "https://a.example/feed.xml", Tags: []string{"ai", "agents"}, Status: core.FeedActive},
		{URL: "https://b.example/feed.xml", Tags: []string{"ai"}, Status: core.FeedActive},
		{URL: "https://c.example/feed.xml", Status: core.FeedActive},
	}
	for _, f := range feeds {
		if _, err := st.AddFeed(ctx, f); err != nil {
			t.Fatalf("AddFeed(%s): %v", f.URL, err)
		}
	}
}

// TestTagsEmptyVocabulary pins the collection-coalescing rule of the output
// contract: a store carrying no tags yields a list that serializes as [] rather
// than null, so an agent never has to special-case an empty vocabulary.
func TestTagsEmptyVocabulary(t *testing.T) {
	app, _, _ := newTestApp(t)

	res, err := app.Tags(context.Background(), feedwatch.TagsRequest{})
	if err != nil {
		t.Fatalf("Tags = %v, want nil", err)
	}
	if len(res.Tags) != 0 {
		t.Errorf("Tags = %v, want empty", res.Tags)
	}

	b, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("Marshal = %v, want nil", err)
	}
	if !strings.Contains(string(b), `"tags":[]`) {
		t.Errorf("marshaled result = %s, want tags as []", b)
	}
}

// TestTagsCountsDisabledFeeds covers behavior 3: a lane whose feeds are all
// disabled is still reported, so a disabled lane stays discoverable.
func TestTagsCountsDisabledFeeds(t *testing.T) {
	app, st, _ := newTestApp(t)
	ctx := context.Background()

	if _, err := st.AddFeed(ctx, core.Feed{
		URL: "https://dead.example/feed.xml", Tags: []string{"archive"}, Status: core.FeedDisabled,
	}); err != nil {
		t.Fatalf("AddFeed: %v", err)
	}

	res, err := app.Tags(ctx, feedwatch.TagsRequest{})
	if err != nil {
		t.Fatalf("Tags = %v, want nil", err)
	}
	want := core.TagCount{Tag: "archive", Feeds: 1}
	if len(res.Tags) != 1 || res.Tags[0] != want {
		t.Errorf("Tags = %v, want [%v]", res.Tags, want)
	}
}

// TestTagsReportsVocabulary covers behavior 1: every distinct tag is reported
// with the number of subscriptions carrying it, sorted by tag.
func TestTagsReportsVocabulary(t *testing.T) {
	app, st, _ := newTestApp(t)
	seedTaggedFeeds(t, st)

	res, err := app.Tags(context.Background(), feedwatch.TagsRequest{})
	if err != nil {
		t.Fatalf("Tags = %v, want nil", err)
	}

	want := []core.TagCount{{Tag: "agents", Feeds: 1}, {Tag: "ai", Feeds: 2}}
	if len(res.Tags) != len(want) {
		t.Fatalf("Tags = %v, want %v", res.Tags, want)
	}
	for i, w := range want {
		if res.Tags[i] != w {
			t.Errorf("Tags[%d] = %v, want %v", i, res.Tags[i], w)
		}
	}
}
