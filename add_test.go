package feedwatch_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/andreswebs/feedwatch"
	"github.com/andreswebs/feedwatch/core"
	"github.com/andreswebs/feedwatch/internal/testsupport"
)

const addFeedURL = "https://example.com/feed.xml"

// addDiscoverHint is the usage message a syntactically unusable feed URL must
// carry, pointing the agent at discover rather than guessing over the network.
const addDiscoverHint = "add requires an absolute http(s) feed URL; " +
	"run 'feedwatch discover <url>' to find a feed from a homepage"

// wantNoFeeds fails when the store holds any subscription, which is how a test
// proves a rejected add wrote nothing.
func wantNoFeeds(t *testing.T, st *testsupport.InMemoryStore) {
	t.Helper()

	feeds, err := st.ListFeeds(context.Background(), core.ListFilter{})
	if err != nil {
		t.Fatalf("ListFeeds: %v", err)
	}
	if len(feeds) != 0 {
		t.Errorf("store holds %d feed(s), want 0: %+v", len(feeds), feeds)
	}
}

func TestAddRequestValidateRejectsNonFeedURL(t *testing.T) {
	for name, raw := range map[string]string{
		"bare host":       "example.com/feed.xml",
		"non-http scheme": "file:///tmp/feed.xml",
		"empty":           "",
	} {
		t.Run(name, func(t *testing.T) {
			req := feedwatch.AddRequest{URL: raw}
			wantUsageError(t, req.Validate(), addDiscoverHint)
		})
	}
}

func TestAddRejectsBodyThatDoesNotParseAsFeed(t *testing.T) {
	app, st, fetcher, parser := newNetworkApp(t)
	fetcher.Register(addFeedURL, htmlPage(`<html><body>a homepage</body></html>`))
	parser.RegisterError(addFeedURL, errors.New("not a feed"))

	_, err := app.Add(context.Background(), feedwatch.AddRequest{URL: addFeedURL})
	wantUsageError(t, err, addFeedURL+" does not parse as a feed; run 'feedwatch discover "+addFeedURL+"' to find its feeds")
	wantNoFeeds(t, st)
}

func TestAddRejectsUnreachableURLAsUsageNotNetwork(t *testing.T) {
	app, st, fetcher, _ := newNetworkApp(t)
	fetcher.RegisterError(addFeedURL, core.NetworkErr(addFeedURL, errors.New("dial tcp: no such host")))

	_, err := app.Add(context.Background(), feedwatch.AddRequest{URL: addFeedURL})
	wantUsageError(t, err, "could not fetch "+addFeedURL+" to validate it as a feed")
	wantNoFeeds(t, st)
}

func TestAddCreatesThenUpdatesIdempotently(t *testing.T) {
	app, _, fetcher, parser := newNetworkApp(t)
	registerFeed(fetcher, parser, addFeedURL, "Example Blog")
	ctx := context.Background()

	first, err := app.Add(ctx, feedwatch.AddRequest{URL: addFeedURL, Alias: "example"})
	if err != nil {
		t.Fatalf("Add = %v, want nil", err)
	}
	if !first.Created {
		t.Errorf("created = false on a fresh subscription, want true")
	}
	if first.URL != addFeedURL {
		t.Errorf("url = %q, want %q", first.URL, addFeedURL)
	}
	if first.Interval != "" {
		t.Errorf("interval = %q, want empty for the configured default", first.Interval)
	}

	second, err := app.Add(ctx, feedwatch.AddRequest{URL: addFeedURL, Alias: "blog", Interval: 30 * time.Minute})
	if err != nil {
		t.Fatalf("re-Add = %v, want nil", err)
	}
	if second.Created {
		t.Errorf("created = true on a re-add, want false")
	}
	if second.Alias != "blog" {
		t.Errorf("alias = %q, want blog", second.Alias)
	}
	if second.Interval != "30m0s" {
		t.Errorf("interval = %q, want 30m0s", second.Interval)
	}
}

func TestAddRejectsBadURLBeforeFetching(t *testing.T) {
	app, st, fetcher, _ := newNetworkApp(t)

	_, err := app.Add(context.Background(), feedwatch.AddRequest{URL: "example.com/feed.xml"})
	wantUsageError(t, err, addDiscoverHint)
	if n := len(fetcher.Requests("example.com/feed.xml")); n != 0 {
		t.Errorf("fetcher saw %d request(s), want 0", n)
	}
	wantNoFeeds(t, st)
}
