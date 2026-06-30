package poll

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/fetch"
	"github.com/andreswebs/feedwatch/internal/parse"
	"github.com/andreswebs/feedwatch/internal/testsupport"
)

const minimalRSS = `<?xml version="1.0"?><rss version="2.0"><channel><title>t</title>` +
	`<item><title>i</title><link>http://example/1</link></item></channel></rss>`

func TestOrchestrateConcurrencyBounded(t *testing.T) {
	const (
		servers     = 6
		concurrency = 2
	)
	var inFlight, maxInFlight int64

	// barrier handler: each request bumps the in-flight gauge, records the
	// peak, holds briefly so peers overlap, then releases.
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		cur := atomic.AddInt64(&inFlight, 1)
		for {
			peak := atomic.LoadInt64(&maxInFlight)
			if cur <= peak || atomic.CompareAndSwapInt64(&maxInFlight, peak, cur) {
				break
			}
		}
		time.Sleep(40 * time.Millisecond)
		atomic.AddInt64(&inFlight, -1)
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(minimalRSS))
	})

	clk := testsupport.FixedClock(fixedTime())
	store := testsupport.NewInMemoryStore(clk)

	feeds := make([]core.Feed, 0, servers)
	for i := 0; i < servers; i++ {
		srv := httptest.NewServer(handler)
		t.Cleanup(srv.Close)
		feeds = append(feeds, seedFeed(t, store, srv.URL+"/feed.xml", fixedTime().Add(-time.Hour)))
	}

	fetcher, err := fetch.New(fetch.WithTimeout(5 * time.Second))
	if err != nil {
		t.Fatalf("fetch.New: %v", err)
	}
	d := Deps{Store: store, Fetcher: fetcher, Parser: parse.New(), Clock: clk, Concurrency: concurrency}

	outcomes := orchestrate(context.Background(), d, feeds)

	if len(outcomes) != servers {
		t.Fatalf("got %d outcomes, want %d", len(outcomes), servers)
	}
	for _, oc := range outcomes {
		if oc.err != nil {
			t.Fatalf("feed %s: unexpected error %v", oc.feed.URL, oc.err)
		}
	}
	peak := atomic.LoadInt64(&maxInFlight)
	if peak > concurrency {
		t.Fatalf("peak in-flight %d exceeds concurrency limit %d", peak, concurrency)
	}
	if peak != concurrency {
		t.Fatalf("peak in-flight %d, want %d (parallelism not reached)", peak, concurrency)
	}
}

func TestOrchestrateCancellationStopsScheduling(t *testing.T) {
	started := make(chan struct{}, 1)
	var firstOnce sync.Once

	// All paths share one host, so per-host grouping serializes them onto a
	// single worker. The first request blocks until its (client) context is
	// cancelled; the rest must never be scheduled.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		firstOnce.Do(func() { started <- struct{}{} })
		<-r.Context().Done()
		http.Error(w, "cancelled", http.StatusServiceUnavailable)
	})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	clk := testsupport.FixedClock(fixedTime())
	store := testsupport.NewInMemoryStore(clk)

	feeds := make([]core.Feed, 0, 3)
	for i := 0; i < 3; i++ {
		u := fmt.Sprintf("%s/feed%d.xml", srv.URL, i)
		feeds = append(feeds, seedFeed(t, store, u, fixedTime().Add(-time.Hour)))
	}

	fetcher, err := fetch.New(fetch.WithTimeout(5 * time.Second))
	if err != nil {
		t.Fatalf("fetch.New: %v", err)
	}
	d := Deps{Store: store, Fetcher: fetcher, Parser: parse.New(), Clock: clk, Concurrency: 8}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan []feedOutcome, 1)
	go func() { done <- orchestrate(ctx, d, feeds) }()

	<-started // first fetch is in flight
	cancel()  // interrupt the run

	var outcomes []feedOutcome
	select {
	case outcomes = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("orchestrate did not return after cancellation")
	}

	if len(outcomes) != 1 {
		t.Fatalf("got %d outcomes, want 1 (scheduling must stop after cancel)", len(outcomes))
	}
	if outcomes[0].err == nil {
		t.Fatal("the in-flight feed should report an error after cancellation")
	}
}
