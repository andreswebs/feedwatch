package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	cliv3 "github.com/urfave/cli/v3"

	"github.com/andreswebs/feedwatch/internal/config"
	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/parse"
	"github.com/andreswebs/feedwatch/internal/store"
	"github.com/andreswebs/feedwatch/internal/testsupport"
)

// runAdd drives the add command through the root with injected doubles for the
// store, fetcher, parser, and clock, capturing stdout, stderr, and the exit
// code the boundary selected. It mirrors runPoll.
func runAdd(t *testing.T, st store.Store, f *testsupport.FakeFetcher, p *testsupport.FakeParser, clk core.Clock, args ...string) runResult {
	t.Helper()

	outF, errF := tempFile(t), tempFile(t)
	d := Deps{
		Cfg:     config.Defaults(),
		Clock:   clk,
		Version: "1.2.3",
		Store:   st,
		Fetch:   f,
		Parse:   p,
		Out:     outF,
		Err:     errF,
	}

	var res runResult
	oldExiter := cliv3.OsExiter
	cliv3.OsExiter = func(code int) {
		res.code = code
		res.exited = true
	}
	t.Cleanup(func() { cliv3.OsExiter = oldExiter })

	_ = NewRootCommand(d).Run(t.Context(), append([]string{"feedwatch", "add"}, args...))

	res.out = readFile(t, outF)
	res.err = readFile(t, errF)
	return res
}

// addEnvelope mirrors the stdout AddResult shape for assertions.
type addEnvelope struct {
	URL      string `json:"url"`
	Alias    string `json:"alias"`
	Interval string `json:"interval"`
	Created  bool   `json:"created"`
}

// TestAddValidFeedStoresAndReportsCreated covers behavior 1: adding a valid feed
// URL that fetches and parses stores the subscription and reports created:true,
// exit 0.
func TestAddValidFeedStoresAndReportsCreated(t *testing.T) {
	st, fetcher, parser, clk := newPollDoubles(t)
	feedURL := "https://blog.example/feed.xml"
	fetcher.Register(feedURL, okResult())
	parser.Register(feedURL, parse.ParsedFeed{})

	res := runAdd(t, st, fetcher, parser, clk, feedURL)

	if res.exited {
		t.Errorf("valid add should exit 0 without invoking OsExiter, got code %d", res.code)
	}
	if res.err != "" {
		t.Errorf("stderr = %q, want empty for a valid add", res.err)
	}

	var env addEnvelope
	if err := json.Unmarshal([]byte(res.out), &env); err != nil {
		t.Fatalf("stdout is not an add envelope: %v\ngot: %q", err, res.out)
	}
	if env.URL != feedURL {
		t.Errorf("url = %q, want %q", env.URL, feedURL)
	}
	if !env.Created {
		t.Errorf("created = false, want true for a new subscription")
	}

	if _, err := st.GetFeed(context.Background(), feedURL); err != nil {
		t.Errorf("feed was not stored: %v", err)
	}
}

// TestAddHTMLPageIsRejectedPointingToDiscover covers behavior 2: a URL that
// fetches but does not parse as a feed (e.g. an HTML page) is rejected as a
// usage error, exit 1, with a message pointing to discover, and is not stored.
func TestAddHTMLPageIsRejectedPointingToDiscover(t *testing.T) {
	st, fetcher, parser, clk := newPollDoubles(t)
	pageURL := "https://example.com"
	fetcher.Register(pageURL, core.FetchResult{Status: 200, Body: []byte("<html></html>"), MIMEType: "text/html"})
	// parser is intentionally not registered for pageURL, so it returns a parse error.

	res := runAdd(t, st, fetcher, parser, clk, pageURL)

	if !res.exited || res.code != 1 {
		t.Fatalf("rejecting a non-feed should exit 1, got exited=%v code=%d", res.exited, res.code)
	}
	if res.out != "" {
		t.Errorf("stdout = %q, want empty on a rejected add", res.out)
	}

	var env errEnvelope
	if err := json.Unmarshal([]byte(res.err), &env); err != nil {
		t.Fatalf("stderr is not an error envelope: %v\ngot: %q", err, res.err)
	}
	if env.Error.Category != string(core.CatUsage) {
		t.Errorf("category = %q, want %q", env.Error.Category, core.CatUsage)
	}
	if !strings.Contains(env.Error.Message, "discover") {
		t.Errorf("message = %q, want it to point to discover", env.Error.Message)
	}

	if _, err := st.GetFeed(context.Background(), pageURL); err == nil {
		t.Errorf("rejected feed should not be stored")
	}
}

// TestAddNonHTTPSchemeIsUsageError covers behavior 3: a non-http(s) scheme or a
// bare host (no scheme) is rejected as a usage error, exit 1, before any fetch.
func TestAddNonHTTPSchemeIsUsageError(t *testing.T) {
	cases := []struct {
		name string
		arg  string
	}{
		{"bare host", "example.com"},
		{"non-http scheme", "ftp://example.com/feed.xml"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, fetcher, parser, clk := newPollDoubles(t)

			res := runAdd(t, st, fetcher, parser, clk, tc.arg)

			if !res.exited || res.code != 1 {
				t.Fatalf("bad URL should exit 1, got exited=%v code=%d", res.exited, res.code)
			}
			var env errEnvelope
			if err := json.Unmarshal([]byte(res.err), &env); err != nil {
				t.Fatalf("stderr is not an error envelope: %v\ngot: %q", err, res.err)
			}
			if env.Error.Category != string(core.CatUsage) {
				t.Errorf("category = %q, want %q", env.Error.Category, core.CatUsage)
			}
			if len(fetcher.Requests(tc.arg)) != 0 {
				t.Errorf("a bad URL should be rejected before any fetch")
			}
		})
	}
}

// TestAddExistingURLUpdatesIdempotently covers behavior 4: re-adding an existing
// URL with a new alias updates the subscription and reports created:false,
// exit 0 (idempotent upsert).
func TestAddExistingURLUpdatesIdempotently(t *testing.T) {
	st, fetcher, parser, clk := newPollDoubles(t)
	feedURL := "https://blog.example/feed.xml"
	fetcher.Register(feedURL, okResult())
	parser.Register(feedURL, parse.ParsedFeed{})

	if _, err := st.AddFeed(context.Background(), core.Feed{URL: feedURL, Status: core.FeedActive}); err != nil {
		t.Fatalf("seed AddFeed: %v", err)
	}

	res := runAdd(t, st, fetcher, parser, clk, "--alias", "blog", feedURL)

	if res.exited {
		t.Errorf("idempotent re-add should exit 0, got code %d", res.code)
	}

	var env addEnvelope
	if err := json.Unmarshal([]byte(res.out), &env); err != nil {
		t.Fatalf("stdout is not an add envelope: %v\ngot: %q", err, res.out)
	}
	if env.Created {
		t.Errorf("created = true, want false for an existing subscription")
	}
	if env.Alias != "blog" {
		t.Errorf("alias = %q, want %q", env.Alias, "blog")
	}

	got, err := st.GetFeed(context.Background(), feedURL)
	if err != nil {
		t.Fatalf("GetFeed: %v", err)
	}
	if got.Alias != "blog" {
		t.Errorf("stored alias = %q, want %q", got.Alias, "blog")
	}
}
