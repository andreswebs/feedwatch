package poll

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/testsupport"
)

func urlsOf(feeds []core.Feed) []string {
	out := make([]string, len(feeds))
	for i, f := range feeds {
		out[i] = f.URL
	}
	return out
}

func TestSelectFeedsDueOnlySkipsNonDue(t *testing.T) {
	clk := testsupport.FixedClock(fixedTime())
	store := testsupport.NewInMemoryStore(clk)

	due := "https://due.example/feed.xml"
	notDue := "https://later.example/feed.xml"
	seedFeed(t, store, due, fixedTime().Add(-time.Hour))
	seedFeed(t, store, notDue, fixedTime().Add(time.Hour))

	d := Deps{Store: store, Clock: clk}

	got, err := selectFeeds(context.Background(), d, nil, false)
	if err != nil {
		t.Fatalf("selectFeeds: %v", err)
	}
	if len(got) != 1 || got[0].URL != due {
		t.Fatalf("due-only selection got %v, want [%s]", urlsOf(got), due)
	}
}

func TestSelectFeedsForceIncludesNonDue(t *testing.T) {
	clk := testsupport.FixedClock(fixedTime())
	store := testsupport.NewInMemoryStore(clk)

	due := "https://due.example/feed.xml"
	notDue := "https://later.example/feed.xml"
	seedFeed(t, store, due, fixedTime().Add(-time.Hour))
	seedFeed(t, store, notDue, fixedTime().Add(time.Hour))

	d := Deps{Store: store, Clock: clk}

	got, err := selectFeeds(context.Background(), d, nil, true)
	if err != nil {
		t.Fatalf("selectFeeds force: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("force selection got %v, want both feeds", urlsOf(got))
	}
}

func TestSelectFeedsNamedResolvesRegardlessOfDue(t *testing.T) {
	clk := testsupport.FixedClock(fixedTime())
	store := testsupport.NewInMemoryStore(clk)

	notDue := "https://later.example/feed.xml"
	seedFeed(t, store, notDue, fixedTime().Add(time.Hour))

	d := Deps{Store: store, Clock: clk}

	got, err := selectFeeds(context.Background(), d, []string{notDue}, false)
	if err != nil {
		t.Fatalf("selectFeeds named: %v", err)
	}
	if len(got) != 1 || got[0].URL != notDue {
		t.Fatalf("named selection got %v, want [%s]", urlsOf(got), notDue)
	}
}

func TestSelectFeedsUnknownNamedIsUsageError(t *testing.T) {
	clk := testsupport.FixedClock(fixedTime())
	store := testsupport.NewInMemoryStore(clk)

	d := Deps{Store: store, Clock: clk}

	_, err := selectFeeds(context.Background(), d, []string{"https://nope.example/feed.xml"}, false)
	if err == nil {
		t.Fatal("selectFeeds with unknown ref: want error, got nil")
	}
	var fe *core.FeedError
	if !errors.As(err, &fe) || fe.Category != core.CatUsage {
		t.Fatalf("want usage-category FeedError, got %v", err)
	}
}
