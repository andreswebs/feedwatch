package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/andreswebs/feedwatch/internal/core"
)

const feedColumns = `url, alias, interval_seconds, status, etag, last_modified,
	failure_count, last_error, last_error_at, last_fetch_at, next_due_at,
	created_at, updated_at`

// AddFeed upserts a subscription keyed by URL and returns the stored feed. An
// alias already bound to a different URL is a usage error.
func (s *Store) AddFeed(ctx context.Context, f core.Feed) (core.Feed, error) {
	if f.Status == "" {
		f.Status = core.FeedActive
	}
	if f.Alias != "" {
		var owner string
		err := s.db.QueryRowContext(ctx,
			`SELECT url FROM feeds WHERE alias = ?`, f.Alias).Scan(&owner)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			// alias free
		case err != nil:
			return core.Feed{}, fmt.Errorf("check alias %q: %w", f.Alias, err)
		case owner != f.URL:
			return core.Feed{}, &core.FeedError{
				FeedURL:  f.URL,
				Category: core.CatUsage,
				Message:  fmt.Sprintf("alias %q already used by %s", f.Alias, owner),
			}
		}
	}

	now := s.now()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO feeds (url, alias, interval_seconds, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(url) DO UPDATE SET
			alias = excluded.alias,
			interval_seconds = excluded.interval_seconds,
			updated_at = excluded.updated_at`,
		f.URL, aliasArg(f.Alias), int64(f.Interval/time.Second), string(f.Status),
		formatTime(now), formatTime(now)); err != nil {
		return core.Feed{}, fmt.Errorf("add feed %q: %w", f.URL, err)
	}
	return s.GetFeed(ctx, f.URL)
}

// GetFeed returns the feed resolved by exact URL or unique alias.
func (s *Store) GetFeed(ctx context.Context, ref string) (core.Feed, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+feedColumns+` FROM feeds WHERE url = ? OR alias = ?`, ref, ref)
	f, err := scanFeed(row)
	if errors.Is(err, sql.ErrNoRows) {
		return core.Feed{}, &core.FeedError{
			FeedURL: ref, Category: core.CatUsage, Message: "feed not found",
		}
	}
	return f, err
}

// RemoveFeed unsubscribes the feed resolved by URL or alias, cascading to its
// items. Removing an unknown feed is a no-op.
func (s *Store) RemoveFeed(ctx context.Context, ref string) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM feeds WHERE url = ? OR alias = ?`, ref, ref); err != nil {
		return fmt.Errorf("remove feed %q: %w", ref, err)
	}
	return nil
}

// ListFeeds returns subscriptions matching the filter, ordered by URL.
func (s *Store) ListFeeds(ctx context.Context, filter core.ListFilter) ([]core.Feed, error) {
	query := `SELECT ` + feedColumns + ` FROM feeds`
	var args []any
	if filter.Status != "" {
		query += ` WHERE status = ?`
		args = append(args, string(filter.Status))
	}
	query += ` ORDER BY url`
	return s.queryFeeds(ctx, query, args...)
}

// DueFeeds returns active feeds with no next-due time or one at or before now.
func (s *Store) DueFeeds(ctx context.Context, now time.Time) ([]core.Feed, error) {
	return s.queryFeeds(ctx,
		`SELECT `+feedColumns+` FROM feeds
		 WHERE status = ? AND (next_due_at IS NULL OR next_due_at <= ?)
		 ORDER BY url`,
		string(core.FeedActive), formatTime(now))
}

