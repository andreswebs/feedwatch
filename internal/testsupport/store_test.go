package testsupport_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/store"
	"github.com/andreswebs/feedwatch/internal/testsupport"
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

	due, err := s.DueFeeds(ctx, now)
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
