package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	cliv3 "github.com/urfave/cli/v3"

	"github.com/andreswebs/feedwatch/internal/config"
	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/store"
	"github.com/andreswebs/feedwatch/internal/testsupport"
)

// runList drives the list command through the root with an injected store
// double, capturing stdout, stderr, and the exit code the boundary selected.
func runList(t *testing.T, st store.Store, clk core.Clock, args ...string) runResult {
	t.Helper()

	outF, errF := tempFile(t), tempFile(t)
	d := Deps{
		Cfg:     config.Defaults(),
		Clock:   clk,
		Version: "1.2.3",
		Store:   st,
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

	_ = NewRootCommand(d).Run(t.Context(), append([]string{"feedwatch", "list"}, args...))

	res.out = readFile(t, outF)
	res.err = readFile(t, errF)
	return res
}

// listEnvelope mirrors the stdout ListResult shape for assertions.
type listEnvelope struct {
	Feeds []struct {
		URL       string `json:"url"`
		Alias     string `json:"alias"`
		Interval  string `json:"interval"`
		Status    string `json:"status"`
		Failures  int    `json:"failures"`
		LastError string `json:"last_error"`
	} `json:"feeds"`
}

// TestListReportsSubscriptions covers behavior 1: with two feeds, list outputs
// both with their status, alias, and failure count, exit 0.
func TestListReportsSubscriptions(t *testing.T) {
	clk := testsupport.FixedClock(pollFixedTime())
	st := testsupport.NewInMemoryStore(clk)

	urlA, urlB := "https://a.example/feed.xml", "https://b.example/feed.xml"
	if _, err := st.AddFeed(context.Background(), core.Feed{URL: urlA, Alias: "aye", Status: core.FeedActive}); err != nil {
		t.Fatalf("AddFeed(%s): %v", urlA, err)
	}
	if _, err := st.AddFeed(context.Background(), core.Feed{URL: urlB, Status: core.FeedActive}); err != nil {
		t.Fatalf("AddFeed(%s): %v", urlB, err)
	}

	res := runList(t, st, clk)

	if res.exited {
		t.Errorf("list should exit 0 without invoking OsExiter, got code %d", res.code)
	}
	if res.err != "" {
		t.Errorf("stderr = %q, want empty", res.err)
	}

	var env listEnvelope
	if err := json.Unmarshal([]byte(res.out), &env); err != nil {
		t.Fatalf("stdout is not a list envelope: %v\ngot: %q", err, res.out)
	}
	if len(env.Feeds) != 2 {
		t.Fatalf("len(feeds) = %d, want 2\ngot: %q", len(env.Feeds), res.out)
	}
	if env.Feeds[0].URL != urlA || env.Feeds[0].Alias != "aye" {
		t.Errorf("feed[0] = %+v, want url=%q alias=aye", env.Feeds[0], urlA)
	}
	if env.Feeds[0].Status != "active" {
		t.Errorf("feed[0].status = %q, want active", env.Feeds[0].Status)
	}
	if env.Feeds[1].URL != urlB {
		t.Errorf("feed[1].url = %q, want %q", env.Feeds[1].URL, urlB)
	}
}

// TestListReportsDisabledAndLastError covers behavior 2: a disabled feed reports
// status "disabled" along with its failure count and last error.
func TestListReportsDisabledAndLastError(t *testing.T) {
	clk := testsupport.FixedClock(pollFixedTime())
	st := testsupport.NewInMemoryStore(clk)

	url := "https://flaky.example/feed.xml"
	if _, err := st.AddFeed(context.Background(), core.Feed{
		URL:          url,
		Status:       core.FeedDisabled,
		FailureCount: 12,
		LastError:    "dns: no such host",
	}); err != nil {
		t.Fatalf("AddFeed(%s): %v", url, err)
	}

	res := runList(t, st, clk)

	if res.exited {
		t.Errorf("list should exit 0, got code %d", res.code)
	}

	var env listEnvelope
	if err := json.Unmarshal([]byte(res.out), &env); err != nil {
		t.Fatalf("stdout is not a list envelope: %v\ngot: %q", err, res.out)
	}
	if len(env.Feeds) != 1 {
		t.Fatalf("len(feeds) = %d, want 1", len(env.Feeds))
	}
	f := env.Feeds[0]
	if f.Status != "disabled" {
		t.Errorf("status = %q, want disabled", f.Status)
	}
	if f.Failures != 12 {
		t.Errorf("failures = %d, want 12", f.Failures)
	}
	if f.LastError != "dns: no such host" {
		t.Errorf("last_error = %q, want %q", f.LastError, "dns: no such host")
	}
}

// TestListEmptyStore covers behavior 3: an empty store yields an empty list, and
// the JSON is an empty array rather than null so an agent can iterate it safely.
func TestListEmptyStore(t *testing.T) {
	clk := testsupport.FixedClock(pollFixedTime())
	st := testsupport.NewInMemoryStore(clk)

	res := runList(t, st, clk)

	if res.exited {
		t.Errorf("list should exit 0, got code %d", res.code)
	}
	if !strings.Contains(res.out, `"feeds":[]`) {
		t.Errorf("stdout = %q, want it to contain \"feeds\":[]", res.out)
	}

	var env listEnvelope
	if err := json.Unmarshal([]byte(res.out), &env); err != nil {
		t.Fatalf("stdout is not a list envelope: %v\ngot: %q", err, res.out)
	}
	if len(env.Feeds) != 0 {
		t.Errorf("len(feeds) = %d, want 0", len(env.Feeds))
	}
}

// TestListTextFormatTable covers the --format text path: a human-friendly table
// with a header row and one line per feed carrying its status and failure count.
func TestListTextFormatTable(t *testing.T) {
	clk := testsupport.FixedClock(pollFixedTime())
	st := testsupport.NewInMemoryStore(clk)

	url := "https://flaky.example/feed.xml"
	if _, err := st.AddFeed(context.Background(), core.Feed{
		URL:          url,
		Alias:        "flaky",
		Status:       core.FeedDisabled,
		FailureCount: 12,
		LastError:    "dns: no such host",
	}); err != nil {
		t.Fatalf("AddFeed(%s): %v", url, err)
	}

	res := runList(t, st, clk, "--format", "text")

	if res.exited {
		t.Errorf("list should exit 0, got code %d", res.code)
	}
	if strings.HasPrefix(strings.TrimSpace(res.out), "{") {
		t.Errorf("text format should not emit JSON, got %q", res.out)
	}
	for _, want := range []string{"URL", "STATUS", "disabled", "flaky", "12", "dns: no such host"} {
		if !strings.Contains(res.out, want) {
			t.Errorf("text output %q missing %q", res.out, want)
		}
	}
}

// TestListReportsInterval covers fee-etoi: a feed with a non-default interval
// reports it in JSON, while a feed left on the default interval omits the field.
func TestListReportsInterval(t *testing.T) {
	clk := testsupport.FixedClock(pollFixedTime())
	st := testsupport.NewInMemoryStore(clk)

	withInterval, defaultInterval := "https://a.example/feed.xml", "https://b.example/feed.xml"
	if _, err := st.AddFeed(context.Background(), core.Feed{URL: withInterval, Interval: 30 * time.Minute, Status: core.FeedActive}); err != nil {
		t.Fatalf("AddFeed(%s): %v", withInterval, err)
	}
	if _, err := st.AddFeed(context.Background(), core.Feed{URL: defaultInterval, Status: core.FeedActive}); err != nil {
		t.Fatalf("AddFeed(%s): %v", defaultInterval, err)
	}

	res := runList(t, st, clk)

	var env listEnvelope
	if err := json.Unmarshal([]byte(res.out), &env); err != nil {
		t.Fatalf("stdout is not a list envelope: %v\ngot: %q", err, res.out)
	}
	if len(env.Feeds) != 2 {
		t.Fatalf("len(feeds) = %d, want 2\ngot: %q", len(env.Feeds), res.out)
	}
	if env.Feeds[0].Interval != "30m0s" {
		t.Errorf("feed[0].interval = %q, want 30m0s", env.Feeds[0].Interval)
	}
	if env.Feeds[1].Interval != "" {
		t.Errorf("feed[1].interval = %q, want empty (default omitted)", env.Feeds[1].Interval)
	}
	if strings.Contains(res.out, `"interval":""`) {
		t.Errorf("a default interval should be omitted from JSON, got %q", res.out)
	}
}

// TestListTextFormatShowsInterval covers fee-etoi: the text table carries an
// INTERVAL column, with a dash for a feed left on the default interval.
func TestListTextFormatShowsInterval(t *testing.T) {
	clk := testsupport.FixedClock(pollFixedTime())
	st := testsupport.NewInMemoryStore(clk)

	url := "https://a.example/feed.xml"
	if _, err := st.AddFeed(context.Background(), core.Feed{URL: url, Alias: "aye", Interval: 30 * time.Minute, Status: core.FeedActive}); err != nil {
		t.Fatalf("AddFeed(%s): %v", url, err)
	}

	res := runList(t, st, clk, "--format", "text")

	for _, want := range []string{"INTERVAL", "30m0s"} {
		if !strings.Contains(res.out, want) {
			t.Errorf("text output %q missing %q", res.out, want)
		}
	}
}
