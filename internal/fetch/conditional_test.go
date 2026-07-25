package fetch_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/fetch"
)

func TestFetchStoredETagSends304NotModified(t *testing.T) {
	const storedETag = `"v1"`
	var gotINM string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotINM = r.Header.Get("If-None-Match")
		w.WriteHeader(http.StatusNotModified)
		_, _ = w.Write([]byte("must not be read"))
	}))
	defer srv.Close()

	f, err := fetch.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	res, err := f.Fetch(context.Background(), core.FetchRequest{URL: srv.URL, ETag: storedETag})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if gotINM != storedETag {
		t.Errorf("If-None-Match = %q, want %q", gotINM, storedETag)
	}
	if !res.NotModified {
		t.Error("NotModified = false, want true on 304")
	}
	if len(res.Body) != 0 {
		t.Errorf("body = %q, want empty on 304", res.Body)
	}
}

func TestFetchStoredLastModifiedSendsIfModifiedSince(t *testing.T) {
	const stored = "Mon, 02 Jan 2006 15:04:05 GMT"
	var gotIMS string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotIMS = r.Header.Get("If-Modified-Since")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	f, err := fetch.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := f.Fetch(context.Background(), core.FetchRequest{URL: srv.URL, LastModified: stored}); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if gotIMS != stored {
		t.Errorf("If-Modified-Since = %q, want %q", gotIMS, stored)
	}
}

func TestFetch200SurfacesValidators(t *testing.T) {
	const (
		etag = `"fresh"`
		lm   = "Tue, 03 Jan 2006 15:04:05 GMT"
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("ETag", etag)
		w.Header().Set("Last-Modified", lm)
		_, _ = w.Write([]byte("<rss>ok</rss>"))
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
	if res.NotModified {
		t.Error("NotModified = true, want false on 200")
	}
	if res.ETag != etag {
		t.Errorf("ETag = %q, want %q", res.ETag, etag)
	}
	if res.LastModified != lm {
		t.Errorf("Last-Modified = %q, want %q", res.LastModified, lm)
	}
}

func TestFetchNoValidatorsSendsNoConditionalHeaders(t *testing.T) {
	var inmPresent, imsPresent bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, inmPresent = r.Header["If-None-Match"]
		_, imsPresent = r.Header["If-Modified-Since"]
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	f, err := fetch.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := f.Fetch(context.Background(), core.FetchRequest{URL: srv.URL}); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if inmPresent {
		t.Error("If-None-Match sent with no stored ETag")
	}
	if imsPresent {
		t.Error("If-Modified-Since sent with no stored Last-Modified")
	}
}
