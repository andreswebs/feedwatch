package fetch_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/fetch"
)

// fastBackoff keeps retry tests quick: the in-call backoff between attempts is
// negligible while the attempt-counting behavior is what the tests assert.
const fastBackoff = time.Millisecond

func TestRetrySucceedsAfterTransient5xx(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("<rss>ok</rss>"))
	}))
	defer srv.Close()

	f, err := fetch.New(fetch.WithRetry(3, fastBackoff))
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
	if got := calls.Load(); got != 3 {
		t.Errorf("requests = %d, want 3 (two 503s then a 200)", got)
	}
}

func TestRetryNotAppliedToDeterministic4xx(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	f, err := fetch.New(fetch.WithRetry(3, fastBackoff))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = f.Fetch(context.Background(), core.FetchRequest{URL: srv.URL})
	if err == nil {
		t.Fatal("Fetch: expected an error for 404")
	}
	var fe *core.FeedError
	if !errors.As(err, &fe) {
		t.Fatalf("error = %T, want *core.FeedError", err)
	}
	if fe.Category != core.CatHTTP || fe.Status != http.StatusNotFound {
		t.Errorf("error = %v, want http category status 404", fe)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("requests = %d, want 1 (404 is deterministic, never retried)", got)
	}
}

func TestRetryBoundedByAttempts(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	f, err := fetch.New(fetch.WithRetry(3, fastBackoff))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = f.Fetch(context.Background(), core.FetchRequest{URL: srv.URL})
	if err == nil {
		t.Fatal("Fetch: expected an error after exhausting retries")
	}
	var fe *core.FeedError
	if !errors.As(err, &fe) {
		t.Fatalf("error = %T, want *core.FeedError", err)
	}
	if fe.Category != core.CatHTTP || fe.Status != http.StatusServiceUnavailable {
		t.Errorf("error = %v, want http category status 503 (the last error)", fe)
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("requests = %d, want 3 (bounded by RetryAttempts)", got)
	}
}

func TestRetryDisabledByDefault(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	f, err := fetch.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := f.Fetch(context.Background(), core.FetchRequest{URL: srv.URL}); err == nil {
		t.Fatal("Fetch: expected an error for 503")
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("requests = %d, want 1 (retry is opt-in via WithRetry)", got)
	}
}
