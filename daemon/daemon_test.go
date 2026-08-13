package daemon_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/andreswebs/feedwatch"
	"github.com/andreswebs/feedwatch/core"
	"github.com/andreswebs/feedwatch/daemon"
	"github.com/andreswebs/feedwatch/internal/testsupport"
	"github.com/andreswebs/feedwatch/store"
)

// flakyDueStore fails its first DueFeeds call and delegates every later one, so a
// scheduler's first poll returns a hard store failure and its next one succeeds.
type flakyDueStore struct {
	store.Store
	failed atomic.Bool
}

func (s *flakyDueStore) DueFeeds(ctx context.Context, now time.Time) ([]core.Feed, error) {
	if s.failed.CompareAndSwap(false, true) {
		return nil, errors.New("simulated store failure")
	}
	return s.Store.DueFeeds(ctx, now)
}

// fixedTestTime is the instant every scheduler test runs at, so due
// calculations and event stamps are reproducible.
func fixedTestTime() time.Time {
	return time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)
}

// newTickedScheduler builds a Scheduler over an App whose collaborators are all
// doubles, driven by a caller-supplied tick channel so the schedule advances
// without sleeping. Every seeded URL fetches and parses as a feed carrying one
// item.
func newTickedScheduler(t *testing.T, ticks <-chan time.Time, urls ...string) *daemon.Scheduler {
	t.Helper()

	st := testsupport.NewInMemoryStore(testsupport.FixedClock(fixedTestTime()))
	app := newApp(t, st, seedFeeds(t, st, urls...)...)
	return daemon.New(app, daemon.WithTicks(ticks), daemon.WithClock(testsupport.FixedClock(fixedTestTime())))
}

// newApp builds an App over the given store and the extra collaborator options,
// which is the sanctioned seam of ADR 0007.
func newApp(t *testing.T, st store.Store, opts ...feedwatch.Option) *feedwatch.App {
	t.Helper()

	all := append([]feedwatch.Option{
		feedwatch.WithStore(st),
		feedwatch.WithClock(testsupport.FixedClock(fixedTestTime())),
	}, opts...)
	app, err := feedwatch.New(feedwatch.Defaults(), all...)
	if err != nil {
		t.Fatalf("feedwatch.New = %v, want nil", err)
	}
	t.Cleanup(func() { _ = app.Close() })
	return app
}

// seedFeeds subscribes each url as an active feed and returns the fetcher and
// parser options that make those URLs resolve to a one-item feed.
func seedFeeds(t *testing.T, st *testsupport.InMemoryStore, urls ...string) []feedwatch.Option {
	t.Helper()

	fetcher := testsupport.NewFakeFetcher()
	parser := testsupport.NewFakeParser()
	for _, url := range urls {
		if _, err := st.AddFeed(context.Background(), core.Feed{URL: url}); err != nil {
			t.Fatalf("AddFeed(%s) = %v, want nil", url, err)
		}
		fetcher.Register(url, core.FetchResult{Status: 200, MIMEType: "application/rss+xml", Body: []byte("<rss/>")})
		parser.Register(url, core.ParsedFeed{Title: "Feed", Items: []core.Item{
			{DedupKey: url + "#1", Title: "One", Link: url + "/1"},
		}})
	}
	return []feedwatch.Option{feedwatch.WithFetcher(fetcher), feedwatch.WithParser(parser)}
}

func TestSchedulerPublishesOneEventPerTick(t *testing.T) {
	ticks := make(chan time.Time)
	s := newTickedScheduler(t, ticks, "https://a.example/feed.xml")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	ticks <- fixedTestTime()
	ev := <-s.Events()

	if ev.Err != nil {
		t.Fatalf("event error = %v, want nil", ev.Err)
	}
	if ev.Result.Polled != 1 {
		t.Errorf("polled = %d, want 1", ev.Result.Polled)
	}
	if ev.Result.NewItems != 1 {
		t.Errorf("new_items = %d, want 1", ev.Result.NewItems)
	}

	cancel()
	<-done
}

func TestSchedulerRunReturnsContextErrorAndClosesEvents(t *testing.T) {
	s := newTickedScheduler(t, make(chan time.Time), "https://a.example/feed.xml")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	cancel()

	// A range over a closed channel terminates; a leaked-open channel hangs here.
	drained := 0
	for range s.Events() {
		drained++
	}
	if drained != 0 {
		t.Errorf("drained %d event(s), want 0: nothing was polled before cancellation", drained)
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Errorf("Run = %v, want context.Canceled", err)
	}
}

func TestSchedulerWithPollOnStartPollsBeforeAnyTick(t *testing.T) {
	st := testsupport.NewInMemoryStore(testsupport.FixedClock(fixedTestTime()))
	app := newApp(t, st, seedFeeds(t, st, "https://a.example/feed.xml")...)
	s := daemon.New(app,
		daemon.WithTicks(make(chan time.Time)),
		daemon.WithPollOnStart(true),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = s.Run(ctx) }()

	ev := <-s.Events()
	if ev.Err != nil {
		t.Fatalf("event error = %v, want nil", ev.Err)
	}
	if ev.Result.Polled != 1 {
		t.Errorf("polled = %d, want 1", ev.Result.Polled)
	}
}

func TestSchedulerWithoutPollOnStartPublishesNothingBeforeTheFirstTick(t *testing.T) {
	s := newTickedScheduler(t, make(chan time.Time), "https://a.example/feed.xml")

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = s.Run(ctx) }()

	// Cancelling without ever ticking closes the channel; a run that polled on
	// start would have left an event in it.
	cancel()
	count := 0
	for range s.Events() {
		count++
	}
	if count != 0 {
		t.Errorf("published %d event(s) before the first tick, want 0", count)
	}
}

