package sqlite

import (
	"context"
	"fmt"
	"strings"

	"github.com/andreswebs/feedwatch/core"
)

// PruneItems trims stored history per the policy, tombstoning matched rows and
// clearing their heavy content while preserving the (feed_url, dedup_key)
// fingerprint so a still-advertised item is never re-emitted as new. Age and
// per-feed-count limits compose; the row, dedup key, and dates are never
// deleted here. Returns the number of rows newly tombstoned.
func (s *Store) PruneItems(ctx context.Context, p core.PrunePolicy) (int, error) {
	var total int
	scope, scopeArgs := feedTagScope(p.Tags, p.Match)

	if p.KeepBefore != nil {
		var b strings.Builder
		b.WriteString(`UPDATE items SET tombstoned = 1, content_html = '', content_text = '', summary = ''
			 WHERE tombstoned = 0 AND COALESCE(published_at, fetched_at) < ?`)
		args := []any{formatTime(*p.KeepBefore)}
		if scope != "" {
			b.WriteString(" AND ")
			b.WriteString(scope)
			args = append(args, scopeArgs...)
		}
		res, err := s.db.ExecContext(ctx, b.String(), args...)
		if err != nil {
			return total, fmt.Errorf("prune by age: %w", err)
		}
		n, _ := res.RowsAffected()
		total += int(n)
	}

	if p.MaxPerFeed > 0 {
		// The scope is applied twice: once on the rows being tombstoned and once
		// on the window's source. Scoping only the outer statement would still
		// rank each in-lane feed's rows against out-of-lane rows, so the rn > N
		// cutoff would fall in the wrong place. Arguments are appended in clause
		// order, so the scope's arguments appear twice too.
		var b strings.Builder
		var args []any
		b.WriteString(`UPDATE items SET tombstoned = 1, content_html = '', content_text = '', summary = ''
			 WHERE tombstoned = 0`)
		if scope != "" {
			b.WriteString(" AND ")
			b.WriteString(scope)
			args = append(args, scopeArgs...)
		}
		b.WriteString(` AND rowid IN (
				SELECT rowid FROM (
					SELECT rowid, ROW_NUMBER() OVER (
						PARTITION BY feed_url
						ORDER BY COALESCE(published_at, fetched_at) DESC, dedup_key DESC
					) AS rn
					FROM items WHERE tombstoned = 0`)
		if scope != "" {
			b.WriteString(" AND ")
			b.WriteString(scope)
			args = append(args, scopeArgs...)
		}
		b.WriteString(`
				) WHERE rn > ?
			 )`)
		args = append(args, p.MaxPerFeed)

		res, err := s.db.ExecContext(ctx, b.String(), args...)
		if err != nil {
			return total, fmt.Errorf("prune by max-per-feed: %w", err)
		}
		n, _ := res.RowsAffected()
		total += int(n)
	}

	return total, nil
}
