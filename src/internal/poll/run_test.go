package poll

import (
	"context"
	"testing"
	"time"

	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/parse"
	"github.com/andreswebs/feedwatch/internal/testsupport"
)

func TestResultExitCode(t *testing.T) {
	cases := []struct {
		name   string
		polled int
		failed int
		want   int
	}{
		{"no feeds polled", 0, 0, 0},
		{"all succeeded", 3, 0, 0},
		{"all failed", 2, 2, 2},
		{"some failed", 3, 1, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := Result{Polled: tc.polled, Failed: tc.failed}
			if got := r.ExitCode(); got != tc.want {
				t.Errorf("ExitCode(polled=%d, failed=%d) = %d, want %d", tc.polled, tc.failed, got, tc.want)
			}
		})
	}
}

// runDeps wires Run's collaborators against the in-memory store double and the
// programmable fetch/parse fakes, the seam Run is meant to be tested through.
func runDeps(s *testsupport.InMemoryStore, f *testsupport.FakeFetcher, p *testsupport.FakeParser, clk core.Clock) Deps {
	return Deps{
		Store:            s,
		Fetcher:          f,
		Parser:           p,
		Clock:            clk,
		Concurrency:      8,
		DefaultInterval:  time.Hour,
		FailureThreshold: 10,
		MaxBackoff:       24 * time.Hour,
	}
}

// okResult is a successful 200 fetch outcome for url.
func okResult(url string) core.FetchResult {
	return core.FetchResult{Status: 200, FinalURL: url, Body: []byte("body"), MIMEType: "application/rss+xml"}
}

func titlesOf(items []core.Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Title
	}
	return out
}