func TestSchedulerPublishesPollErrorAndKeepsRunning(t *testing.T) {
	const url = "https://a.example/feed.xml"
	mem := testsupport.NewInMemoryStore(testsupport.FixedClock(fixedTestTime()))
	st := &flakyDueStore{Store: mem}
	app := newApp(t, st, seedFeeds(t, mem, url)...)
	ticks := make(chan time.Time)
	s := daemon.New(app, daemon.WithTicks(ticks))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = s.Run(ctx) }()

	ticks <- fixedTestTime()
	first := <-s.Events()
	if first.Err == nil {
		t.Fatalf("first event error = nil, want the store failure")
	}

	ticks <- fixedTestTime()
	second := <-s.Events()
	if second.Err != nil {
		t.Fatalf("second event error = %v, want nil", second.Err)
	}
	if second.Result.Polled != 1 {
		t.Errorf("polled = %d, want 1", second.Result.Polled)
	}
}

// gatedFetcher blocks every fetch until release is closed, announcing on entered
// that a poll is in flight. It is how a test holds a poll open while ticks
// arrive.
type gatedFetcher struct {
	entered chan struct{}
	release chan struct{}
}

func (g *gatedFetcher) Fetch(ctx context.Context, req core.FetchRequest) (core.FetchResult, error) {
	g.entered <- struct{}{}
	select {
	case <-g.release:
	case <-ctx.Done():
		return core.FetchResult{}, ctx.Err()
	}
	return core.FetchResult{Status: 200, MIMEType: "application/rss+xml", Body: []byte("<rss/>"), FinalURL: req.URL}, nil
}

func TestSchedulerDropsTicksArrivingDuringAnInFlightPoll(t *testing.T) {
	const url = "https://a.example/feed.xml"
	mem := testsupport.NewInMemoryStore(testsupport.FixedClock(fixedTestTime()))
	if _, err := mem.AddFeed(context.Background(), core.Feed{URL: url}); err != nil {
		t.Fatalf("AddFeed = %v, want nil", err)
	}
	parser := testsupport.NewFakeParser()
	parser.Register(url, core.ParsedFeed{Title: "Feed"})
	gate := &gatedFetcher{entered: make(chan struct{}), release: make(chan struct{})}
	app := newApp(t, mem, feedwatch.WithFetcher(gate), feedwatch.WithParser(parser))

	ticks := make(chan time.Time)
	s := daemon.New(app, daemon.WithTicks(ticks))
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = s.Run(ctx) }()

	ticks <- fixedTestTime()
	<-gate.entered

	// Both sends complete only because the scheduler drains and drops them while
	// the poll is held open; a queued tick would stack a second run.
	ticks <- fixedTestTime()
	ticks <- fixedTestTime()
	close(gate.release)

	if ev := <-s.Events(); ev.Result.Polled != 1 {
		t.Errorf("polled = %d, want 1", ev.Result.Polled)
	}

	cancel()
	extra := 0
	for range s.Events() {
		extra++
	}
	if extra != 0 {
		t.Errorf("published %d extra event(s), want 0: dropped ticks were queued", extra)
	}
}

func TestSchedulerStampsEventsWithTheInjectedClock(t *testing.T) {
	stamp := fixedTestTime().Add(3 * time.Hour)
	st := testsupport.NewInMemoryStore(testsupport.FixedClock(fixedTestTime()))
	app := newApp(t, st, seedFeeds(t, st, "https://a.example/feed.xml")...)
	ticks := make(chan time.Time)
	s := daemon.New(app, daemon.WithTicks(ticks), daemon.WithClock(testsupport.FixedClock(stamp)))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = s.Run(ctx) }()

	ticks <- fixedTestTime()
	if ev := <-s.Events(); !ev.At.Equal(stamp) {
		t.Errorf("event at = %s, want %s", ev.At, stamp)
	}
}

func TestSchedulerPollsDueFeedsOnlyAndNeverForces(t *testing.T) {
	const dueURL = "https://due.example/feed.xml"
	st := testsupport.NewInMemoryStore(testsupport.FixedClock(fixedTestTime()))
	notDue := fixedTestTime().Add(time.Hour)
	if _, err := st.AddFeed(context.Background(), core.Feed{
		URL:       "https://later.example/feed.xml",
		NextDueAt: &notDue,
	}); err != nil {
		t.Fatalf("AddFeed = %v, want nil", err)
	}
	app := newApp(t, st, seedFeeds(t, st, dueURL)...)
	ticks := make(chan time.Time)
	s := daemon.New(app, daemon.WithTicks(ticks))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = s.Run(ctx) }()

	ticks <- fixedTestTime()
	ev := <-s.Events()
	if ev.Err != nil {
		t.Fatalf("event error = %v, want nil", ev.Err)
	}
	if ev.Result.Polled != 1 {
		t.Errorf("polled = %d, want 1: the scheduler forced a feed that was not due", ev.Result.Polled)
	}
	if ev.Result.Skipped != 1 {
		t.Errorf("skipped = %d, want 1", ev.Result.Skipped)
	}
}

func TestSchedulerRunTwiceReportsAlreadyRunning(t *testing.T) {
	ticks := make(chan time.Time)
	s := newTickedScheduler(t, ticks, "https://a.example/feed.xml")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := make(chan error, 1)
	go func() { first <- s.Run(ctx) }()

	// The tick is received only by a Run already in its loop, so the second call
	// below is unambiguously concurrent with the first.
	ticks <- fixedTestTime()
	<-s.Events()

	if err := s.Run(ctx); !errors.Is(err, daemon.ErrAlreadyRunning) {
		t.Errorf("second Run = %v, want ErrAlreadyRunning", err)
	}

	cancel()
	<-first
}
