package command

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/andreswebs/feedwatch/core"
	"github.com/andreswebs/feedwatch/internal/testsupport"
	"github.com/andreswebs/feedwatch/store"
)

// runRm drives the rm command through the root with an injected store double,
// capturing stdout, stderr, and the exit code the boundary selected.
func runRm(t *testing.T, st store.Store, clk core.Clock, args ...string) runResult {
	t.Helper()

	d := Deps{Clock: clk, Version: "1.2.3", opts: storeOpts(st)}
	return drive(t, d, append([]string{"rm"}, args...)...)
}

// rmEnvelope mirrors the stdout feedwatch.RmResult shape for assertions.
type rmEnvelope struct {
	Removed []string `json:"removed"`
}

// parseRmEnvelope decodes an rm result from stdout.
func parseRmEnvelope(t *testing.T, out string) rmEnvelope {
	t.Helper()

	var env rmEnvelope
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("stdout is not an rm envelope: %v\ngot: %q", err, out)
	}
	return env
}

// rmTagFixture subscribes two feeds carrying "ai" (one of which also carries
// "go") and one carrying nothing, so a tag selection has an out-of-lane
// survivor to assert on.
func rmTagFixture(t *testing.T, st store.Store) (both, ai, none string) {
	t.Helper()

	both = "https://a.example/feed.xml"
	ai = "https://b.example/feed.xml"
	none = "https://c.example/feed.xml"
	for _, f := range []core.Feed{
		{URL: both, Tags: []string{"ai", "go"}, Status: core.FeedActive},
		{URL: ai, Tags: []string{"ai"}, Status: core.FeedActive},
		{URL: none, Status: core.FeedActive},
	} {
		if _, err := st.AddFeed(context.Background(), f); err != nil {
			t.Fatalf("AddFeed(%s): %v", f.URL, err)
		}
	}
	return both, ai, none
}

// feedURLs reads back the URLs the store still holds, so a destructive command
// is asserted on store state rather than on its own report.
func feedURLs(t *testing.T, st store.Store) []string {
	t.Helper()

	feeds, err := st.ListFeeds(context.Background(), core.ListFilter{})
	if err != nil {
		t.Fatalf("ListFeeds: %v", err)
	}
	out := make([]string, 0, len(feeds))
	for _, f := range feeds {
		out = append(out, f.URL)
	}
	return out
}

// TestRmByURL covers behavior 1: rm by URL removes the subscription and its
// items, reports the removed URL, and exits 0.
func TestRmByURL(t *testing.T) {
	clk := testsupport.FixedClock(pollFixedTime())
	st := testsupport.NewInMemoryStore(clk)

	url := "https://a.example/feed.xml"
	if _, err := st.AddFeed(context.Background(), core.Feed{URL: url, Status: core.FeedActive}); err != nil {
		t.Fatalf("AddFeed(%s): %v", url, err)
	}
	if _, err := st.UpsertItems(context.Background(), url, []core.Item{
		{DedupKey: "k1", Title: "one"},
	}); err != nil {
		t.Fatalf("UpsertItems: %v", err)
	}

	res := runRm(t, st, clk, url)

	if res.code != 0 {
		t.Errorf("rm should exit 0 without invoking OsExiter, got code %d", res.code)
	}
	if res.err != "" {
		t.Errorf("stderr = %q, want empty", res.err)
	}

	if env := parseRmEnvelope(t, res.out); !reflect.DeepEqual(env.Removed, []string{url}) {
		t.Errorf("removed = %v, want %v", env.Removed, []string{url})
	}
	if want := `"removed":["` + url + `"]`; !strings.Contains(res.out, want) {
		t.Errorf("stdout = %q, want it to carry %s: removed is a list on every path", res.out, want)
	}

	if _, err := st.GetFeed(context.Background(), url); err == nil {
		t.Errorf("feed %q still present after rm", url)
	}
	qr, err := st.QueryItems(context.Background(), core.ItemQuery{Feeds: []string{url}})
	if err != nil {
		t.Fatalf("QueryItems: %v", err)
	}
	if len(qr.Items) != 0 {
		t.Errorf("len(items) = %d after rm, want 0 (items should cascade)", len(qr.Items))
	}
}

// TestRmByAlias covers behavior 2: rm resolves a unique alias and removes the
// feed, reporting its canonical URL.
func TestRmByAlias(t *testing.T) {
	clk := testsupport.FixedClock(pollFixedTime())
	st := testsupport.NewInMemoryStore(clk)

	url := "https://a.example/feed.xml"
	if _, err := st.AddFeed(context.Background(), core.Feed{URL: url, Alias: "aye", Status: core.FeedActive}); err != nil {
		t.Fatalf("AddFeed(%s): %v", url, err)
	}

	res := runRm(t, st, clk, "aye")

	if res.code != 0 {
		t.Errorf("rm should exit 0, got code %d", res.code)
	}

	if env := parseRmEnvelope(t, res.out); !reflect.DeepEqual(env.Removed, []string{url}) {
		t.Errorf("removed = %v, want canonical url %v", env.Removed, []string{url})
	}
	if _, err := st.GetFeed(context.Background(), url); err == nil {
		t.Errorf("feed %q still present after rm by alias", url)
	}
}

