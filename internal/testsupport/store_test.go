package testsupport_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/andreswebs/feedwatch/core"
	"github.com/andreswebs/feedwatch/internal/testsupport"
	"github.com/andreswebs/feedwatch/store"
)

// Compile-time conformance: the double satisfies the consumer interface.
var _ store.Store = (*testsupport.InMemoryStore)(nil)

func newStore(t *testing.T) *testsupport.InMemoryStore {
	t.Helper()
	clk := testsupport.FixedClock(time.Date(2026, 6, 27, 10, 0, 0, 0, time.UTC))
	return testsupport.NewInMemoryStore(clk)
}

func TestInMemoryStoreRoundTripsFeedAndItems(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	const url = "https://blog.example/feed.xml"
	if _, err := s.AddFeed(ctx, core.Feed{URL: url, Alias: "ex"}); err != nil {
		t.Fatalf("AddFeed: %v", err)
	}

	got, err := s.GetFeed(ctx, "ex") // resolve by alias
	if err != nil {
		t.Fatalf("GetFeed: %v", err)
	}
	if got.URL != url || got.Status != core.FeedActive {
		t.Errorf("feed = %+v, want url %q active", got, url)
	}

	items := []core.Item{
		{DedupKey: "a", Title: "First", Link: "https://blog.example/a"},
		{DedupKey: "b", Title: "Second", Link: "https://blog.example/b"},
	}
	newItems, err := s.UpsertItems(ctx, url, items)
	if err != nil {
		t.Fatalf("UpsertItems: %v", err)
	}
	if len(newItems) != 2 {
		t.Fatalf("new items = %d, want 2", len(newItems))
	}

	res, err := s.QueryItems(ctx, core.ItemQuery{Feeds: []string{url}})
	if err != nil {
		t.Fatalf("QueryItems: %v", err)
	}
	if len(res.Items) != 2 {
		t.Errorf("queried items = %d, want 2", len(res.Items))
	}
}

// TestInMemoryStoreTimeFieldAxis mirrors the SQLite fetch-axis behavior: the
// publication axis filters on published_at, the fetch axis on fetched_at, so an
// item published before the window but fetched inside it is excluded on the
// publication axis and included on the fetch axis.
func TestInMemoryStoreTimeFieldAxis(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	const url = "https://blog.example/feed.xml"
	if _, err := s.AddFeed(ctx, core.Feed{URL: url}); err != nil {
		t.Fatalf("AddFeed: %v", err)
	}

	oldPub := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	recentFetch := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	if _, err := s.UpsertItems(ctx, url, []core.Item{
		{DedupKey: "late", Title: "late", PublishedAt: &oldPub, FetchedAt: recentFetch},
	}); err != nil {
		t.Fatalf("UpsertItems: %v", err)
	}

	cutoff := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	published, err := s.QueryItems(ctx, core.ItemQuery{Since: &cutoff, TimeField: "published"})
	if err != nil {
		t.Fatalf("QueryItems published: %v", err)
	}
	if len(published.Items) != 0 {
		t.Errorf("publication axis should exclude item published in jan; got %+v", published.Items)
	}

	fetched, err := s.QueryItems(ctx, core.ItemQuery{Since: &cutoff, TimeField: "fetched"})
	if err != nil {
		t.Fatalf("QueryItems fetched: %v", err)
	}
	if len(fetched.Items) != 1 || fetched.Items[0].DedupKey != "late" {
		t.Errorf("fetch axis should include item fetched in may; got %+v", fetched.Items)
	}
}

