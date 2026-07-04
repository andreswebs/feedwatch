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

// runItems drives the items command through the root with an injected store
// double, capturing stdout, stderr, and the exit code the boundary selected.
func runItems(t *testing.T, st store.Store, clk core.Clock, args ...string) runResult {
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

	_ = NewRootCommand(d).Run(t.Context(), append([]string{"feedwatch", "items"}, args...))

	res.out = readFile(t, outF)
	res.err = readFile(t, errF)
	return res
}

// itemsEnvelope mirrors the stdout ItemsResult shape for assertions.
type itemsEnvelope struct {
	Items []struct {
		FeedURL     string     `json:"feed_url"`
		Title       string     `json:"title"`
		Link        string     `json:"link"`
		Summary     string     `json:"summary"`
		PublishedAt *time.Time `json:"published_at"`
	} `json:"items"`
}

// seedItem stores one item under feedURL with the given dedup key, title, and
// published time. A zero published time is stored as null with fetchedAt set, so
// the coalesce-to-fetched path can be exercised.
func seedItem(t *testing.T, s store.Store, feedURL, key, title string, published, fetched time.Time) {
	t.Helper()
	if _, err := s.AddFeed(context.Background(), core.Feed{URL: feedURL, Status: core.FeedActive}); err != nil {
		t.Fatalf("AddFeed(%s): %v", feedURL, err)
	}
	it := core.Item{
		DedupKey:  key,
		Title:     title,
		Link:      feedURL + "/" + key,
		FetchedAt: fetched,
	}
	if !published.IsZero() {
		p := published
		it.PublishedAt = &p
	}
	if _, err := s.UpsertItems(context.Background(), feedURL, []core.Item{it}); err != nil {
		t.Fatalf("UpsertItems(%s/%s): %v", feedURL, key, err)
	}
}

func parseItemsEnvelope(t *testing.T, out string) itemsEnvelope {
	t.Helper()
	var env itemsEnvelope
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("stdout is not an items envelope: %v\ngot: %q", err, out)
	}
	return env
}

// TestItemsByFeed covers behavior 1: items --feed X returns that feed's items as
// JSON, excluding other feeds.
func TestItemsByFeed(t *testing.T) {
	now := pollFixedTime()
	clk := testsupport.FixedClock(now)
	st := testsupport.NewInMemoryStore(clk)

	urlX, urlY := "https://x.example/feed.xml", "https://y.example/feed.xml"
	seedItem(t, st, urlX, "x1", "X one", now.Add(-time.Hour), now)
	seedItem(t, st, urlY, "y1", "Y one", now.Add(-time.Hour), now)

	res := runItems(t, st, clk, "--feed", urlX)

	if res.exited {
		t.Errorf("items should exit 0, got code %d (stderr=%q)", res.code, res.err)
	}
	env := parseItemsEnvelope(t, res.out)
	if len(env.Items) != 1 {
		t.Fatalf("len(items) = %d, want 1\ngot: %q", len(env.Items), res.out)
	}
	if env.Items[0].FeedURL != urlX || env.Items[0].Title != "X one" {
		t.Errorf("item = %+v, want feed=%q title=%q", env.Items[0], urlX, "X one")
	}
}

