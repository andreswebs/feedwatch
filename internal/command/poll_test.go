package command

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/andreswebs/feedwatch/core"
	"github.com/andreswebs/feedwatch/internal/testsupport"
	"github.com/andreswebs/feedwatch/store"
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

	d := Deps{Clock: clk, Version: "1.2.3", opts: netOpts(st, f, p)}
	return drive(t, d, args...)
}

// pollEnvelope mirrors the stdout PollResult shape for assertions.
type pollEnvelope struct {
	Polled    int              `json:"polled"`
	Succeeded int              `json:"succeeded"`
	Failed    int              `json:"failed"`
	Skipped   int              `json:"skipped"`
	Fetched   int              `json:"fetched"`
	NewItems  int              `json:"new_items"`
	Deduped   int              `json:"deduped"`
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
	parser.Register(urlA, core.ParsedFeed{Items: []core.Item{{GUID: "a1", Title: "Item A", Link: "https://a.example/1"}}})
	parser.Register(urlB, core.ParsedFeed{Items: []core.Item{{GUID: "b1", Title: "Item B", Link: "https://b.example/1"}}})

	res := runPoll(t, st, fetcher, parser, clk, "poll")

	if res.code != 0 {
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
	parser.Register(oldURL, core.ParsedFeed{Items: []core.Item{{GUID: "g1", Title: "Item", Link: "https://aihero.dev/1"}}})

	res := runPoll(t, st, fetcher, parser, clk, "poll")

	if res.code != 0 {
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
// per-feed failures are result data on stdout (the failures array), the envelope
// is still written to stdout with no items, the stderr batch error object is
// gone, and the run exits 2.
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

	if strings.Contains(res.err, `"errors":[`) {
		t.Errorf("stderr still carries the removed batch errors object: %q", res.err)
	}
}

// TestPollMixedExits3 covers behaviors 3 and 4: a poll with one success and one
// failure exits 3, the stdout envelope carries the success item plus the failure
// in its failures array, and the removed stderr batch object is gone.
func TestPollMixedExits3(t *testing.T) {
	st, fetcher, parser, clk := newPollDoubles(t)

	good := "https://good.example/feed.xml"
	bad := "https://bad.example/feed.xml"
	seedDueFeed(t, st, good)
	seedDueFeed(t, st, bad)
	fetcher.Register(good, okResult())
	parser.Register(good, core.ParsedFeed{Items: []core.Item{{GUID: "g1", Title: "Good Item", Link: "https://good.example/1"}}})
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

	if strings.Contains(res.err, `"errors":[`) {
		t.Errorf("stderr still carries the removed batch errors object: %q", res.err)
	}
}

// TestPollFailureMessageCarriesDetail covers that every failures[] entry in the
// poll envelope carries a non-empty "message" field with the underlying error
// detail, and that different categories carry the expected detail text.
func TestPollFailureMessageCarriesDetail(t *testing.T) {
	st, fetcher, parser, clk := newPollDoubles(t)

	networkURL := "https://net.example/feed.xml"
	httpURL := "https://http.example/feed.xml"
	parseURL := "https://parse.example/feed.xml"
	seedDueFeed(t, st, networkURL)
	seedDueFeed(t, st, httpURL)
	seedDueFeed(t, st, parseURL)

	fetcher.RegisterError(networkURL, core.NetworkErr(networkURL, &core.FeedError{
		Category: core.CatNetwork,
		Message:  "connection refused",
	}))
	fetcher.RegisterError(httpURL, core.HTTPErr(httpURL, 500, &core.FeedError{
		Category: core.CatHTTP,
		Message:  "server returned HTTP 500",
	}))
	// parseURL fetches OK but the parser returns a parse error.
	fetcher.Register(parseURL, okResult())
	parser.RegisterError(parseURL, core.ParseErr(parseURL, &core.FeedError{
		Category: core.CatParse,
		Message:  "could not detect feed type",
	}))

	res := runPoll(t, st, fetcher, parser, clk, "poll")

	if res.code != 2 {
		t.Errorf("exit code = %d, want 2 (all failed)", res.code)
	}

	var env pollEnvelope
	if err := json.Unmarshal([]byte(res.out), &env); err != nil {
		t.Fatalf("stdout is not a poll envelope: %v\ngot: %q", err, res.out)
	}
	if len(env.Failures) != 3 {
		t.Fatalf("failures length = %d, want 3", len(env.Failures))
	}

	byURL := make(map[string]map[string]any, 3)
	for _, f := range env.Failures {
		u, _ := f["feed_url"].(string)
		byURL[u] = f
	}

	for _, url := range []string{networkURL, httpURL, parseURL} {
		f, ok := byURL[url]
		if !ok {
			t.Errorf("no failure entry for %s", url)
			continue
		}
		msg, _ := f["message"].(string)
		if msg == "" {
			t.Errorf("failure for %s has empty message", url)
		}
	}
	if msg, _ := byURL[httpURL]["message"].(string); msg == "" || !containsAny(msg, "500", "HTTP") {
		t.Errorf("http failure message = %q, want it to reference 500 or HTTP", msg)
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
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

// TestPollMidPersistFailureWritesPartialEnvelopeAndExits70 covers the fee-oyw2
// fix: a hard store-write failure partway through persistence must not
// discard the work already committed. The feed persisted before the failure
// (sorted first by URL) is reported on stdout, and the invocation still exits
// as a hard failure (70, the internal/unclassified class of ADR 0001).
func TestPollMidPersistFailureWritesPartialEnvelopeAndExits70(t *testing.T) {
	st, fetcher, parser, clk := newPollDoubles(t)

	// Feeds are polled in URL order, so goodURL persists before badURL's write fails.
	const goodURL = "https://aaa-good.example/feed.xml"
	const badURL = "https://zzz-bad.example/feed.xml"
	seedDueFeed(t, st, goodURL)
	seedDueFeed(t, st, badURL)
	fetcher.Register(goodURL, okResult())
	fetcher.Register(badURL, okResult())
	parser.Register(goodURL, core.ParsedFeed{Items: []core.Item{{GUID: "g1", Title: "Good Item", Link: "https://aaa-good.example/1"}}})
	parser.Register(badURL, core.ParsedFeed{Items: []core.Item{{GUID: "b1", Title: "Bad Item", Link: "https://zzz-bad.example/1"}}})

	failing := &testsupport.FailingUpsertStore{Store: st, FailURL: badURL}

	res := runPoll(t, failing, fetcher, parser, clk, "poll")

	if res.code != 70 {
		t.Errorf("exit code = %d, want 70 (internal) for a mid-persist hard failure", res.code)
	}

	var env pollEnvelope
	if err := json.Unmarshal([]byte(res.out), &env); err != nil {
		t.Fatalf("stdout is not a poll envelope: %v\ngot: %q", err, res.out)
	}
	if env.NewItems != 1 || len(env.Items) != 1 {
		t.Fatalf("new_items = %d, items = %d, want 1 and 1 (only the feed persisted before the failure)", env.NewItems, len(env.Items))
	}
	if env.Items[0]["title"] != "Good Item" {
		t.Errorf("stdout item title = %v, want %q", env.Items[0]["title"], "Good Item")
	}

	var errEnv map[string]any
	if err := json.Unmarshal([]byte(res.err), &errEnv); err != nil {
		t.Fatalf("stderr is not a JSON error object: %v\ngot: %q", err, res.err)
	}
}

// TestPollEarlyHardFailureLeavesStdoutEmpty covers that an early hard failure
// (here an unknown named feed, a usage error failing before any fetch or
// persist) leaves stdout empty and exits 64, unlike a mid-persist failure.
func TestPollEarlyHardFailureLeavesStdoutEmpty(t *testing.T) {
	st, fetcher, parser, clk := newPollDoubles(t)

	res := runPoll(t, st, fetcher, parser, clk, "poll", "https://unknown.example/feed.xml")

	if res.code != 64 {
		t.Errorf("exit code = %d, want 64 (usage) for an early hard failure", res.code)
	}
	if res.out != "" {
		t.Errorf("stdout = %q, want empty for an early hard failure", res.out)
	}
}

// TestPollEnvelopeHasFetchedAndDedupedCounters covers that the stdout poll
// envelope carries fetched and deduped. First poll: fetched==new_items,
// deduped==0. Second forced poll: fetched==original count, new_items==0,
// deduped==fetched.
func TestPollEnvelopeHasFetchedAndDedupedCounters(t *testing.T) {
	st, fetcher, parser, clk := newPollDoubles(t)

	url := "https://a.example/feed.xml"
	seedDueFeed(t, st, url)
	fetcher.Register(url, okResult())
	parser.Register(url, core.ParsedFeed{Items: []core.Item{
		{GUID: "a1", Title: "Item 1", Link: "https://a.example/1"},
		{GUID: "a2", Title: "Item 2", Link: "https://a.example/2"},
	}})

	res := runPoll(t, st, fetcher, parser, clk, "poll")

	var env pollEnvelope
	if err := json.Unmarshal([]byte(res.out), &env); err != nil {
		t.Fatalf("stdout is not a poll envelope: %v\ngot: %q", err, res.out)
	}
	if env.Fetched != 2 {
		t.Errorf("fetched = %d, want 2 (first poll, all new)", env.Fetched)
	}
	if env.NewItems != 2 {
		t.Errorf("new_items = %d, want 2", env.NewItems)
	}
	if env.Deduped != 0 {
		t.Errorf("deduped = %d, want 0 (first poll, nothing previously seen)", env.Deduped)
	}

	// Second poll (--force to bypass scheduling): same items, none new.
	res2 := runPoll(t, st, fetcher, parser, clk, "poll", "--force")

	var env2 pollEnvelope
	if err := json.Unmarshal([]byte(res2.out), &env2); err != nil {
		t.Fatalf("stdout is not a poll envelope: %v\ngot: %q", err, res2.out)
	}
	if env2.Fetched != 2 {
		t.Errorf("second fetched = %d, want 2", env2.Fetched)
	}
	if env2.NewItems != 0 {
		t.Errorf("second new_items = %d, want 0", env2.NewItems)
	}
	if env2.Deduped != 2 {
		t.Errorf("second deduped = %d, want 2", env2.Deduped)
	}
}

// seedLanePollFeeds seeds the lane fixture the tag-scoped poll and check tests
// share — a feed in two lanes, a feed in one, and an untagged feed — as active,
// due, healthy subscriptions.
func seedLanePollFeeds(t *testing.T, st store.Store, f *testsupport.FakeFetcher, p *testsupport.FakeParser) (both, one, none string) {
	t.Helper()

	both, one, none = "https://both.example/feed.xml", "https://one.example/feed.xml", "https://none.example/feed.xml"
	due := pollFixedTime().Add(-time.Hour)
	for _, feed := range []core.Feed{
		{URL: both, Tags: []string{"ai", "agents"}, Status: core.FeedActive, NextDueAt: &due},
		{URL: one, Tags: []string{"ai"}, Status: core.FeedActive, NextDueAt: &due},
		{URL: none, Status: core.FeedActive, NextDueAt: &due},
	} {
		if _, err := st.AddFeed(context.Background(), feed); err != nil {
			t.Fatalf("AddFeed(%s): %v", feed.URL, err)
		}
		f.Register(feed.URL, okResult())
		p.Register(feed.URL, core.ParsedFeed{})
	}
	return both, one, none
}

// TestPollTagFlagsReachTheRequest covers behaviors 1, 3, and 4 at the CLI
// boundary: it fails before any selection logic if the flags do not bind into
// the request.
func TestPollTagFlagsReachTheRequest(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantPolled  int
		wantSkipped int
	}{
		{"no flag polls every due feed", []string{"poll"}, 3, 0},
		{"one tag narrows to the lane", []string{"poll", "--tag", "ai"}, 2, 0},
		{"two tags default to match all", []string{"poll", "--tag", "ai", "--tag", "agents"}, 1, 0},
		{"match any unions the lanes", []string{"poll", "--tag", "ai", "--match", "any", "--tag", "agents"}, 2, 0},
		{"force narrows the active selection", []string{"poll", "--force", "--tag", "ai"}, 2, 0},
		{"an empty lane is not an error", []string{"poll", "--tag", "nosuchlane"}, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st, fetcher, parser, clk := newPollDoubles(t)
			seedLanePollFeeds(t, st, fetcher, parser)

			res := runPoll(t, st, fetcher, parser, clk, tt.args...)

			if res.code != 0 {
				t.Fatalf("poll should exit 0, got code %d\nstderr: %q", res.code, res.err)
			}
			var env pollEnvelope
			if err := json.Unmarshal([]byte(res.out), &env); err != nil {
				t.Fatalf("stdout is not a poll envelope: %v\ngot: %q", err, res.out)
			}
			if env.Polled != tt.wantPolled {
				t.Errorf("polled = %d, want %d", env.Polled, tt.wantPolled)
			}
			if env.Skipped != tt.wantSkipped {
				t.Errorf("skipped = %d, want %d", env.Skipped, tt.wantSkipped)
			}
		})
	}
}

// TestPollSkippedCountsAgainstTheLane covers behavior 3 at the CLI boundary:
// with one of the lane's two feeds not yet due, skipped is 1 rather than the
// 2 an unscoped count over the whole store would report.
func TestPollSkippedCountsAgainstTheLane(t *testing.T) {
	st, fetcher, parser, clk := newPollDoubles(t)
	_, one, _ := seedLanePollFeeds(t, st, fetcher, parser)
	now := pollFixedTime()
	if _, err := st.RecordSuccess(context.Background(), one, now, now.Add(time.Hour), ""); err != nil {
		t.Fatalf("RecordSuccess: %v", err)
	}

	res := runPoll(t, st, fetcher, parser, clk, "poll", "--tag", "ai")

	if res.code != 0 {
		t.Fatalf("poll should exit 0, got code %d\nstderr: %q", res.code, res.err)
	}
	var env pollEnvelope
	if err := json.Unmarshal([]byte(res.out), &env); err != nil {
		t.Fatalf("stdout is not a poll envelope: %v\ngot: %q", err, res.out)
	}
	if env.Polled != 1 || env.Skipped != 1 {
		t.Errorf("polled/skipped = %d/%d, want 1/1 against the two-feed lane", env.Polled, env.Skipped)
	}
}

// TestPollRejectsInvalidTagSelection covers behaviors 5 and 10 at the CLI
// boundary: an ambiguous or malformed selection exits 64 with an empty stdout,
// and nothing is fetched.
func TestPollRejectsInvalidTagSelection(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{"tag with named feed", []string{"poll", "--tag", "ai", "https://both.example/feed.xml"}, []string{"--tag", "named feeds"}},
		{"unknown match", []string{"poll", "--tag", "ai", "--match", "bogus"}, []string{"all", "any"}},
		{"tag with whitespace", []string{"poll", "--tag", "a b"}, []string{"whitespace"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st, fetcher, parser, clk := newPollDoubles(t)
			both, _, _ := seedLanePollFeeds(t, st, fetcher, parser)

			res := runPoll(t, st, fetcher, parser, clk, tt.args...)

			if res.code != 64 {
				t.Fatalf("an invalid tag selection should exit 64 (usage), got code=%d\nstdout: %q", res.code, res.out)
			}
			if res.out != "" {
				t.Errorf("stdout = %q, want empty on a rejected poll", res.out)
			}
			if n := len(fetcher.Requests(both)); n != 0 {
				t.Errorf("feed was fetched %d time(s), want 0 on a rejected poll", n)
			}

			var env errEnvelope
			if err := json.Unmarshal([]byte(res.err), &env); err != nil {
				t.Fatalf("stderr is not an error envelope: %v\ngot: %q", err, res.err)
			}
			if env.Error.Code != core.ErrUsage.Code() {
				t.Errorf("code = %q, want %q", env.Error.Code, core.ErrUsage.Code())
			}
			for _, want := range tt.want {
				if !strings.Contains(env.Error.Message, want) {
					t.Errorf("message = %q, want it to mention %q", env.Error.Message, want)
				}
			}
		})
	}
}

// TestPollFieldsProjectsItems covers --fields on poll: each reported item
// carries only feed_url plus the requested fields, and the counts are unchanged.
func TestPollFieldsProjectsItems(t *testing.T) {
	st, fetcher, parser, clk := newPollDoubles(t)

	const url = "https://a.example/feed.xml"
	seedDueFeed(t, st, url)
	fetcher.Register(url, okResult())
	parser.Register(url, core.ParsedFeed{Items: []core.Item{{
		GUID: "a1", Title: "Item A", Link: "https://a.example/1",
		ContentHTML: "<p>long body</p>", ContentText: "long body",
	}}})

	res := runPoll(t, st, fetcher, parser, clk, "poll", "--fields", "title,link")

	if res.code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr: %s", res.code, res.err)
	}
	var env pollEnvelope
	if err := json.Unmarshal([]byte(res.out), &env); err != nil {
		t.Fatalf("stdout is not a poll envelope: %v\ngot: %q", err, res.out)
	}
	if env.Polled != 1 || env.NewItems != 1 || len(env.Items) != 1 {
		t.Fatalf("polled = %d, new_items = %d, items = %d, want 1, 1, 1", env.Polled, env.NewItems, len(env.Items))
	}
	want := map[string]any{"feed_url": url, "title": "Item A", "link": "https://a.example/1"}
	if len(env.Items[0]) != len(want) {
		t.Errorf("item = %v, want exactly %v", env.Items[0], want)
	}
	for k, v := range want {
		if env.Items[0][k] != v {
			t.Errorf("item[%q] = %v, want %v", k, env.Items[0][k], v)
		}
	}
	if !pollEnvelopeHasField(t, res.out, "failures", "[]") || !pollEnvelopeHasField(t, res.out, "renamed", "[]") {
		t.Errorf("failures and renamed must serialize as [] under projection, got %q", res.out)
	}
}

