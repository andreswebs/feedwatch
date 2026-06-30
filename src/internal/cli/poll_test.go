package cli

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	cliv3 "github.com/urfave/cli/v3"

	"github.com/andreswebs/feedwatch/internal/config"
	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/parse"
	"github.com/andreswebs/feedwatch/internal/store"
	"github.com/andreswebs/feedwatch/internal/testsupport"
)

func pollFixedTime() time.Time {
	return time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)
}

// seedDueFeed adds an active feed whose next-due time has already elapsed, so an
// unforced poll selects it.
func seedDueFeed(t *testing.T, s store.Store, url string) {
	t.Helper()
	due := pollFixedTime().Add(-time.Hour)
	if _, err := s.AddFeed(context.Background(), core.Feed{URL: url, Status: core.FeedActive, NextDueAt: &due}); err != nil {
		t.Fatalf("AddFeed(%s): %v", url, err)
	}
}

// runPoll drives the poll command through the root with injected doubles for the
// store, fetcher, parser, and clock, capturing stdout, stderr, and the exit code
// the boundary selected.
func runPoll(t *testing.T, st store.Store, f *testsupport.FakeFetcher, p *testsupport.FakeParser, clk core.Clock, args ...string) runResult {
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

	_ = NewRootCommand(d).Run(t.Context(), append([]string{"feedwatch"}, args...))

	res.out = readFile(t, outF)
	res.err = readFile(t, errF)
	return res
}

// pollEnvelope mirrors the stdout PollResult shape for assertions.
type pollEnvelope struct {
	Polled   int              `json:"polled"`
	Skipped  int              `json:"skipped"`
	NewItems int              `json:"new_items"`
	Items    []map[string]any `json:"items"`
}

func newPollDoubles(t *testing.T) (store.Store, *testsupport.FakeFetcher, *testsupport.FakeParser, core.Clock) {
	t.Helper()
	clk := testsupport.FixedClock(pollFixedTime())
	return testsupport.NewInMemoryStore(clk), testsupport.NewFakeFetcher(), testsupport.NewFakeParser(), clk
}

func okResult() core.FetchResult {
	return core.FetchResult{Status: 200, Body: []byte("body"), MIMEType: "application/rss+xml"}
}

// TestPollAllSuccessExits0 covers behavior 1: a poll where every feed fetches and
// parses cleanly writes the envelope to stdout, leaves stderr empty, and exits 0.
func TestPollAllSuccessExits0(t *testing.T) {
	st, fetcher, parser, clk := newPollDoubles(t)

	urlA := "https://a.example/feed.xml"
	urlB := "https://b.example/feed.xml"
	seedDueFeed(t, st, urlA)
	seedDueFeed(t, st, urlB)
	fetcher.Register(urlA, okResult())
	fetcher.Register(urlB, okResult())
	parser.Register(urlA, parse.ParsedFeed{Items: []core.Item{{GUID: "a1", Title: "Item A", Link: "https://a.example/1"}}})
	parser.Register(urlB, parse.ParsedFeed{Items: []core.Item{{GUID: "b1", Title: "Item B", Link: "https://b.example/1"}}})

	res := runPoll(t, st, fetcher, parser, clk, "poll")

	if res.exited {
		t.Errorf("all-success poll should exit 0 without invoking OsExiter, got code %d", res.code)
	}
	if res.err != "" {
		t.Errorf("stderr = %q, want empty for an all-success poll", res.err)
	}

	var env pollEnvelope
	if err := json.Unmarshal([]byte(res.out), &env); err != nil {
		t.Fatalf("stdout is not a poll envelope: %v\ngot: %q", err, res.out)
	}
	if env.Polled != 2 {
		t.Errorf("polled = %d, want 2", env.Polled)
	}
	if env.NewItems != 2 {
		t.Errorf("new_items = %d, want 2", env.NewItems)
	}
	if len(env.Items) != 2 {
		t.Errorf("items length = %d, want 2", len(env.Items))
	}
}