// TestItemsSinceUntilWindow covers behavior 2: --since/--until filter by time
// window on the publication axis, excluding null-publication items (the Req 3
// breaking change: no coalesce to fetched_at).
func TestItemsSinceUntilWindow(t *testing.T) {
	now := pollFixedTime()
	clk := testsupport.FixedClock(now)
	st := testsupport.NewInMemoryStore(clk)

	url := "https://x.example/feed.xml"
	seedItem(t, st, url, "old", "old", now.Add(-10*24*time.Hour), now.Add(-10*24*time.Hour))
	seedItem(t, st, url, "recent", "recent", now.Add(-24*time.Hour), now.Add(-24*time.Hour))
	// null published_at, fetched recently: must be excluded on the publication axis.
	seedItem(t, st, url, "nopub", "nopub", time.Time{}, now.Add(-12*time.Hour))

	res := runItems(t, st, clk, "--since", "7d")
	if res.exited {
		t.Errorf("items --since should exit 0, got code %d (stderr=%q)", res.code, res.err)
	}
	env := parseItemsEnvelope(t, res.out)
	got := map[string]bool{}
	for _, it := range env.Items {
		got[it.Title] = true
	}
	if got["old"] {
		t.Errorf("--since 7d should exclude the 10-day-old item; got %q", res.out)
	}
	if !got["recent"] {
		t.Errorf("--since 7d should include the recent dated item; got %q", res.out)
	}
	if got["nopub"] {
		t.Errorf("publication-axis --since 7d should exclude the null-published item; got %q", res.out)
	}

	// --until excludes the recent item, keeps the old one.
	until := now.Add(-5 * 24 * time.Hour).Format(time.RFC3339)
	res = runItems(t, st, clk, "--until", until)
	if res.exited {
		t.Errorf("items --until should exit 0, got code %d", res.code)
	}
	env = parseItemsEnvelope(t, res.out)
	got = map[string]bool{}
	for _, it := range env.Items {
		got[it.Title] = true
	}
	if !got["old"] {
		t.Errorf("--until should include the old item; got %q", res.out)
	}
	if got["recent"] {
		t.Errorf("--until should exclude the recent item; got %q", res.out)
	}
}

// TestItemsPaginationAndOrder covers behavior 3: --limit/--offset paginate and
// --order published asc|desc orders.
func TestItemsPaginationAndOrder(t *testing.T) {
	now := pollFixedTime()
	clk := testsupport.FixedClock(now)
	st := testsupport.NewInMemoryStore(clk)

	url := "https://x.example/feed.xml"
	seedItem(t, st, url, "a", "first", now.Add(-3*time.Hour), now)
	seedItem(t, st, url, "b", "second", now.Add(-2*time.Hour), now)
	seedItem(t, st, url, "c", "third", now.Add(-1*time.Hour), now)

	// Ascending order, second page of size 1 -> the middle item.
	res := runItems(t, st, clk, "--order", "published asc", "--limit", "1", "--offset", "1")
	if res.exited {
		t.Errorf("items paginate should exit 0, got code %d", res.code)
	}
	env := parseItemsEnvelope(t, res.out)
	if len(env.Items) != 1 {
		t.Fatalf("len(items) = %d, want 1\ngot: %q", len(env.Items), res.out)
	}
	if env.Items[0].Title != "second" {
		t.Errorf("asc page[1] = %q, want %q", env.Items[0].Title, "second")
	}

	// Descending (default) order: newest first.
	res = runItems(t, st, clk, "--order", "published desc")
	env = parseItemsEnvelope(t, res.out)
	if len(env.Items) != 3 || env.Items[0].Title != "third" {
		t.Errorf("desc order should lead with %q; got %q", "third", res.out)
	}
}

// TestItemsContains covers behavior 4: --contains filters by substring over the
// title and content.
func TestItemsContains(t *testing.T) {
	now := pollFixedTime()
	clk := testsupport.FixedClock(now)
	st := testsupport.NewInMemoryStore(clk)

	url := "https://x.example/feed.xml"
	seedItem(t, st, url, "r", "Go 1.30 release", now.Add(-2*time.Hour), now)
	seedItem(t, st, url, "n", "weekly newsletter", now.Add(-1*time.Hour), now)

	res := runItems(t, st, clk, "--contains", "release")
	if res.exited {
		t.Errorf("items --contains should exit 0, got code %d", res.code)
	}
	env := parseItemsEnvelope(t, res.out)
	if len(env.Items) != 1 || env.Items[0].Title != "Go 1.30 release" {
		t.Errorf("--contains release should match only the release item; got %q", res.out)
	}
}

