package store

import (
	"context"
	"time"

	"github.com/andreswebs/feedwatch/internal/core"
)

// Store persists subscriptions and item history behind a backend-agnostic
// interface. Methods take a context for cancellation and deadlines only.
// Implementations are safe for concurrent use across distinct feeds, which
// never share rows. Feed references (ref) resolve either an exact URL or a
// unique alias.
type Store interface {
	// AddFeed upserts a subscription, returning the stored feed.
	AddFeed(ctx context.Context, f core.Feed) (core.Feed, error)
	// RemoveFeed unsubscribes the feed identified by ref.
	RemoveFeed(ctx context.Context, ref string) error
	// GetFeed returns the feed identified by ref.
	GetFeed(ctx context.Context, ref string) (core.Feed, error)
	// ListFeeds returns the subscriptions matching the filter.
	ListFeeds(ctx context.Context, f core.ListFilter) ([]core.Feed, error)
	// DueFeeds returns active feeds whose next-due time is at or before now.
	DueFeeds(ctx context.Context, now time.Time) ([]core.Feed, error)
	// SetStatus enables or disables a feed.
	SetStatus(ctx context.Context, url string, s core.FeedStatus) error
	// SetValidators writes conditional-GET validators, skipping empty values.
	SetValidators(ctx context.Context, url, etag, lastModified string) error
	// RecordSuccess clears failure state and schedules the next poll. When
	// finalURL is non-empty and differs from url, the feed is renamed to
	// finalURL (a permanent-redirect rewrite), cascading to its items, unless
	// finalURL is already subscribed, in which case the rewrite is skipped.
	RecordSuccess(ctx context.Context, url string, fetchedAt, nextDue time.Time, finalURL string) error
	// RecordFailure increments failure state and schedules a backed-off retry.
	RecordFailure(ctx context.Context, url string, cat core.Category, msg string, at, nextDue time.Time) error
	// UpsertItems inserts items, returning only those not seen before.
	UpsertItems(ctx context.Context, feedURL string, items []core.Item) (newItems []core.Item, err error)
	// QueryItems returns stored items matching the query.
	QueryItems(ctx context.Context, q core.ItemQuery) ([]core.Item, error)
	// PruneItems deletes item rows per the policy, preserving dedup state.
	PruneItems(ctx context.Context, p core.PrunePolicy) (deleted int, err error)
	// SchemaVersion reports the applied migration version.
	SchemaVersion(ctx context.Context) (int, error)
	// Pending reports how many migrations have not yet been applied.
	Pending(ctx context.Context) (int, error)
	// Migrate applies pending migrations, returning the count applied.
	Migrate(ctx context.Context) (applied int, err error)
	// Close releases the underlying resources.
	Close() error
}
