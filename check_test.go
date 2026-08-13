package feedwatch_test

import (
	"context"
	"errors"
	"testing"

	"github.com/andreswebs/feedwatch"
	"github.com/andreswebs/feedwatch/core"
	"github.com/andreswebs/feedwatch/internal/testsupport"
)

// seedFeed subscribes url in st and registers it as a healthy feed, which is the
// starting state every check test varies from.
func seedFeed(t *testing.T, st *testsupport.InMemoryStore, f *testsupport.FakeFetcher, p *testsupport.FakeParser, url string) {
	t.Helper()

	if _, err := st.AddFeed(context.Background(), core.Feed{URL: url}); err != nil {
		t.Fatalf("AddFeed(%s): %v", url, err)
	}
	registerFeed(f, p, url, "Seeded Feed")
}

func TestCheckTargetsActiveFeedsOnly(t *testing.T) {
	app, st, fetcher, parser := newNetworkApp(t)
	ctx := context.Background()
	seedFeed(t, st, fetcher, parser, "https://a.example/feed.xml")
	seedFeed(t, st, fetcher, parser, "https://b.example/feed.xml")
	if err := st.SetStatus(ctx, "https://b.example/feed.xml", core.FeedDisabled); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	res, err := app.Check(ctx, feedwatch.CheckRequest{})
	if err != nil {
		t.Fatalf("Check = %v, want nil", err)
	}
	if res.Checked != 1 || res.Passed != 1 || res.Failed != 0 {
		t.Errorf("checked/passed/failed = %d/%d/%d, want 1/1/0", res.Checked, res.Passed, res.Failed)
	}
	if n := len(fetcher.Requests("https://b.example/feed.xml")); n != 0 {
		t.Errorf("disabled feed was fetched %d time(s), want 0", n)
	}
}

func TestCheckUnknownRefIsHardError(t *testing.T) {
	app, st, fetcher, parser := newNetworkApp(t)
	seedFeed(t, st, fetcher, parser, "https://a.example/feed.xml")

	res, err := app.Check(context.Background(), feedwatch.CheckRequest{Feeds: []string{"nope"}})
	if err == nil {
		t.Fatalf("Check = nil error, want a hard failure for an unknown ref")
	}
	if res.Checked != 0 || res.Failures != nil {
		t.Errorf("result = %+v, want the zero envelope so nothing is rendered", res)
	}
}

func TestCheckReportsPerFeedFailuresWithoutStoreWrites(t *testing.T) {
	app, st, fetcher, parser := newNetworkApp(t)
	ctx := context.Background()
	const good, bad = "https://good.example/feed.xml", "https://bad.example/feed.xml"
	seedFeed(t, st, fetcher, parser, good)
	seedFeed(t, st, fetcher, parser, bad)
	fetcher.RegisterError(bad, core.HTTPErr(bad, 404, errors.New("server returned HTTP 404")))

	res, err := app.Check(ctx, feedwatch.CheckRequest{})
	if err != nil {
		t.Fatalf("Check = %v, want nil", err)
	}
	if res.Checked != 2 || res.Passed != 1 || res.Failed != 1 {
		t.Errorf("checked/passed/failed = %d/%d/%d, want 2/1/1", res.Checked, res.Passed, res.Failed)
	}
	if len(res.Failures) != 1 {
		t.Fatalf("failures = %d, want 1: %+v", len(res.Failures), res.Failures)
	}
	if got := res.Failures[0]; got.FeedURL != bad || got.Category != core.CatHTTP || got.Status != 404 {
		t.Errorf("failure = %+v, want %s/http/404", got, bad)
	}
	if code := res.ExitCode(); code != 3 {
		t.Errorf("exit code = %d, want 3 for a partial outcome", code)
	}

	// Check never records a failure against the feed: the lifecycle counters
	// belong to poll, so a check leaves every feed exactly as it found it.
	feed, err := st.GetFeed(ctx, bad)
	if err != nil {
		t.Fatalf("GetFeed: %v", err)
	}
	if feed.FailureCount != 0 || feed.LastError != "" || feed.Status != core.FeedActive {
		t.Errorf("feed state = %+v, want untouched (0 failures, no last error, active)", feed)
	}
}

func TestCheckExitCodeSummarizesOutcome(t *testing.T) {
	cases := map[string]struct {
		res  feedwatch.CheckResult
		want int
	}{
		"nothing checked": {feedwatch.CheckResult{}, 0},
		"all passed":      {feedwatch.CheckResult{Checked: 3, Passed: 3}, 0},
		"all failed":      {feedwatch.CheckResult{Checked: 3, Failed: 3}, 2},
		"partial":         {feedwatch.CheckResult{Checked: 3, Passed: 1, Failed: 2}, 3},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := tc.res.ExitCode(); got != tc.want {
				t.Errorf("ExitCode() = %d, want %d", got, tc.want)
			}
		})
	}
}