// TestItemsFieldsProjection covers behavior 5: --fields title,link returns only
// those fields (plus retained identity), leaving unselected fields empty.
func TestItemsFieldsProjection(t *testing.T) {
	now := pollFixedTime()
	clk := testsupport.FixedClock(now)
	st := testsupport.NewInMemoryStore(clk)

	url := "https://x.example/feed.xml"
	it := core.Item{
		DedupKey:    "k",
		Title:       "titled",
		Link:        "https://x.example/post",
		Summary:     "a summary that should be projected away",
		PublishedAt: ptrTime(now.Add(-time.Hour)),
		FetchedAt:   now,
	}
	if _, err := st.AddFeed(context.Background(), core.Feed{URL: url, Status: core.FeedActive}); err != nil {
		t.Fatalf("AddFeed: %v", err)
	}
	if _, err := st.UpsertItems(context.Background(), url, []core.Item{it}); err != nil {
		t.Fatalf("UpsertItems: %v", err)
	}

	res := runItems(t, st, clk, "--fields", "title,link")
	if res.exited {
		t.Errorf("items --fields should exit 0, got code %d", res.code)
	}
	env := parseItemsEnvelope(t, res.out)
	if len(env.Items) != 1 {
		t.Fatalf("len(items) = %d, want 1\ngot: %q", len(env.Items), res.out)
	}
	got := env.Items[0]
	if got.Title != "titled" || got.Link != "https://x.example/post" {
		t.Errorf("projected item = %+v, want title+link populated", got)
	}
	if got.Summary != "" {
		t.Errorf("--fields title,link should drop summary, got %q", got.Summary)
	}
}

// itemKeys decodes the items envelope into the per-item key sets, so a test can
// assert the exact projection shape (which fields are present, not just their
// values).
func itemKeys(t *testing.T, out string) []map[string]json.RawMessage {
	t.Helper()
	var env struct {
		Items []map[string]json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("stdout is not an items envelope: %v\ngot: %q", err, out)
	}
	return env.Items
}

// TestItemsFieldsExactSubset asserts --fields returns exactly feed_url plus the
// requested fields, with no fixed base set leaking in (BUG-005, fee-j4w1).
func TestItemsFieldsExactSubset(t *testing.T) {
	now := pollFixedTime()
	clk := testsupport.FixedClock(now)
	st := testsupport.NewInMemoryStore(clk)

	url := "https://x.example/feed.xml"
	seedItem(t, st, url, "k", "titled", now.Add(-time.Hour), now)

	res := runItems(t, st, clk, "--fields", "summary")
	if res.exited {
		t.Errorf("items --fields summary should exit 0, got code %d (stderr=%q)", res.code, res.err)
	}
	rows := itemKeys(t, res.out)
	if len(rows) != 1 {
		t.Fatalf("len(items) = %d, want 1\ngot: %q", len(rows), res.out)
	}
	gotKeys := map[string]bool{}
	for k := range rows[0] {
		gotKeys[k] = true
	}
	want := map[string]bool{"feed_url": true, "summary": true}
	if len(gotKeys) != len(want) {
		t.Fatalf("--fields summary keys = %v, want exactly %v\ngot: %q", gotKeys, want, res.out)
	}
	for k := range want {
		if !gotKeys[k] {
			t.Errorf("--fields summary missing key %q; got %v", k, gotKeys)
		}
	}
}

// TestItemsFieldsUnknownRejected covers the usage error: an unknown field name,
// whether alone or alongside valid names, is rejected with exit 1 and empty
// stdout (BUG-005, fee-j4w1).
func TestItemsFieldsUnknownRejected(t *testing.T) {
	now := pollFixedTime()
	clk := testsupport.FixedClock(now)

	for _, arg := range []string{"bogusfield", "title,bogus"} {
		st := testsupport.NewInMemoryStore(clk)
		res := runItems(t, st, clk, "--fields", arg)
		if !res.exited || res.code != 1 {
			t.Errorf("--fields %q should exit 1, got exited=%v code=%d", arg, res.exited, res.code)
		}
		if res.out != "" {
			t.Errorf("--fields %q stdout should be empty on usage error, got %q", arg, res.out)
		}
	}
}