// SetStatus enables or disables a feed.
func (s *Store) SetStatus(ctx context.Context, url string, st core.FeedStatus) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE feeds SET status = ?, updated_at = ? WHERE url = ?`,
		string(st), formatTime(s.now()), url); err != nil {
		return fmt.Errorf("set status %q: %w", url, err)
	}
	return nil
}

// SetValidators writes conditional-GET validators, never overwriting a stored
// value with an empty one and skipping the write entirely when both are empty.
func (s *Store) SetValidators(ctx context.Context, url, etag, lastModified string) error {
	sets := []string{}
	args := []any{}
	if etag != "" {
		sets = append(sets, "etag = ?")
		args = append(args, etag)
	}
	if lastModified != "" {
		sets = append(sets, "last_modified = ?")
		args = append(args, lastModified)
	}
	if len(sets) == 0 {
		return nil
	}
	sets = append(sets, "updated_at = ?")
	args = append(args, formatTime(s.now()), url)

	var b strings.Builder
	b.WriteString("UPDATE feeds SET ")
	b.WriteString(strings.Join(sets, ", "))
	b.WriteString(" WHERE url = ?")
	if _, err := s.db.ExecContext(ctx, b.String(), args...); err != nil {
		return fmt.Errorf("set validators %q: %w", url, err)
	}
	return nil
}

// RecordSuccess clears failure state and schedules the next poll. When finalURL
// names a permanent-redirect target distinct from url, the feed is renamed to
// it and its items cascade, all within one transaction; a target already
// subscribed is left alone (no merge).
func (s *Store) RecordSuccess(ctx context.Context, url string, fetchedAt, nextDue time.Time, finalURL string) error {
	if finalURL == "" || finalURL == url {
		if err := s.recordSuccessExec(ctx, s.db, url, url, fetchedAt, nextDue); err != nil {
			return err
		}
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin record success %q: %w", url, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// Renaming a primary key while child rows still reference it trips the
	// immediate items->feeds foreign key mid-transaction; deferring its check
	// to COMMIT lets both rows be rewritten in either order. The pragma resets
	// itself at COMMIT and is scoped to this transaction's connection.
	if _, err := tx.ExecContext(ctx, `PRAGMA defer_foreign_keys = ON`); err != nil {
		return fmt.Errorf("defer foreign keys: %w", err)
	}

	target := finalURL
	var taken int
	switch err := tx.QueryRowContext(ctx,
		`SELECT 1 FROM feeds WHERE url = ?`, finalURL).Scan(&taken); {
	case errors.Is(err, sql.ErrNoRows):
		// target free: proceed with the rename
	case err != nil:
		return fmt.Errorf("check redirect target %q: %w", finalURL, err)
	default:
		target = url // already subscribed: keep the original URL
	}

	if err := s.recordSuccessExec(ctx, tx, url, target, fetchedAt, nextDue); err != nil {
		return err
	}
	if target != url {
		if _, err := tx.ExecContext(ctx,
			`UPDATE items SET feed_url = ? WHERE feed_url = ?`, target, url); err != nil {
			return fmt.Errorf("rewrite items feed_url %q -> %q: %w", url, target, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit record success %q: %w", url, err)
	}
	committed = true
	return nil
}

// execer is the ExecContext surface shared by *sql.DB and *sql.Tx, letting
// recordSuccessExec run either standalone or inside the rename transaction.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// recordSuccessExec writes the success bookkeeping for the feed at url, setting
// its (possibly renamed) URL to target.
func (s *Store) recordSuccessExec(ctx context.Context, ex execer, url, target string, fetchedAt, nextDue time.Time) error {
	if _, err := ex.ExecContext(ctx,
		`UPDATE feeds SET failure_count = 0, last_error = '', last_error_at = NULL,
			last_fetch_at = ?, next_due_at = ?, url = ?, updated_at = ? WHERE url = ?`,
		formatTime(fetchedAt), formatTime(nextDue), target, formatTime(s.now()), url); err != nil {
		return fmt.Errorf("record success %q: %w", url, err)
	}
	return nil
}

// RecordFailure increments failure state and schedules a backed-off retry.
func (s *Store) RecordFailure(ctx context.Context, url string, _ core.Category, msg string, at, nextDue time.Time) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE feeds SET failure_count = failure_count + 1, last_error = ?,
			last_error_at = ?, next_due_at = ?, updated_at = ? WHERE url = ?`,
		msg, formatTime(at), formatTime(nextDue), formatTime(s.now()), url); err != nil {
		return fmt.Errorf("record failure %q: %w", url, err)
	}
	return nil
}

// queryFeeds runs a feed query and scans the full result set.
func (s *Store) queryFeeds(ctx context.Context, query string, args ...any) ([]core.Feed, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query feeds: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var feeds []core.Feed
	for rows.Next() {
		f, err := scanFeed(rows)
		if err != nil {
			return nil, err
		}
		feeds = append(feeds, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate feeds: %w", err)
	}
	return feeds, nil
}

// aliasArg maps an empty alias to SQL NULL so multiple aliasless feeds do not
// collide on the UNIQUE constraint.
func aliasArg(alias string) any {
	if alias == "" {
		return nil
	}
	return alias
}

// rowScanner abstracts *sql.Row and *sql.Rows for the shared feed scanner.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanFeed(row rowScanner) (core.Feed, error) {
	var (
		f                                    core.Feed
		alias, etag, lastModified, lastError sql.NullString
		lastErrorAt, lastFetchAt, nextDueAt  sql.NullString
		createdAt, updatedAt                 string
		intervalSeconds                      int64
		status                               string
	)
	if err := row.Scan(&f.URL, &alias, &intervalSeconds, &status, &etag,
		&lastModified, &f.FailureCount, &lastError, &lastErrorAt, &lastFetchAt,
		&nextDueAt, &createdAt, &updatedAt); err != nil {
		return core.Feed{}, err
	}

	f.Alias = alias.String
	f.Interval = time.Duration(intervalSeconds) * time.Second
	f.Status = core.FeedStatus(status)
	f.ETag = etag.String
	f.LastModified = lastModified.String
	f.LastError = lastError.String

	var err error
	if f.LastErrorAt, err = parseTimePtr(lastErrorAt); err != nil {
		return core.Feed{}, err
	}
	if f.LastFetchAt, err = parseTimePtr(lastFetchAt); err != nil {
		return core.Feed{}, err
	}
	if f.NextDueAt, err = parseTimePtr(nextDueAt); err != nil {
		return core.Feed{}, err
	}
	if f.CreatedAt, err = time.Parse(timeLayout, createdAt); err != nil {
		return core.Feed{}, fmt.Errorf("parse created_at: %w", err)
	}
	if f.UpdatedAt, err = time.Parse(timeLayout, updatedAt); err != nil {
		return core.Feed{}, fmt.Errorf("parse updated_at: %w", err)
	}
	f.CreatedAt = f.CreatedAt.UTC()
	f.UpdatedAt = f.UpdatedAt.UTC()
	return f, nil
}
