package feedwatch_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/andreswebs/feedwatch"
	"github.com/andreswebs/feedwatch/core"
	"github.com/andreswebs/feedwatch/internal/testsupport"
)

const discoverPageURL = "https://example.com"

// discoverBadURLs are the inputs DiscoverRequest.Validate must reject, keyed by
// what makes each one unusable.
var discoverBadURLs = map[string]string{
	"bare host":       "example.com",
	"non-http scheme": "ftp://example.com/feed.xml",
	"empty":           "",
}

func TestDiscoverRequestValidateRejectsNonWebURL(t *testing.T) {
	for name, raw := range discoverBadURLs {
		t.Run(name, func(t *testing.T) {
			req := feedwatch.DiscoverRequest{URL: raw}
			wantUsageError(t, req.Validate(), "discover requires an absolute http(s) URL")
		})
	}
}

// newDiscoverApp builds an App whose store path points into a fresh temporary
// directory, so a test can prove discovery left no store behind. It returns the
// App, the programmable fetcher and parser, and that directory.
func newDiscoverApp(t *testing.T) (*feedwatch.App, *testsupport.FakeFetcher, *testsupport.FakeParser, string) {
	t.Helper()

	dir := t.TempDir()
	cfg := feedwatch.Defaults()
	cfg.Store = filepath.Join(dir, "feedwatch.db")

	fetcher := testsupport.NewFakeFetcher()
	parser := testsupport.NewFakeParser()
	app, err := feedwatch.New(cfg, feedwatch.WithFetcher(fetcher), feedwatch.WithParser(parser))
	if err != nil {
		t.Fatalf("New = %v, want nil", err)
	}
	t.Cleanup(func() { _ = app.Close() })
	return app, fetcher, parser, dir
}

func TestDiscoverWithoutCandidatesReturnsEmptySliceAndOpensNoStore(t *testing.T) {
	app, fetcher, _, dir := newDiscoverApp(t)
	fetcher.Register(discoverPageURL, htmlPage(`<html><head></head><body>no feeds here</body></html>`))

	res, err := app.Discover(context.Background(), feedwatch.DiscoverRequest{URL: discoverPageURL})
	if err != nil {
		t.Fatalf("Discover = %v, want nil", err)
	}
	if res.Candidates == nil {
		t.Errorf("candidates is nil, want an empty slice")
	}
	if len(res.Candidates) != 0 {
		t.Errorf("candidates = %d, want 0: %+v", len(res.Candidates), res.Candidates)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("discover created %d file(s) in the store directory, want 0: %v", len(entries), entries)
	}
}

func TestDiscoverReportsAutodiscoveredCandidate(t *testing.T) {
	const feedURL = "https://example.com/feed.xml"

	app, fetcher, parser, _ := newDiscoverApp(t)
	fetcher.Register(discoverPageURL, htmlPage(
		`<html><head><link rel="alternate" type="application/rss+xml" href="/feed.xml"></head></html>`))
	fetcher.Register(feedURL, core.FetchResult{Status: 200, MIMEType: "application/rss+xml", Body: []byte("<rss/>")})
	parser.Register(feedURL, core.ParsedFeed{Title: "Example Blog"})

	res, err := app.Discover(context.Background(), feedwatch.DiscoverRequest{URL: discoverPageURL})
	if err != nil {
		t.Fatalf("Discover = %v, want nil", err)
	}
	if len(res.Candidates) != 1 {
		t.Fatalf("candidates = %d, want 1: %+v", len(res.Candidates), res.Candidates)
	}
	got := res.Candidates[0]
	if got.URL != feedURL {
		t.Errorf("url = %q, want %q", got.URL, feedURL)
	}
	if got.Source != core.SourceAutodiscovery {
		t.Errorf("source = %q, want %q", got.Source, core.SourceAutodiscovery)
	}
	if got.Title != "Example Blog" {
		t.Errorf("title = %q, want %q", got.Title, "Example Blog")
	}
}

func TestDiscoverRejectsBadURLBeforeFetching(t *testing.T) {
	app, fetcher, _, _ := newDiscoverApp(t)

	_, err := app.Discover(context.Background(), feedwatch.DiscoverRequest{URL: "example.com"})
	wantUsageError(t, err, "discover requires an absolute http(s) URL")
	if n := len(fetcher.Requests("example.com")); n != 0 {
		t.Errorf("fetcher saw %d request(s), want 0", n)
	}
}
