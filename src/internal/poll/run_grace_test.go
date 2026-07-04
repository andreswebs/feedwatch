package poll

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/parse"
	"github.com/andreswebs/feedwatch/internal/testsupport"
)

// graceAfterCancel must not impose any deadline while the parent context is
// live: an uninterrupted caller should be able to run arbitrarily long.
func TestGraceAfterCancelNoDeadlineBeforeParentCancel(t *testing.T) {
	ctx, stop := graceAfterCancel(context.Background(), time.Millisecond)
	defer stop()

	if _, ok := ctx.Deadline(); ok {
		t.Fatal("graceAfterCancel: returned context has a deadline before parent cancellation")
	}
	if err := ctx.Err(); err != nil {
		t.Fatalf("graceAfterCancel: returned context is done before parent cancellation: %v", err)
	}
}

// Once the parent is cancelled, the returned context must survive until grace
// elapses (so completed work can still be flushed) and then be cancelled.
func TestGraceAfterCancelCancelsAfterGraceOnParentCancel(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	grace := 20 * time.Millisecond
	ctx, stop := graceAfterCancel(parent, grace)
	defer stop()

	cancelParent()

	// Immediately after the interrupt, persistence must still be able to proceed.
	if err := ctx.Err(); err != nil {
		t.Fatalf("graceAfterCancel: context cancelled immediately on parent cancel, want grace period: %v", err)
	}

	select {
	case <-ctx.Done():
	case <-time.After(10 * grace):
		t.Fatal("graceAfterCancel: context was never cancelled after grace elapsed")
	}
}

// stop must release the returned context immediately, without waiting for grace,
// so a normal completed run is not left hanging on the watcher goroutine.
func TestGraceAfterCancelStopIsImmediate(t *testing.T) {
	ctx, stop := graceAfterCancel(context.Background(), time.Hour)
	stop()

	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("graceAfterCancel: stop did not cancel the returned context")
	}
}

// deadlineCheckStore wraps the in-memory store so its write methods record
// whether the context they were called with carries a deadline. It is the seam
// that reproduces the reported bug: an uninterrupted poll must persist without
// any deadline, however large the batch.
type deadlineCheckStore struct {
	*testsupport.InMemoryStore
	mu    sync.Mutex
	sawDL bool
}

func (s *deadlineCheckStore) note(ctx context.Context) {
	if _, ok := ctx.Deadline(); ok {
		s.mu.Lock()
		s.sawDL = true
		s.mu.Unlock()
	}
}

func (s *deadlineCheckStore) SetValidators(ctx context.Context, url, etag, lastModified string) error {
	s.note(ctx)
	return s.InMemoryStore.SetValidators(ctx, url, etag, lastModified)
}

func (s *deadlineCheckStore) UpsertItems(ctx context.Context, feedURL string, items []core.Item) ([]core.Item, error) {
	s.note(ctx)
	return s.InMemoryStore.UpsertItems(ctx, feedURL, items)
}

func (s *deadlineCheckStore) RecordSuccess(ctx context.Context, url string, fetchedAt, nextDue time.Time, finalURL string) (string, error) {
	s.note(ctx)
	return s.InMemoryStore.RecordSuccess(ctx, url, fetchedAt, nextDue, finalURL)
}

func (s *deadlineCheckStore) sawDeadline() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sawDL
}

// A normal, uncancelled poll must persist its results without any deadline on
// the store writes, regardless of how many feeds or items there are. Before the
// fix, every persist write carried the unconditional 5s grace deadline.
func TestRunUninterruptedPersistsWithoutDeadline(t *testing.T) {
	clk := testsupport.FixedClock(fixedTime())
	base := testsupport.NewInMemoryStore(clk)
	s := &deadlineCheckStore{InMemoryStore: base}

	url := "https://a.example/feed.xml"
	seedFeed(t, base, url, fixedTime().Add(-time.Hour))

	f := testsupport.NewFakeFetcher()
	f.Register(url, okResult(url))
	p := testsupport.NewFakeParser()
	p.Register(url, parse.ParsedFeed{Items: []core.Item{{GUID: "a1", Title: "a1"}}})

	d := Deps{
		Store:            s,
		Fetcher:          f,
		Parser:           p,
		Clock:            clk,
		Concurrency:      8,
		DefaultInterval:  time.Hour,
		FailureThreshold: 10,
		MaxBackoff:       24 * time.Hour,
	}
	_, _, err := Run(context.Background(), d, nil, false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if s.sawDeadline() {
		t.Fatal("Run: a store write on an uninterrupted poll carried a deadline")
	}
}
