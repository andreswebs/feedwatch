package feedwatch_test

import (
	"errors"
	"testing"
	"time"

	"github.com/andreswebs/feedwatch"
	"github.com/andreswebs/feedwatch/core"
	"github.com/andreswebs/feedwatch/internal/testsupport"
)

// fixedTestTime is the instant every use-case test runs at, so relative windows,
// backoff, and due calculations are reproducible.
func fixedTestTime() time.Time {
	return time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)
}

// newTestApp builds an App over an in-memory store and a fixed clock, which is
// the sanctioned seam of ADR 0007: the use cases are driven through the public
// options rather than through package internals. It returns the store so a test
// can seed it and read state back through the Store interface.
func newTestApp(t *testing.T) (*feedwatch.App, *testsupport.InMemoryStore, time.Time) {
	t.Helper()

	now := fixedTestTime()
	st := testsupport.NewInMemoryStore(testsupport.FixedClock(now))
	app, err := feedwatch.New(feedwatch.Defaults(),
		feedwatch.WithStore(st),
		feedwatch.WithClock(testsupport.FixedClock(now)),
	)
	if err != nil {
		t.Fatalf("New = %v, want nil", err)
	}
	t.Cleanup(func() { _ = app.Close() })
	return app, st, now
}

// newNetworkApp builds an App over an in-memory store with programmable network
// collaborators, so a test can make a URL fetch, fail to fetch, or fail to
// parse. It is the seam the network use cases (add, check) are driven through.
func newNetworkApp(t *testing.T) (*feedwatch.App, *testsupport.InMemoryStore, *testsupport.FakeFetcher, *testsupport.FakeParser) {
	t.Helper()

	clk := testsupport.FixedClock(fixedTestTime())
	st := testsupport.NewInMemoryStore(clk)
	fetcher := testsupport.NewFakeFetcher()
	parser := testsupport.NewFakeParser()
	app, err := feedwatch.New(feedwatch.Defaults(),
		feedwatch.WithStore(st),
		feedwatch.WithClock(clk),
		feedwatch.WithFetcher(fetcher),
		feedwatch.WithParser(parser),
	)
	if err != nil {
		t.Fatalf("New = %v, want nil", err)
	}
	t.Cleanup(func() { _ = app.Close() })
	return app, st, fetcher, parser
}

// registerFeed makes url fetch and parse as a feed titled title.
func registerFeed(f *testsupport.FakeFetcher, p *testsupport.FakeParser, url, title string) {
	f.Register(url, core.FetchResult{Status: 200, MIMEType: "application/rss+xml", Body: []byte("<rss/>")})
	p.Register(url, core.ParsedFeed{Title: title})
}

// htmlPage is a fetch result for an HTML page carrying body, the shape a
// homepage returns when an agent mistakes it for a feed.
func htmlPage(body string) core.FetchResult {
	return core.FetchResult{Status: 200, MIMEType: "text/html", Body: []byte(body)}
}

// wantUsageError fails unless err is a usage-category error carrying msg, which
// is the classification every frontend maps to its own failure encoding (exit 64
// in the CLI).
func wantUsageError(t *testing.T, err error, msg string) {
	t.Helper()

	if err == nil {
		t.Fatalf("got nil error, want usage error %q", msg)
	}
	var fe *core.FeedError
	if !errors.As(err, &fe) {
		t.Fatalf("error %v is not a *core.FeedError", err)
	}
	if fe.Category != core.CatUsage {
		t.Errorf("category = %q, want %q", fe.Category, core.CatUsage)
	}
	if fe.Message != msg {
		t.Errorf("message = %q, want %q", fe.Message, msg)
	}
}
