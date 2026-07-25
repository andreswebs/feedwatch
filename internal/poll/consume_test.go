package poll

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/parse"
	"github.com/andreswebs/feedwatch/internal/store/sqlite"
)

// newConsumeStore opens a migrated sqlite store on a temp-file database with the
// package fixed clock, the real store the dedup-and-consume stage persists into.
func newConsumeStore(t *testing.T) *sqlite.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "feedwatch.db")
	s, err := sqlite.Open(path, sqlite.WithClock(fixedTime))
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

func addFeed(t *testing.T, s *sqlite.Store, f core.Feed) core.Feed {
	t.Helper()
	stored, err := s.AddFeed(context.Background(), f)
	if err != nil {
		t.Fatalf("AddFeed(%s): %v", f.URL, err)
	}
	return stored
}

func consumeDeps(s *sqlite.Store) Deps {
	return Deps{
		Store:            s,
		Clock:            fixedTime,
		DefaultInterval:  time.Hour,
		FailureThreshold: 10,
		MaxBackoff:       24 * time.Hour,
	}
}

// Behavior 1 (tracer): a feed's new items are returned and marked seen; an
// immediate second poll over the same items yields zero new (dedup).
func TestConsumeReturnsNewItemsThenDedups(t *testing.T) {
	s := newConsumeStore(t)
	ctx := context.Background()
	const url = "https://blog.example/feed.xml"
	feed := addFeed(t, s, core.Feed{URL: url})

	items := []core.Item{
		{GUID: "g1", Title: "First", Link: "https://blog.example/1"},
		{GUID: "g2", Title: "Second", Link: "https://blog.example/2"},
	}
	oc := feedOutcome{feed: feed, result: core.FetchResult{Status: 200}, parsed: parse.ParsedFeed{Items: items}}

	totals, feedErrs, err := consume(ctx, consumeDeps(s), []feedOutcome{oc})
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if len(feedErrs) != 0 {
		t.Fatalf("unexpected feed errors: %v", feedErrs)
	}
	if totals.newItems != 2 {
		t.Fatalf("newItems = %d, want 2", totals.newItems)
	}
	if got := len(totals.newByFeed[url]); got != 2 {
		t.Fatalf("newByFeed[%s] = %d items, want 2", url, got)
	}

	// Re-query proves the items were persisted (marked seen).
	stored, err := s.QueryItems(ctx, core.ItemQuery{})
	if err != nil {
		t.Fatalf("QueryItems: %v", err)
	}
	if len(stored.Items) != 2 {
		t.Fatalf("stored items = %d, want 2", len(stored.Items))
	}

	// Second poll over the same items: nothing new.
	totals2, _, err := consume(ctx, consumeDeps(s), []feedOutcome{oc})
	if err != nil {
		t.Fatalf("consume (second): %v", err)
	}
	if totals2.newItems != 0 {
		t.Fatalf("second poll newItems = %d, want 0 (dedup)", totals2.newItems)
	}
}

// Behavior 2: validators from the fetch are persisted; an empty new ETag does
// not clobber a stored one.
func TestConsumePersistsValidatorsWithoutEmptyClobber(t *testing.T) {
	s := newConsumeStore(t)
	ctx := context.Background()
	const url = "https://blog.example/feed.xml"
	feed := addFeed(t, s, core.Feed{URL: url})

	oc := feedOutcome{feed: feed, result: core.FetchResult{Status: 200, ETag: `"abc"`, LastModified: "Wed, 21 Oct 2025 07:28:00 GMT"}}
	if _, _, err := consume(ctx, consumeDeps(s), []feedOutcome{oc}); err != nil {
		t.Fatalf("consume: %v", err)
	}
	stored, err := s.GetFeed(ctx, url)
	if err != nil {
		t.Fatalf("GetFeed: %v", err)
	}
	if stored.ETag != `"abc"` || stored.LastModified != "Wed, 21 Oct 2025 07:28:00 GMT" {
		t.Fatalf("validators not persisted: etag=%q last_modified=%q", stored.ETag, stored.LastModified)
	}

	// A later fetch with no validators must not erase the stored ETag.
	feed2, err := s.GetFeed(ctx, url)
	if err != nil {
		t.Fatalf("GetFeed: %v", err)
	}
	ocEmpty := feedOutcome{feed: feed2, result: core.FetchResult{Status: 200}}
	if _, _, err := consume(ctx, consumeDeps(s), []feedOutcome{ocEmpty}); err != nil {
		t.Fatalf("consume (empty validators): %v", err)
	}
	after, err := s.GetFeed(ctx, url)
	if err != nil {
		t.Fatalf("GetFeed: %v", err)
	}
	if after.ETag != `"abc"` {
		t.Fatalf("empty ETag clobbered stored validator: got %q, want %q", after.ETag, `"abc"`)
	}
}