// TestPollAllFailedExits2 covers behavior 2: when every targeted feed fails, the
// per-feed errors go to stderr, the envelope is still written to stdout with no
// items, and the run exits 2.
func TestPollAllFailedExits2(t *testing.T) {
	st, fetcher, parser, clk := newPollDoubles(t)

	urlA := "https://a.example/feed.xml"
	urlB := "https://b.example/feed.xml"
	seedDueFeed(t, st, urlA)
	seedDueFeed(t, st, urlB)
	fetcher.RegisterError(urlA, core.HTTPErr(urlA, 404, context.DeadlineExceeded))
	fetcher.RegisterError(urlB, core.NetworkErr(urlB, context.DeadlineExceeded))

	res := runPoll(t, st, fetcher, parser, clk, "poll")

	if res.code != 2 {
		t.Errorf("exit code = %d, want 2 for an all-failed poll", res.code)
	}

	var env pollEnvelope
	if err := json.Unmarshal([]byte(res.out), &env); err != nil {
		t.Fatalf("stdout is not a poll envelope: %v\ngot: %q", err, res.out)
	}
	if env.Polled != 2 {
		t.Errorf("polled = %d, want 2", env.Polled)
	}
	if len(env.Items) != 0 {
		t.Errorf("items length = %d, want 0 for an all-failed poll", len(env.Items))
	}

	var errEnv struct {
		Errors []map[string]any `json:"errors"`
	}
	if err := json.Unmarshal([]byte(res.err), &errEnv); err != nil {
		t.Fatalf("stderr is not a JSON errors object: %v\ngot: %q", err, res.err)
	}
	if len(errEnv.Errors) != 2 {
		t.Errorf("stderr errors length = %d, want 2", len(errEnv.Errors))
	}
}

// TestPollMixedExits3 covers behaviors 3 and 4: a poll with one success and one
// failure exits 3, the stdout envelope carries only the success, and the failure
// appears on stderr and never on stdout.
func TestPollMixedExits3(t *testing.T) {
	st, fetcher, parser, clk := newPollDoubles(t)

	good := "https://good.example/feed.xml"
	bad := "https://bad.example/feed.xml"
	seedDueFeed(t, st, good)
	seedDueFeed(t, st, bad)
	fetcher.Register(good, okResult())
	parser.Register(good, parse.ParsedFeed{Items: []core.Item{{GUID: "g1", Title: "Good Item", Link: "https://good.example/1"}}})
	fetcher.RegisterError(bad, core.HTTPErr(bad, 500, context.DeadlineExceeded))

	res := runPoll(t, st, fetcher, parser, clk, "poll")

	if res.code != 3 {
		t.Errorf("exit code = %d, want 3 for a mixed poll", res.code)
	}

	var env pollEnvelope
	if err := json.Unmarshal([]byte(res.out), &env); err != nil {
		t.Fatalf("stdout is not a poll envelope: %v\ngot: %q", err, res.out)
	}
	if env.NewItems != 1 || len(env.Items) != 1 {
		t.Errorf("new_items = %d, items = %d, want 1 and 1", env.NewItems, len(env.Items))
	}
	if env.Items[0]["title"] != "Good Item" {
		t.Errorf("stdout item title = %v, want %q", env.Items[0]["title"], "Good Item")
	}

	var errEnv struct {
		Errors []map[string]any `json:"errors"`
	}
	if err := json.Unmarshal([]byte(res.err), &errEnv); err != nil {
		t.Fatalf("stderr is not a JSON errors object: %v\ngot: %q", err, res.err)
	}
	if len(errEnv.Errors) != 1 {
		t.Fatalf("stderr errors length = %d, want 1", len(errEnv.Errors))
	}
	if errEnv.Errors[0]["feed_url"] != bad {
		t.Errorf("stderr error feed_url = %v, want %q", errEnv.Errors[0]["feed_url"], bad)
	}
}