// TestInMemoryStoreOmittedNoDate mirrors the SQLite parity case: a
// publication-axis date window excludes null-publication items and counts them,
// the fetch axis and an unfiltered query report zero, and publication-axis
// ordering places dateless items last under desc and first under asc.
func TestInMemoryStoreOmittedNoDate(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	const url = "https://blog.example/feed.xml"
	if _, err := s.AddFeed(ctx, core.Feed{URL: url}); err != nil {
		t.Fatalf("AddFeed: %v", err)
	}

	dated := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	fetched := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	if _, err := s.UpsertItems(ctx, url, []core.Item{
		{DedupKey: "dated", Title: "dated", PublishedAt: &dated, FetchedAt: dated},
		{DedupKey: "nopub1", Title: "nopub1", FetchedAt: fetched},
		{DedupKey: "nopub2", Title: "nopub2", FetchedAt: fetched},
	}); err != nil {
		t.Fatalf("UpsertItems: %v", err)
	}

	cutoff := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	pub, err := s.QueryItems(ctx, core.ItemQuery{Since: &cutoff, TimeField: "published"})
	if err != nil {
		t.Fatalf("QueryItems published: %v", err)
	}
	if len(pub.Items) != 1 || pub.Items[0].DedupKey != "dated" {
		t.Fatalf("publication axis should return only the dated item; got %+v", pub.Items)
	}
	if pub.OmittedNoDate != 2 {
		t.Errorf("publication axis OmittedNoDate = %d, want 2", pub.OmittedNoDate)
	}

	fetch, err := s.QueryItems(ctx, core.ItemQuery{Since: &cutoff, TimeField: "fetched"})
	if err != nil {
		t.Fatalf("QueryItems fetched: %v", err)
	}
	if len(fetch.Items) != 3 || fetch.OmittedNoDate != 0 {
		t.Errorf("fetch axis: items=%d omitted=%d, want 3 and 0", len(fetch.Items), fetch.OmittedNoDate)
	}

	all, err := s.QueryItems(ctx, core.ItemQuery{})
	if err != nil {
		t.Fatalf("QueryItems all: %v", err)
	}
	if all.OmittedNoDate != 0 {
		t.Errorf("unfiltered OmittedNoDate = %d, want 0", all.OmittedNoDate)
	}

	desc, err := s.QueryItems(ctx, core.ItemQuery{Order: core.ItemOrder{Field: "published", Desc: true}})
	if err != nil {
		t.Fatalf("QueryItems desc: %v", err)
	}
	if len(desc.Items) != 3 || desc.Items[0].DedupKey != "dated" {
		t.Errorf("publication desc should lead with the dated item; got %+v", desc.Items)
	}
	if desc.Items[2].PublishedAt != nil {
		t.Errorf("publication desc should place a dateless item last; got %+v", desc.Items)
	}

	asc, err := s.QueryItems(ctx, core.ItemQuery{Order: core.ItemOrder{Field: "published", Desc: false}})
	if err != nil {
		t.Fatalf("QueryItems asc: %v", err)
	}
	if len(asc.Items) != 3 || asc.Items[2].DedupKey != "dated" {
		t.Errorf("publication asc should place the dated item last; got %+v", asc.Items)
	}
	if asc.Items[0].PublishedAt != nil {
		t.Errorf("publication asc should place a dateless item first; got %+v", asc.Items)
	}
}

func TestInMemoryStoreGetUnknownFeedIsUsageError(t *testing.T) {
	s := newStore(t)
	_, err := s.GetFeed(context.Background(), "https://nope.example/feed")
	var fe *core.FeedError
	if !errors.As(err, &fe) || fe.Category != core.CatUsage {
		t.Fatalf("err = %v, want usage-category FeedError", err)
	}
}

func TestInMemoryStoreAliasConflictIsUsageError(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	if _, err := s.AddFeed(ctx, core.Feed{URL: "https://a.example/feed", Alias: "dup"}); err != nil {
		t.Fatalf("AddFeed a: %v", err)
	}
	_, err := s.AddFeed(ctx, core.Feed{URL: "https://b.example/feed", Alias: "dup"})
	var fe *core.FeedError
	if !errors.As(err, &fe) || fe.Category != core.CatUsage {
		t.Fatalf("err = %v, want usage-category FeedError", err)
	}
}

