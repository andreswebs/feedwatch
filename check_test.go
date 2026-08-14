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

// seedLaneCheckFeeds seeds the lane fixture the tag-scoped check tests share: a
// feed in two lanes, a feed in one, and an untagged feed, all healthy.
func seedLaneCheckFeeds(t *testing.T, st *testsupport.InMemoryStore, f *testsupport.FakeFetcher, p *testsupport.FakeParser) (both, one, none string) {
	t.Helper()

	both, one, none = "https://both.example/feed.xml", "https://one.example/feed.xml", "https://none.example/feed.xml"
	for _, feed := range []core.Feed{
		{URL: both, Tags: []string{"ai", "agents"}},
		{URL: one, Tags: []string{"ai"}},
		{URL: none},
	} {
		if _, err := st.AddFeed(context.Background(), feed); err != nil {
			t.Fatalf("AddFeed(%s): %v", feed.URL, err)
		}
		registerFeed(f, p, feed.URL, "Seeded Feed")
	}
	return both, one, none
}

// TestCheckScopesToTheLane covers behavior 7: --tag narrows the active
// selection, and checked reflects the lane rather than the whole store.
func TestCheckScopesToTheLane(t *testing.T) {
	app, st, fetcher, parser := newNetworkApp(t)
	_, _, none := seedLaneCheckFeeds(t, st, fetcher, parser)

	res, err := app.Check(context.Background(), feedwatch.CheckRequest{Tags: []string{"ai"}})
	if err != nil {
		t.Fatalf("Check = %v, want nil", err)
	}
	if res.Checked != 2 || res.Passed != 2 {
		t.Errorf("checked/passed = %d/%d, want 2/2 for the lane", res.Checked, res.Passed)
	}
	if n := len(fetcher.Requests(none)); n != 0 {
		t.Errorf("out-of-lane feed was fetched %d time(s), want 0", n)
	}
}

// TestCheckMultiTagMatch pins the same intersection-by-default semantics poll
// uses, so the two commands answer "which lane" identically.
func TestCheckMultiTagMatch(t *testing.T) {
	tests := []struct {
		name  string
		match string
		want  int
	}{
		{"default matches all tags", "", 1},
		{"match any unions the lanes", "any", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app, st, fetcher, parser := newNetworkApp(t)
			seedLaneCheckFeeds(t, st, fetcher, parser)

			res, err := app.Check(context.Background(), feedwatch.CheckRequest{
				Tags: []string{"ai", "agents"}, Match: tt.match,
			})
			if err != nil {
				t.Fatalf("Check = %v, want nil", err)
			}
			if res.Checked != tt.want {
				t.Errorf("checked = %d, want %d", res.Checked, tt.want)
			}
		})
	}
}

// TestCheckDisabledFeedInLaneIsSkipped covers behavior 9 for check: status
// filtering still comes first inside a lane.
func TestCheckDisabledFeedInLaneIsSkipped(t *testing.T) {
	app, st, fetcher, parser := newNetworkApp(t)
	ctx := context.Background()
	_, one, _ := seedLaneCheckFeeds(t, st, fetcher, parser)
	if err := st.SetStatus(ctx, one, core.FeedDisabled); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	res, err := app.Check(ctx, feedwatch.CheckRequest{Tags: []string{"ai"}})
	if err != nil {
		t.Fatalf("Check = %v, want nil", err)
	}
	if res.Checked != 1 {
		t.Errorf("checked = %d, want only the active in-lane feed", res.Checked)
	}
	if n := len(fetcher.Requests(one)); n != 0 {
		t.Errorf("disabled in-lane feed was fetched %d time(s), want 0", n)
	}
}

// checkTagAndFeedsMessage is the usage message rejecting --tag alongside named
// feeds, pinned so the library and CLI tests cannot drift from each other.
const checkTagAndFeedsMessage = "--tag cannot be combined with named feeds; " +
	"name feeds to check exactly those, or use --tag to check a lane"

// TestCheckRejectsTagWithNamedFeeds covers behavior 8: check follows poll's
// rule, rejecting the ambiguous selection before anything is fetched.
func TestCheckRejectsTagWithNamedFeeds(t *testing.T) {
	app, st, fetcher, parser := newNetworkApp(t)
	both, _, _ := seedLaneCheckFeeds(t, st, fetcher, parser)
	req := feedwatch.CheckRequest{Feeds: []string{both}, Tags: []string{"ai"}}

	wantUsageError(t, req.Validate(), checkTagAndFeedsMessage)

	res, err := app.Check(context.Background(), req)
	wantUsageError(t, err, checkTagAndFeedsMessage)
	if res.Checked != 0 {
		t.Errorf("checked = %d, want 0 so no envelope is rendered", res.Checked)
	}
	if n := len(fetcher.Requests(both)); n != 0 {
		t.Errorf("feed was fetched %d time(s), want 0 on a rejected check", n)
	}
}

// TestCheckRejectsInvalidTagSelection covers behavior 10 for check.
func TestCheckRejectsInvalidTagSelection(t *testing.T) {
	tests := []struct {
		name string
		req  feedwatch.CheckRequest
		msg  string
	}{
		{"unknown match", feedwatch.CheckRequest{Tags: []string{"ai"}, Match: "bogus"}, `match must be 'all' or 'any', got "bogus"`},
		{"tag with whitespace", feedwatch.CheckRequest{Tags: []string{"a b"}}, `tag must not contain whitespace, got "a b"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app, st, fetcher, parser := newNetworkApp(t)
			both, _, _ := seedLaneCheckFeeds(t, st, fetcher, parser)

			wantUsageError(t, tt.req.Validate(), tt.msg)

			_, err := app.Check(context.Background(), tt.req)
			wantUsageError(t, err, tt.msg)
			if n := len(fetcher.Requests(both)); n != 0 {
				t.Errorf("feed was fetched %d time(s), want 0 on a rejected check", n)
			}
		})
	}
}
