package store_test

import (
	"context"
	"time"

	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/store"
)

// fakeStore is a hand-written double proving the interface is satisfiable and
// consumer-shaped. It exercises only the signatures, not behavior; behavior
// tests live with the SQLite implementation.
type fakeStore struct{}

func (*fakeStore) AddFeed(context.Context, core.Feed) (core.Feed, error) {
	return core.Feed{}, nil
}
func (*fakeStore) RemoveFeed(context.Context, string) error { return nil }
func (*fakeStore) GetFeed(context.Context, string) (core.Feed, error) {
	return core.Feed{}, nil
}
func (*fakeStore) ListFeeds(context.Context, core.ListFilter) ([]core.Feed, error) {
	return nil, nil
}
func (*fakeStore) DueFeeds(context.Context, time.Time) ([]core.Feed, error) {
	return nil, nil
}
func (*fakeStore) SetStatus(context.Context, string, core.FeedStatus) error { return nil }
func (*fakeStore) SetValidators(context.Context, string, string, string) error {
	return nil
}
func (*fakeStore) RecordSuccess(context.Context, string, time.Time, time.Time, string) (string, error) {
	return "", nil
}
func (*fakeStore) RecordFailure(context.Context, string, core.Category, string, time.Time, time.Time) error {
	return nil
}
func (*fakeStore) UpsertItems(context.Context, string, []core.Item) ([]core.Item, error) {
	return nil, nil
}
func (*fakeStore) QueryItems(context.Context, core.ItemQuery) (core.ItemQueryResult, error) {
	return core.ItemQueryResult{}, nil
}
func (*fakeStore) PruneItems(context.Context, core.PrunePolicy) (int, error) {
	return 0, nil
}
func (*fakeStore) SchemaVersion(context.Context) (int, error) { return 0, nil }
func (*fakeStore) Pending(context.Context) (int, error)       { return 0, nil }
func (*fakeStore) Migrate(context.Context) (int, error)       { return 0, nil }
func (*fakeStore) Close() error                               { return nil }

// Compile-time conformance: the fake satisfies store.Store.
var _ store.Store = (*fakeStore)(nil)
