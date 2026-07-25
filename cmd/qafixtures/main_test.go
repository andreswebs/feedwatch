package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServeFeedSetsValidatorsAndContentType(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/feeds/rss20.xml", nil)
	handleFeeds(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/rss+xml" {
		t.Errorf("Content-Type = %q, want application/rss+xml", got)
	}
	if rec.Header().Get("ETag") == "" {
		t.Error("ETag header is empty")
	}
	if rec.Header().Get("Last-Modified") == "" {
		t.Error("Last-Modified header is empty")
	}
	if rec.Body.Len() == 0 {
		t.Error("body is empty")
	}
}

func TestServeFeedContentTypeOverride(t *testing.T) {
	rec := httptest.NewRecorder()
	// A semicolon in the query must be percent-encoded (%3B); a raw ';' is
	// dropped by Go's query parser.
	req := httptest.NewRequest(http.MethodGet, "/feeds/utf16le-bom.xml?ct=application/xml%3Bcharset=utf-8", nil)
	handleFeeds(rec, req)

	if got := rec.Header().Get("Content-Type"); got != "application/xml;charset=utf-8" {
		t.Errorf("Content-Type = %q, want the ?ct override", got)
	}
}

func TestServeFeedUnknownIs404(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/feeds/does-not-exist.xml", nil)
	handleFeeds(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestConditionalGETNotModified(t *testing.T) {
	// First fetch to learn the ETag.
	first := httptest.NewRecorder()
	handleFeeds(first, httptest.NewRequest(http.MethodGet, "/feeds/rss20.xml", nil))
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag on first response")
	}

	t.Run("matching If-None-Match yields 304", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/feeds/rss20.xml", nil)
		req.Header.Set("If-None-Match", etag)
		handleFeeds(rec, req)
		if rec.Code != http.StatusNotModified {
			t.Fatalf("status = %d, want 304", rec.Code)
		}
		if rec.Body.Len() != 0 {
			t.Error("304 response should have no body")
		}
	})

	t.Run("stale If-None-Match yields 200", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/feeds/rss20.xml", nil)
		req.Header.Set("If-None-Match", `"fixture-deadbeef"`)
		handleFeeds(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	})

	t.Run("future If-Modified-Since yields 304", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/feeds/rss20.xml", nil)
		req.Header.Set("If-Modified-Since", "Fri, 01 Jan 2027 00:00:00 GMT")
		handleFeeds(rec, req)
		if rec.Code != http.StatusNotModified {
			t.Fatalf("status = %d, want 304", rec.Code)
		}
	})

	t.Run("past If-Modified-Since yields 200", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/feeds/rss20.xml", nil)
		req.Header.Set("If-Modified-Since", "Wed, 01 Jan 2025 00:00:00 GMT")
		handleFeeds(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	})
}

func TestHandleStatus(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		wantStatus int
		wantRetry  string
	}{
		{"not found", "/status/404", http.StatusNotFound, ""},
		{"server error", "/status/500", http.StatusInternalServerError, ""},
		{"too many requests default retry-after", "/status/429", http.StatusTooManyRequests, "1"},
		{"too many requests custom retry-after", "/status/429?retry-after=5", http.StatusTooManyRequests, "5"},
		{"non-numeric is 400", "/status/nope", http.StatusBadRequest, ""},
		{"out of range is 400", "/status/999", http.StatusBadRequest, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handleStatus(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if got := rec.Header().Get("Retry-After"); got != tc.wantRetry {
				t.Errorf("Retry-After = %q, want %q", got, tc.wantRetry)
			}
		})
	}
}

func TestHandleFlaky(t *testing.T) {
	// Unique name keeps this test independent of the shared flaky counter.
	const path = "/flaky/rss20.xml?n=2"
	want := []int{
		http.StatusServiceUnavailable,
		http.StatusServiceUnavailable,
		http.StatusOK,
		http.StatusOK,
	}
	for i, code := range want {
		rec := httptest.NewRecorder()
		handleFlaky(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != code {
			t.Fatalf("request %d: status = %d, want %d", i+1, rec.Code, code)
		}
		if code == http.StatusServiceUnavailable && rec.Header().Get("Retry-After") != "1" {
			t.Errorf("request %d: missing Retry-After on 503", i+1)
		}
	}
}

func TestHandleRedirect(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		wantStatus int
		wantLoc    string
	}{
		{"default 302", "/redirect?to=http://127.0.0.1:9/feeds/atom.xml", http.StatusFound, "http://127.0.0.1:9/feeds/atom.xml"},
		{"permanent 308", "/redirect?to=http://127.0.0.1:9/x&code=308", http.StatusPermanentRedirect, "http://127.0.0.1:9/x"},
		{"missing target is 400", "/redirect", http.StatusBadRequest, ""},
		{"non-3xx code is 400", "/redirect?to=http://x/&code=200", http.StatusBadRequest, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handleRedirect(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if got := rec.Header().Get("Location"); got != tc.wantLoc {
				t.Errorf("Location = %q, want %q", got, tc.wantLoc)
			}
		})
	}
}

func TestHandleSlowServesAfterDelay(t *testing.T) {
	rec := httptest.NewRecorder()
	handleSlow(rec, httptest.NewRequest(http.MethodGet, "/slow/rss20.xml?ms=0", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.Len() == 0 {
		t.Error("body is empty")
	}
}

func TestHandleSlowHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "/slow/rss20.xml?ms=60000", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	handleSlow(rec, req)
	if rec.Body.Len() != 0 {
		t.Error("cancelled request should serve no body")
	}
}

func TestProbeRoutes(t *testing.T) {
	mux := newMux()

	t.Run("feed.xml serves a parseable feed", func(t *testing.T) {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/feed.xml", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if got := rec.Header().Get("Content-Type"); got != "application/rss+xml" {
			t.Errorf("Content-Type = %q, want application/rss+xml", got)
		}
		if rec.Body.Len() == 0 {
			t.Error("body is empty")
		}
	})

	t.Run("index.xml serves a non-feed for the validator to drop", func(t *testing.T) {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/index.xml", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if got := rec.Header().Get("Content-Type"); got != "application/xml" {
			t.Errorf("Content-Type = %q, want application/xml", got)
		}
	})

	t.Run("unregistered probe path still 404s", func(t *testing.T) {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/atom.xml", nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})
}

func TestContentType(t *testing.T) {
	tests := map[string]string{
		"atom.xml":           "application/atom+xml",
		"jsonfeed.json":      "application/feed+json",
		"autodiscovery.html": "text/html; charset=utf-8",
		"subs.opml":          "text/x-opml",
		"rss20.xml":          "application/rss+xml",
		"sitemap.xml":        "application/xml",
	}
	for name, want := range tests {
		if got := contentType(name); got != want {
			t.Errorf("contentType(%q) = %q, want %q", name, got, want)
		}
	}
}
