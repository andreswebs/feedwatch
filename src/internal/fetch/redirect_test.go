package fetch_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/fetch"
)

func TestFetchDirectLoopbackURLIsAllowed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<rss>ok</rss>"))
	}))
	defer srv.Close()

	f, err := fetch.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	res, err := f.Fetch(context.Background(), core.FetchRequest{URL: srv.URL})
	if err != nil {
		t.Fatalf("direct loopback fetch blocked: %v", err)
	}
	if res.Status != http.StatusOK {
		t.Errorf("status = %d, want 200", res.Status)
	}
}

func TestFetchPermanentRedirectSetsFinalURLAndPermanent(t *testing.T) {
	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<rss>moved</rss>"))
	}))
	defer final.Close()

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, final.URL, http.StatusMovedPermanently)
	}))
	defer origin.Close()

	f, err := fetch.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	res, err := f.Fetch(context.Background(), core.FetchRequest{URL: origin.URL})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if res.FinalURL != final.URL+"/" && res.FinalURL != final.URL {
		t.Errorf("FinalURL = %q, want %q", res.FinalURL, final.URL)
	}
	if !res.Permanent {
		t.Error("Permanent = false, want true after 301 redirect")
	}
}

func TestFetchTemporaryRedirectIsNotPermanent(t *testing.T) {
	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<rss>ok</rss>"))
	}))
	defer final.Close()

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, final.URL, http.StatusFound)
	}))
	defer origin.Close()

	f, err := fetch.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	res, err := f.Fetch(context.Background(), core.FetchRequest{URL: origin.URL})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if res.Permanent {
		t.Error("Permanent = true, want false after 302 redirect")
	}
}
