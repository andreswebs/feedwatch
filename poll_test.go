package feedwatch_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/andreswebs/feedwatch"
	"github.com/andreswebs/feedwatch/core"
	"github.com/andreswebs/feedwatch/internal/poll"
	"github.com/andreswebs/feedwatch/internal/testsupport"
)

// pollFeed is one seeded subscription and the items its wire body carries. A
// non-nil fetchErr makes the feed fail instead, which is how a test drives the
// failure lifecycle. tags places the feed in a lane, and notDue schedules it
// into the future so an unforced poll leaves it out.
type pollFeed struct {
	url      string
	items    []core.Item
	fetchErr error
	tags     []string
	notDue   bool
	disabled bool
}

// wireItem builds a parsed wire item with a stable dedup key.
func wireItem(key, title string) core.Item {
	return core.Item{DedupKey: key, Title: title, Link: "https://example.com/" + key}
}

// newPollApp builds an App over an in-memory store with programmable network
// collaborators and the given configuration overrides applied to Defaults, then
// seeds each feed as an active subscription whose fetch returns its items.
func newPollApp(t *testing.T, tune func(*feedwatch.Config), warn feedwatch.Warner, feeds ...pollFeed) (*feedwatch.App, *testsupport.InMemoryStore, *testsupport.FakeFetcher) {
	t.Helper()

	clk := testsupport.FixedClock(fixedTestTime())
	st := testsupport.NewInMemoryStore(clk)
	fetcher := testsupport.NewFakeFetcher()
	parser := testsupport.NewFakeParser()

	cfg := feedwatch.Defaults()
	if tune != nil {
		tune(&cfg)
	}

	opts := []feedwatch.Option{
		feedwatch.WithStore(st),
		feedwatch.WithClock(clk),
		feedwatch.WithFetcher(fetcher),
		feedwatch.WithParser(parser),
	}
	if warn != nil {
		opts = append(opts, feedwatch.WithWarner(warn))
	}
	app, err := feedwatch.New(cfg, opts...)
	if err != nil {
		t.Fatalf("New = %v, want nil", err)
	}
	t.Cleanup(func() { _ = app.Close() })

	for _, f := range feeds {
		seed := core.Feed{URL: f.url, Tags: f.tags, Status: core.FeedActive}
		if f.notDue {
			due := fixedTestTime().Add(time.Hour)
			seed.NextDueAt = &due
		}
		if f.disabled {
			seed.Status = core.FeedDisabled
		}
		if _, err := st.AddFeed(context.Background(), seed); err != nil {
			t.Fatalf("AddFeed(%s): %v", f.url, err)
		}
		if f.fetchErr != nil {
			fetcher.RegisterError(f.url, f.fetchErr)
			continue
		}
		fetcher.Register(f.url, core.FetchResult{Status: 200, MIMEType: "application/rss+xml", Body: []byte("<rss/>")})
		parser.Register(f.url, core.ParsedFeed{Title: "Feed", Items: f.items})
	}
	return app, st, fetcher
}

func TestPollReportsConsistentCountsAndEmptyFailures(t *testing.T) {
	app, _, _ := newPollApp(t, nil, nil, pollFeed{
		url:   "https://a.example/feed.xml",
		items: []core.Item{wireItem("i1", "One"), wireItem("i2", "Two")},
	})

	res, err := app.Poll(context.Background(), feedwatch.PollRequest{})
	if err != nil {
		t.Fatalf("Poll = %v, want nil", err)
	}
	if res.Failures == nil {
		t.Errorf("failures is nil, want an empty slice")
	}
	if len(res.Failures) != 0 {
		t.Errorf("failures = %+v, want none", res.Failures)
	}
	if res.Polled != res.Succeeded+res.Failed {
		t.Errorf("polled = %d, want succeeded+failed = %d", res.Polled, res.Succeeded+res.Failed)
	}
	if res.Deduped != res.Fetched-res.NewItems {
		t.Errorf("deduped = %d, want fetched-new_items = %d", res.Deduped, res.Fetched-res.NewItems)
	}
	if res.NewItems != 2 {
		t.Errorf("new_items = %d, want 2", res.NewItems)
	}
	if res.ExitCode() != 0 {
		t.Errorf("exit code = %d, want 0", res.ExitCode())
	}
}

