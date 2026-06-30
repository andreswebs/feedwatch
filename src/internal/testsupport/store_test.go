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

	stored, err := s.QueryItems(ctx, core.ItemQuery{Feeds: []string{url}})
	if err != nil {
		t.Fatalf("QueryItems: %v", err)
	}
	if len(stored) != 2 {
		t.Errorf("queried items = %d, want 2", len(stored))
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
	if err := s.RecordSuccess(ctx, "https://due.example/feed", past, past, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordSuccess(ctx, "https://notdue.example/feed", past, future, ""); err != nil {
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
	if len(remaining) != 1 || remaining[0].DedupKey != "c" {
		t.Errorf("remaining = %+v, want only newest item c", remaining)
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
