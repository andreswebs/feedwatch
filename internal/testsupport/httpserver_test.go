package testsupport_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/fetch"
	"github.com/andreswebs/feedwatch/internal/testsupport"
)

func TestFeedServerServesRegisteredBody(t *testing.T) {
	srv := testsupport.NewFeedServer()
	defer srv.Close()
	srv.Register("/feed.xml", testsupport.Endpoint{
		Body:        "<rss>ok</rss>",
		ContentType: "application/rss+xml",
		ETag:        `"v1"`,
	})

	f, err := fetch.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	res, err := f.Fetch(context.Background(), core.FetchRequest{URL: srv.URL("/feed.xml")})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if res.Status != http.StatusOK || string(res.Body) != "<rss>ok</rss>" {
		t.Errorf("res = %+v, want 200 with canned body", res)
	}
	if srv.Hits("/feed.xml") != 1 {
		t.Errorf("hits = %d, want 1", srv.Hits("/feed.xml"))
	}
}

func TestFeedServerReturns304OnMatchingValidator(t *testing.T) {
	srv := testsupport.NewFeedServer()
	defer srv.Close()
	srv.Register("/feed.xml", testsupport.Endpoint{Body: "<rss>ok</rss>", ETag: `"v1"`})

	f, err := fetch.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	res, err := f.Fetch(context.Background(), core.FetchRequest{
		URL:  srv.URL("/feed.xml"),
		ETag: `"v1"`,
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !res.NotModified {
		t.Errorf("NotModified = false, want true on matching If-None-Match")
	}
	if got := srv.LastRequest("/feed.xml").Get("If-None-Match"); got != `"v1"` {
		t.Errorf("recorded If-None-Match = %q, want the carried validator", got)
	}
}
