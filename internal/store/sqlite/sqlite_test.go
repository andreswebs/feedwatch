package sqlite_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/andreswebs/feedwatch/core"
	"github.com/andreswebs/feedwatch/internal/store/sqlite"
	"github.com/andreswebs/feedwatch/store"
)

var testNow = time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)

// fixedClock is a core.Clock returning a constant instant for deterministic
// timestamp assertions.
func fixedClock() time.Time { return testNow }

// newStore opens a migrated store on a temp-file database with a fixed clock.
func newStore(t *testing.T) *sqlite.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "feedwatch.db")
	s, err := sqlite.Open(path, sqlite.WithClock(fixedClock))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if cerr := s.Close(); cerr != nil {
			t.Errorf("Close: %v", cerr)
		}
	})
	if _, err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return s
}

// Compile-time conformance: *sqlite.Store satisfies store.Store.
var _ store.Store = (*sqlite.Store)(nil)

func TestAddFeedRoundTrip(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	got, err := s.AddFeed(ctx, core.Feed{URL: "https://blog.example/feed.xml", Alias: "blog", Interval: time.Hour})
	if err != nil {
		t.Fatalf("AddFeed: %v", err)
	}
	if got.URL != "https://blog.example/feed.xml" {
		t.Errorf("URL = %q", got.URL)
	}
	if got.Alias != "blog" {
		t.Errorf("Alias = %q", got.Alias)
	}
	if got.Interval != time.Hour {
		t.Errorf("Interval = %v", got.Interval)
	}
	if got.Status != core.FeedActive {
		t.Errorf("Status = %q, want active", got.Status)
	}
	if got.CreatedAt != testNow {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, testNow)
	}

	byURL, err := s.GetFeed(ctx, "https://blog.example/feed.xml")
	if err != nil {
		t.Fatalf("GetFeed by url: %v", err)
	}
	if byURL.URL != got.URL || byURL.Alias != "blog" {
		t.Errorf("GetFeed by url = %+v", byURL)
	}

	byAlias, err := s.GetFeed(ctx, "blog")
	if err != nil {
		t.Fatalf("GetFeed by alias: %v", err)
	}
	if byAlias.URL != got.URL {
		t.Errorf("GetFeed by alias URL = %q", byAlias.URL)
	}
}

func TestAddFeedUpsertsSameURL(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	const u = "https://blog.example/feed.xml"

	if _, err := s.AddFeed(ctx, core.Feed{URL: u, Alias: "old", Interval: time.Hour}); err != nil {
		t.Fatalf("first AddFeed: %v", err)
	}
	if _, err := s.AddFeed(ctx, core.Feed{URL: u, Alias: "new", Interval: 30 * time.Minute}); err != nil {
		t.Fatalf("second AddFeed: %v", err)
	}

	feeds, err := s.ListFeeds(ctx, core.ListFilter{})
	if err != nil {
		t.Fatalf("ListFeeds: %v", err)
	}
	if len(feeds) != 1 {
		t.Fatalf("len(feeds) = %d, want 1 (no duplicate)", len(feeds))
	}
	if feeds[0].Alias != "new" {
		t.Errorf("Alias = %q, want updated to new", feeds[0].Alias)
	}
	if feeds[0].Interval != 30*time.Minute {
		t.Errorf("Interval = %v, want updated to 30m", feeds[0].Interval)
	}
}

func TestAddFeedAliasCollision(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	if _, err := s.AddFeed(ctx, core.Feed{URL: "https://a.example/feed", Alias: "dup"}); err != nil {
		t.Fatalf("AddFeed a: %v", err)
	}
	_, err := s.AddFeed(ctx, core.Feed{URL: "https://b.example/feed", Alias: "dup"})
	if err == nil {
		t.Fatal("expected error on alias collision, got nil")
	}
	var fe *core.FeedError
	if !errors.As(err, &fe) || fe.Category != core.CatUsage {
		t.Errorf("error = %v, want *FeedError with CatUsage", err)
	}
}

// addTestFeed inserts a feed so item operations satisfy the foreign key.
func addTestFeed(t *testing.T, s *sqlite.Store, url string) {
	t.Helper()
	if _, err := s.AddFeed(context.Background(), core.Feed{URL: url}); err != nil {
		t.Fatalf("AddFeed %q: %v", url, err)
	}
}

