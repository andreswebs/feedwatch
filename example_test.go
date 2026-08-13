package feedwatch_test

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/andreswebs/feedwatch"
	"github.com/andreswebs/feedwatch/core"
	"github.com/andreswebs/feedwatch/store"
)

// ExampleNew builds an App over a store of the embedder's choosing. New performs
// no I/O, so nothing exists on disk until a use case runs.
func ExampleNew() {
	dir, err := os.MkdirTemp("", "feedwatch-example")
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	cfg := feedwatch.Defaults()
	cfg.Store = filepath.Join(dir, "feedwatch.db")

	app, err := feedwatch.New(cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = app.Close() }()

	_, statErr := os.Stat(cfg.Store)
	fmt.Println("backend:", cfg.Backend())
	fmt.Println("store created by New:", statErr == nil)

	// Output:
	// backend: sqlite
	// store created by New: false
}

// ExampleApp_Add subscribes to an explicit feed URL. Add proves the URL fetches
// and parses as a feed before recording it, so it reaches the network.
func ExampleApp_Add() {
	app, err := feedwatch.New(feedwatch.Defaults())
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = app.Close() }()

	res, err := app.Add(context.Background(), feedwatch.AddRequest{
		URL:   "https://blog.go.dev/feed.atom",
		Alias: "godev",
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("subscribed to %s (created: %t)\n", res.URL, res.Created)
}

// ExampleApp_Poll polls the feeds whose interval has elapsed and reports the
// items feedwatch had never seen before. A second immediate poll returns none.
func ExampleApp_Poll() {
	app, err := feedwatch.New(feedwatch.Defaults())
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = app.Close() }()

	res, err := app.Poll(context.Background(), feedwatch.PollRequest{})
	if err != nil && res.Polled == 0 {
		log.Fatal(err)
	}
	for _, item := range res.Items {
		fmt.Printf("%s\t%s\n", item.Title, item.Link)
	}
	fmt.Printf("polled %d, new %d, failed %d\n", res.Polled, res.NewItems, res.Failed)
}

// ExampleApp_Items queries stored history with a time window and a field
// projection. The projected shape is selected by ItemsRequest.Envelope, so the
// caller never inspects Fields itself.
func ExampleApp_Items() {
	dir, err := os.MkdirTemp("", "feedwatch-example")
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	cfg := feedwatch.Defaults()
	cfg.Store = filepath.Join(dir, "feedwatch.db")
	app, err := feedwatch.New(cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = app.Close() }()

	req := feedwatch.ItemsRequest{
		Since:     "7d",
		TimeField: "fetched",
		Order:     "published desc",
		Limit:     50,
		Fields:    []string{"title", "link", "published_at"},
	}
	res, err := app.Items(context.Background(), req)
	if err != nil {
		log.Fatal(err)
	}

	switch env := req.Envelope(res).(type) {
	case feedwatch.ProjectedItemsResult:
		fmt.Println("projected items:", len(env.Items))
	case feedwatch.ItemsResult:
		fmt.Println("full items:", len(env.Items))
	}

	// Output:
	// projected items: 0
}

// exampleStore is a stand-in for an alternative backend. A real one implements
// every store.Store method; embedding the interface keeps the example to the
// method being illustrated, and is not a pattern to ship: an unimplemented
// method would panic at call time rather than fail to compile.
type exampleStore struct {
	store.Store
	upserts int
}

// UpsertItems records the call and delegates. A backend returns only the items
// whose dedup key it had never recorded for that feed before.
func (s *exampleStore) UpsertItems(ctx context.Context, feedURL string, items []core.Item) ([]core.Item, error) {
	s.upserts++
	return s.Store.UpsertItems(ctx, feedURL, items)
}

// ExampleWithStore hands the App a custom store backend. The store belongs to
// the embedder: App.Close never closes an injected one.
func ExampleWithStore() {
	var backend store.Store = &exampleStore{}
	defer func() { _ = backend.Close() }()

	app, err := feedwatch.New(feedwatch.Defaults(), feedwatch.WithStore(backend))
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = app.Close() }()

	if _, err := app.List(context.Background(), feedwatch.ListRequest{}); err != nil {
		log.Fatal(err)
	}
}

// ExampleApp_Poll_errors classifies failures by category rather than by matching
// message strings, which is what every frontend does to derive its own failure
// encoding. Per-feed failures are result data on the envelope; a returned error
// is a whole-invocation failure.
func ExampleApp_Poll_errors() {
	app, err := feedwatch.New(feedwatch.Defaults())
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = app.Close() }()

	res, err := app.Poll(context.Background(), feedwatch.PollRequest{})
	if err != nil {
		var ferr *core.FeedError
		if errors.As(err, &ferr) {
			switch ferr.Category {
			case core.CatUsage:
				fmt.Println("bad request:", ferr.Detail())
			case core.CatConfig:
				fmt.Println("bad configuration:", ferr.Detail())
			case core.CatStore:
				fmt.Println("store unavailable:", ferr.Detail())
			default:
				fmt.Println("failed:", ferr.Detail())
			}
		}
		// A mid-persist failure carries a truthful partial envelope, which
		// res.Polled > 0 identifies; anything else means nothing was done.
		if res.Polled == 0 {
			return
		}
	}

	for _, f := range res.Failures {
		switch f.Category {
		case core.CatHTTP:
			fmt.Printf("%s: HTTP %d\n", f.FeedURL, f.Status)
		case core.CatTimeout, core.CatNetwork:
			fmt.Printf("%s: retry later (%s)\n", f.FeedURL, f.Category)
		case core.CatParse:
			fmt.Printf("%s: not a usable feed\n", f.FeedURL)
		}
	}
}
