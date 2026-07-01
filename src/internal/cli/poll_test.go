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
	Polled    int              `json:"polled"`
	Succeeded int              `json:"succeeded"`
	Failed    int              `json:"failed"`
	Skipped   int              `json:"skipped"`
	NewItems  int              `json:"new_items"`
	Items     []map[string]any `json:"items"`
	Failures  []map[string]any `json:"failures"`
	Renamed   []map[string]any `json:"renamed"`
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
	if env.Succeeded != 2 || env.Failed != 0 {
		t.Errorf("succeeded = %d, failed = %d, want 2 and 0", env.Succeeded, env.Failed)
	}
	if env.NewItems != 2 {
		t.Errorf("new_items = %d, want 2", env.NewItems)
	}
	if len(env.Items) != 2 {
		t.Errorf("items length = %d, want 2", len(env.Items))
	}
	if env.Failures == nil {
		t.Errorf("failures should be an empty list, got JSON null/absent")
	}
	if len(env.Failures) != 0 {
		t.Errorf("failures length = %d, want 0 for an all-success poll", len(env.Failures))
	}
	// failures must serialize as [] (present, empty), never null.
	if !pollEnvelopeHasField(t, res.out, "failures", "[]") {
		t.Errorf("failures must serialize as [], got non-empty-array JSON in %q", res.out)
	}
	// renamed must likewise serialize as [] (present, empty), never null.
	if !pollEnvelopeHasField(t, res.out, "renamed", "[]") {
		t.Errorf("renamed must serialize as [], got non-empty-array JSON in %q", res.out)
	}
}

// TestPollReportsPermanentRedirectRename covers Req 6: a poll that renames a feed
// on a permanent redirect reports the rename in the envelope's renamed list with
// both URLs and emits one info stderr log line naming the count.
func TestPollReportsPermanentRedirectRename(t *testing.T) {
	st, fetcher, parser, clk := newPollDoubles(t)

	oldURL := "https://aihero.dev/rss.xml"
	newURL := "https://www.aihero.dev/rss.xml"
	seedDueFeed(t, st, oldURL)
	fetcher.Register(oldURL, core.FetchResult{Status: 200, FinalURL: newURL, Permanent: true,
		Body: []byte("body"), MIMEType: "application/rss+xml"})
	parser.Register(oldURL, parse.ParsedFeed{Items: []core.Item{{GUID: "g1", Title: "Item", Link: "https://aihero.dev/1"}}})

	res := runPoll(t, st, fetcher, parser, clk, "poll")

	if res.exited {
		t.Errorf("rename poll should exit 0, got code %d (stderr=%q)", res.code, res.err)
	}

	var env pollEnvelope
	if err := json.Unmarshal([]byte(res.out), &env); err != nil {
		t.Fatalf("stdout is not a poll envelope: %v\ngot: %q", err, res.out)
	}
	if len(env.Renamed) != 1 {
		t.Fatalf("renamed length = %d, want 1\ngot: %q", len(env.Renamed), res.out)
	}
	if env.Renamed[0]["from"] != oldURL {
		t.Errorf("renamed from = %v, want %q", env.Renamed[0]["from"], oldURL)
	}
	if env.Renamed[0]["to"] != newURL {
		t.Errorf("renamed to = %v, want %q", env.Renamed[0]["to"], newURL)
	}

	logged := decodeLogLine(t, res.err, "renamed feeds after permanent redirect")
	if logged["count"] != float64(1) {
		t.Errorf("log count = %v, want 1 (stderr=%q)", logged["count"], res.err)
	}
}

// pollEnvelopeHasField reports whether the named top-level field of the poll
// envelope JSON serialized to wantRaw, comparing the compacted raw bytes so a
// distinction such as [] versus null is preserved.
func pollEnvelopeHasField(t *testing.T, out, field, wantRaw string) bool {
	t.Helper()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatalf("stdout is not a JSON object: %v\ngot: %q", err, out)
	}
	got, ok := raw[field]
	if !ok {
		return false
	}
	return string(got) == wantRaw
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
	if env.Succeeded != 0 || env.Failed != 2 {
		t.Errorf("succeeded = %d, failed = %d, want 0 and 2", env.Succeeded, env.Failed)
	}
	if len(env.Failures) != 2 {
		t.Errorf("failures length = %d, want 2 for an all-failed poll", len(env.Failures))
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
	if env.Polled != 2 || env.Succeeded != 1 || env.Failed != 1 {
		t.Errorf("polled = %d, succeeded = %d, failed = %d, want 2, 1, 1", env.Polled, env.Succeeded, env.Failed)
	}
	if len(env.Failures) != 1 {
		t.Fatalf("failures length = %d, want 1 for a mixed poll", len(env.Failures))
	}
	f := env.Failures[0]
	if f["feed_url"] != bad {
		t.Errorf("failure feed_url = %v, want %q", f["feed_url"], bad)
	}
	if f["category"] != string(core.CatHTTP) {
		t.Errorf("failure category = %v, want %q", f["category"], core.CatHTTP)
	}
	if status, ok := f["status"].(float64); !ok || int(status) != 500 {
		t.Errorf("failure status = %v, want 500", f["status"])
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

// TestPollFailureOmitsStatusForNonHTTP covers that a failure with no HTTP status
// (here a network error) carries no "status" key in its stdout failures entry,
// while still reporting feed_url and category.
func TestPollFailureOmitsStatusForNonHTTP(t *testing.T) {
	st, fetcher, parser, clk := newPollDoubles(t)

	bad := "https://bad.example/feed.xml"
	seedDueFeed(t, st, bad)
	fetcher.RegisterError(bad, core.NetworkErr(bad, context.DeadlineExceeded))

	res := runPoll(t, st, fetcher, parser, clk, "poll")

	if res.code != 2 {
		t.Errorf("exit code = %d, want 2 for an all-failed poll", res.code)
	}

	var env pollEnvelope
	if err := json.Unmarshal([]byte(res.out), &env); err != nil {
		t.Fatalf("stdout is not a poll envelope: %v\ngot: %q", err, res.out)
	}
	if len(env.Failures) != 1 {
		t.Fatalf("failures length = %d, want 1", len(env.Failures))
	}
	f := env.Failures[0]
	if f["feed_url"] != bad {
		t.Errorf("failure feed_url = %v, want %q", f["feed_url"], bad)
	}
	if f["category"] != string(core.CatNetwork) {
		t.Errorf("failure category = %v, want %q", f["category"], core.CatNetwork)
	}
	if _, ok := f["status"]; ok {
		t.Errorf("failure for a network error must omit status, got %v", f["status"])
	}
}