// TestRmUnknownRef covers behavior 3: rm of an unknown ref is a usage error
// (exit 64) with a structured error object on stderr and no stdout result.
func TestRmUnknownRef(t *testing.T) {
	clk := testsupport.FixedClock(pollFixedTime())
	st := testsupport.NewInMemoryStore(clk)

	res := runRm(t, st, clk, "https://missing.example/feed.xml")

	if res.code != 64 {
		t.Errorf("rm of unknown ref should exit 64 (usage), got code=%d", res.code)
	}
	if res.out != "" {
		t.Errorf("stdout = %q, want empty on a usage failure", res.out)
	}

	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(res.err), &env); err != nil {
		t.Fatalf("stderr is not a JSON error object: %v\ngot: %q", err, res.err)
	}
	if env.Error.Code != core.ErrUsage.Code() {
		t.Errorf("code = %q, want %q", env.Error.Code, core.ErrUsage.Code())
	}
}

// TestRmByTagRemovesLane covers behavior 5: rm --tag unsubscribes every in-lane
// feed, reports them all in URL order, and leaves the out-of-lane feed alone.
func TestRmByTagRemovesLane(t *testing.T) {
	clk := testsupport.FixedClock(pollFixedTime())
	st := testsupport.NewInMemoryStore(clk)
	both, ai, none := rmTagFixture(t, st)

	res := runRm(t, st, clk, "--tag", "ai")

	if res.code != 0 {
		t.Fatalf("rm --tag should exit 0, got code %d (stderr=%q)", res.code, res.err)
	}
	if env := parseRmEnvelope(t, res.out); !reflect.DeepEqual(env.Removed, []string{both, ai}) {
		t.Errorf("removed = %v, want %v in URL order", env.Removed, []string{both, ai})
	}
	if got := feedURLs(t, st); !reflect.DeepEqual(got, []string{none}) {
		t.Errorf("remaining feeds = %v, want %v", got, []string{none})
	}
}

// TestRmByTagMatchSemantics covers behavior 6: two tags under the default
// --match all select only the feed carrying both, while --match any selects
// every feed carrying either.
func TestRmByTagMatchSemantics(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want func(both, ai, none string) (removed, remaining []string)
	}{
		{
			"all", []string{"--tag", "ai", "--tag", "go"},
			func(both, ai, none string) ([]string, []string) {
				return []string{both}, []string{ai, none}
			},
		},
		{
			"any", []string{"--tag", "ai", "--tag", "go", "--match", "any"},
			func(both, ai, none string) ([]string, []string) {
				return []string{both, ai}, []string{none}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clk := testsupport.FixedClock(pollFixedTime())
			st := testsupport.NewInMemoryStore(clk)
			both, ai, none := rmTagFixture(t, st)
			wantRemoved, wantRemaining := tc.want(both, ai, none)

			res := runRm(t, st, clk, tc.args...)

			if res.code != 0 {
				t.Fatalf("rm should exit 0, got code %d (stderr=%q)", res.code, res.err)
			}
			if env := parseRmEnvelope(t, res.out); !reflect.DeepEqual(env.Removed, wantRemoved) {
				t.Errorf("removed = %v, want %v", env.Removed, wantRemoved)
			}
			if got := feedURLs(t, st); !reflect.DeepEqual(got, wantRemaining) {
				t.Errorf("remaining feeds = %v, want %v", got, wantRemaining)
			}
		})
	}
}

// TestRmSelectorUsageErrorsLeaveStoreUntouched covers behaviors 7 and 8. Both
// assert on store state, not just the exit code: a destructive command that
// validates after resolving is exactly the failure this pair exists to catch.
func TestRmSelectorUsageErrorsLeaveStoreUntouched(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"ref and tag together", []string{"https://a.example/feed.xml", "--tag", "ai"}},
		{"neither ref nor tag", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clk := testsupport.FixedClock(pollFixedTime())
			st := testsupport.NewInMemoryStore(clk)
			both, ai, none := rmTagFixture(t, st)

			res := runRm(t, st, clk, tc.args...)

			if res.code != 64 {
				t.Errorf("rm should exit 64 (usage), got code=%d", res.code)
			}
			if res.out != "" {
				t.Errorf("stdout = %q, want empty on a usage failure", res.out)
			}
			if got, want := feedURLs(t, st), []string{both, ai, none}; !reflect.DeepEqual(got, want) {
				t.Errorf("remaining feeds = %v, want every feed %v: nothing may be removed", got, want)
			}
		})
	}
}

// TestRmEmptyLane covers behavior 9: a lane no feed carries removes nothing and
// exits 0 with an empty list, matching the "an empty lane is not an error" rule.
func TestRmEmptyLane(t *testing.T) {
	clk := testsupport.FixedClock(pollFixedTime())
	st := testsupport.NewInMemoryStore(clk)
	both, ai, none := rmTagFixture(t, st)

	res := runRm(t, st, clk, "--tag", "nosuchlane")

	if res.code != 0 {
		t.Fatalf("rm of an empty lane should exit 0, got code %d (stderr=%q)", res.code, res.err)
	}
	if !strings.Contains(res.out, `"removed":[]`) {
		t.Errorf("stdout = %q, want an empty removed list, never null", res.out)
	}
	if got, want := feedURLs(t, st), []string{both, ai, none}; !reflect.DeepEqual(got, want) {
		t.Errorf("remaining feeds = %v, want every feed %v", got, want)
	}
}
