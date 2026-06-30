package fetch_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/fetch"
)

// fakeFetcher is a hand-written double proving the Fetcher interface is
// satisfiable. Behavior tests below exercise the real *Fetcher.
type fakeFetcher struct{}

func (*fakeFetcher) Fetch(context.Context, core.FetchRequest) (core.FetchResult, error) {
	return core.FetchResult{}, nil
}

// Compile-time conformance: both the fake and the real client satisfy Fetcher.
var (
	_ fetch.Fetcher = (*fakeFetcher)(nil)
	_ fetch.Fetcher = (*fetch.Client)(nil)
)

func TestFetchReturnsBodyAndStatus(t *testing.T) {
	const want = "<rss>ok</rss>"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
		w.Header().Set("ETag", `"abc"`)
		w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
		_, _ = w.Write([]byte(want))
	}))
	defer srv.Close()

	f, err := fetch.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	res, err := f.Fetch(context.Background(), core.FetchRequest{URL: srv.URL})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if res.Status != http.StatusOK {
		t.Errorf("status = %d, want 200", res.Status)
	}
	if got := string(res.Body); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	if res.MIMEType != "application/rss+xml" {
		t.Errorf("mime = %q, want application/rss+xml (params stripped)", res.MIMEType)
	}
	if res.ETag != `"abc"` {
		t.Errorf("etag = %q, want \"abc\"", res.ETag)
	}
	if res.LastModified != "Mon, 02 Jan 2006 15:04:05 GMT" {
		t.Errorf("last-modified = %q", res.LastModified)
	}
	if res.FinalURL != srv.URL {
		t.Errorf("final url = %q, want %q", res.FinalURL, srv.URL)
	}
}

func TestFetchSendsConfiguredUserAgent(t *testing.T) {
	const ua = "feedwatch/test"
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	f, err := fetch.New(fetch.WithUserAgent(ua))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := f.Fetch(context.Background(), core.FetchRequest{URL: srv.URL}); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got != ua {
		t.Errorf("User-Agent = %q, want %q", got, ua)
	}
}

func TestFetchOverallTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	defer close(release)

	f, err := fetch.New(fetch.WithTimeout(50 * time.Millisecond))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = f.Fetch(context.Background(), core.FetchRequest{URL: srv.URL})
	if err == nil {
		t.Fatal("Fetch: expected timeout error, got nil")
	}
	var fe *core.FeedError
	if !errors.As(err, &fe) {
		t.Fatalf("error = %T, want *core.FeedError", err)
	}
	if fe.Category != core.CatTimeout {
		t.Errorf("category = %q, want %q", fe.Category, core.CatTimeout)
	}
	if fe.FeedURL != srv.URL {
		t.Errorf("feed url = %q, want %q", fe.FeedURL, srv.URL)
	}
}

func TestFetchUnreachableHostIsNetworkError(t *testing.T) {
	f, err := fetch.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = f.Fetch(context.Background(), core.FetchRequest{URL: "http://127.0.0.1:0"})
	if err == nil {
		t.Fatal("Fetch: expected error for unreachable host")
	}
	var fe *core.FeedError
	if !errors.As(err, &fe) {
		t.Fatalf("error = %T, want *core.FeedError", err)
	}
	if fe.Category != core.CatNetwork {
		t.Errorf("category = %q, want %q", fe.Category, core.CatNetwork)
	}
}

func TestNewRejectsBadProxyURL(t *testing.T) {
	if _, err := fetch.New(fetch.WithProxy("://not a url")); err == nil {
		t.Fatal("New: expected error for malformed proxy URL")
	}
}

func TestNewRejectsMissingCABundle(t *testing.T) {
	if _, err := fetch.New(fetch.WithCABundle("/no/such/ca-bundle.pem")); err == nil {
		t.Fatal("New: expected error for missing CA bundle")
	}
}