// Run wiring: due feeds are selected, fetched, parsed, persisted, and the new
// items come back in feed-selection order (each feed's items in parse order).
func TestRunDueFeedsReturnNewItemsInSelectionOrder(t *testing.T) {
	clk := testsupport.FixedClock(fixedTime())
	s := testsupport.NewInMemoryStore(clk)

	urlA := "https://a.example/feed.xml"
	urlB := "https://b.example/feed.xml"
	seedFeed(t, s, urlA, fixedTime().Add(-time.Hour))
	seedFeed(t, s, urlB, fixedTime().Add(-time.Hour))

	f := testsupport.NewFakeFetcher()
	f.Register(urlA, okResult(urlA))
	f.Register(urlB, okResult(urlB))

	p := testsupport.NewFakeParser()
	p.Register(urlA, parse.ParsedFeed{Items: []core.Item{{GUID: "a1", Title: "a1"}, {GUID: "a2", Title: "a2"}}})
	p.Register(urlB, parse.ParsedFeed{Items: []core.Item{{GUID: "b1", Title: "b1"}}})

	result, feedErrs, err := Run(context.Background(), runDeps(s, f, p, clk), nil, false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(feedErrs) != 0 {
		t.Fatalf("unexpected feed errors: %v", feedErrs)
	}
	if result.Polled != 2 || result.Skipped != 0 || result.NewItems != 3 || result.Failed != 0 {
		t.Fatalf("result = %+v, want polled=2 skipped=0 new=3 failed=0", result)
	}
	if result.ExitCode() != 0 {
		t.Fatalf("ExitCode = %d, want 0", result.ExitCode())
	}
	if got := titlesOf(result.Items); len(got) != 3 || got[0] != "a1" || got[1] != "a2" || got[2] != "b1" {
		t.Fatalf("items out of selection order: %v, want [a1 a2 b1]", got)
	}
}

// skippedCount: on the unnamed, unforced due path, active feeds not yet due are
// counted as skipped and never fetched.
func TestRunSkipsFeedsNotYetDue(t *testing.T) {
	clk := testsupport.FixedClock(fixedTime())
	s := testsupport.NewInMemoryStore(clk)

	due := "https://due.example/feed.xml"
	later := "https://later.example/feed.xml"
	seedFeed(t, s, due, fixedTime().Add(-time.Hour))
	seedFeed(t, s, later, fixedTime().Add(time.Hour)) // scheduled in the future

	f := testsupport.NewFakeFetcher()
	f.Register(due, okResult(due))
	p := testsupport.NewFakeParser()
	p.Register(due, parse.ParsedFeed{Items: []core.Item{{GUID: "d1", Title: "d1"}}})

	result, _, err := Run(context.Background(), runDeps(s, f, p, clk), nil, false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Polled != 1 || result.Skipped != 1 {
		t.Fatalf("result = %+v, want polled=1 skipped=1", result)
	}
	if len(f.Requests(later)) != 0 {
		t.Fatalf("not-due feed was fetched: %d requests", len(f.Requests(later)))
	}
}

// skippedCount short-circuit: --force polls every active feed regardless of
// schedule and reports nothing skipped.
func TestRunForceIgnoresScheduleAndSkipsNothing(t *testing.T) {
	clk := testsupport.FixedClock(fixedTime())
	s := testsupport.NewInMemoryStore(clk)

	url := "https://later.example/feed.xml"
	seedFeed(t, s, url, fixedTime().Add(time.Hour)) // not due

	f := testsupport.NewFakeFetcher()
	f.Register(url, okResult(url))
	p := testsupport.NewFakeParser()
	p.Register(url, parse.ParsedFeed{Items: []core.Item{{GUID: "x1", Title: "x1"}}})

	result, _, err := Run(context.Background(), runDeps(s, f, p, clk), nil, true)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Polled != 1 || result.Skipped != 0 {
		t.Fatalf("result = %+v, want polled=1 skipped=0", result)
	}
}

// skippedCount short-circuit: a named feed is polled regardless of schedule and
// reports nothing skipped.
func TestRunNamedFeedPolledRegardlessOfScheduleAndSkipsNothing(t *testing.T) {
	clk := testsupport.FixedClock(fixedTime())
	s := testsupport.NewInMemoryStore(clk)

	url := "https://later.example/feed.xml"
	seedFeed(t, s, url, fixedTime().Add(time.Hour)) // not due

	f := testsupport.NewFakeFetcher()
	f.Register(url, okResult(url))
	p := testsupport.NewFakeParser()
	p.Register(url, parse.ParsedFeed{Items: []core.Item{{GUID: "n1", Title: "n1"}}})

	result, _, err := Run(context.Background(), runDeps(s, f, p, clk), []string{url}, false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Polled != 1 || result.Skipped != 0 {
		t.Fatalf("result = %+v, want polled=1 skipped=0", result)
	}
}

// An unknown named ref is a hard, whole-invocation failure: Run returns the
// error and an empty result, never a per-feed outcome.
func TestRunUnknownNamedFeedIsHardError(t *testing.T) {
	clk := testsupport.FixedClock(fixedTime())
	s := testsupport.NewInMemoryStore(clk)
	d := runDeps(s, testsupport.NewFakeFetcher(), testsupport.NewFakeParser(), clk)

	result, feedErrs, err := Run(context.Background(), d, []string{"https://nope.example/feed.xml"}, false)
	if err == nil {
		t.Fatal("Run: want hard error for unknown ref, got nil")
	}
	if feedErrs != nil {
		t.Fatalf("want nil per-feed errors on select failure, got %v", feedErrs)
	}
	if result.Polled != 0 || result.Items != nil {
		t.Fatalf("want zero result on select failure, got %+v", result)
	}
}

// consume wiring + exit code: a run where some feeds succeed and some fail
// surfaces the per-feed error, counts the failure, and maps to exit 3.
func TestRunPartialFailureExitsThree(t *testing.T) {
	clk := testsupport.FixedClock(fixedTime())
	s := testsupport.NewInMemoryStore(clk)

	good := "https://good.example/feed.xml"
	bad := "https://bad.example/feed.xml"
	seedFeed(t, s, good, fixedTime().Add(-time.Hour))
	seedFeed(t, s, bad, fixedTime().Add(-time.Hour))

	f := testsupport.NewFakeFetcher()
	f.Register(good, okResult(good))
	f.RegisterError(bad, core.HTTPErr(bad, 500, nil))
	p := testsupport.NewFakeParser()
	p.Register(good, parse.ParsedFeed{Items: []core.Item{{GUID: "g1", Title: "g1"}}})

	result, feedErrs, err := Run(context.Background(), runDeps(s, f, p, clk), nil, false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Polled != 2 || result.Failed != 1 || result.NewItems != 1 {
		t.Fatalf("result = %+v, want polled=2 failed=1 new=1", result)
	}
	if result.ExitCode() != 3 {
		t.Fatalf("ExitCode = %d, want 3", result.ExitCode())
	}
	if len(feedErrs) != 1 || feedErrs[0].Category != core.CatHTTP {
		t.Fatalf("feed errors = %v, want one http error", feedErrs)
	}
}

// consume wiring + exit code: a run where every polled feed fails maps to exit 2.
func TestRunAllFailedExitsTwo(t *testing.T) {
	clk := testsupport.FixedClock(fixedTime())
	s := testsupport.NewInMemoryStore(clk)

	url := "https://bad.example/feed.xml"
	seedFeed(t, s, url, fixedTime().Add(-time.Hour))

	f := testsupport.NewFakeFetcher()
	f.RegisterError(url, core.HTTPErr(url, 500, nil))

	result, feedErrs, err := Run(context.Background(), runDeps(s, f, testsupport.NewFakeParser(), clk), nil, false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Polled != 1 || result.Failed != 1 {
		t.Fatalf("result = %+v, want polled=1 failed=1", result)
	}
	if result.ExitCode() != 2 {
		t.Fatalf("ExitCode = %d, want 2", result.ExitCode())
	}
	if len(feedErrs) != 1 {
		t.Fatalf("feed errors = %v, want one", feedErrs)
	}
}

// effectiveInterval through Run: a successful poll reschedules the feed by its
// own interval, else a parsed <ttl>, else the configured default.
func TestRunSchedulesNextDueByEffectiveInterval(t *testing.T) {
	cases := []struct {
		name         string
		feedInterval time.Duration
		ttl          time.Duration
		want         time.Duration // expected next-due offset from now
	}{
		{"feed interval wins over ttl", 30 * time.Minute, 15 * time.Minute, 30 * time.Minute},
		{"ttl when feed has no interval", 0, 15 * time.Minute, 15 * time.Minute},
		{"default when neither is set", 0, 0, time.Hour}, // runDeps DefaultInterval
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			clk := testsupport.FixedClock(fixedTime())
			s := testsupport.NewInMemoryStore(clk)

			url := "https://x.example/feed.xml"
			due := fixedTime().Add(-time.Hour)
			if _, err := s.AddFeed(ctx, core.Feed{URL: url, Status: core.FeedActive, NextDueAt: &due, Interval: tc.feedInterval}); err != nil {
				t.Fatalf("AddFeed: %v", err)
			}

			f := testsupport.NewFakeFetcher()
			f.Register(url, okResult(url))
			p := testsupport.NewFakeParser()
			p.Register(url, parse.ParsedFeed{TTL: tc.ttl, Items: []core.Item{{GUID: "i1", Title: "i1"}}})

			if _, _, err := Run(ctx, runDeps(s, f, p, clk), nil, false); err != nil {
				t.Fatalf("Run: %v", err)
			}

			stored, err := s.GetFeed(ctx, url)
			if err != nil {
				t.Fatalf("GetFeed: %v", err)
			}
			wantDue := fixedTime().Add(tc.want)
			if stored.NextDueAt == nil || !stored.NextDueAt.Equal(wantDue) {
				t.Fatalf("NextDueAt = %v, want %v", stored.NextDueAt, wantDue)
			}
		})
	}
}

// Run end to end across two polls: the second (forced, since the first
// rescheduled the feed past now) surfaces nothing new, proving dedup is wired
// through Run.
func TestRunForceRepollSurfacesNothingNew(t *testing.T) {
	ctx := context.Background()
	clk := testsupport.FixedClock(fixedTime())
	s := testsupport.NewInMemoryStore(clk)

	url := "https://blog.example/feed.xml"
	seedFeed(t, s, url, fixedTime().Add(-time.Hour))

	f := testsupport.NewFakeFetcher()
	f.Register(url, okResult(url))
	p := testsupport.NewFakeParser()
	p.Register(url, parse.ParsedFeed{Items: []core.Item{{GUID: "g1", Title: "g1"}, {GUID: "g2", Title: "g2"}}})
	d := runDeps(s, f, p, clk)

	first, _, err := Run(ctx, d, nil, false)
	if err != nil {
		t.Fatalf("Run (first): %v", err)
	}
	if first.NewItems != 2 {
		t.Fatalf("first poll NewItems = %d, want 2", first.NewItems)
	}

	second, _, err := Run(ctx, d, nil, true)
	if err != nil {
		t.Fatalf("Run (second): %v", err)
	}
	if second.Polled != 1 || second.NewItems != 0 {
		t.Fatalf("second poll = %+v, want polled=1 new=0 (dedup)", second)
	}
}

// Run surfaces a permanent-redirect rename in the result: a 301/308 to a fresh
// URL yields a {from,to} entry; a redirect whose target is already subscribed is
// declined and yields none; and a poll with no rename yields an empty slice.
func TestRunReportsPermanentRedirectRename(t *testing.T) {
	const oldURL = "https://aihero.dev/rss.xml"
	const newURL = "https://www.aihero.dev/rss.xml"

	t.Run("fresh target reports rename", func(t *testing.T) {
		clk := testsupport.FixedClock(fixedTime())
		s := testsupport.NewInMemoryStore(clk)
		seedFeed(t, s, oldURL, fixedTime().Add(-time.Hour))

		f := testsupport.NewFakeFetcher()
		f.Register(oldURL, core.FetchResult{Status: 200, FinalURL: newURL, Permanent: true,
			Body: []byte("body"), MIMEType: "application/rss+xml"})
		p := testsupport.NewFakeParser()
		p.Register(oldURL, parse.ParsedFeed{Items: []core.Item{{GUID: "g1", Title: "t1"}}})

		result, _, err := Run(context.Background(), runDeps(s, f, p, clk), nil, false)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if len(result.Renamed) != 1 {
			t.Fatalf("Renamed = %+v, want one entry", result.Renamed)
		}
		if result.Renamed[0] != (core.FeedRename{From: oldURL, To: newURL}) {
			t.Errorf("Renamed[0] = %+v, want {from:%q to:%q}", result.Renamed[0], oldURL, newURL)
		}
	})

	t.Run("declined when target already subscribed", func(t *testing.T) {
		clk := testsupport.FixedClock(fixedTime())
		s := testsupport.NewInMemoryStore(clk)
		seedFeed(t, s, oldURL, fixedTime().Add(-time.Hour))
		seedFeed(t, s, newURL, fixedTime().Add(time.Hour)) // already subscribed, not due

		f := testsupport.NewFakeFetcher()
		f.Register(oldURL, core.FetchResult{Status: 200, FinalURL: newURL, Permanent: true,
			Body: []byte("body"), MIMEType: "application/rss+xml"})
		p := testsupport.NewFakeParser()
		p.Register(oldURL, parse.ParsedFeed{Items: []core.Item{{GUID: "g1", Title: "t1"}}})

		result, _, err := Run(context.Background(), runDeps(s, f, p, clk), []string{oldURL}, false)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if len(result.Renamed) != 0 {
			t.Fatalf("Renamed = %+v, want none (rename declined)", result.Renamed)
		}
	})

	t.Run("temporary redirect reports no rename", func(t *testing.T) {
		clk := testsupport.FixedClock(fixedTime())
		s := testsupport.NewInMemoryStore(clk)
		seedFeed(t, s, oldURL, fixedTime().Add(-time.Hour))

		f := testsupport.NewFakeFetcher()
		f.Register(oldURL, core.FetchResult{Status: 200, FinalURL: newURL, Permanent: false,
			Body: []byte("body"), MIMEType: "application/rss+xml"})
		p := testsupport.NewFakeParser()
		p.Register(oldURL, parse.ParsedFeed{Items: []core.Item{{GUID: "g1", Title: "t1"}}})

		result, _, err := Run(context.Background(), runDeps(s, f, p, clk), nil, false)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if len(result.Renamed) != 0 {
			t.Fatalf("Renamed = %+v, want none (temporary redirect)", result.Renamed)
		}
	})
}