// TestItemsFieldsFeedURLNoOp covers that naming feed_url in --fields is accepted
// as a no-op (not an unknown-field error): feed_url is the always-on identity
// field and is emitted regardless, so listing it changes nothing (fee-n4p6).
func TestItemsFieldsFeedURLNoOp(t *testing.T) {
	now := pollFixedTime()
	clk := testsupport.FixedClock(now)

	cases := []struct {
		name string
		arg  string
		want map[string]bool
	}{
		{"feed_url alone", "feed_url", map[string]bool{"feed_url": true}},
		{"with others", "title,link,feed_url", map[string]bool{"feed_url": true, "title": true, "link": true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := testsupport.NewInMemoryStore(clk)
			url := "https://x.example/feed.xml"
			seedItem(t, st, url, "k", "titled", now.Add(-time.Hour), now)

			res := runItems(t, st, clk, "--fields", tc.arg)
			if res.exited {
				t.Fatalf("--fields %q should exit 0, got code %d (stderr=%q)", tc.arg, res.code, res.err)
			}
			rows := itemKeys(t, res.out)
			if len(rows) != 1 {
				t.Fatalf("len(items) = %d, want 1\ngot: %q", len(rows), res.out)
			}
			gotKeys := map[string]bool{}
			for k := range rows[0] {
				gotKeys[k] = true
			}
			if len(gotKeys) != len(tc.want) {
				t.Fatalf("--fields %q keys = %v, want exactly %v", tc.arg, gotKeys, tc.want)
			}
			for k := range tc.want {
				if !gotKeys[k] {
					t.Errorf("--fields %q missing key %q; got %v", tc.arg, k, gotKeys)
				}
			}
		})
	}
}

// TestItemsFieldsDidYouMean covers that a close typo in --fields still exits 1
// with empty stdout, but the usage error carries a did-you-mean suggestion;
// a name with no close match exits 1 with no suggestion (fee-n4p6).
func TestItemsFieldsDidYouMean(t *testing.T) {
	now := pollFixedTime()
	clk := testsupport.FixedClock(now)

	cases := []struct {
		name      string
		arg       string
		wantInErr string
		notInErr  string
	}{
		{"typo suggests", "tilte", `did you mean "title"?`, ""},
		{"typo within list suggests", "title,athor", `did you mean "author"?`, ""},
		{"no close match no suggestion", "zzzzzz", `unknown field "zzzzzz"`, "did you mean"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := testsupport.NewInMemoryStore(clk)
			res := runItems(t, st, clk, "--fields", tc.arg)
			if !res.exited || res.code != 1 {
				t.Fatalf("--fields %q should exit 1, got exited=%v code=%d", tc.arg, res.exited, res.code)
			}
			if res.out != "" {
				t.Errorf("--fields %q stdout should be empty on usage error, got %q", tc.arg, res.out)
			}
			msg := errorMessage(t, res.err)
			if !strings.Contains(msg, tc.wantInErr) {
				t.Errorf("--fields %q error message = %q, want it to contain %q", tc.arg, msg, tc.wantInErr)
			}
			if tc.notInErr != "" && strings.Contains(msg, tc.notInErr) {
				t.Errorf("--fields %q error message = %q, should not contain %q", tc.arg, msg, tc.notInErr)
			}
		})
	}
}

// errorMessage decodes the structured stderr error object and returns its
// message, so assertions compare the human-readable text rather than its
// JSON-escaped form.
func errorMessage(t *testing.T, stderr string) string {
	t.Helper()
	var env struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(stderr), &env); err != nil {
		t.Fatalf("stderr is not a structured error object: %v\ngot: %q", err, stderr)
	}
	return env.Error.Message
}

// TestItemsNoFieldsFullItem covers that omitting --fields returns the full
// normalized item, preserving the documented null published_at.
func TestItemsNoFieldsFullItem(t *testing.T) {
	now := pollFixedTime()
	clk := testsupport.FixedClock(now)
	st := testsupport.NewInMemoryStore(clk)

	url := "https://x.example/feed.xml"
	seedItem(t, st, url, "nopub", "nopub", time.Time{}, now)

	res := runItems(t, st, clk)
	if res.exited {
		t.Errorf("items should exit 0, got code %d", res.code)
	}
	rows := itemKeys(t, res.out)
	if len(rows) != 1 {
		t.Fatalf("len(items) = %d, want 1\ngot: %q", len(rows), res.out)
	}
	pub, ok := rows[0]["published_at"]
	if !ok {
		t.Fatalf("full item should always carry published_at; got %q", res.out)
	}
	if string(pub) != "null" {
		t.Errorf("unparseable published_at should serialize as null, got %s", pub)
	}
}

// TestItemsInvalidOrder covers a usage error: an unparseable --order value is a
// whole-invocation usage failure (exit 1) with no result on stdout.
func TestItemsInvalidOrder(t *testing.T) {
	now := pollFixedTime()
	clk := testsupport.FixedClock(now)
	st := testsupport.NewInMemoryStore(clk)

	res := runItems(t, st, clk, "--order", "sideways")
	if !res.exited || res.code != 1 {
		t.Errorf("invalid --order should exit 1, got exited=%v code=%d", res.exited, res.code)
	}
	if res.out != "" {
		t.Errorf("stdout should be empty on usage error, got %q", res.out)
	}
}