func TestPollSecondRunOfUnchangedFeedReportsNothingNew(t *testing.T) {
	app, _, _ := newPollApp(t, nil, nil, pollFeed{
		url:   "https://a.example/feed.xml",
		items: []core.Item{wireItem("i1", "One")},
	})
	ctx := context.Background()

	if _, err := app.Poll(ctx, feedwatch.PollRequest{Force: true}); err != nil {
		t.Fatalf("first Poll = %v, want nil", err)
	}
	res, err := app.Poll(ctx, feedwatch.PollRequest{Force: true})
	if err != nil {
		t.Fatalf("second Poll = %v, want nil", err)
	}
	if res.NewItems != 0 || len(res.Items) != 0 {
		t.Errorf("new_items = %d with %d item(s), want 0", res.NewItems, len(res.Items))
	}
	if res.Deduped != 1 {
		t.Errorf("deduped = %d, want 1", res.Deduped)
	}
	if res.ExitCode() != 0 {
		t.Errorf("exit code = %d, want 0", res.ExitCode())
	}
}

// advisory records one warning raised through the App's Warner.
type advisory struct {
	code    string
	message string
	hint    string
	details any
}

func TestPollRaisesOneAdvisoryWhenAFeedIsAutoDisabled(t *testing.T) {
	const url = "https://flaky.example/feed.xml"

	var got []advisory
	app, st, _ := newPollApp(t,
		func(c *feedwatch.Config) { c.FailureThreshold = 1 },
		func(code, message, hint string, details any) {
			got = append(got, advisory{code, message, hint, details})
		},
		pollFeed{url: url, fetchErr: core.NetworkErr(url, errors.New("dial tcp: no such host"))},
	)
	ctx := context.Background()

	res, err := app.Poll(ctx, feedwatch.PollRequest{Force: true})
	if err != nil {
		t.Fatalf("Poll = %v, want nil: a per-feed failure is result data", err)
	}
	if res.Failed != 1 || res.ExitCode() != 2 {
		t.Errorf("failed = %d with exit code %d, want 1 and 2", res.Failed, res.ExitCode())
	}

	feed, err := st.GetFeed(ctx, url)
	if err != nil {
		t.Fatalf("GetFeed: %v", err)
	}
	if feed.Status != core.FeedDisabled {
		t.Errorf("status = %q, want disabled after crossing the threshold", feed.Status)
	}

	if len(got) != 1 {
		t.Fatalf("advisories = %d, want exactly 1: %+v", len(got), got)
	}
	if got[0].code != "feed_auto_disabled" {
		t.Errorf("code = %q, want feed_auto_disabled", got[0].code)
	}
	if got[0].message != "feed disabled after 1 consecutive failures" {
		t.Errorf("message = %q", got[0].message)
	}
	if got[0].hint != "re-enable with: feedwatch enable <feed>" {
		t.Errorf("hint = %q", got[0].hint)
	}
}

func TestPollExitCodeAgreesWithTheOrchestrator(t *testing.T) {
	for _, tc := range []struct{ polled, failed int }{
		{0, 0}, {1, 0}, {1, 1}, {3, 0}, {3, 1}, {3, 3}, {5, 2},
	} {
		env := feedwatch.PollResult{
			Polled:    tc.polled,
			Failed:    tc.failed,
			Succeeded: tc.polled - tc.failed,
		}
		want := poll.Result{Polled: tc.polled, Failed: tc.failed}.ExitCode()
		if got := env.ExitCode(); got != want {
			t.Errorf("polled=%d failed=%d: PollResult.ExitCode() = %d, want %d",
				tc.polled, tc.failed, got, want)
		}
	}
}

func TestPollReportsPartialResultWhenPersistenceFailsMidRun(t *testing.T) {
	const good, bad = "https://good.example/feed.xml", "https://bad.example/feed.xml"

	clk := testsupport.FixedClock(fixedTestTime())
	inner := testsupport.NewInMemoryStore(clk)
	st := &testsupport.FailingUpsertStore{Store: inner, FailURL: bad}
	fetcher := testsupport.NewFakeFetcher()
	parser := testsupport.NewFakeParser()

	cfg := feedwatch.Defaults()
	cfg.Concurrency = 1
	app, err := feedwatch.New(cfg,
		feedwatch.WithStore(st),
		feedwatch.WithClock(clk),
		feedwatch.WithFetcher(fetcher),
		feedwatch.WithParser(parser),
	)
	if err != nil {
		t.Fatalf("New = %v, want nil", err)
	}
	t.Cleanup(func() { _ = app.Close() })

	ctx := context.Background()
	for _, url := range []string{good, bad} {
		if _, err := st.AddFeed(ctx, core.Feed{URL: url}); err != nil {
			t.Fatalf("AddFeed(%s): %v", url, err)
		}
		fetcher.Register(url, core.FetchResult{Status: 200, MIMEType: "application/rss+xml", Body: []byte("<rss/>")})
		parser.Register(url, core.ParsedFeed{Title: "Feed", Items: []core.Item{wireItem("i1", "One")}})
	}

	res, err := app.Poll(ctx, feedwatch.PollRequest{Force: true})
	if err == nil {
		t.Fatalf("Poll = nil error, want the mid-persist write failure")
	}
	if res.Polled == 0 {
		t.Errorf("polled = 0, want the feeds already persisted before the failure")
	}
}