func TestInMemoryStoreUpsertDedupsOnSecondPoll(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	const url = "https://blog.example/feed.xml"
	items := []core.Item{{DedupKey: "a", Title: "First"}}

	first, err := s.UpsertItems(ctx, url, items)
	if err != nil || len(first) != 1 {
		t.Fatalf("first upsert = %v, %v; want 1 new", first, err)
	}
	second, err := s.UpsertItems(ctx, url, items)
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if len(second) != 0 {
		t.Errorf("second upsert returned %d new items, want 0", len(second))
	}
}

func TestInMemoryStoreDueFeedsHonorsStatusAndSchedule(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	now := time.Date(2026, 6, 27, 10, 0, 0, 0, time.UTC)
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)

	if _, err := s.AddFeed(ctx, core.Feed{URL: "https://due.example/feed"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddFeed(ctx, core.Feed{URL: "https://notdue.example/feed"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddFeed(ctx, core.Feed{URL: "https://off.example/feed"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordSuccess(ctx, "https://due.example/feed", past, past, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordSuccess(ctx, "https://notdue.example/feed", past, future, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.SetStatus(ctx, "https://off.example/feed", core.FeedDisabled); err != nil {
		t.Fatal(err)
	}

	due, err := s.DueFeeds(ctx, now, core.ListFilter{})
	if err != nil {
		t.Fatalf("DueFeeds: %v", err)
	}
	if len(due) != 1 || due[0].URL != "https://due.example/feed" {
		t.Errorf("due = %+v, want only the past-due active feed", due)
	}
}

func TestInMemoryStoreSetValidatorsSkipsEmpty(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	const url = "https://blog.example/feed.xml"
	if _, err := s.AddFeed(ctx, core.Feed{URL: url}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetValidators(ctx, url, `"etag1"`, "Mon, 02 Jan 2006 15:04:05 GMT"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetValidators(ctx, url, "", ""); err != nil {
		t.Fatal(err)
	}
	f, err := s.GetFeed(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	if f.ETag != `"etag1"` || f.LastModified != "Mon, 02 Jan 2006 15:04:05 GMT" {
		t.Errorf("validators = %q / %q, want preserved (empty must not overwrite)", f.ETag, f.LastModified)
	}
}

func TestInMemoryStorePruneByMaxPerFeedPreservesDedup(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	const url = "https://blog.example/feed.xml"
	mk := func(key string, day int) core.Item {
		pub := time.Date(2026, 6, day, 0, 0, 0, 0, time.UTC)
		return core.Item{DedupKey: key, Title: key, PublishedAt: &pub}
	}
	items := []core.Item{mk("a", 1), mk("b", 2), mk("c", 3)}
	if _, err := s.UpsertItems(ctx, url, items); err != nil {
		t.Fatal(err)
	}

	deleted, err := s.PruneItems(ctx, core.PrunePolicy{MaxPerFeed: 1})
	if err != nil {
		t.Fatalf("PruneItems: %v", err)
	}
	if deleted != 2 {
		t.Errorf("deleted = %d, want 2", deleted)
	}

	remaining, err := s.QueryItems(ctx, core.ItemQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining.Items) != 1 || remaining.Items[0].DedupKey != "c" {
		t.Errorf("remaining = %+v, want only newest item c", remaining.Items)
	}

	// A pruned item still advertised must not re-emit as new (dedup preserved).
	reNew, err := s.UpsertItems(ctx, url, []core.Item{mk("a", 1)})
	if err != nil {
		t.Fatal(err)
	}
	if len(reNew) != 0 {
		t.Errorf("re-upsert of pruned item returned %d new, want 0", len(reNew))
	}
}

// TestInMemoryStoreRecordSuccessReportsRename covers the RecordSuccess return
// value: a permanent-redirect rewrite to a fresh URL returns the new URL and
// cascades items, while a rewrite whose target is already subscribed is declined
// and returns "".
func TestInMemoryStoreRecordSuccessReportsRename(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 27, 10, 0, 0, 0, time.UTC)
	const oldURL = "https://blog.example/redirect"
	const newURL = "https://blog.example/feed.xml"

	t.Run("fresh target renames", func(t *testing.T) {
		s := newStore(t)
		if _, err := s.AddFeed(ctx, core.Feed{URL: oldURL}); err != nil {
			t.Fatalf("AddFeed: %v", err)
		}
		renamedTo, err := s.RecordSuccess(ctx, oldURL, now, now.Add(time.Hour), newURL)
		if err != nil {
			t.Fatalf("RecordSuccess: %v", err)
		}
		if renamedTo != newURL {
			t.Errorf("renamedTo = %q, want %q", renamedTo, newURL)
		}
		if _, err := s.GetFeed(ctx, newURL); err != nil {
			t.Errorf("feed not resolvable under new URL: %v", err)
		}
	})

	t.Run("target subscribed declines rename", func(t *testing.T) {
		s := newStore(t)
		if _, err := s.AddFeed(ctx, core.Feed{URL: oldURL}); err != nil {
			t.Fatalf("AddFeed old: %v", err)
		}
		if _, err := s.AddFeed(ctx, core.Feed{URL: newURL}); err != nil {
			t.Fatalf("AddFeed new: %v", err)
		}
		renamedTo, err := s.RecordSuccess(ctx, oldURL, now, now.Add(time.Hour), newURL)
		if err != nil {
			t.Fatalf("RecordSuccess: %v", err)
		}
		if renamedTo != "" {
			t.Errorf("renamedTo = %q, want \"\" (target already subscribed)", renamedTo)
		}
		if _, err := s.GetFeed(ctx, oldURL); err != nil {
			t.Errorf("original feed gone after declined rename: %v", err)
		}
	})

	t.Run("no rewrite target returns empty", func(t *testing.T) {
		s := newStore(t)
		if _, err := s.AddFeed(ctx, core.Feed{URL: oldURL}); err != nil {
			t.Fatalf("AddFeed: %v", err)
		}
		renamedTo, err := s.RecordSuccess(ctx, oldURL, now, now.Add(time.Hour), "")
		if err != nil {
			t.Fatalf("RecordSuccess: %v", err)
		}
		if renamedTo != "" {
			t.Errorf("renamedTo = %q, want \"\" (no rewrite)", renamedTo)
		}
	})
}

// ---------------------------------------------------------------------------
// Tag parity: each test below is the hand-written twin of a tag test in
// internal/store/sqlite/sqlite_test.go. There is no executable conformance
// suite, so a behavior proved on only one side is exactly the drift these
// twins exist to catch.
// ---------------------------------------------------------------------------

// ptrTime returns a pointer to t, for the optional publication time.
func ptrTime(t time.Time) *time.Time { return &t }

// addTaggedFeed subscribes url carrying tags, so a lane fixture reads as one
// line per feed.
func addTaggedFeed(t *testing.T, s *testsupport.InMemoryStore, url string, tags ...string) {
	t.Helper()
	if _, err := s.AddFeed(context.Background(), core.Feed{URL: url, Tags: tags}); err != nil {
		t.Fatalf("AddFeed %q: %v", url, err)
	}
}

// addUntaggedFeed subscribes url with no tags, the out-of-lane control.
func addUntaggedFeed(t *testing.T, s *testsupport.InMemoryStore, url string) {
	t.Helper()
	if _, err := s.AddFeed(context.Background(), core.Feed{URL: url}); err != nil {
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

// itemKeys collects the dedup keys of a result in order, for set assertions.
func itemKeys(items []core.Item) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.DedupKey)
	}
	return out
}

// tagBase anchors the interleaved publication times of the tag fixture.
var tagBase = time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)

// tagFixture mirrors the SQLite tag fixture: three feeds — one in both lanes,
// one in the "ai" lane only, one untagged — each carrying four items whose
// publication times interleave across feeds. The interleaving is what makes a
// page of the tag-filtered set differ from the same page of the unfiltered set.
// Dedup keys encode the hour offset, so they sort in publication order.
func tagFixture(t *testing.T, s *testsupport.InMemoryStore) (both, one, none string) {
	t.Helper()
	ctx := context.Background()
	both = "https://both.example/feed"
	one = "https://one.example/feed"
	none = "https://none.example/feed"
	addTaggedFeed(t, s, both, "ai", "agents")
	addTaggedFeed(t, s, one, "ai")
	addUntaggedFeed(t, s, none)

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

// TestInMemoryStoreAddFeedRoundTripsCanonicalTags mirrors the SQLite behavior
// that AddFeed stores a feed's tags in canonical form and GetFeed reads them
// back sorted, lowercased, and deduped.
func TestInMemoryStoreAddFeedRoundTripsCanonicalTags(t *testing.T) {
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

// TestInMemoryStoreAddFeedUntaggedReadsBackEmptyNonNil mirrors the SQLite
// behavior that an untagged feed reads back with an empty, non-nil tag set, so
// a caller can range and marshal it without a nil check.
func TestInMemoryStoreAddFeedUntaggedReadsBackEmptyNonNil(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	const feed = "https://blog.example/feed.xml"
	addUntaggedFeed(t, s, feed)

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

// TestInMemoryStoreAddFeedReAddPreservesTags mirrors the SQLite upsert's
// deliberate omission of tags from its DO UPDATE clause: re-adding an existing
// feed applies the new alias but never drops the feed out of its lanes.
func TestInMemoryStoreAddFeedReAddPreservesTags(t *testing.T) {
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

// TestInMemoryStoreSetTagsReplacesAndClears mirrors the SQLite SetTags: it
// replaces the whole set, canonicalizing on write, and clears it to an empty
// set when given no tags.
func TestInMemoryStoreSetTagsReplacesAndClears(t *testing.T) {
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

// TestInMemoryStoreSetTagsUnknownURLIsNoOp mirrors SetStatus: SetTags is keyed
// by exact URL and an unknown URL changes nothing rather than erroring.
func TestInMemoryStoreSetTagsUnknownURLIsNoOp(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	if err := s.SetTags(ctx, "https://nope.example/feed", []string{"ai"}); err != nil {
		t.Fatalf("SetTags on unknown URL: %v", err)
	}
	feeds, err := s.ListFeeds(ctx, core.ListFilter{})
	if err != nil {
		t.Fatalf("ListFeeds: %v", err)
	}
	if len(feeds) != 0 {
		t.Errorf("feeds = %v, want none created by a no-op SetTags", feedURLs(feeds))
	}
}

// TestInMemoryStoreListFeedsFiltersByTag mirrors the SQLite json_each
// predicate: no requested tags matches every feed, MatchAll requires every
// requested tag, MatchAny at least one, and requested tags are canonicalized.
func TestInMemoryStoreListFeedsFiltersByTag(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	const (
		both = "https://both.example/feed"
		one  = "https://one.example/feed"
		none = "https://none.example/feed"
	)
	addTaggedFeed(t, s, both, "ai", "agents")
	addTaggedFeed(t, s, one, "ai")
	addUntaggedFeed(t, s, none)

	tests := []struct {
		name   string
		filter core.ListFilter
		want   []string
	}{
		{"no tags matches every feed", core.ListFilter{}, []string{both, none, one}},
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

// TestInMemoryStoreListFeedsCombinesTagAndStatus mirrors the SQLite behavior
// that a tag filter and a status filter compose by AND.
func TestInMemoryStoreListFeedsCombinesTagAndStatus(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	const (
		activeInLane   = "https://a-active.example/feed"
		disabledInLane = "https://b-disabled.example/feed"
		disabledOut    = "https://c-disabled.example/feed"
	)
	addTaggedFeed(t, s, activeInLane, "ai")
	addTaggedFeed(t, s, disabledInLane, "ai")
	addUntaggedFeed(t, s, disabledOut)
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

// TestInMemoryStoreDueFeedsFiltersByTag mirrors the SQLite DueFeeds tag
// narrowing: an out-of-lane due feed, an in-lane not-due feed, and an in-lane
// disabled feed are all excluded.
func TestInMemoryStoreDueFeedsFiltersByTag(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	const (
		dueInLane      = "https://a-due.example/feed"
		notDueInLane   = "https://b-notdue.example/feed"
		dueOutOfLane   = "https://c-due.example/feed"
		disabledInLane = "https://d-disabled.example/feed"
	)
	addTaggedFeed(t, s, dueInLane, "ai")
	addTaggedFeed(t, s, notDueInLane, "ai")
	addUntaggedFeed(t, s, dueOutOfLane)
	addTaggedFeed(t, s, disabledInLane, "ai")
	if _, err := s.RecordSuccess(ctx, notDueInLane, now, now.Add(time.Hour), ""); err != nil {
		t.Fatalf("RecordSuccess: %v", err)
	}
	if err := s.SetStatus(ctx, disabledInLane, core.FeedDisabled); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	got, err := s.DueFeeds(ctx, now, core.ListFilter{Tags: []string{"ai"}})
	if err != nil {
		t.Fatalf("DueFeeds: %v", err)
	}
	if want := []string{dueInLane}; !slices.Equal(feedURLs(got), want) {
		t.Errorf("DueFeeds = %v, want %v", feedURLs(got), want)
	}
}

// TestInMemoryStoreTagCounts mirrors the SQLite TagCounts: counts sorted by
// tag, over feeds of any status, and an empty result for an untagged store.
func TestInMemoryStoreTagCounts(t *testing.T) {
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
	addUntaggedFeed(t, s, "https://c.example/feed")
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

// TestInMemoryStoreQueryItemsFiltersByTag mirrors the SQLite item lane scope:
// items carry no tags of their own, so a lane is inherited from the
// subscription, under both match modes.
func TestInMemoryStoreQueryItemsFiltersByTag(t *testing.T) {
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

// TestInMemoryStoreQueryItemsTagComposesWithOtherFilters mirrors the SQLite
// behavior that a tag filter intersects the other item predicates rather than
// widening them: --feed plus --tag means "from this feed, if it is in the lane".
func TestInMemoryStoreQueryItemsTagComposesWithOtherFilters(t *testing.T) {
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

// TestInMemoryStoreQueryItemsTagFilterPaginatesOverFilteredSet mirrors the
// SQLite behavior that the lane scope is applied before LIMIT/OFFSET, so a page
// is the nth page of the filtered set. A filter applied after pagination would
// return the unfiltered page's survivors, which the interleaved fixture makes a
// different answer.
func TestInMemoryStoreQueryItemsTagFilterPaginatesOverFilteredSet(t *testing.T) {
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

// TestInMemoryStoreQueryItemsOmittedNoDateHonorsTag mirrors the SQLite behavior
// that OmittedNoDate counts only dateless items inside the lane, so an undated
// out-of-lane item never inflates the count. That requires the lane scope to be
// applied before the count, not after.
func TestInMemoryStoreQueryItemsOmittedNoDateHonorsTag(t *testing.T) {
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

// TestInMemoryStorePruneItemsByAgeHonorsTag mirrors the SQLite scoped age
// prune: only in-lane items are tombstoned, an equally old out-of-lane item
// survives, and a pruned key keeps its fingerprint so a re-poll never re-emits
// it as new.
func TestInMemoryStorePruneItemsByAgeHonorsTag(t *testing.T) {
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

// TestInMemoryStorePruneItemsByMaxPerFeedHonorsTag mirrors the SQLite scoped
// per-feed prune: N are kept per in-lane feed and out-of-lane feeds are left
// entirely untouched. Out-of-lane items must not be ranked at all, or the
// cutoff falls in the wrong place.
func TestInMemoryStorePruneItemsByMaxPerFeedHonorsTag(t *testing.T) {
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

// TestInMemoryStorePruneItemsUnknownTagPrunesNothing mirrors the SQLite
// behavior that a tag matching no feed prunes nothing rather than falling back
// to the whole store.
func TestInMemoryStorePruneItemsUnknownTagPrunesNothing(t *testing.T) {
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
