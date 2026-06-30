package poll

import (
	"context"
	"testing"
	"time"

	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/parse"
	"github.com/andreswebs/feedwatch/internal/testsupport"
)

func fixedTime() time.Time {
	return time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)
}

// seedFeed adds an active feed to the store with the given URL and next-due time.
func seedFeed(t *testing.T, s *testsupport.InMemoryStore, url string, nextDue time.Time) core.Feed {
	t.Helper()
	due := nextDue
	f := core.Feed{URL: url, Status: core.FeedActive, NextDueAt: &due}
	stored, err := s.AddFeed(context.Background(), f)
	if err != nil {
		t.Fatalf("AddFeed(%s): %v", url, err)
	}
	return stored
}

func TestOrchestrateTwoFeeds(t *testing.T) {
	clk := testsupport.FixedClock(fixedTime())
	store := testsupport.NewInMemoryStore(clk)

	urlA := "https://a.example/feed.xml"
	urlB := "https://b.example/feed.xml"
	feedA := seedFeed(t, store, urlA, fixedTime().Add(-time.Hour))
	feedB := seedFeed(t, store, urlB, fixedTime().Add(-time.Hour))

	fetcher := testsupport.NewFakeFetcher()
	fetcher.Register(urlA, core.FetchResult{Status: 200, Body: []byte("a"), MIMEType: "application/rss+xml"})
	fetcher.Register(urlB, core.FetchResult{Status: 200, Body: []byte("b"), MIMEType: "application/rss+xml"})

	parser := testsupport.NewFakeParser()
	parser.Register(urlA, parse.ParsedFeed{Items: []core.Item{{Title: "a1"}}})
	parser.Register(urlB, parse.ParsedFeed{Items: []core.Item{{Title: "b1"}}})

	d := Deps{Store: store, Fetcher: fetcher, Parser: parser, Clock: clk, Concurrency: 8}

	outcomes := orchestrate(context.Background(), d, []core.Feed{feedA, feedB})

	if len(outcomes) != 2 {
		t.Fatalf("got %d outcomes, want 2", len(outcomes))
	}
	if outcomes[0].feed.URL != urlA || outcomes[1].feed.URL != urlB {
		t.Fatalf("outcomes out of input order: %q, %q", outcomes[0].feed.URL, outcomes[1].feed.URL)
	}
	for _, oc := range outcomes {
		if oc.err != nil {
			t.Fatalf("feed %s: unexpected error %v", oc.feed.URL, oc.err)
		}
		if len(oc.parsed.Items) != 1 {
			t.Fatalf("feed %s: got %d items, want 1", oc.feed.URL, len(oc.parsed.Items))
		}
	}
}

func TestOrchestrateOneFailureDoesNotCancelSiblings(t *testing.T) {
	clk := testsupport.FixedClock(fixedTime())
	store := testsupport.NewInMemoryStore(clk)

	bad := "https://bad.example/feed.xml"
	good := "https://good.example/feed.xml"
	feedBad := seedFeed(t, store, bad, fixedTime().Add(-time.Hour))
	feedGood := seedFeed(t, store, good, fixedTime().Add(-time.Hour))

	fetcher := testsupport.NewFakeFetcher()
	fetcher.RegisterError(bad, core.HTTPErr(bad, 500, nil))
	fetcher.Register(good, core.FetchResult{Status: 200, Body: []byte("g"), MIMEType: "application/rss+xml"})

	parser := testsupport.NewFakeParser()
	parser.Register(good, parse.ParsedFeed{Items: []core.Item{{Title: "g1"}}})

	d := Deps{Store: store, Fetcher: fetcher, Parser: parser, Clock: clk, Concurrency: 8}

	outcomes := orchestrate(context.Background(), d, []core.Feed{feedBad, feedGood})

	if len(outcomes) != 2 {
		t.Fatalf("got %d outcomes, want 2 (failure must not drop siblings)", len(outcomes))
	}
	if outcomes[0].err == nil || outcomes[0].err.Category != core.CatHTTP {
		t.Fatalf("bad feed outcome: want http error, got %+v", outcomes[0].err)
	}
	if outcomes[1].err != nil {
		t.Fatalf("good feed outcome: unexpected error %v", outcomes[1].err)
	}
	if len(outcomes[1].parsed.Items) != 1 {
		t.Fatalf("good feed: got %d items, want 1", len(outcomes[1].parsed.Items))
	}
}

func TestOrchestrate304YieldsNoItems(t *testing.T) {
	clk := testsupport.FixedClock(fixedTime())
	store := testsupport.NewInMemoryStore(clk)

	url := "https://nm.example/feed.xml"
	feed := seedFeed(t, store, url, fixedTime().Add(-time.Hour))

	fetcher := testsupport.NewFakeFetcher()
	fetcher.Register(url, core.FetchResult{NotModified: true, Status: 304})

	parser := testsupport.NewFakeParser() // not registered: Parse must not be called

	d := Deps{Store: store, Fetcher: fetcher, Parser: parser, Clock: clk, Concurrency: 8}

	outcomes := orchestrate(context.Background(), d, []core.Feed{feed})

	if len(outcomes) != 1 {
		t.Fatalf("got %d outcomes, want 1", len(outcomes))
	}
	oc := outcomes[0]
	if oc.err != nil {
		t.Fatalf("304 outcome: unexpected error %v", oc.err)
	}
	if !oc.result.NotModified {
		t.Fatal("304 outcome: result.NotModified should be true")
	}
	if len(oc.parsed.Items) != 0 {
		t.Fatalf("304 outcome: got %d items, want 0", len(oc.parsed.Items))
	}
}