// Behavior 3: a successful poll records success, advancing next-due and honoring
// a parsed <ttl> over the default interval, and clearing failure state.
func TestConsumeRecordSuccessHonorsTTL(t *testing.T) {
	s := newConsumeStore(t)
	ctx := context.Background()
	const url = "https://blog.example/feed.xml"
	feed := addFeed(t, s, core.Feed{URL: url})

	const ttl = 15 * time.Minute
	oc := feedOutcome{feed: feed, result: core.FetchResult{Status: 200}, parsed: parse.ParsedFeed{TTL: ttl}}
	if _, _, err := consume(ctx, consumeDeps(s), []feedOutcome{oc}); err != nil {
		t.Fatalf("consume: %v", err)
	}

	stored, err := s.GetFeed(ctx, url)
	if err != nil {
		t.Fatalf("GetFeed: %v", err)
	}
	wantDue := fixedTime().Add(ttl)
	if stored.NextDueAt == nil || !stored.NextDueAt.Equal(wantDue) {
		t.Fatalf("NextDueAt = %v, want %v (ttl honored)", stored.NextDueAt, wantDue)
	}
	if stored.LastFetchAt == nil || !stored.LastFetchAt.Equal(fixedTime()) {
		t.Fatalf("LastFetchAt = %v, want %v", stored.LastFetchAt, fixedTime())
	}
	if stored.FailureCount != 0 {
		t.Fatalf("FailureCount = %d, want 0", stored.FailureCount)
	}
}

// Behavior 4: a failed outcome drives the failure lifecycle (count up, backoff
// scheduled) and is surfaced as a per-feed error rather than a hard error.
func TestConsumeRecordsFailure(t *testing.T) {
	s := newConsumeStore(t)
	ctx := context.Background()
	const url = "https://blog.example/feed.xml"
	feed := addFeed(t, s, core.Feed{URL: url, Interval: 30 * time.Minute})

	oc := feedOutcome{feed: feed, err: core.HTTPErr(url, 500, nil)}
	totals, feedErrs, err := consume(ctx, consumeDeps(s), []feedOutcome{oc})
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if totals.failed != 1 || totals.polled != 1 {
		t.Fatalf("totals = %+v, want polled=1 failed=1", totals)
	}
	if len(feedErrs) != 1 || feedErrs[0].Category != core.CatHTTP {
		t.Fatalf("feed errors = %v, want one http error", feedErrs)
	}

	stored, err := s.GetFeed(ctx, url)
	if err != nil {
		t.Fatalf("GetFeed: %v", err)
	}
	if stored.FailureCount != 1 {
		t.Fatalf("FailureCount = %d, want 1", stored.FailureCount)
	}
	// First failure backs off by the base (the feed's 30m interval).
	wantDue := fixedTime().Add(30 * time.Minute)
	if stored.NextDueAt == nil || !stored.NextDueAt.Equal(wantDue) {
		t.Fatalf("NextDueAt = %v, want %v (backoff base)", stored.NextDueAt, wantDue)
	}
}