// TestItemsFetchedAtSelectable covers behavior: fetched_at is a selectable field
// returning feed_url + fetched_at, and is present (never null) on the default
// full item.
func TestItemsFetchedAtSelectable(t *testing.T) {
	now := pollFixedTime()
	clk := testsupport.FixedClock(now)
	st := testsupport.NewInMemoryStore(clk)

	url := "https://x.example/feed.xml"
	seedItem(t, st, url, "k", "titled", now.Add(-time.Hour), now.Add(-30*time.Minute))

	res := runItems(t, st, clk, "--fields", "fetched_at")
	if res.exited {
		t.Errorf("items --fields fetched_at should exit 0, got code %d (stderr=%q)", res.code, res.err)
	}
	rows := itemKeys(t, res.out)
	if len(rows) != 1 {
		t.Fatalf("len(items) = %d, want 1\ngot: %q", len(rows), res.out)
	}
	gotKeys := map[string]bool{}
	for k := range rows[0] {
		gotKeys[k] = true
	}
	want := map[string]bool{"feed_url": true, "fetched_at": true}
	if len(gotKeys) != len(want) {
		t.Fatalf("--fields fetched_at keys = %v, want exactly %v\ngot: %q", gotKeys, want, res.out)
	}
	if string(rows[0]["fetched_at"]) == "null" {
		t.Errorf("fetched_at must never be null; got %s", rows[0]["fetched_at"])
	}

	// Default (unprojected) output always carries fetched_at.
	res = runItems(t, st, clk)
	rows = itemKeys(t, res.out)
	if len(rows) != 1 {
		t.Fatalf("len(items) = %d, want 1\ngot: %q", len(rows), res.out)
	}
	if _, ok := rows[0]["fetched_at"]; !ok {
		t.Errorf("default item output should include fetched_at; got %q", res.out)
	}
}

// TestItemsTimeFieldAxis covers behavior: --time-field selects the axis for
// --since/--until. An item published long ago but fetched recently is excluded
// on the default publication axis and included on the fetch axis.
func TestItemsTimeFieldAxis(t *testing.T) {
	now := pollFixedTime()
	clk := testsupport.FixedClock(now)
	st := testsupport.NewInMemoryStore(clk)

	url := "https://x.example/feed.xml"
	// Published 10 days ago, but only fetched 1 day ago.
	seedItem(t, st, url, "late", "late arrival", now.Add(-10*24*time.Hour), now.Add(-24*time.Hour))

	// Default publication axis: --since 7d excludes it (published 10d ago).
	res := runItems(t, st, clk, "--since", "7d")
	if res.exited {
		t.Errorf("items --since should exit 0, got code %d", res.code)
	}
	if env := parseItemsEnvelope(t, res.out); len(env.Items) != 0 {
		t.Errorf("publication axis --since 7d should exclude the late item; got %q", res.out)
	}

	// Fetch axis: --since 7d includes it (fetched 1d ago).
	res = runItems(t, st, clk, "--since", "7d", "--time-field", "fetched")
	if res.exited {
		t.Errorf("items --time-field fetched should exit 0, got code %d (stderr=%q)", res.code, res.err)
	}
	if env := parseItemsEnvelope(t, res.out); len(env.Items) != 1 {
		t.Errorf("fetch axis --since 7d should include the late item; got %q", res.out)
	}
}

// TestItemsTimeFieldInvalid covers the usage error: an unrecognized --time-field
// value is a whole-invocation usage failure (exit 1) with empty stdout.
func TestItemsTimeFieldInvalid(t *testing.T) {
	now := pollFixedTime()
	clk := testsupport.FixedClock(now)
	st := testsupport.NewInMemoryStore(clk)

	res := runItems(t, st, clk, "--time-field", "sideways")
	if !res.exited || res.code != 1 {
		t.Errorf("invalid --time-field should exit 1, got exited=%v code=%d", res.exited, res.code)
	}
	if res.out != "" {
		t.Errorf("stdout should be empty on usage error, got %q", res.out)
	}
}

