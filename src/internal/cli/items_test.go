package cli

import (
	"context"
	"encoding/json"
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
// window, and a null published_at coalesces to fetched_at.
func TestItemsSinceUntilWindow(t *testing.T) {
	now := pollFixedTime()
	clk := testsupport.FixedClock(now)
	st := testsupport.NewInMemoryStore(clk)

	url := "https://x.example/feed.xml"
	seedItem(t, st, url, "old", "old", now.Add(-10*24*time.Hour), now.Add(-10*24*time.Hour))
	seedItem(t, st, url, "recent", "recent", now.Add(-24*time.Hour), now.Add(-24*time.Hour))
	// null published_at, fetched recently: must be included via coalesce.
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
	if !got["recent"] || !got["nopub"] {
		t.Errorf("--since 7d should include recent and null-published items; got %q", res.out)
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

func ptrTime(t time.Time) *time.Time { return &t }