func TestPollReturnsZeroResultWhenTheStoreCannotBeOpened(t *testing.T) {
	cfg := feedwatch.Defaults()
	cfg.Store = filepath.Join(t.TempDir(), "missing-dir", "feedwatch.db")

	app, err := feedwatch.New(cfg, feedwatch.WithFetcher(testsupport.NewFakeFetcher()))
	if err != nil {
		t.Fatalf("New = %v, want nil", err)
	}
	t.Cleanup(func() { _ = app.Close() })

	res, err := app.Poll(context.Background(), feedwatch.PollRequest{})
	if err == nil {
		t.Fatalf("Poll = nil error, want an unopenable store to fail")
	}
	if res.Polled != 0 {
		t.Errorf("polled = %d, want 0 so no envelope is rendered for an early failure", res.Polled)
	}
	if !errors.Is(err, core.ErrStoreUnavailable) {
		t.Errorf("error = %v, want a store-unavailable failure", err)
	}
}

// laneFeeds is the lane fixture every tag-scoped poll test varies from: a feed
// in two lanes, a feed in one, and an untagged feed, each carrying one item.
func laneFeeds() (both, one, none pollFeed) {
	return pollFeed{
			url: "https://both.example/feed.xml", tags: []string{"ai", "agents"},
			items: []core.Item{wireItem("b1", "Both")},
		},
		pollFeed{
			url: "https://one.example/feed.xml", tags: []string{"ai"},
			items: []core.Item{wireItem("o1", "One")},
		},
		pollFeed{
			url:   "https://none.example/feed.xml",
			items: []core.Item{wireItem("n1", "None")},
		}
}

// TestPollForceScopesToTheLane is the tracer bullet: a forced poll narrowed by
// --tag fetches exactly the in-lane feeds and never dials the others.
func TestPollForceScopesToTheLane(t *testing.T) {
	both, one, none := laneFeeds()
	app, _, fetcher := newPollApp(t, nil, nil, both, one, none)

	res, err := app.Poll(context.Background(), feedwatch.PollRequest{Force: true, Tags: []string{"ai"}})
	if err != nil {
		t.Fatalf("Poll = %v, want nil", err)
	}
	if res.Polled != 2 {
		t.Errorf("polled = %d, want 2 in-lane feeds", res.Polled)
	}
	if n := len(fetcher.Requests(none.url)); n != 0 {
		t.Errorf("out-of-lane feed was fetched %d time(s), want 0", n)
	}
}

// TestPollTagNarrowsTheDueSelection covers behavior 2: --tag narrows the due
// selection rather than implying --force, so a lane runs on its own cadence.
func TestPollTagNarrowsTheDueSelection(t *testing.T) {
	both, one, none := laneFeeds()
	one.notDue = true
	app, _, fetcher := newPollApp(t, nil, nil, both, one, none)

	res, err := app.Poll(context.Background(), feedwatch.PollRequest{Tags: []string{"ai"}})
	if err != nil {
		t.Fatalf("Poll = %v, want nil", err)
	}
	if res.Polled != 1 {
		t.Errorf("polled = %d, want only the due in-lane feed", res.Polled)
	}
	if n := len(fetcher.Requests(one.url)); n != 0 {
		t.Errorf("undue in-lane feed was fetched %d time(s), want 0", n)
	}
	if n := len(fetcher.Requests(none.url)); n != 0 {
		t.Errorf("due out-of-lane feed was fetched %d time(s), want 0", n)
	}
}

// TestPollSkippedCountsAgainstTheLane covers behavior 3: skipped is measured
// against the lane, not the whole store, so it never reports feeds that were
// never candidates.
func TestPollSkippedCountsAgainstTheLane(t *testing.T) {
	both, one, none := laneFeeds()
	one.notDue = true
	app, _, _ := newPollApp(t, nil, nil, both, one, none)

	res, err := app.Poll(context.Background(), feedwatch.PollRequest{Tags: []string{"ai"}})
	if err != nil {
		t.Fatalf("Poll = %v, want nil", err)
	}
	if res.Polled != 1 || res.Skipped != 1 {
		t.Errorf("polled/skipped = %d/%d, want 1/1 against the two-feed lane", res.Polled, res.Skipped)
	}
}

