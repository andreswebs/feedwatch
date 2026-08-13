package feedwatch_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/andreswebs/feedwatch"
	"github.com/andreswebs/feedwatch/core"
	"github.com/andreswebs/feedwatch/internal/poll"
	"github.com/andreswebs/feedwatch/internal/testsupport"
)

// pollFeed is one seeded subscription and the items its wire body carries. A
// non-nil fetchErr makes the feed fail instead, which is how a test drives the
// failure lifecycle.
type pollFeed struct {
	url      string
	items    []core.Item
	fetchErr error
}

// wireItem builds a parsed wire item with a stable dedup key.
func wireItem(key, title string) core.Item {
	return core.Item{DedupKey: key, Title: title, Link: "https://example.com/" + key}
}

// newPollApp builds an App over an in-memory store with programmable network
// collaborators and the given configuration overrides applied to Defaults, then
// seeds each feed as an active subscription whose fetch returns its items.
func newPollApp(t *testing.T, tune func(*feedwatch.Config), warn feedwatch.Warner, feeds ...pollFeed) (*feedwatch.App, *testsupport.InMemoryStore) {
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
		if _, err := st.AddFeed(context.Background(), core.Feed{URL: f.url}); err != nil {
			t.Fatalf("AddFeed(%s): %v", f.url, err)
		}
		if f.fetchErr != nil {
			fetcher.RegisterError(f.url, f.fetchErr)
			continue
		}
		fetcher.Register(f.url, core.FetchResult{Status: 200, MIMEType: "application/rss+xml", Body: []byte("<rss/>")})
		parser.Register(f.url, core.ParsedFeed{Title: "Feed", Items: f.items})
	}
	return app, st
}

func TestPollReportsConsistentCountsAndEmptyFailures(t *testing.T) {
	app, _ := newPollApp(t, nil, nil, pollFeed{
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
	app, _ := newPollApp(t, nil, nil, pollFeed{
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
	app, st := newPollApp(t,
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
