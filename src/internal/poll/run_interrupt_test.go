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

// ctxCheckStore wraps the in-memory store so its write methods fail with the
// context error once the context is cancelled, mirroring the real SQLite store
// whose writes abort on a cancelled context. It is the seam that reproduces the
// interrupt bug: if poll persists completed work on the cancelled poll context,
// these writes fail and the whole run errors out.
type ctxCheckStore struct {
	*testsupport.InMemoryStore
}

func (s ctxCheckStore) SetValidators(ctx context.Context, url, etag, lastModified string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.InMemoryStore.SetValidators(ctx, url, etag, lastModified)
}

func (s ctxCheckStore) UpsertItems(ctx context.Context, feedURL string, items []core.Item) ([]core.Item, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.InMemoryStore.UpsertItems(ctx, feedURL, items)
}

func (s ctxCheckStore) RecordSuccess(ctx context.Context, url string, fetchedAt, nextDue time.Time, finalURL string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return s.InMemoryStore.RecordSuccess(ctx, url, fetchedAt, nextDue, finalURL)
}

func (s ctxCheckStore) RecordFailure(ctx context.Context, url string, cat core.Category, msg string, at, nextDue time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.InMemoryStore.RecordFailure(ctx, url, cat, msg, at, nextDue)
}

// pausingFetcher returns immediately for the fast URL and blocks any other URL
// until the context is cancelled, then fails it. Each path signals once it is
// entered so a test can wait until the fast feed has completed and the slow one
// is genuinely in flight before delivering the interrupt.
type pausingFetcher struct {
	fast     string
	fastRes  core.FetchResult
	fastOnce sync.Once
	slowOnce sync.Once
	fastHit  chan struct{}
	slowHit  chan struct{}
}

func (f *pausingFetcher) Fetch(ctx context.Context, req core.FetchRequest) (core.FetchResult, error) {
	if req.URL == f.fast {
		f.fastOnce.Do(func() { close(f.fastHit) })
		return f.fastRes, nil
	}
	f.slowOnce.Do(func() { close(f.slowHit) })
	<-ctx.Done()
	return core.FetchResult{}, core.NetworkErr(req.URL, ctx.Err())
}

// An interrupt mid-poll must not discard the work that already completed: the
// fast feed's fetch finished before the signal, so its items must be persisted
// and surfaced in the envelope, and Run must not return a hard error from the
// cancelled context.
func TestRunInterruptPersistsCompletedFeeds(t *testing.T) {
	clk := testsupport.FixedClock(fixedTime())
	base := testsupport.NewInMemoryStore(clk)
	s := ctxCheckStore{base}

	fast := "https://a.example/feed.xml"
	slow := "https://b.example/feed.xml"
	seedFeed(t, base, fast, fixedTime().Add(-time.Hour))
	seedFeed(t, base, slow, fixedTime().Add(-time.Hour))

	p := testsupport.NewFakeParser()
	p.Register(fast, parse.ParsedFeed{Items: []core.Item{{GUID: "a1", Title: "a1"}}})

	f := &pausingFetcher{
		fast:    fast,
		fastRes: okResult(fast),
		fastHit: make(chan struct{}),
		slowHit: make(chan struct{}),
	}

	ctx, cancel := context.WithCancel(context.Background())
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

	type runResult struct {
		result Result
		err    error
	}
	done := make(chan runResult, 1)
	go func() {
		r, _, err := Run(ctx, d, nil, false)
		done <- runResult{r, err}
	}()

	<-f.fastHit // the fast feed completed
	<-f.slowHit // the slow feed is in flight
	cancel()    // deliver the interrupt

	got := <-done
	if got.err != nil {
		t.Fatalf("Run returned a hard error on interrupt: %v", got.err)
	}

	items, err := base.QueryItems(context.Background(), core.ItemQuery{Feeds: []string{fast}})
	if err != nil {
		t.Fatalf("QueryItems: %v", err)
	}
	if len(items.Items) != 1 || items.Items[0].Title != "a1" {
		t.Fatalf("completed feed not persisted: %v", items.Items)
	}

	if got.result.NewItems != 1 {
		t.Fatalf("result.NewItems = %d, want 1 (completed feed in envelope)", got.result.NewItems)
	}

	stored, err := base.GetFeed(context.Background(), fast)
	if err != nil {
		t.Fatalf("GetFeed: %v", err)
	}
	if stored.LastFetchAt == nil {
		t.Fatal("completed feed was not recorded as a success (LastFetchAt nil)")
	}
}
