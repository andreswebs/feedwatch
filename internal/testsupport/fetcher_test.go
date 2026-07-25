package testsupport_test

import (
	"context"
	"errors"
	"testing"

	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/fetch"
	"github.com/andreswebs/feedwatch/internal/testsupport"
)

// Compile-time conformance: the double satisfies the consumer interface.
var _ fetch.Fetcher = (*testsupport.FakeFetcher)(nil)

func TestFakeFetcherReturnsCannedResultForRegisteredURL(t *testing.T) {
	const url = "https://blog.example/feed.xml"
	f := testsupport.NewFakeFetcher()
	f.Register(url, core.FetchResult{Status: 200, Body: []byte("<rss>ok</rss>"), MIMEType: "application/rss+xml"})

	res, err := f.Fetch(context.Background(), core.FetchRequest{URL: url})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if res.Status != 200 || string(res.Body) != "<rss>ok</rss>" {
		t.Errorf("result = %+v, want canned body and status", res)
	}
}

func TestFakeFetcherErrorsForUnregisteredURL(t *testing.T) {
	f := testsupport.NewFakeFetcher()

	_, err := f.Fetch(context.Background(), core.FetchRequest{URL: "https://unknown.example/feed"})
	if err == nil {
		t.Fatal("Fetch: expected error for unregistered URL, got nil")
	}
	var fe *core.FeedError
	if !errors.As(err, &fe) {
		t.Fatalf("error = %T, want *core.FeedError", err)
	}
	if fe.Category != core.CatNetwork {
		t.Errorf("category = %q, want %q", fe.Category, core.CatNetwork)
	}
}

func TestFakeFetcherRecordsRequest(t *testing.T) {
	const url = "https://blog.example/feed.xml"
	f := testsupport.NewFakeFetcher()
	f.Register(url, core.FetchResult{Status: 200})

	req := core.FetchRequest{URL: url, ETag: `"abc"`, LastModified: "Mon, 02 Jan 2006 15:04:05 GMT"}
	if _, err := f.Fetch(context.Background(), req); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got := f.Requests(url); len(got) != 1 || got[0].ETag != `"abc"` {
		t.Errorf("recorded requests = %+v, want one carrying the ETag", got)
	}
}