// TestPollMultiTagMatch covers behavior 4: several tags intersect by default
// and union under --match any.
func TestPollMultiTagMatch(t *testing.T) {
	tests := []struct {
		name  string
		match string
		want  int
	}{
		{"default matches all tags", "", 1},
		{"match all is spelled explicitly", "all", 1},
		{"match any unions the lanes", "any", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			both, one, none := laneFeeds()
			app, _, _ := newPollApp(t, nil, nil, both, one, none)

			res, err := app.Poll(context.Background(), feedwatch.PollRequest{
				Force: true, Tags: []string{"ai", "agents"}, Match: tt.match,
			})
			if err != nil {
				t.Fatalf("Poll = %v, want nil", err)
			}
			if res.Polled != tt.want {
				t.Errorf("polled = %d, want %d", res.Polled, tt.want)
			}
		})
	}
}

// TestPollDisabledFeedInLaneIsSkipped covers behavior 9: status filtering comes
// first, so a disabled feed carrying the tag is still left alone.
func TestPollDisabledFeedInLaneIsSkipped(t *testing.T) {
	both, one, none := laneFeeds()
	one.disabled = true
	app, _, fetcher := newPollApp(t, nil, nil, both, one, none)

	res, err := app.Poll(context.Background(), feedwatch.PollRequest{Force: true, Tags: []string{"ai"}})
	if err != nil {
		t.Fatalf("Poll = %v, want nil", err)
	}
	if res.Polled != 1 {
		t.Errorf("polled = %d, want only the active in-lane feed", res.Polled)
	}
	if n := len(fetcher.Requests(one.url)); n != 0 {
		t.Errorf("disabled in-lane feed was fetched %d time(s), want 0", n)
	}
}

// TestPollRejectsTagWithNamedFeeds covers behavior 5: naming feeds and naming a
// lane are two different selections, so combining them is a usage error that
// fetches nothing rather than a silent narrowing.
func TestPollRejectsTagWithNamedFeeds(t *testing.T) {
	both, one, none := laneFeeds()
	app, _, fetcher := newPollApp(t, nil, nil, both, one, none)
	req := feedwatch.PollRequest{Feeds: []string{both.url}, Tags: []string{"ai"}}

	wantUsageError(t, req.Validate(), pollTagAndFeedsMessage)

	res, err := app.Poll(context.Background(), req)
	wantUsageError(t, err, pollTagAndFeedsMessage)
	if res.Polled != 0 {
		t.Errorf("polled = %d, want 0 so no envelope is rendered", res.Polled)
	}
	if n := len(fetcher.Requests(both.url)); n != 0 {
		t.Errorf("feed was fetched %d time(s), want 0 on a rejected poll", n)
	}
}

// pollTagAndFeedsMessage is the usage message rejecting --tag alongside named
// feeds, pinned here so the library and CLI tests cannot drift from each other.
const pollTagAndFeedsMessage = "--tag cannot be combined with named feeds; " +
	"name feeds to poll exactly those, or use --tag to poll a lane"

// TestPollRejectsInvalidTagSelection covers behavior 10: the shared tag filter
// rejects an unknown --match value and an unstorable tag name.
func TestPollRejectsInvalidTagSelection(t *testing.T) {
	tests := []struct {
		name string
		req  feedwatch.PollRequest
		msg  string
	}{
		{"unknown match", feedwatch.PollRequest{Tags: []string{"ai"}, Match: "bogus"}, `match must be 'all' or 'any', got "bogus"`},
		{"tag with whitespace", feedwatch.PollRequest{Tags: []string{"a b"}}, `tag must not contain whitespace, got "a b"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			both, one, none := laneFeeds()
			app, _, fetcher := newPollApp(t, nil, nil, both, one, none)

			wantUsageError(t, tt.req.Validate(), tt.msg)

			_, err := app.Poll(context.Background(), tt.req)
			wantUsageError(t, err, tt.msg)
			if n := len(fetcher.Requests(both.url)); n != 0 {
				t.Errorf("feed was fetched %d time(s), want 0 on a rejected poll", n)
			}
		})
	}
}

// TestPollWithoutTagsIsUnchanged covers behavior 6: the untagged scheduled poll
// still targets every due active feed, whatever lanes they carry.
func TestPollWithoutTagsIsUnchanged(t *testing.T) {
	both, one, none := laneFeeds()
	one.notDue = true
	app, _, _ := newPollApp(t, nil, nil, both, one, none)

	res, err := app.Poll(context.Background(), feedwatch.PollRequest{})
	if err != nil {
		t.Fatalf("Poll = %v, want nil", err)
	}
	if res.Polled != 2 || res.Skipped != 1 {
		t.Errorf("polled/skipped = %d/%d, want 2/1 across the whole store", res.Polled, res.Skipped)
	}
}
