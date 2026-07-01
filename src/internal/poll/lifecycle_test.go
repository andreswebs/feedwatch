package poll_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/poll"
	"github.com/andreswebs/feedwatch/internal/store/sqlite"
)

var testNow = time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)

func fixedClock() time.Time { return testNow }

// newStore opens a migrated store on a temp-file database with a fixed clock
// and seeds a single active feed for lifecycle tests.
func newStore(t *testing.T, feedURL string, interval time.Duration) *sqlite.Store {
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
	if _, err := s.AddFeed(context.Background(), core.Feed{URL: feedURL, Interval: interval}); err != nil {
		t.Fatalf("AddFeed: %v", err)
	}
	return s
}

func TestRecordFailureBackoffGrowsExponentiallyAndCaps(t *testing.T) {
	const url = "https://blog.example/feed.xml"
	s := newStore(t, url, 0)
	ctx := context.Background()

	base, maxBackoff := time.Minute, 8*time.Minute
	// 1m, 2m, 4m, 8m (cap), 8m (cap)
	wants := []time.Duration{1 * time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 8 * time.Minute}
	for i, want := range wants {
		if err := poll.RecordFailure(ctx, s, fixedClock, url, core.CatTimeout, "timeout", 100, base, maxBackoff); err != nil {
			t.Fatalf("RecordFailure #%d: %v", i+1, err)
		}
		f, err := s.GetFeed(ctx, url)
		if err != nil {
			t.Fatalf("GetFeed #%d: %v", i+1, err)
		}
		if f.FailureCount != i+1 {
			t.Errorf("after failure %d: FailureCount = %d, want %d", i+1, f.FailureCount, i+1)
		}
		if f.NextDueAt == nil || !f.NextDueAt.Equal(testNow.Add(want)) {
			t.Errorf("after failure %d: NextDueAt = %v, want %v", i+1, f.NextDueAt, testNow.Add(want))
		}
	}
}

func TestRecordFailureDisablesAtThreshold(t *testing.T) {
	const url = "https://blog.example/feed.xml"
	s := newStore(t, url, 0)
	ctx := context.Background()

	const threshold = 3
	base, maxBackoff := time.Minute, time.Hour
	for i := 0; i < threshold-1; i++ {
		if err := poll.RecordFailure(ctx, s, fixedClock, url, core.CatHTTP, "503", threshold, base, maxBackoff); err != nil {
			t.Fatalf("RecordFailure #%d: %v", i+1, err)
		}
		f, err := s.GetFeed(ctx, url)
		if err != nil {
			t.Fatalf("GetFeed #%d: %v", i+1, err)
		}
		if f.Status != core.FeedActive {
			t.Errorf("after failure %d: Status = %q, want active", i+1, f.Status)
		}
	}
	if err := poll.RecordFailure(ctx, s, fixedClock, url, core.CatHTTP, "503", threshold, base, maxBackoff); err != nil {
		t.Fatalf("RecordFailure at threshold: %v", err)
	}
	f, err := s.GetFeed(ctx, url)
	if err != nil {
		t.Fatalf("GetFeed: %v", err)
	}
	if f.Status != core.FeedDisabled {
		t.Errorf("Status = %q, want disabled", f.Status)
	}

	due, err := s.DueFeeds(ctx, testNow.Add(365*24*time.Hour))
	if err != nil {
		t.Fatalf("DueFeeds: %v", err)
	}
	if len(due) != 0 {
		t.Errorf("DueFeeds returned %d feeds, want 0 (disabled feed excluded)", len(due))
	}
}

func TestRecordSuccessResetsFailureStateAndSchedulesInterval(t *testing.T) {
	const url = "https://blog.example/feed.xml"
	s := newStore(t, url, 30*time.Minute) // feed declares its own interval
	ctx := context.Background()

	// Accrue failure state first.
	if err := poll.RecordFailure(ctx, s, fixedClock, url, core.CatNetwork, "boom", 10, time.Minute, time.Hour); err != nil {
		t.Fatalf("RecordFailure: %v", err)
	}

	const def = time.Hour
	if _, err := poll.RecordSuccess(ctx, s, fixedClock, url, 30*time.Minute, def, ""); err != nil {
		t.Fatalf("RecordSuccess: %v", err)
	}

	f, err := s.GetFeed(ctx, url)
	if err != nil {
		t.Fatalf("GetFeed: %v", err)
	}
	if f.FailureCount != 0 {
		t.Errorf("FailureCount = %d, want 0", f.FailureCount)
	}
	if f.LastError != "" {
		t.Errorf("LastError = %q, want empty", f.LastError)
	}
	if f.LastErrorAt != nil {
		t.Errorf("LastErrorAt = %v, want nil", f.LastErrorAt)
	}
	if f.LastFetchAt == nil || !f.LastFetchAt.Equal(testNow) {
		t.Errorf("LastFetchAt = %v, want %v", f.LastFetchAt, testNow)
	}
	if f.NextDueAt == nil || !f.NextDueAt.Equal(testNow.Add(30*time.Minute)) {
		t.Errorf("NextDueAt = %v, want %v", f.NextDueAt, testNow.Add(30*time.Minute))
	}
}

func TestRecordSuccessFallsBackToDefaultInterval(t *testing.T) {
	const url = "https://blog.example/feed.xml"
	s := newStore(t, url, 0) // no declared interval
	ctx := context.Background()

	const def = time.Hour
	if _, err := poll.RecordSuccess(ctx, s, fixedClock, url, 0, def, ""); err != nil {
		t.Fatalf("RecordSuccess: %v", err)
	}
	f, err := s.GetFeed(ctx, url)
	if err != nil {
		t.Fatalf("GetFeed: %v", err)
	}
	if f.NextDueAt == nil || !f.NextDueAt.Equal(testNow.Add(def)) {
		t.Errorf("NextDueAt = %v, want %v", f.NextDueAt, testNow.Add(def))
	}
}

func TestRecordFailureFirstFailureSchedulesBaseBackoff(t *testing.T) {
	const url = "https://blog.example/feed.xml"
	s := newStore(t, url, 0)
	ctx := context.Background()

	base, maxBackoff := time.Minute, time.Hour
	if err := poll.RecordFailure(ctx, s, fixedClock, url, core.CatNetwork, "dns failure", 10, base, maxBackoff); err != nil {
		t.Fatalf("RecordFailure: %v", err)
	}

	f, err := s.GetFeed(ctx, url)
	if err != nil {
		t.Fatalf("GetFeed: %v", err)
	}
	if f.FailureCount != 1 {
		t.Errorf("FailureCount = %d, want 1", f.FailureCount)
	}
	if f.LastError != "dns failure" {
		t.Errorf("LastError = %q, want %q", f.LastError, "dns failure")
	}
	if f.NextDueAt == nil || !f.NextDueAt.Equal(testNow.Add(base)) {
		t.Errorf("NextDueAt = %v, want %v", f.NextDueAt, testNow.Add(base))
	}
	if f.Status != core.FeedActive {
		t.Errorf("Status = %q, want active", f.Status)
	}
}
