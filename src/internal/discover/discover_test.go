package discover_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/discover"
	"github.com/andreswebs/feedwatch/internal/fetch"
	"github.com/andreswebs/feedwatch/internal/parse"
)

const atomFeed = `<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>Example Atom</title>
  <id>urn:uuid:feed</id>
  <updated>2026-06-27T10:00:00Z</updated>
  <entry><title>Item</title><id>urn:uuid:item</id></entry>
</feed>`

const rssFeed = `<?xml version="1.0"?>
<rss version="2.0"><channel><title>Example RSS</title>
<item><title>Item</title><guid>g1</guid></item></channel></rss>`

const sitemap = `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>https://example.com/a</loc></url>
  <url><loc>https://example.com/b</loc></url>
</urlset>`

// newDeps builds discover.Deps backed by the production fetcher and parser, the
// real collaborators discover depends on. httptest binds loopback, which the
// fetcher dials directly (the SSRF guard only engages on redirects), so no
// allow-private knob is needed.
func newDeps(t *testing.T) discover.Deps {
	t.Helper()
	f, err := fetch.New()
	if err != nil {
		t.Fatalf("fetch.New: %v", err)
	}
	return discover.Deps{Fetcher: f, Parser: parse.New()}
}

func htmlPage(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(body))
	}
}

func xmlBody(contentType, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write([]byte(body))
	}
}

// TestAutodiscoveryLink covers behavior 1: a page carrying a rel="alternate"
// feed link yields that feed as a candidate with source "autodiscovery".
func TestAutodiscoveryLink(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", htmlPage(`<html><head>
<link rel="alternate" type="application/atom+xml" href="/feed.atom" title="Example Atom">
</head><body>hi</body></html>`))
	mux.HandleFunc("/feed.atom", xmlBody("application/atom+xml", atomFeed))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	got, err := discover.Discover(context.Background(), newDeps(t), srv.URL)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("candidates = %d, want 1: %+v", len(got), got)
	}
	c := got[0]
	if c.Source != "autodiscovery" {
		t.Errorf("source = %q, want autodiscovery", c.Source)
	}
	if c.URL != srv.URL+"/feed.atom" {
		t.Errorf("url = %q, want %q", c.URL, srv.URL+"/feed.atom")
	}
	if c.Type != "application/atom+xml" {
		t.Errorf("type = %q, want application/atom+xml", c.Type)
	}
	if c.Title == "" {
		t.Errorf("title is empty, want a feed title")
	}
}

// TestProbeFallback covers behavior 2: a page with no autodiscovery link but a
// feed at a common path yields a candidate with source "probe".
func TestProbeFallback(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", htmlPage(`<html><head><title>No links here</title></head></html>`))
	mux.HandleFunc("/feed", xmlBody("application/rss+xml", rssFeed))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	got, err := discover.Discover(context.Background(), newDeps(t), srv.URL)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("candidates = %d, want 1: %+v", len(got), got)
	}
	c := got[0]
	if c.Source != "probe" {
		t.Errorf("source = %q, want probe", c.Source)
	}
	if c.URL != srv.URL+"/feed" {
		t.Errorf("url = %q, want %q", c.URL, srv.URL+"/feed")
	}
}

// TestSitemapNotReturned covers behavior 3: generic XML that is not a feed (a
// sitemap) is rejected and never returned as a candidate.
func TestSitemapNotReturned(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", htmlPage(`<html><head></head></html>`))
	// A probe path serving a sitemap with a generic XML content type.
	mux.HandleFunc("/feed.xml", xmlBody("application/xml", sitemap))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	got, err := discover.Discover(context.Background(), newDeps(t), srv.URL)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("candidates = %d, want 0 (sitemap must be rejected): %+v", len(got), got)
	}
}

// TestEveryCandidateParses covers behavior 4: an advertised link that does not
// parse as a feed is dropped, so every returned candidate is a real feed.
func TestEveryCandidateParses(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", htmlPage(`<html><head>
<link rel="alternate" type="application/atom+xml" href="/real.atom">
<link rel="alternate" type="application/rss+xml" href="/notafeed">
</head></html>`))
	mux.HandleFunc("/real.atom", xmlBody("application/atom+xml", atomFeed))
	mux.HandleFunc("/notafeed", htmlPage(`<html><body>not a feed</body></html>`))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	got, err := discover.Discover(context.Background(), newDeps(t), srv.URL)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("candidates = %d, want 1 (only the valid feed): %+v", len(got), got)
	}
	if got[0].URL != srv.URL+"/real.atom" {
		t.Errorf("url = %q, want %q", got[0].URL, srv.URL+"/real.atom")
	}

	// Behavior 4 (explicit): each returned candidate fetches and parses.
	deps := newDeps(t)
	for _, c := range got {
		res, ferr := deps.Fetcher.Fetch(context.Background(), core.FetchRequest{URL: c.URL})
		if ferr != nil {
			t.Fatalf("candidate %q did not fetch: %v", c.URL, ferr)
		}
		if _, perr := deps.Parser.Parse(context.Background(), res.Body, c.URL); perr != nil {
			t.Errorf("candidate %q did not parse as a feed: %v", c.URL, perr)
		}
	}
}