// TestPollFieldsUnknownRejected covers that an unknown --fields name is a usage
// error (exit 64, empty stdout) raised before any feed is fetched.
func TestPollFieldsUnknownRejected(t *testing.T) {
	st, fetcher, parser, clk := newPollDoubles(t)

	const url = "https://a.example/feed.xml"
	seedDueFeed(t, st, url)
	fetcher.Register(url, okResult())

	res := runPoll(t, st, fetcher, parser, clk, "poll", "--fields", "title,bogus")

	if res.code != 64 {
		t.Errorf("exit code = %d, want 64 (usage)", res.code)
	}
	if res.out != "" {
		t.Errorf("stdout = %q, want empty on a usage error", res.out)
	}
	if n := len(fetcher.Requests(url)); n != 0 {
		t.Errorf("feed was fetched %d time(s), want 0 on a rejected poll", n)
	}
}

// TestPollFieldsProjectsPartialEnvelope covers that a mid-persist failure still
// renders the partial envelope in the projected shape.
func TestPollFieldsProjectsPartialEnvelope(t *testing.T) {
	st, fetcher, parser, clk := newPollDoubles(t)

	const goodURL = "https://aaa-good.example/feed.xml"
	const badURL = "https://zzz-bad.example/feed.xml"
	seedDueFeed(t, st, goodURL)
	seedDueFeed(t, st, badURL)
	fetcher.Register(goodURL, okResult())
	fetcher.Register(badURL, okResult())
	parser.Register(goodURL, core.ParsedFeed{Items: []core.Item{{GUID: "g1", Title: "Good Item", Link: "https://aaa-good.example/1", ContentText: "body"}}})
	parser.Register(badURL, core.ParsedFeed{Items: []core.Item{{GUID: "b1", Title: "Bad Item"}}})

	failing := &testsupport.FailingUpsertStore{Store: st, FailURL: badURL}
	res := runPoll(t, failing, fetcher, parser, clk, "poll", "--fields", "title")

	if res.code != 70 {
		t.Errorf("exit code = %d, want 70", res.code)
	}
	var env pollEnvelope
	if err := json.Unmarshal([]byte(res.out), &env); err != nil {
		t.Fatalf("stdout is not a poll envelope: %v\ngot: %q", err, res.out)
	}
	if len(env.Items) != 1 || len(env.Items[0]) != 2 || env.Items[0]["title"] != "Good Item" {
		t.Errorf("items = %v, want one row of feed_url and title", env.Items)
	}
}
