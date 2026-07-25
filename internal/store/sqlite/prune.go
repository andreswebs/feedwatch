package sqlite

import (
	"context"
	"fmt"

	"github.com/andreswebs/feedwatch/internal/core"
)

// PruneItems trims stored history per the policy, tombstoning matched rows and
// clearing their heavy content while preserving the (feed_url, dedup_key)
// fingerprint so a still-advertised item is never re-emitted as new. Age and
// per-feed-count limits compose; the row, dedup key, and dates are never
// deleted here. Returns the number of rows newly tombstoned.
func (s *Store) PruneItems(ctx context.Context, p core.PrunePolicy) (int, error) {
	var total int

	if p.KeepBefore != nil {
		res, err := s.db.ExecContext(ctx,
			`UPDATE items SET tombstoned = 1, content_html = '', content_text = '', summary = ''
			 WHERE tombstoned = 0 AND COALESCE(published_at, fetched_at) < ?`,
			formatTime(*p.KeepBefore))
		if err != nil {
			return total, fmt.Errorf("prune by age: %w", err)
		}
		n, _ := res.RowsAffected()
		total += int(n)
	}

	if p.MaxPerFeed > 0 {
		res, err := s.db.ExecContext(ctx,
			`UPDATE items SET tombstoned = 1, content_html = '', content_text = '', summary = ''
			 WHERE tombstoned = 0 AND rowid IN (
				SELECT rowid FROM (
					SELECT rowid, ROW_NUMBER() OVER (
						PARTITION BY feed_url
						ORDER BY COALESCE(published_at, fetched_at) DESC, dedup_key DESC
					) AS rn
					FROM items WHERE tombstoned = 0
				) WHERE rn > ?
			 )`,
			p.MaxPerFeed)
		if err != nil {
			return total, fmt.Errorf("prune by max-per-feed: %w", err)
		}
		n, _ := res.RowsAffected()
		total += int(n)
	}

	return total, nil
}