func TestUpsertItemsReturnsOnlyNew(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	const feed = "https://blog.example/feed.xml"
	addTestFeed(t, s, feed)

	items := []core.Item{
		{FeedURL: feed, DedupKey: "a", Title: "A", FetchedAt: testNow},
		{FeedURL: feed, DedupKey: "b", Title: "B", FetchedAt: testNow},
	}
	got, err := s.UpsertItems(ctx, feed, items)
	if err != nil {
		t.Fatalf("UpsertItems: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("first upsert returned %d new, want 2", len(got))
	}

	got2, err := s.UpsertItems(ctx, feed, items)
	if err != nil {
		t.Fatalf("UpsertItems (second): %v", err)
	}
	if len(got2) != 0 {
		t.Fatalf("second upsert returned %d new, want 0", len(got2))
	}
}

// TestUpsertItemsResolvesFetchedAt asserts a new item with a zero FetchedAt is
// stamped with the store clock and that the returned item carries that resolved
// time, so the poll envelope never reports a zero fetched_at.
func TestUpsertItemsResolvesFetchedAt(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	const feed = "https://blog.example/feed.xml"
	addTestFeed(t, s, feed)

	got, err := s.UpsertItems(ctx, feed, []core.Item{
		{FeedURL: feed, DedupKey: "a", Title: "A"}, // zero FetchedAt
	})
	if err != nil {
		t.Fatalf("UpsertItems: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("returned %d new, want 1", len(got))
	}
	if got[0].FetchedAt.IsZero() {
		t.Errorf("returned item FetchedAt is zero; want it stamped with the store clock")
	}
	if !got[0].FetchedAt.Equal(testNow) {
		t.Errorf("returned item FetchedAt = %s, want %s", got[0].FetchedAt, testNow)
	}
}

func TestUpsertItemsRefreshesContent(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	const feed = "https://blog.example/feed.xml"
	addTestFeed(t, s, feed)

	if _, err := s.UpsertItems(ctx, feed, []core.Item{
		{FeedURL: feed, DedupKey: "a", Title: "old title", ContentHTML: "<p>old</p>", FetchedAt: testNow},
	}); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	got, err := s.UpsertItems(ctx, feed, []core.Item{
		{FeedURL: feed, DedupKey: "a", Title: "new title", ContentHTML: "<p>new</p>", FetchedAt: testNow},
	})
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("refresh marked %d items new, want 0", len(got))
	}

	qr, err := s.QueryItems(ctx, core.ItemQuery{Feeds: []string{feed}})
	if err != nil {
		t.Fatalf("QueryItems: %v", err)
	}
	stored := qr.Items
	if len(stored) != 1 {
		t.Fatalf("len(stored) = %d, want 1", len(stored))
	}
	if stored[0].Title != "new title" || stored[0].ContentHTML != "<p>new</p>" {
		t.Errorf("content not refreshed: %+v", stored[0])
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

func TestQueryItemsFilters(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	const feed = "https://blog.example/feed.xml"
	addTestFeed(t, s, feed)

	jan := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mar := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	may := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	if _, err := s.UpsertItems(ctx, feed, []core.Item{
		{FeedURL: feed, DedupKey: "jan", Title: "January release", PublishedAt: ptrTime(jan), FetchedAt: testNow},
		{FeedURL: feed, DedupKey: "mar", Title: "March update", PublishedAt: ptrTime(mar), FetchedAt: testNow},
		{FeedURL: feed, DedupKey: "may", Title: "May notes", PublishedAt: ptrTime(may), FetchedAt: testNow},
	}); err != nil {
		t.Fatalf("UpsertItems: %v", err)
	}

	t.Run("since", func(t *testing.T) {
		got, err := s.QueryItems(ctx, core.ItemQuery{Since: ptrTime(mar)})
		if err != nil {
			t.Fatalf("QueryItems: %v", err)
		}
		if len(got.Items) != 2 {
			t.Fatalf("since mar returned %d, want 2", len(got.Items))
		}
	})
	t.Run("until", func(t *testing.T) {
		got, err := s.QueryItems(ctx, core.ItemQuery{Until: ptrTime(mar)})
		if err != nil {
			t.Fatalf("QueryItems: %v", err)
		}
		if len(got.Items) != 2 {
			t.Fatalf("until mar returned %d, want 2", len(got.Items))
		}
	})
	t.Run("contains", func(t *testing.T) {
		got, err := s.QueryItems(ctx, core.ItemQuery{Contains: "release"})
		if err != nil {
			t.Fatalf("QueryItems: %v", err)
		}
		if len(got.Items) != 1 || got.Items[0].DedupKey != "jan" {
			t.Fatalf("contains returned %+v", got.Items)
		}
	})
	t.Run("order published desc", func(t *testing.T) {
		got, err := s.QueryItems(ctx, core.ItemQuery{Order: core.ItemOrder{Field: "published", Desc: true}})
		if err != nil {
			t.Fatalf("QueryItems: %v", err)
		}
		if len(got.Items) != 3 || got.Items[0].DedupKey != "may" || got.Items[2].DedupKey != "jan" {
			t.Fatalf("order desc returned %+v", got.Items)
		}
	})
	t.Run("limit offset", func(t *testing.T) {
		got, err := s.QueryItems(ctx, core.ItemQuery{
			Order: core.ItemOrder{Field: "published", Desc: false}, Limit: 1, Offset: 1,
		})
		if err != nil {
			t.Fatalf("QueryItems: %v", err)
		}
		if len(got.Items) != 1 || got.Items[0].DedupKey != "mar" {
			t.Fatalf("limit/offset returned %+v", got.Items)
		}
	})
}

// TestQueryItemsNullPublishedOrdering covers the honest publication-axis order:
// a null publication time sorts last under descending order and first under
// ascending order, never substituting the fetch time (fee-aag4, Req 3).
func TestQueryItemsNullPublishedOrdering(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	const feed = "https://blog.example/feed.xml"
	addTestFeed(t, s, feed)

	early := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	late := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	// "nodate" has no published_at; despite a late fetched_at it never jumps the
	// dated item on the publication axis.
	if _, err := s.UpsertItems(ctx, feed, []core.Item{
		{FeedURL: feed, DedupKey: "dated", Title: "dated", PublishedAt: ptrTime(early), FetchedAt: early},
		{FeedURL: feed, DedupKey: "nodate", Title: "nodate", FetchedAt: late},
	}); err != nil {
		t.Fatalf("UpsertItems: %v", err)
	}

	desc, err := s.QueryItems(ctx, core.ItemQuery{Order: core.ItemOrder{Field: "published", Desc: true}})
	if err != nil {
		t.Fatalf("QueryItems desc: %v", err)
	}
	if len(desc.Items) != 2 || desc.Items[0].DedupKey != "dated" || desc.Items[1].DedupKey != "nodate" {
		t.Fatalf("desc: null-published item should sort last; got %+v", desc.Items)
	}
	if desc.Items[0].PublishedAt == nil {
		t.Error("dated item lost its published_at")
	}

	asc, err := s.QueryItems(ctx, core.ItemQuery{Order: core.ItemOrder{Field: "published", Desc: false}})
	if err != nil {
		t.Fatalf("QueryItems asc: %v", err)
	}
	if len(asc.Items) != 2 || asc.Items[0].DedupKey != "nodate" || asc.Items[1].DedupKey != "dated" {
		t.Fatalf("asc: null-published item should sort first; got %+v", asc.Items)
	}
}

// TestQueryItemsTimeFieldAxis covers the --time-field selection: the publication
// axis filters on published_at, the fetch axis filters on fetched_at, so an item
// published before the window but fetched inside it is excluded on the
// publication axis and included on the fetch axis.
func TestQueryItemsTimeFieldAxis(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	const feed = "https://blog.example/feed.xml"
	addTestFeed(t, s, feed)

	oldPub := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	recentFetch := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	if _, err := s.UpsertItems(ctx, feed, []core.Item{
		{FeedURL: feed, DedupKey: "late", Title: "late", PublishedAt: ptrTime(oldPub), FetchedAt: recentFetch},
	}); err != nil {
		t.Fatalf("UpsertItems: %v", err)
	}

	cutoff := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	published, err := s.QueryItems(ctx, core.ItemQuery{Since: ptrTime(cutoff), TimeField: "published"})
	if err != nil {
		t.Fatalf("QueryItems published: %v", err)
	}
	if len(published.Items) != 0 {
		t.Errorf("publication axis since mar should exclude item published in jan; got %+v", published.Items)
	}

	fetched, err := s.QueryItems(ctx, core.ItemQuery{Since: ptrTime(cutoff), TimeField: "fetched"})
	if err != nil {
		t.Fatalf("QueryItems fetched: %v", err)
	}
	if len(fetched.Items) != 1 || fetched.Items[0].DedupKey != "late" {
		t.Errorf("fetch axis since mar should include item fetched in may; got %+v", fetched.Items)
	}
}

// TestQueryItemsOmittedNoDate covers Req 3: a publication-axis date window
// excludes null-publication items and reports the dropped count; the fetch axis
// and an unfiltered query report zero.
func TestQueryItemsOmittedNoDate(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	const feed = "https://blog.example/feed.xml"
	addTestFeed(t, s, feed)

	dated := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	fetched := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	if _, err := s.UpsertItems(ctx, feed, []core.Item{
		{FeedURL: feed, DedupKey: "dated", Title: "dated", PublishedAt: ptrTime(dated), FetchedAt: dated},
		{FeedURL: feed, DedupKey: "nopub1", Title: "nopub1", FetchedAt: fetched},
		{FeedURL: feed, DedupKey: "nopub2", Title: "nopub2", FetchedAt: fetched},
	}); err != nil {
		t.Fatalf("UpsertItems: %v", err)
	}

	cutoff := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	t.Run("publication axis excludes and counts", func(t *testing.T) {
		got, err := s.QueryItems(ctx, core.ItemQuery{Since: ptrTime(cutoff), TimeField: "published"})
		if err != nil {
			t.Fatalf("QueryItems: %v", err)
		}
		if len(got.Items) != 1 || got.Items[0].DedupKey != "dated" {
			t.Fatalf("publication axis should return only the dated item; got %+v", got.Items)
		}
		if got.OmittedNoDate != 2 {
			t.Errorf("OmittedNoDate = %d, want 2", got.OmittedNoDate)
		}
	})

	t.Run("fetch axis includes all, omits nothing", func(t *testing.T) {
		got, err := s.QueryItems(ctx, core.ItemQuery{Since: ptrTime(cutoff), TimeField: "fetched"})
		if err != nil {
			t.Fatalf("QueryItems: %v", err)
		}
		if len(got.Items) != 3 {
			t.Fatalf("fetch axis should return all 3 items; got %d", len(got.Items))
		}
		if got.OmittedNoDate != 0 {
			t.Errorf("fetch axis OmittedNoDate = %d, want 0", got.OmittedNoDate)
		}
	})

	t.Run("no date filter omits nothing", func(t *testing.T) {
		got, err := s.QueryItems(ctx, core.ItemQuery{})
		if err != nil {
			t.Fatalf("QueryItems: %v", err)
		}
		if len(got.Items) != 3 || got.OmittedNoDate != 0 {
			t.Errorf("unfiltered query: items=%d omitted=%d, want 3 and 0", len(got.Items), got.OmittedNoDate)
		}
	})
}

func TestQueryItemsFieldProjection(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	const feed = "https://blog.example/feed.xml"
	addTestFeed(t, s, feed)

	if _, err := s.UpsertItems(ctx, feed, []core.Item{
		{FeedURL: feed, DedupKey: "a", Title: "Title A", Link: "https://x/a",
			ContentHTML: "<p>heavy body</p>", Summary: "summary", FetchedAt: testNow},
	}); err != nil {
		t.Fatalf("UpsertItems: %v", err)
	}

	qr, err := s.QueryItems(ctx, core.ItemQuery{Fields: []string{"title", "link"}})
	if err != nil {
		t.Fatalf("QueryItems: %v", err)
	}
	got := qr.Items
	if len(got) != 1 {
		t.Fatalf("len(got) = %d", len(got))
	}
	if got[0].Title != "Title A" || got[0].Link != "https://x/a" {
		t.Errorf("requested fields missing: %+v", got[0])
	}
	if got[0].ContentHTML != "" || got[0].Summary != "" {
		t.Errorf("unprojected heavy fields should be empty: %+v", got[0])
	}
}

func TestRecordFailureThenSuccess(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	const feed = "https://blog.example/feed.xml"
	addTestFeed(t, s, feed)

	at := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	due := time.Date(2026, 6, 1, 1, 0, 0, 0, time.UTC)
	if err := s.RecordFailure(ctx, feed, core.CatNetwork, "dns: no such host", at, due); err != nil {
		t.Fatalf("RecordFailure: %v", err)
	}
	if err := s.RecordFailure(ctx, feed, core.CatNetwork, "dns: no such host", at, due); err != nil {
		t.Fatalf("RecordFailure 2: %v", err)
	}
	f, err := s.GetFeed(ctx, feed)
	if err != nil {
		t.Fatalf("GetFeed: %v", err)
	}
	if f.FailureCount != 2 {
		t.Errorf("FailureCount = %d, want 2", f.FailureCount)
	}
	if f.LastError != "dns: no such host" {
		t.Errorf("LastError = %q", f.LastError)
	}
	if f.LastErrorAt == nil || !f.LastErrorAt.Equal(at) {
		t.Errorf("LastErrorAt = %v, want %v", f.LastErrorAt, at)
	}

	fetched := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	nextDue := time.Date(2026, 6, 2, 1, 0, 0, 0, time.UTC)
	if _, err := s.RecordSuccess(ctx, feed, fetched, nextDue, ""); err != nil {
		t.Fatalf("RecordSuccess: %v", err)
	}
	f, err = s.GetFeed(ctx, feed)
	if err != nil {
		t.Fatalf("GetFeed: %v", err)
	}
	if f.FailureCount != 0 || f.LastError != "" || f.LastErrorAt != nil {
		t.Errorf("success did not clear failure state: %+v", f)
	}
	if f.LastFetchAt == nil || !f.LastFetchAt.Equal(fetched) {
		t.Errorf("LastFetchAt = %v, want %v", f.LastFetchAt, fetched)
	}
}

func TestDueFeeds(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

	addTestFeed(t, s, "https://never.example/feed") // next_due_at NULL -> due
	addTestFeed(t, s, "https://past.example/feed")
	addTestFeed(t, s, "https://future.example/feed")
	addTestFeed(t, s, "https://disabled.example/feed")

	if _, err := s.RecordSuccess(ctx, "https://past.example/feed", now, now.Add(-time.Hour), ""); err != nil {
		t.Fatalf("past due: %v", err)
	}
	if _, err := s.RecordSuccess(ctx, "https://future.example/feed", now, now.Add(time.Hour), ""); err != nil {
		t.Fatalf("future due: %v", err)
	}
	if err := s.SetStatus(ctx, "https://disabled.example/feed", core.FeedDisabled); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	due, err := s.DueFeeds(ctx, now, core.ListFilter{})
	if err != nil {
		t.Fatalf("DueFeeds: %v", err)
	}
	got := map[string]bool{}
	for _, f := range due {
		got[f.URL] = true
	}
	if !got["https://never.example/feed"] || !got["https://past.example/feed"] {
		t.Errorf("expected never+past due, got %v", got)
	}
	if got["https://future.example/feed"] {
		t.Error("future feed should not be due")
	}
	if got["https://disabled.example/feed"] {
		t.Error("disabled feed should never be due")
	}
}

func TestPruneItemsByAge(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	const feed = "https://blog.example/feed.xml"
	addTestFeed(t, s, feed)

	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	if _, err := s.UpsertItems(ctx, feed, []core.Item{
		{FeedURL: feed, DedupKey: "old", Title: "old", ContentHTML: "<p>old</p>", PublishedAt: ptrTime(old), FetchedAt: old},
		{FeedURL: feed, DedupKey: "recent", Title: "recent", PublishedAt: ptrTime(recent), FetchedAt: recent},
	}); err != nil {
		t.Fatalf("UpsertItems: %v", err)
	}

	cutoff := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	deleted, err := s.PruneItems(ctx, core.PrunePolicy{KeepBefore: &cutoff})
	if err != nil {
		t.Fatalf("PruneItems: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}

	qr, err := s.QueryItems(ctx, core.ItemQuery{Feeds: []string{feed}})
	if err != nil {
		t.Fatalf("QueryItems: %v", err)
	}
	got := qr.Items
	if len(got) != 1 || got[0].DedupKey != "recent" {
		t.Fatalf("query after prune returned %+v", got)
	}

	// Re-upserting the pruned key returns no new items: the tombstone preserves
	// the fingerprint.
	newItems, err := s.UpsertItems(ctx, feed, []core.Item{
		{FeedURL: feed, DedupKey: "old", Title: "old again", ContentHTML: "<p>old</p>", PublishedAt: ptrTime(old), FetchedAt: old},
	})
	if err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	if len(newItems) != 0 {
		t.Fatalf("pruned key re-emitted as new: %+v", newItems)
	}
}

func TestPruneItemsByMaxPerFeed(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	const feed = "https://blog.example/feed.xml"
	addTestFeed(t, s, feed)

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var items []core.Item
	for i := 0; i < 5; i++ {
		ts := base.Add(time.Duration(i) * time.Hour)
		items = append(items, core.Item{
			FeedURL: feed, DedupKey: fmt.Sprintf("k%d", i),
			Title: fmt.Sprintf("item %d", i), PublishedAt: ptrTime(ts), FetchedAt: ts,
		})
	}
	if _, err := s.UpsertItems(ctx, feed, items); err != nil {
		t.Fatalf("UpsertItems: %v", err)
	}

	deleted, err := s.PruneItems(ctx, core.PrunePolicy{MaxPerFeed: 2})
	if err != nil {
		t.Fatalf("PruneItems: %v", err)
	}
	if deleted != 3 {
		t.Fatalf("deleted = %d, want 3", deleted)
	}

	qr, err := s.QueryItems(ctx, core.ItemQuery{
		Feeds: []string{feed}, Order: core.ItemOrder{Field: "published", Desc: true},
	})
	if err != nil {
		t.Fatalf("QueryItems: %v", err)
	}
	got := qr.Items
	if len(got) != 2 || got[0].DedupKey != "k4" || got[1].DedupKey != "k3" {
		t.Fatalf("kept wrong items: %+v", got)
	}
}

func TestRemoveFeedCascades(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	const feed = "https://blog.example/feed.xml"
	addTestFeed(t, s, feed)

	if _, err := s.UpsertItems(ctx, feed, []core.Item{
		{FeedURL: feed, DedupKey: "live", Title: "live", FetchedAt: testNow},
		{FeedURL: feed, DedupKey: "doomed", Title: "doomed", FetchedAt: testNow},
	}); err != nil {
		t.Fatalf("UpsertItems: %v", err)
	}
	// Tombstone one so we prove removal clears tombstoned rows too.
	if _, err := s.PruneItems(ctx, core.PrunePolicy{MaxPerFeed: 1}); err != nil {
		t.Fatalf("PruneItems: %v", err)
	}

	if err := s.RemoveFeed(ctx, feed); err != nil {
		t.Fatalf("RemoveFeed: %v", err)
	}
	if _, err := s.GetFeed(ctx, feed); err == nil {
		t.Error("feed still present after RemoveFeed")
	}

	// A fresh feed with the same URL must see no leftover items (live or
	// tombstoned): re-upserting both keys returns both as new.
	addTestFeed(t, s, feed)
	newItems, err := s.UpsertItems(ctx, feed, []core.Item{
		{FeedURL: feed, DedupKey: "live", Title: "live", FetchedAt: testNow},
		{FeedURL: feed, DedupKey: "doomed", Title: "doomed", FetchedAt: testNow},
	})
	if err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	if len(newItems) != 2 {
		t.Fatalf("RemoveFeed left rows behind; re-upsert returned %d new, want 2", len(newItems))
	}
}

func TestOpenUnwritablePath(t *testing.T) {
	// A path whose parent directory does not exist cannot be created.
	bad := filepath.Join(t.TempDir(), "missing-dir", "feedwatch.db")
	s, err := sqlite.Open(bad)
	if s != nil {
		_ = s.Close()
	}
	if err == nil {
		t.Fatal("expected error opening unwritable path, got nil")
	}
	if !errors.Is(err, core.ErrStoreUnavailable) {
		t.Errorf("error = %v, want errors.Is(core.ErrStoreUnavailable)", err)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	s := newStore(t) // newStore already migrated once
	ctx := context.Background()

	v, err := s.SchemaVersion(ctx)
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if v < 1 {
		t.Fatalf("SchemaVersion = %d, want >= 1 after migrate", v)
	}

	applied, err := s.Migrate(ctx)
	if err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	if applied != 0 {
		t.Errorf("second Migrate applied %d, want 0 (idempotent)", applied)
	}
}

func TestSetValidatorsSkipsEmpty(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	const feed = "https://blog.example/feed.xml"
	addTestFeed(t, s, feed)

	if err := s.SetValidators(ctx, feed, "etag-1", "Mon, 01 Jun 2026 00:00:00 GMT"); err != nil {
		t.Fatalf("SetValidators: %v", err)
	}
	// An empty value must not overwrite the stored one.
	if err := s.SetValidators(ctx, feed, "", ""); err != nil {
		t.Fatalf("SetValidators empty: %v", err)
	}
	f, err := s.GetFeed(ctx, feed)
	if err != nil {
		t.Fatalf("GetFeed: %v", err)
	}
	if f.ETag != "etag-1" || f.LastModified != "Mon, 01 Jun 2026 00:00:00 GMT" {
		t.Errorf("empty values overwrote validators: %+v", f)
	}

	// A new etag updates only that validator.
	if err := s.SetValidators(ctx, feed, "etag-2", ""); err != nil {
		t.Fatalf("SetValidators etag-2: %v", err)
	}
	f, err = s.GetFeed(ctx, feed)
	if err != nil {
		t.Fatalf("GetFeed: %v", err)
	}
	if f.ETag != "etag-2" || f.LastModified != "Mon, 01 Jun 2026 00:00:00 GMT" {
		t.Errorf("validator update wrong: %+v", f)
	}
}

func TestListFeedsFilterByStatus(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	addTestFeed(t, s, "https://a.example/feed")
	addTestFeed(t, s, "https://b.example/feed")
	if err := s.SetStatus(ctx, "https://b.example/feed", core.FeedDisabled); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	active, err := s.ListFeeds(ctx, core.ListFilter{Status: core.FeedActive})
	if err != nil {
		t.Fatalf("ListFeeds: %v", err)
	}
	if len(active) != 1 || active[0].URL != "https://a.example/feed" {
		t.Fatalf("active filter returned %+v", active)
	}
}

func TestRecordSuccessRewritesURLAndCascadesItems(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	const oldURL = "https://blog.example/redirect"
	const newURL = "https://blog.example/feed.xml"
	addTestFeed(t, s, oldURL)
	if _, err := s.UpsertItems(ctx, oldURL, []core.Item{
		{FeedURL: oldURL, DedupKey: "a", Title: "A", FetchedAt: testNow},
	}); err != nil {
		t.Fatalf("UpsertItems: %v", err)
	}

	renamedTo, err := s.RecordSuccess(ctx, oldURL, testNow, testNow.Add(time.Hour), newURL)
	if err != nil {
		t.Fatalf("RecordSuccess: %v", err)
	}
	if renamedTo != newURL {
		t.Errorf("renamedTo = %q, want %q", renamedTo, newURL)
	}

	if _, err := s.GetFeed(ctx, oldURL); err == nil {
		t.Fatalf("feed still resolvable under old URL %q", oldURL)
	}
	got, err := s.GetFeed(ctx, newURL)
	if err != nil {
		t.Fatalf("GetFeed %q: %v", newURL, err)
	}
	if got.URL != newURL {
		t.Errorf("URL = %q, want %q", got.URL, newURL)
	}

	items, err := s.QueryItems(ctx, core.ItemQuery{Feeds: []string{newURL}})
	if err != nil {
		t.Fatalf("QueryItems: %v", err)
	}
	if len(items.Items) != 1 {
		t.Fatalf("items under new URL = %d, want 1", len(items.Items))
	}
}

func TestRecordSuccessSkipsRewriteWhenTargetSubscribed(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	const oldURL = "https://blog.example/redirect"
	const newURL = "https://blog.example/feed.xml"
	addTestFeed(t, s, oldURL)
	addTestFeed(t, s, newURL)

	renamedTo, err := s.RecordSuccess(ctx, oldURL, testNow, testNow.Add(time.Hour), newURL)
	if err != nil {
		t.Fatalf("RecordSuccess: %v", err)
	}
	if renamedTo != "" {
		t.Errorf("renamedTo = %q, want \"\" (rename declined, target subscribed)", renamedTo)
	}

	// Both subscriptions survive: no merge, original kept.
	if _, err := s.GetFeed(ctx, oldURL); err != nil {
		t.Errorf("original URL gone: %v", err)
	}
	if _, err := s.GetFeed(ctx, newURL); err != nil {
		t.Errorf("target URL gone: %v", err)
	}
}

func TestRecordSuccessSameURLIsNoRename(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	const feed = "https://blog.example/feed.xml"
	addTestFeed(t, s, feed)

	renamedTo, err := s.RecordSuccess(ctx, feed, testNow, testNow.Add(time.Hour), feed)
	if err != nil {
		t.Fatalf("RecordSuccess: %v", err)
	}
	if renamedTo != "" {
		t.Errorf("renamedTo = %q, want \"\" (same URL is no rename)", renamedTo)
	}
	got, err := s.GetFeed(ctx, feed)
	if err != nil {
		t.Fatalf("GetFeed: %v", err)
	}
	if got.URL != feed {
		t.Errorf("URL = %q, want %q", got.URL, feed)
	}
}

// addTaggedFeed subscribes url carrying tags, so a lane fixture reads as one
// line per feed.
func addTaggedFeed(t *testing.T, s *sqlite.Store, url string, tags ...string) {
	t.Helper()
	if _, err := s.AddFeed(context.Background(), core.Feed{URL: url, Tags: tags}); err != nil {
		t.Fatalf("AddFeed %q: %v", url, err)
	}
}

// feedURLs collects the URLs of a feed listing in order, for set assertions.
func feedURLs(feeds []core.Feed) []string {
	out := make([]string, 0, len(feeds))
	for _, f := range feeds {
		out = append(out, f.URL)
	}
	return out
}

// AddFeed stores a feed's tags in canonical form and GetFeed reads them back,
// proving the column, the write-side canonicalization, and the scan path.
func TestAddFeedRoundTripsCanonicalTags(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	const feed = "https://blog.example/feed.xml"

	got, err := s.AddFeed(ctx, core.Feed{URL: feed, Tags: []string{"AI", "agents", "ai"}})
	if err != nil {
		t.Fatalf("AddFeed: %v", err)
	}
	want := []string{"agents", "ai"}
	if !slices.Equal(got.Tags, want) {
		t.Errorf("AddFeed Tags = %v, want %v", got.Tags, want)
	}
	reread, err := s.GetFeed(ctx, feed)
	if err != nil {
		t.Fatalf("GetFeed: %v", err)
	}
	if !slices.Equal(reread.Tags, want) {
		t.Errorf("GetFeed Tags = %v, want %v", reread.Tags, want)
	}
}

// A feed added with no tags reads back with an empty, non-nil tag set, so a
// caller can range and marshal it without a nil check.
func TestAddFeedUntaggedReadsBackEmptyNonNil(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	const feed = "https://blog.example/feed.xml"
	addTestFeed(t, s, feed)

	got, err := s.GetFeed(ctx, feed)
	if err != nil {
		t.Fatalf("GetFeed: %v", err)
	}
	if got.Tags == nil {
		t.Fatal("Tags = nil, want non-nil empty slice")
	}
	if len(got.Tags) != 0 {
		t.Errorf("Tags = %v, want empty", got.Tags)
	}
}

// Re-adding an existing feed without tags preserves the stored set: the upsert
// deliberately omits tags from its DO UPDATE clause, so a routine re-add never
// drops a feed out of its lanes.
func TestAddFeedReAddPreservesTags(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	const feed = "https://blog.example/feed.xml"
	addTaggedFeed(t, s, feed, "ai", "agents")

	got, err := s.AddFeed(ctx, core.Feed{URL: feed, Alias: "blog"})
	if err != nil {
		t.Fatalf("re-AddFeed: %v", err)
	}
	if got.Alias != "blog" {
		t.Errorf("Alias = %q, want the new alias to be applied", got.Alias)
	}
	if want := []string{"agents", "ai"}; !slices.Equal(got.Tags, want) {
		t.Errorf("Tags = %v, want the stored set %v preserved", got.Tags, want)
	}
}

// SetTags replaces a feed's whole tag set, canonicalizing on write, and clears
// it to an empty set when given no tags.
func TestSetTagsReplacesAndClears(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	const feed = "https://blog.example/feed.xml"
	addTaggedFeed(t, s, feed, "ai")

	if err := s.SetTags(ctx, feed, []string{"Research", "go", "research"}); err != nil {
		t.Fatalf("SetTags: %v", err)
	}
	got, err := s.GetFeed(ctx, feed)
	if err != nil {
		t.Fatalf("GetFeed: %v", err)
	}
	if want := []string{"go", "research"}; !slices.Equal(got.Tags, want) {
		t.Errorf("Tags = %v, want %v", got.Tags, want)
	}

	if err := s.SetTags(ctx, feed, nil); err != nil {
		t.Fatalf("SetTags clear: %v", err)
	}
	got, err = s.GetFeed(ctx, feed)
	if err != nil {
		t.Fatalf("GetFeed after clear: %v", err)
	}
	if got.Tags == nil || len(got.Tags) != 0 {
		t.Errorf("Tags = %v, want an empty non-nil set", got.Tags)
	}
}

// ListFeeds honors the filter's tags under both match modes and combines them
// with the status filter. The fixture carries a feed in both lanes, a feed in
// one, and an untagged feed, so the two modes give different answers.
func TestListFeedsFiltersByTag(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	const (
		both = "https://both.example/feed"
		one  = "https://one.example/feed"
		none = "https://none.example/feed"
	)
	addTaggedFeed(t, s, both, "ai", "agents")
	addTaggedFeed(t, s, one, "ai")
	addTestFeed(t, s, none)

	tests := []struct {
		name   string
		filter core.ListFilter
		want   []string
	}{
		{"single tag excludes untagged", core.ListFilter{Tags: []string{"ai"}}, []string{both, one}},
		{"match all requires every tag", core.ListFilter{Tags: []string{"ai", "agents"}, Match: core.MatchAll}, []string{both}},
		{"match any requires one tag", core.ListFilter{Tags: []string{"ai", "agents"}, Match: core.MatchAny}, []string{both, one}},
		{"default match is all", core.ListFilter{Tags: []string{"ai", "agents"}}, []string{both}},
		{"tags are canonicalized", core.ListFilter{Tags: []string{"AI", "ai"}}, []string{both, one}},
		{"unknown tag matches nothing", core.ListFilter{Tags: []string{"nope"}}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := s.ListFeeds(ctx, tt.filter)
			if err != nil {
				t.Fatalf("ListFeeds: %v", err)
			}
			if !slices.Equal(feedURLs(got), tt.want) {
				t.Errorf("ListFeeds = %v, want %v", feedURLs(got), tt.want)
			}
		})
	}
}

// A tag filter and a status filter compose: only feeds matching both are
// listed.
func TestListFeedsCombinesTagAndStatus(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	const (
		activeInLane   = "https://a-active.example/feed"
		disabledInLane = "https://b-disabled.example/feed"
		disabledOut    = "https://c-disabled.example/feed"
	)
	addTaggedFeed(t, s, activeInLane, "ai")
	addTaggedFeed(t, s, disabledInLane, "ai")
	addTestFeed(t, s, disabledOut)
	for _, url := range []string{disabledInLane, disabledOut} {
		if err := s.SetStatus(ctx, url, core.FeedDisabled); err != nil {
			t.Fatalf("SetStatus %q: %v", url, err)
		}
	}

	got, err := s.ListFeeds(ctx, core.ListFilter{Status: core.FeedDisabled, Tags: []string{"ai"}})
	if err != nil {
		t.Fatalf("ListFeeds: %v", err)
	}
	if want := []string{disabledInLane}; !slices.Equal(feedURLs(got), want) {
		t.Errorf("ListFeeds = %v, want %v", feedURLs(got), want)
	}
}

// DueFeeds narrows by the filter's tags, so a lane can be polled on its own
// schedule: an out-of-lane due feed and an in-lane not-due feed are both
// excluded.
func TestDueFeedsFiltersByTag(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	const (
		dueInLane    = "https://a-due.example/feed"
		notDueInLane = "https://b-notdue.example/feed"
		dueOutOfLane = "https://c-due.example/feed"
	)
	addTaggedFeed(t, s, dueInLane, "ai")
	addTaggedFeed(t, s, notDueInLane, "ai")
	addTestFeed(t, s, dueOutOfLane)
	if _, err := s.RecordSuccess(ctx, notDueInLane, now, now.Add(time.Hour), ""); err != nil {
		t.Fatalf("RecordSuccess: %v", err)
	}

	got, err := s.DueFeeds(ctx, now, core.ListFilter{Tags: []string{"ai"}})
	if err != nil {
		t.Fatalf("DueFeeds: %v", err)
	}
	if want := []string{dueInLane}; !slices.Equal(feedURLs(got), want) {
		t.Errorf("DueFeeds = %v, want %v", feedURLs(got), want)
	}
}

// TagCounts reports the lane vocabulary sorted by tag with the number of
// subscriptions carrying each, counting feeds of any status, and returns an
// empty result when nothing is tagged.
func TestTagCounts(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	got, err := s.TagCounts(ctx)
	if err != nil {
		t.Fatalf("TagCounts: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("TagCounts on an untagged store = %v, want empty", got)
	}

	addTaggedFeed(t, s, "https://a.example/feed", "ai", "agents")
	addTaggedFeed(t, s, "https://b.example/feed", "ai")
	addTestFeed(t, s, "https://c.example/feed")
	if err := s.SetStatus(ctx, "https://b.example/feed", core.FeedDisabled); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	got, err = s.TagCounts(ctx)
	if err != nil {
		t.Fatalf("TagCounts: %v", err)
	}
	want := []core.TagCount{{Tag: "agents", Feeds: 1}, {Tag: "ai", Feeds: 2}}
	if !slices.Equal(got, want) {
		t.Errorf("TagCounts = %v, want %v", got, want)
	}
}

// tagBase anchors the interleaved publication times of the tag fixture.
var tagBase = time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)

// tagFixture subscribes three feeds — one in both lanes, one in the "ai" lane
// only, one untagged — each carrying four items whose publication times
// interleave across feeds. The interleaving is what makes a page of the
// tag-filtered set differ from the same page of the unfiltered set. Dedup keys
// encode the hour offset, so they sort in publication order.
func tagFixture(t *testing.T, s *sqlite.Store) (both, one, none string) {
	t.Helper()
	ctx := context.Background()
	both = "https://both.example/feed"
	one = "https://one.example/feed"
	none = "https://none.example/feed"
	addTaggedFeed(t, s, both, "ai", "agents")
	addTaggedFeed(t, s, one, "ai")
	addTestFeed(t, s, none)

	for _, f := range []struct {
		url   string
		hours []int
	}{
		{both, []int{1, 3, 5, 7}},
		{one, []int{2, 4, 6, 8}},
		{none, []int{0, 9, 10, 11}},
	} {
		items := make([]core.Item, 0, len(f.hours))
		for _, h := range f.hours {
			ts := tagBase.Add(time.Duration(h) * time.Hour)
			items = append(items, core.Item{
				FeedURL: f.url, DedupKey: fmt.Sprintf("k%02d", h),
				Title:       fmt.Sprintf("item %02d", h),
				ContentText: fmt.Sprintf("body of item %02d", h),
				PublishedAt: ptrTime(ts), FetchedAt: ts,
			})
		}
		if _, err := s.UpsertItems(ctx, f.url, items); err != nil {
			t.Fatalf("UpsertItems %q: %v", f.url, err)
		}
	}
	return both, one, none
}

// itemKeys collects the dedup keys of a result in order, for set assertions.
func itemKeys(items []core.Item) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.DedupKey)
	}
	return out
}

// QueryItems narrows to items whose feed carries the requested tags, under both
// match modes; items themselves are untagged, so the lane is inherited from the
// subscription.
func TestQueryItemsFiltersByTag(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	tagFixture(t, s)

	inLane := []string{"k01", "k02", "k03", "k04", "k05", "k06", "k07", "k08"}
	bothOnly := []string{"k01", "k03", "k05", "k07"}

	tests := []struct {
		name  string
		query core.ItemQuery
		want  []string
	}{
		{"single tag excludes untagged feed", core.ItemQuery{Tags: []string{"ai"}}, inLane},
		{"match all requires every tag", core.ItemQuery{Tags: []string{"ai", "agents"}, Match: core.MatchAll}, bothOnly},
		{"match any requires one tag", core.ItemQuery{Tags: []string{"ai", "agents"}, Match: core.MatchAny}, inLane},
		{"default match is all", core.ItemQuery{Tags: []string{"ai", "agents"}}, bothOnly},
		{"tags are canonicalized", core.ItemQuery{Tags: []string{"AI", "ai"}}, inLane},
		{"unknown tag matches nothing", core.ItemQuery{Tags: []string{"nope"}}, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := s.QueryItems(ctx, tt.query)
			if err != nil {
				t.Fatalf("QueryItems: %v", err)
			}
			if !slices.Equal(itemKeys(got.Items), tt.want) {
				t.Errorf("QueryItems = %v, want %v", itemKeys(got.Items), tt.want)
			}
		})
	}
}

// A tag filter intersects with the other item predicates rather than widening
// them: --feed plus --tag means "from this feed, which must also be in the
// lane".
func TestQueryItemsTagComposesWithOtherFilters(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	both, one, none := tagFixture(t, s)

	tests := []struct {
		name  string
		query core.ItemQuery
		want  []string
	}{
		{
			"feed inside the lane intersects",
			core.ItemQuery{Feeds: []string{both}, Tags: []string{"ai"}},
			[]string{"k01", "k03", "k05", "k07"},
		},
		{
			"feed outside the lane yields nothing",
			core.ItemQuery{Feeds: []string{none}, Tags: []string{"ai"}},
			[]string{},
		},
		{
			"feed lacking the second tag yields nothing",
			core.ItemQuery{Feeds: []string{one}, Tags: []string{"ai", "agents"}},
			[]string{},
		},
		{
			"tag narrows a date window",
			core.ItemQuery{
				Tags:  []string{"ai"},
				Since: ptrTime(tagBase.Add(4 * time.Hour)),
				Until: ptrTime(tagBase.Add(9 * time.Hour)),
			},
			[]string{"k04", "k05", "k06", "k07", "k08"},
		},
		{
			"tag narrows a substring match",
			core.ItemQuery{Tags: []string{"ai"}, Contains: "item 0"},
			[]string{"k01", "k02", "k03", "k04", "k05", "k06", "k07", "k08"},
		},
		{
			"tag narrows a substring match to one feed's body",
			core.ItemQuery{Tags: []string{"ai", "agents"}, Contains: "body of item 05"},
			[]string{"k05"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := s.QueryItems(ctx, tt.query)
			if err != nil {
				t.Fatalf("QueryItems: %v", err)
			}
			if !slices.Equal(itemKeys(got.Items), tt.want) {
				t.Errorf("QueryItems = %v, want %v", itemKeys(got.Items), tt.want)
			}
		})
	}
}

// The tag filter is applied in SQL before LIMIT/OFFSET, so a page is the nth
// page of the filtered set. A Go-side filter applied after pagination would
// return the unfiltered page's survivors instead, which the interleaved fixture
// makes a different answer.
func TestQueryItemsTagFilterPaginatesOverFilteredSet(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	tagFixture(t, s)

	got, err := s.QueryItems(ctx, core.ItemQuery{Tags: []string{"ai"}, Limit: 2, Offset: 2})
	if err != nil {
		t.Fatalf("QueryItems: %v", err)
	}
	// Filtered ascending order is k01 k02 k03 k04 k05 k06 k07 k08, so the third
	// and fourth are k03 and k04; unfiltered it would be k02 and k03.
	if want := []string{"k03", "k04"}; !slices.Equal(itemKeys(got.Items), want) {
		t.Errorf("page = %v, want %v", itemKeys(got.Items), want)
	}
}

// OmittedNoDate counts only dateless items inside the tag filter: an undated
// item on an out-of-lane feed never inflates the count. This proves
// countOmittedNoDate picked the tag clause up from the shared non-date filters.
func TestQueryItemsOmittedNoDateHonorsTag(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	both, _, none := tagFixture(t, s)

	for _, url := range []string{both, none} {
		if _, err := s.UpsertItems(ctx, url, []core.Item{
			{FeedURL: url, DedupKey: "undated", Title: "undated", FetchedAt: tagBase},
		}); err != nil {
			t.Fatalf("UpsertItems %q: %v", url, err)
		}
	}

	got, err := s.QueryItems(ctx, core.ItemQuery{
		Tags: []string{"ai"}, Since: ptrTime(tagBase), TimeField: "published",
	})
	if err != nil {
		t.Fatalf("QueryItems: %v", err)
	}
	if got.OmittedNoDate != 1 {
		t.Errorf("OmittedNoDate = %d, want 1 (only the in-lane undated item)", got.OmittedNoDate)
	}
}

// A tag-scoped age prune tombstones only in-lane items; an equally old
// out-of-lane item survives, and the pruned key keeps its fingerprint so a
// re-poll never re-emits it as new.
func TestPruneItemsByAgeHonorsTag(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	both, _, _ := tagFixture(t, s)

	cutoff := tagBase.Add(4 * time.Hour)
	pruned, err := s.PruneItems(ctx, core.PrunePolicy{KeepBefore: &cutoff, Tags: []string{"ai"}})
	if err != nil {
		t.Fatalf("PruneItems: %v", err)
	}
	// In-lane items before the cutoff: k01, k03 (both) and k02 (one).
	if pruned != 3 {
		t.Fatalf("pruned = %d, want 3", pruned)
	}

	got, err := s.QueryItems(ctx, core.ItemQuery{})
	if err != nil {
		t.Fatalf("QueryItems: %v", err)
	}
	want := []string{"k00", "k04", "k05", "k06", "k07", "k08", "k09", "k10", "k11"}
	if !slices.Equal(itemKeys(got.Items), want) {
		t.Fatalf("surviving items = %v, want %v", itemKeys(got.Items), want)
	}

	// The out-of-lane feed's oldest item is older than every pruned one and is
	// still live, so the scope really bound the age pass.
	if !slices.Contains(itemKeys(got.Items), "k00") {
		t.Errorf("out-of-lane item k00 was pruned by an in-lane scope")
	}

	newItems, err := s.UpsertItems(ctx, both, []core.Item{
		{FeedURL: both, DedupKey: "k01", Title: "item 01 again", FetchedAt: tagBase},
	})
	if err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	if len(newItems) != 0 {
		t.Errorf("pruned in-lane key re-emitted as new: %+v", newItems)
	}
}

// A tag-scoped max-per-feed prune keeps N per in-lane feed and leaves
// out-of-lane feeds entirely untouched. The ROW_NUMBER() window must rank each
// feed's rows within the scope; an unscoped window source would rank in-lane
// rows against out-of-lane rows and tombstone the wrong items.
func TestPruneItemsByMaxPerFeedHonorsTag(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	tagFixture(t, s)

	pruned, err := s.PruneItems(ctx, core.PrunePolicy{MaxPerFeed: 2, Tags: []string{"ai"}})
	if err != nil {
		t.Fatalf("PruneItems: %v", err)
	}
	// Two in-lane feeds of four items each keep their two newest.
	if pruned != 4 {
		t.Fatalf("pruned = %d, want 4", pruned)
	}

	got, err := s.QueryItems(ctx, core.ItemQuery{})
	if err != nil {
		t.Fatalf("QueryItems: %v", err)
	}
	want := []string{"k00", "k05", "k06", "k07", "k08", "k09", "k10", "k11"}
	if !slices.Equal(itemKeys(got.Items), want) {
		t.Errorf("surviving items = %v, want %v", itemKeys(got.Items), want)
	}
}

// A tag matching no feed prunes nothing rather than falling back to the whole
// store.
func TestPruneItemsUnknownTagPrunesNothing(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	tagFixture(t, s)

	cutoff := tagBase.Add(24 * time.Hour)
	pruned, err := s.PruneItems(ctx, core.PrunePolicy{
		KeepBefore: &cutoff, MaxPerFeed: 1, Tags: []string{"nope"},
	})
	if err != nil {
		t.Fatalf("PruneItems: %v", err)
	}
	if pruned != 0 {
		t.Errorf("pruned = %d, want 0", pruned)
	}
}
