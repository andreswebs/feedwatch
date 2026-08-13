package feedwatch_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/andreswebs/feedwatch"
	"github.com/andreswebs/feedwatch/core"
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