// Behavior 5: items are keyed by the dedup precedence before upsert. An item
// re-advertised under the same GUID but a changed title and link is not new,
// proving the key is the GUID, not the mutable title/link.
func TestConsumeAssignsDedupKeysBeforeUpsert(t *testing.T) {
	s := newConsumeStore(t)
	ctx := context.Background()
	const url = "https://blog.example/feed.xml"
	feed := addFeed(t, s, core.Feed{URL: url})

	first := feedOutcome{feed: feed, result: core.FetchResult{Status: 200},
		parsed: parse.ParsedFeed{Items: []core.Item{{GUID: "stable", Title: "v1", Link: "https://blog.example/v1"}}}}
	totals, _, err := consume(ctx, consumeDeps(s), []feedOutcome{first})
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if totals.newItems != 1 {
		t.Fatalf("newItems = %d, want 1", totals.newItems)
	}

	// Same GUID, changed title and link: keyed on GUID, so not new.
	second := feedOutcome{feed: feed, result: core.FetchResult{Status: 200},
		parsed: parse.ParsedFeed{Items: []core.Item{{GUID: "stable", Title: "v2", Link: "https://blog.example/v2"}}}}
	totals2, _, err := consume(ctx, consumeDeps(s), []feedOutcome{second})
	if err != nil {
		t.Fatalf("consume (second): %v", err)
	}
	if totals2.newItems != 0 {
		t.Fatalf("changed title/link under same GUID counted as new: newItems = %d, want 0", totals2.newItems)
	}
}

// A 301/308 permanent redirect rewrites the stored feed URL through consume;
// a 302/307 (Permanent=false) and a same-URL final do not.
func TestConsumeRewritesURLOnPermanentRedirect(t *testing.T) {
	const oldURL = "https://blog.example/redirect"
	const newURL = "https://blog.example/feed.xml"

	tests := []struct {
		name      string
		result    core.FetchResult
		wantURL   string
		wantitems int
	}{
		{
			name:      "permanent rewrites",
			result:    core.FetchResult{Status: 200, FinalURL: newURL, Permanent: true},
			wantURL:   newURL,
			wantitems: 1,
		},
		{
			name:    "temporary keeps url",
			result:  core.FetchResult{Status: 200, FinalURL: newURL, Permanent: false},
			wantURL: oldURL, wantitems: 1,
		},
		{
			name:    "permanent same url keeps url",
			result:  core.FetchResult{Status: 200, FinalURL: oldURL, Permanent: true},
			wantURL: oldURL, wantitems: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newConsumeStore(t)
			ctx := context.Background()
			feed := addFeed(t, s, core.Feed{URL: oldURL})
			oc := feedOutcome{
				feed:   feed,
				result: tc.result,
				parsed: parse.ParsedFeed{Items: []core.Item{{GUID: "g1", Title: "First"}}},
			}

			if _, _, err := consume(ctx, consumeDeps(s), []feedOutcome{oc}); err != nil {
				t.Fatalf("consume: %v", err)
			}

			if _, err := s.GetFeed(ctx, tc.wantURL); err != nil {
				t.Fatalf("GetFeed(%q): %v", tc.wantURL, err)
			}
			items, err := s.QueryItems(ctx, core.ItemQuery{Feeds: []string{tc.wantURL}})
			if err != nil {
				t.Fatalf("QueryItems: %v", err)
			}
			if len(items.Items) != tc.wantitems {
				t.Errorf("items under %q = %d, want %d", tc.wantURL, len(items.Items), tc.wantitems)
			}
		})
	}
}

// A 304 not-modified permanent redirect still rewrites the stored URL.
func TestConsumeRewritesURLOnNotModifiedPermanentRedirect(t *testing.T) {
	s := newConsumeStore(t)
	ctx := context.Background()
	const oldURL = "https://blog.example/redirect"
	const newURL = "https://blog.example/feed.xml"
	feed := addFeed(t, s, core.Feed{URL: oldURL})

	oc := feedOutcome{
		feed:   feed,
		result: core.FetchResult{Status: 304, NotModified: true, FinalURL: newURL, Permanent: true},
	}
	if _, _, err := consume(ctx, consumeDeps(s), []feedOutcome{oc}); err != nil {
		t.Fatalf("consume: %v", err)
	}
	if _, err := s.GetFeed(ctx, newURL); err != nil {
		t.Errorf("GetFeed(%q): %v", newURL, err)
	}
}