// TestItemsTimeFieldOrderIndependent covers that --order is independent of
// --time-field: windowing on the fetch axis while sorting by publication time.
func TestItemsTimeFieldOrderIndependent(t *testing.T) {
	now := pollFixedTime()
	clk := testsupport.FixedClock(now)
	st := testsupport.NewInMemoryStore(clk)

	url := "https://x.example/feed.xml"
	// Both fetched within the window; published in reverse fetch order.
	seedItem(t, st, url, "a", "older pub", now.Add(-3*24*time.Hour), now.Add(-2*time.Hour))
	seedItem(t, st, url, "b", "newer pub", now.Add(-1*24*time.Hour), now.Add(-1*time.Hour))

	res := runItems(t, st, clk, "--since", "7d", "--time-field", "fetched", "--order", "published asc")
	if res.exited {
		t.Errorf("items should exit 0, got code %d (stderr=%q)", res.code, res.err)
	}
	env := parseItemsEnvelope(t, res.out)
	if len(env.Items) != 2 {
		t.Fatalf("len(items) = %d, want 2\ngot: %q", len(env.Items), res.out)
	}
	if env.Items[0].Title != "older pub" {
		t.Errorf("--order published asc should lead with %q; got %q", "older pub", res.out)
	}
}

// omittedEnvelope decodes the items envelope's omitted_no_date field, using a
// pointer so an absent field (omitempty) is distinguishable from an explicit 0.
type omittedEnvelope struct {
	OmittedNoDate *int `json:"omitted_no_date"`
}

func parseOmitted(t *testing.T, out string) *int {
	t.Helper()
	var env omittedEnvelope
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("stdout is not an items envelope: %v\ngot: %q", err, out)
	}
	return env.OmittedNoDate
}

// TestItemsOmittedNoDateReported covers Req 3: a publication-axis date window
// that drops null-publication items reports the count as omitted_no_date and
// emits one info stderr line naming the count and axis.
func TestItemsOmittedNoDateReported(t *testing.T) {
	now := pollFixedTime()
	clk := testsupport.FixedClock(now)
	st := testsupport.NewInMemoryStore(clk)

	url := "https://x.example/feed.xml"
	seedItem(t, st, url, "dated", "dated", now.Add(-24*time.Hour), now.Add(-24*time.Hour))
	seedItem(t, st, url, "nopub1", "nopub1", time.Time{}, now.Add(-12*time.Hour))
	seedItem(t, st, url, "nopub2", "nopub2", time.Time{}, now.Add(-6*time.Hour))

	res := runItems(t, st, clk, "--since", "7d")
	if res.exited {
		t.Fatalf("items --since should exit 0, got code %d (stderr=%q)", res.code, res.err)
	}

	env := parseItemsEnvelope(t, res.out)
	if len(env.Items) != 1 || env.Items[0].Title != "dated" {
		t.Errorf("--since 7d should return only the dated item; got %q", res.out)
	}
	got := parseOmitted(t, res.out)
	if got == nil || *got != 2 {
		t.Errorf("omitted_no_date = %v, want 2\ngot: %q", got, res.out)
	}

	logged := decodeLogLine(t, res.err, "excluded items with no publication date")
	if logged["count"] != float64(2) {
		t.Errorf("log count = %v, want 2 (stderr=%q)", logged["count"], res.err)
	}
	if logged["axis"] != "published" {
		t.Errorf("log axis = %v, want published (stderr=%q)", logged["axis"], res.err)
	}
}

// TestItemsOmittedNoDateAbsentWhenNoneDropped covers that omitted_no_date is
// absent (omitempty) and no info line is emitted when the publication-axis window
// drops nothing.
func TestItemsOmittedNoDateAbsentWhenNoneDropped(t *testing.T) {
	now := pollFixedTime()
	clk := testsupport.FixedClock(now)
	st := testsupport.NewInMemoryStore(clk)

	url := "https://x.example/feed.xml"
	seedItem(t, st, url, "dated", "dated", now.Add(-24*time.Hour), now.Add(-24*time.Hour))

	res := runItems(t, st, clk, "--since", "7d")
	if res.exited {
		t.Fatalf("items --since should exit 0, got code %d (stderr=%q)", res.code, res.err)
	}
	if got := parseOmitted(t, res.out); got != nil {
		t.Errorf("omitted_no_date should be absent when nothing dropped; got %v\nout: %q", *got, res.out)
	}
	if strings.Contains(res.err, "excluded items with no publication date") {
		t.Errorf("no info line should be emitted when nothing dropped; stderr=%q", res.err)
	}
}

