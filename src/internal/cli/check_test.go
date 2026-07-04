package cli

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	cliv3 "github.com/urfave/cli/v3"

	"github.com/andreswebs/feedwatch/internal/config"
	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/parse"
	"github.com/andreswebs/feedwatch/internal/store"
	"github.com/andreswebs/feedwatch/internal/testsupport"
)

// checkEnvelope mirrors the stdout CheckResult shape for assertions.
type checkEnvelope struct {
	Checked  int              `json:"checked"`
	OK       int              `json:"ok"`
	Failed   int              `json:"failed"`
	Failures []map[string]any `json:"failures"`
}

func newCheckDoubles(t *testing.T) (store.Store, *testsupport.FakeFetcher, *testsupport.FakeParser) {
	t.Helper()
	clk := testsupport.FixedClock(pollFixedTime())
	return testsupport.NewInMemoryStore(clk), testsupport.NewFakeFetcher(), testsupport.NewFakeParser()
}

// runCheck drives the check command through the root with injected doubles,
// capturing stdout, stderr, and the exit code.
func runCheck(t *testing.T, st store.Store, f *testsupport.FakeFetcher, p *testsupport.FakeParser, args ...string) runResult {
	t.Helper()

	outF, errF := tempFile(t), tempFile(t)
	d := Deps{
		Cfg:     config.Defaults(),
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

// seedActiveFeed adds an active feed with no schedule constraints so it appears
// in the active list for check.
func seedActiveFeed(t *testing.T, s store.Store, url string) {
	t.Helper()
	if _, err := s.AddFeed(context.Background(), core.Feed{URL: url, Status: core.FeedActive}); err != nil {
		t.Fatalf("AddFeed(%s): %v", url, err)
	}
}

// TestCheckAllSuccessExits0 is the tracer bullet: two good feeds checked, exit 0,
// envelope shows checked=2 ok=2 failed=0, failures is an empty list.
func TestCheckAllSuccessExits0(t *testing.T) {
	st, fetcher, parser := newCheckDoubles(t)

	urlA := "https://a.example/feed.xml"
	urlB := "https://b.example/feed.xml"
	seedActiveFeed(t, st, urlA)
	seedActiveFeed(t, st, urlB)
	fetcher.Register(urlA, okResult())
	fetcher.Register(urlB, okResult())
	parser.Register(urlA, parse.ParsedFeed{Items: []core.Item{{GUID: "a1", Title: "Item A"}}})
	parser.Register(urlB, parse.ParsedFeed{Items: []core.Item{{GUID: "b1", Title: "Item B"}}})

	res := runCheck(t, st, fetcher, parser, "check")

	if res.exited {
		t.Errorf("all-success check should exit 0 without invoking OsExiter, got code %d", res.code)
	}

	var env checkEnvelope
	if err := json.Unmarshal([]byte(res.out), &env); err != nil {
		t.Fatalf("stdout is not a check envelope: %v\ngot: %q", err, res.out)
	}
	if env.Checked != 2 {
		t.Errorf("checked = %d, want 2", env.Checked)
	}
	if env.OK != 2 {
		t.Errorf("ok = %d, want 2", env.OK)
	}
	if env.Failed != 0 {
		t.Errorf("failed = %d, want 0", env.Failed)
	}
	if env.Failures == nil {
		t.Errorf("failures should be an empty list, got JSON null/absent")
	}
	if len(env.Failures) != 0 {
		t.Errorf("failures length = %d, want 0 for an all-success check", len(env.Failures))
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(res.out), &raw); err != nil {
		t.Fatalf("stdout is not a JSON object: %v", err)
	}
	if string(raw["failures"]) != "[]" {
		t.Errorf("failures must serialize as [], got %s", raw["failures"])
	}
}

// TestCheckAllFailedExits2 covers: when every feed fails, exit 2, envelope has
// categorized failures, failures list is non-empty.
func TestCheckAllFailedExits2(t *testing.T) {
	st, fetcher, parser := newCheckDoubles(t)

	urlA := "https://a.example/feed.xml"
	urlB := "https://b.example/feed.xml"
	seedActiveFeed(t, st, urlA)
	seedActiveFeed(t, st, urlB)
	fetcher.RegisterError(urlA, core.HTTPErr(urlA, 404, context.DeadlineExceeded))
	fetcher.RegisterError(urlB, core.NetworkErr(urlB, context.DeadlineExceeded))

	res := runCheck(t, st, fetcher, parser, "check")

	if res.code != 2 {
		t.Errorf("exit code = %d, want 2 for an all-failed check", res.code)
	}

	var env checkEnvelope
	if err := json.Unmarshal([]byte(res.out), &env); err != nil {
		t.Fatalf("stdout is not a check envelope: %v\ngot: %q", err, res.out)
	}
	if env.Checked != 2 {
		t.Errorf("checked = %d, want 2", env.Checked)
	}
	if env.OK != 0 {
		t.Errorf("ok = %d, want 0", env.OK)
	}
	if env.Failed != 2 {
		t.Errorf("failed = %d, want 2", env.Failed)
	}
	if len(env.Failures) != 2 {
		t.Errorf("failures length = %d, want 2", len(env.Failures))
	}
}

// TestCheckMixedExits3 covers: one success, one failure → exit 3, envelope
// reflects the correct categorized failure.
func TestCheckMixedExits3(t *testing.T) {
	st, fetcher, parser := newCheckDoubles(t)

	good := "https://good.example/feed.xml"
	bad := "https://bad.example/feed.xml"
	seedActiveFeed(t, st, good)
	seedActiveFeed(t, st, bad)
	fetcher.Register(good, okResult())
	parser.Register(good, parse.ParsedFeed{})
	fetcher.RegisterError(bad, core.HTTPErr(bad, 404, context.DeadlineExceeded))

	res := runCheck(t, st, fetcher, parser, "check")

	if res.code != 3 {
		t.Errorf("exit code = %d, want 3 for a mixed check", res.code)
	}

	var env checkEnvelope
	if err := json.Unmarshal([]byte(res.out), &env); err != nil {
		t.Fatalf("stdout is not a check envelope: %v\ngot: %q", err, res.out)
	}
	if env.Checked != 2 || env.OK != 1 || env.Failed != 1 {
		t.Errorf("checked=%d ok=%d failed=%d, want 2 1 1", env.Checked, env.OK, env.Failed)
	}
	if len(env.Failures) != 1 {
		t.Fatalf("failures length = %d, want 1", len(env.Failures))
	}
	f := env.Failures[0]
	if f["feed_url"] != bad {
		t.Errorf("failure feed_url = %v, want %q", f["feed_url"], bad)
	}
	if f["category"] != string(core.CatHTTP) {
		t.Errorf("failure category = %v, want %q", f["category"], core.CatHTTP)
	}
	if status, ok := f["status"].(float64); !ok || int(status) != 404 {
		t.Errorf("failure status = %v, want 404", f["status"])
	}
	if _, ok := f["message"]; !ok {
		t.Errorf("failure message field must be present")
	}
}

// TestCheckParseFailureReportedAsParseCategory covers: a feed that parses as
// HTML (not a feed) is reported with category "parse".
func TestCheckParseFailureReportedAsParseCategory(t *testing.T) {
	st, fetcher, parser := newCheckDoubles(t)

	url := "https://html.example/page.html"
	seedActiveFeed(t, st, url)
	fetcher.Register(url, core.FetchResult{Status: 200, Body: []byte("<html>not a feed</html>"), MIMEType: "text/html"})
	parser.RegisterError(url, &core.FeedError{
		FeedURL:  url,
		Category: core.CatParse,
		Message:  "not a feed",
		Err:      errors.New("not a feed"),
	})

	res := runCheck(t, st, fetcher, parser, "check")

	if res.code != 2 {
		t.Errorf("exit code = %d, want 2", res.code)
	}

	var env checkEnvelope
	if err := json.Unmarshal([]byte(res.out), &env); err != nil {
		t.Fatalf("stdout is not a check envelope: %v\ngot: %q", err, res.out)
	}
	if len(env.Failures) != 1 {
		t.Fatalf("failures length = %d, want 1", len(env.Failures))
	}
	if env.Failures[0]["category"] != string(core.CatParse) {
		t.Errorf("failure category = %v, want %q", env.Failures[0]["category"], core.CatParse)
	}
}

// TestCheckNamedRefByAlias covers: a named alias resolves to the correct feed
// and is the only feed checked.
func TestCheckNamedRefByAlias(t *testing.T) {
	st, fetcher, parser := newCheckDoubles(t)

	url := "https://named.example/feed.xml"
	if _, err := st.AddFeed(context.Background(), core.Feed{URL: url, Alias: "myalias", Status: core.FeedActive}); err != nil {
		t.Fatalf("AddFeed: %v", err)
	}
	// also seed a second feed that should NOT be checked
	seedActiveFeed(t, st, "https://other.example/feed.xml")

	fetcher.Register(url, okResult())
	parser.Register(url, parse.ParsedFeed{})

	res := runCheck(t, st, fetcher, parser, "check", "myalias")

	if res.exited {
		t.Errorf("single named good feed should exit 0, got code %d", res.code)
	}

	var env checkEnvelope
	if err := json.Unmarshal([]byte(res.out), &env); err != nil {
		t.Fatalf("stdout is not a check envelope: %v\ngot: %q", err, res.out)
	}
	if env.Checked != 1 {
		t.Errorf("checked = %d, want 1 (only named feed)", env.Checked)
	}
}

// TestCheckUnknownRefExits1 covers: an unknown named ref is a usage error, exit 1.
func TestCheckUnknownRefExits1(t *testing.T) {
	st, fetcher, parser := newCheckDoubles(t)

	res := runCheck(t, st, fetcher, parser, "check", "https://no-such.example/feed.xml")

	if res.code != 1 {
		t.Errorf("exit code = %d, want 1 for unknown ref", res.code)
	}
}

// TestCheckNoStoreWrites covers: after a check that fetches feeds with items,
// the store still contains no items and feed state (validators, failure
// counters, schedule) is unchanged.
func TestCheckNoStoreWrites(t *testing.T) {
	st, fetcher, parser := newCheckDoubles(t)

	url := "https://a.example/feed.xml"
	seedActiveFeed(t, st, url)

	feedBefore, err := st.GetFeed(context.Background(), url)
	if err != nil {
		t.Fatalf("GetFeed before: %v", err)
	}

	fetcher.Register(url, okResult())
	parser.Register(url, parse.ParsedFeed{
		Items: []core.Item{{GUID: "g1", Title: "Item 1", Link: "https://a.example/1"}},
	})

	res := runCheck(t, st, fetcher, parser, "check")

	if res.exited {
		t.Errorf("should exit 0, got code %d", res.code)
	}

	// No items should be stored.
	q, err := st.QueryItems(context.Background(), core.ItemQuery{})
	if err != nil {
		t.Fatalf("QueryItems: %v", err)
	}
	if len(q.Items) != 0 {
		t.Errorf("items stored = %d, want 0 after check", len(q.Items))
	}

	// Feed state (validators, failure count, schedule) must be unchanged.
	feedAfter, err := st.GetFeed(context.Background(), url)
	if err != nil {
		t.Fatalf("GetFeed after: %v", err)
	}
	if feedAfter.ETag != feedBefore.ETag {
		t.Errorf("ETag changed: before=%q after=%q", feedBefore.ETag, feedAfter.ETag)
	}
	if feedAfter.LastModified != feedBefore.LastModified {
		t.Errorf("LastModified changed")
	}
	if feedAfter.FailureCount != feedBefore.FailureCount {
		t.Errorf("FailureCount changed: before=%d after=%d", feedBefore.FailureCount, feedAfter.FailureCount)
	}
}

// TestCheckSchemaRegistered covers: schema check returns a registered contract
// with the poll-style exit codes and a reflected output schema.
func TestCheckSchemaRegistered(t *testing.T) {
	st, fetcher, parser := newCheckDoubles(t)
	res := runCheck(t, st, fetcher, parser, "schema", "check")

	if res.exited {
		t.Errorf("schema check should exit 0, got code %d", res.code)
	}

	var schema map[string]any
	if err := json.Unmarshal([]byte(res.out), &schema); err != nil {
		t.Fatalf("stdout is not a JSON object: %v\ngot: %q", err, res.out)
	}
	if schema["command"] != "check" {
		t.Errorf("schema command = %v, want check", schema["command"])
	}
	exitCodes, ok := schema["exit_codes"].(map[string]any)
	if !ok {
		t.Fatalf("exit_codes is not an object: %v", schema["exit_codes"])
	}
	if _, ok := exitCodes["2"]; !ok {
		t.Errorf("exit_codes must include code 2 (all failed)")
	}
	if _, ok := exitCodes["3"]; !ok {
		t.Errorf("exit_codes must include code 3 (partial)")
	}
}
