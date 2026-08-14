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

// runList drives the list command through the root with an injected store
// double, capturing stdout, stderr, and the exit code the boundary selected.
func runList(t *testing.T, st store.Store, clk core.Clock, args ...string) runResult {
	t.Helper()

	d := Deps{Clock: clk, Version: "1.2.3", opts: storeOpts(st)}
	return drive(t, d, append([]string{"list"}, args...)...)
}

// listEnvelope mirrors the stdout feedwatch.ListResult shape for assertions.
type listEnvelope struct {
	Feeds []struct {
		URL       string   `json:"url"`
		Alias     string   `json:"alias"`
		Interval  string   `json:"interval"`
		Tags      []string `json:"tags"`
		Status    string   `json:"status"`
		Failures  int      `json:"failures"`
		LastError string   `json:"last_error"`
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

	if res.code != 0 {
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

	if res.code != 0 {
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

	if res.code != 0 {
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

	if res.code != 0 {
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

// TestListTextFormatShowsTags covers behavior 8: the text table carries a TAGS
// column, joined with commas, with a dash for an untagged feed.
func TestListTextFormatShowsTags(t *testing.T) {
	clk := testsupport.FixedClock(pollFixedTime())
	st := testsupport.NewInMemoryStore(clk)

	tagged, untagged := "https://a.example/feed.xml", "https://b.example/feed.xml"
	if _, err := st.AddFeed(context.Background(), core.Feed{URL: tagged, Tags: []string{"ai", "agents"}, Status: core.FeedActive}); err != nil {
		t.Fatalf("AddFeed(%s): %v", tagged, err)
	}
	if _, err := st.AddFeed(context.Background(), core.Feed{URL: untagged, Status: core.FeedActive}); err != nil {
		t.Fatalf("AddFeed(%s): %v", untagged, err)
	}

	res := runList(t, st, clk, "--format", "text")

	for _, want := range []string{"TAGS", "agents, ai"} {
		if !strings.Contains(res.out, want) {
			t.Errorf("text output %q missing %q", res.out, want)
		}
	}
	if !strings.Contains(res.out, untagged+"  -") {
		t.Errorf("text output %q should render an untagged feed's tags as a dash", res.out)
	}
}

// TestListReportsTagsAsArray covers behavior 2 at the CLI boundary: an untagged
// feed reports "tags":[], asserted on the raw stdout bytes so a null regression
// is caught where a decoded struct would hide it.
func TestListReportsTagsAsArray(t *testing.T) {
	clk := testsupport.FixedClock(pollFixedTime())
	st := testsupport.NewInMemoryStore(clk)

	url := "https://a.example/feed.xml"
	if _, err := st.AddFeed(context.Background(), core.Feed{URL: url, Status: core.FeedActive}); err != nil {
		t.Fatalf("AddFeed(%s): %v", url, err)
	}

	res := runList(t, st, clk)

	if !strings.Contains(res.out, `"tags":[]`) {
		t.Errorf("stdout = %q, want an untagged feed's tags as []", res.out)
	}
}

// seedLaneFeeds stores the three-feed lane fixture the tag-filter tests share:
// one feed in both lanes, one in a single lane, and one untagged.
func seedLaneFeeds(t *testing.T, st *testsupport.InMemoryStore) (both, one, none string) {
	t.Helper()

	both, one, none = "https://both.example/feed.xml", "https://one.example/feed.xml", "https://none.example/feed.xml"
	ctx := context.Background()
	for _, f := range []core.Feed{
		{URL: both, Tags: []string{"ai", "agents"}, Status: core.FeedActive},
		{URL: one, Tags: []string{"ai"}, Status: core.FeedActive},
		{URL: none, Status: core.FeedActive},
	} {
		if _, err := st.AddFeed(ctx, f); err != nil {
			t.Fatalf("AddFeed(%s): %v", f.URL, err)
		}
	}
	return both, one, none
}

// listedURLs decodes a list envelope from stdout and projects it onto its feed
// URLs, which is what every tag-filter assertion compares.
func listedURLs(t *testing.T, res runResult) []string {
	t.Helper()

	var env listEnvelope
	if err := json.Unmarshal([]byte(res.out), &env); err != nil {
		t.Fatalf("stdout is not a list envelope: %v\ngot: %q", err, res.out)
	}
	urls := make([]string, 0, len(env.Feeds))
	for _, f := range env.Feeds {
		urls = append(urls, f.URL)
	}
	return urls
}

// TestListTagFlagsReachTheRequest covers behaviors 1, 2, 3, and 7 at the CLI
// boundary. It fails before any filtering logic if the action does not bind the
// flags into the request, which is the trap list fell into while its request
// type was empty.
func TestListTagFlagsReachTheRequest(t *testing.T) {
	clk := testsupport.FixedClock(pollFixedTime())
	st := testsupport.NewInMemoryStore(clk)
	both, one, none := seedLaneFeeds(t, st)

	tests := []struct {
		name string
		args []string
		want []string
	}{
		{"no flag reports every subscription", nil, []string{both, none, one}},
		{"one tag narrows to the lane", []string{"--tag", "ai"}, []string{both, one}},
		{"two tags default to match all", []string{"--tag", "ai", "--tag", "agents"}, []string{both}},
		{"match any unions the lanes", []string{"--tag", "ai", "--tag", "agents", "--match", "any"}, []string{both, one}},
		{"an empty lane is not an error", []string{"--tag", "nosuchlane"}, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := runList(t, st, clk, tt.args...)

			if res.code != 0 {
				t.Fatalf("list should exit 0, got code %d\nstderr: %q", res.code, res.err)
			}
			got := listedURLs(t, res)
			if len(got) != len(tt.want) {
				t.Fatalf("urls = %v, want %v", got, tt.want)
			}
			for i, u := range tt.want {
				if got[i] != u {
					t.Fatalf("urls = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

// TestListTagSpellingsAreEquivalent covers behavior 4, which pins the central
// syntax decision: a slice flag splits on commas, so --tag a,b and --tag a
// --tag b must produce identical results and --match must be the only thing
// that changes the answer.
func TestListTagSpellingsAreEquivalent(t *testing.T) {
	clk := testsupport.FixedClock(pollFixedTime())
	st := testsupport.NewInMemoryStore(clk)
	seedLaneFeeds(t, st)

	for _, match := range []string{"all", "any"} {
		t.Run(match, func(t *testing.T) {
			comma := runList(t, st, clk, "--tag", "ai,agents", "--match", match)
			repeat := runList(t, st, clk, "--tag", "ai", "--tag", "agents", "--match", match)

			if comma.out != repeat.out {
				t.Errorf("--tag ai,agents = %q, want it identical to --tag ai --tag agents = %q", comma.out, repeat.out)
			}
		})
	}
}

// TestListRejectsInvalidTagSelection covers behaviors 5 and 6: an unknown
// --match value and an unstorable tag name exit 64 with an empty stdout and an
// error envelope naming the problem.
func TestListRejectsInvalidTagSelection(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{"unknown match", []string{"--tag", "ai", "--match", "bogus"}, []string{"all", "any"}},
		{"empty tag", []string{"--tag", ""}, []string{"empty"}},
		{"tag with whitespace", []string{"--tag", "a b"}, []string{"whitespace"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clk := testsupport.FixedClock(pollFixedTime())
			st := testsupport.NewInMemoryStore(clk)
			seedLaneFeeds(t, st)

			res := runList(t, st, clk, tt.args...)

			if res.code != 64 {
				t.Fatalf("an invalid tag selection should exit 64 (usage), got code=%d\nstdout: %q", res.code, res.out)
			}
			if res.out != "" {
				t.Errorf("stdout = %q, want empty on a rejected list", res.out)
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