// TestItemsOmittedNoDateFetchAxisUnaffected covers that the fetch axis never
// reports omitted_no_date: fetched_at is never null, so dateless items match.
func TestItemsOmittedNoDateFetchAxisUnaffected(t *testing.T) {
	now := pollFixedTime()
	clk := testsupport.FixedClock(now)
	st := testsupport.NewInMemoryStore(clk)

	url := "https://x.example/feed.xml"
	seedItem(t, st, url, "nopub", "nopub", time.Time{}, now.Add(-12*time.Hour))

	res := runItems(t, st, clk, "--since", "7d", "--time-field", "fetched")
	if res.exited {
		t.Fatalf("items fetch axis should exit 0, got code %d (stderr=%q)", res.code, res.err)
	}
	env := parseItemsEnvelope(t, res.out)
	if len(env.Items) != 1 {
		t.Errorf("fetch axis --since 7d should include the dateless item; got %q", res.out)
	}
	if got := parseOmitted(t, res.out); got != nil {
		t.Errorf("fetch axis should never report omitted_no_date; got %v", *got)
	}
}

// TestItemsFieldsErrorListsValidFields covers that a usage error for an unknown
// --fields value always includes the "valid fields:" prefix and at least one
// known field name, so the caller never needs a separate round-trip to discover
// valid names (fee-6ol6).
func TestItemsFieldsErrorListsValidFields(t *testing.T) {
	now := pollFixedTime()
	clk := testsupport.FixedClock(now)
	st := testsupport.NewInMemoryStore(clk)

	res := runItems(t, st, clk, "--fields", "published")
	if !res.exited || res.code != 1 {
		t.Fatalf("--fields published should exit 1, got exited=%v code=%d", res.exited, res.code)
	}
	msg := errorMessage(t, res.err)
	if !strings.Contains(msg, "valid fields:") {
		t.Errorf("error message missing 'valid fields:': %q", msg)
	}
	if !strings.Contains(msg, "published_at") {
		t.Errorf("error message missing 'published_at' in field list: %q", msg)
	}
}

// TestItemsNullOrderingHonest covers Req 3 ordering: a null-publication item
// sorts last under publication desc and first under publication asc, with no
// fetch-time substitution.
func TestItemsNullOrderingHonest(t *testing.T) {
	now := pollFixedTime()
	clk := testsupport.FixedClock(now)
	st := testsupport.NewInMemoryStore(clk)

	url := "https://x.example/feed.xml"
	seedItem(t, st, url, "dated", "dated", now.Add(-48*time.Hour), now.Add(-48*time.Hour))
	// Dateless but fetched most recently: must not lead under publication desc.
	seedItem(t, st, url, "nopub", "nopub", time.Time{}, now.Add(-1*time.Hour))

	res := runItems(t, st, clk, "--order", "published desc")
	if res.exited {
		t.Fatalf("items should exit 0, got code %d (stderr=%q)", res.code, res.err)
	}
	env := parseItemsEnvelope(t, res.out)
	if len(env.Items) != 2 || env.Items[0].Title != "dated" || env.Items[1].Title != "nopub" {
		t.Errorf("publication desc should place dateless item last; got %q", res.out)
	}

	res = runItems(t, st, clk, "--order", "published asc")
	env = parseItemsEnvelope(t, res.out)
	if len(env.Items) != 2 || env.Items[0].Title != "nopub" || env.Items[1].Title != "dated" {
		t.Errorf("publication asc should place dateless item first; got %q", res.out)
	}
}

// decodeLogLine finds the JSON slog line on stderr whose msg matches want and
// returns it as a decoded map, failing the test if no such line is present.
func decodeLogLine(t *testing.T, stderr, want string) map[string]any {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSpace(stderr), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			continue
		}
		if m["msg"] == want {
			return m
		}
	}
	t.Fatalf("no stderr log line with msg %q; got: %q", want, stderr)
	return nil
}

func ptrTime(t time.Time) *time.Time { return &t }
