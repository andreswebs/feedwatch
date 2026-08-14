package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/andreswebs/feedwatch/core"
)

// tagPredicate builds a SQL fragment matching feeds carrying the requested
// tags, with its bound arguments. It returns an empty string for no tags, so a
// zero-valued filter adds no clause. The col argument names the feeds-table
// tags column to test, so both the feeds query and the items subquery can use
// it.
//
// SQLite has no array type, so a tag set is stored as a JSON array and matched
// by expanding it with json_each. This is the only SQL-side JSON use in the
// store (everything else marshals Go-side); modernc.org/sqlite ships the JSON1
// functions, so no build tag is needed.
func tagPredicate(col string, tags []string, m core.TagMatch) (string, []any) {
	canon := core.CanonicalTags(tags)
	if len(canon) == 0 {
		return "", nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(canon)), ", ")
	args := make([]any, 0, len(canon)+1)
	for _, t := range canon {
		args = append(args, t)
	}

	var b strings.Builder
	if m == core.MatchAny {
		b.WriteString("EXISTS (SELECT 1 FROM json_each(")
		b.WriteString(col)
		b.WriteString(") WHERE value IN (")
		b.WriteString(placeholders)
		b.WriteString("))")
		return b.String(), args
	}
	// MatchAll (the zero value) counts the distinct requested tags the feed
	// actually carries and demands all of them. Canonicalizing first is what
	// makes the comparison count correct when the caller repeats a spelling.
	b.WriteString("(SELECT count(DISTINCT value) FROM json_each(")
	b.WriteString(col)
	b.WriteString(") WHERE value IN (")
	b.WriteString(placeholders)
	b.WriteString(")) = ?")
	args = append(args, len(canon))
	return b.String(), args
}

// feedTagScope renders a tag filter as a predicate over the items table's
// feed_url, with its bound arguments, and an empty string when no tag is
// requested. Items carry no tags of their own: a lane is a property of the
// subscription, so an item is in-lane exactly when its feed is.
//
// It is a subquery rather than a JOIN on purpose. A JOIN would make updated_at
// ambiguous (both tables have it) and force every item column reference to be
// qualified, and the subquery form drops into any existing WHERE clause, which
// is what lets the item query and the two prune statements share it.
func feedTagScope(tags []string, m core.TagMatch) (string, []any) {
	pred, args := tagPredicate("feeds.tags", tags, m)
	if pred == "" {
		return "", nil
	}
	var b strings.Builder
	b.WriteString("feed_url IN (SELECT url FROM feeds WHERE ")
	b.WriteString(pred)
	b.WriteString(")")
	return b.String(), args
}

// encodeTags renders a tag set as the canonical JSON array stored in the tags
// column, never null and never a bare JSON null.
func encodeTags(tags []string) (string, error) {
	b, err := json.Marshal(core.CanonicalTags(tags))
	if err != nil {
		return "", fmt.Errorf("encode tags: %w", err)
	}
	return string(b), nil
}

// SetTags replaces a feed's tag set with the canonical form of tags, writing an
// empty array when tags is empty. It is keyed by exact URL, like SetStatus and
// SetValidators; add, remove, and clear semantics are computed by the caller.
func (s *Store) SetTags(ctx context.Context, url string, tags []string) error {
	encoded, err := encodeTags(tags)
	if err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE feeds SET tags = ?, updated_at = ? WHERE url = ?`,
		encoded, formatTime(s.now()), url); err != nil {
		return fmt.Errorf("set tags %q: %w", url, err)
	}
	return nil
}

// TagCounts returns each distinct tag with the number of subscriptions carrying
// it, sorted by tag, counting feeds of any status.
func (s *Store) TagCounts(ctx context.Context) ([]core.TagCount, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT value AS tag, count(*) AS feeds
		 FROM feeds, json_each(feeds.tags)
		 GROUP BY value
		 ORDER BY value`)
	if err != nil {
		return nil, fmt.Errorf("query tag counts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	counts := []core.TagCount{}
	for rows.Next() {
		var c core.TagCount
		if err := rows.Scan(&c.Tag, &c.Feeds); err != nil {
			return nil, fmt.Errorf("scan tag count: %w", err)
		}
		counts = append(counts, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tag counts: %w", err)
	}
	return counts, nil
}

// decodeTags parses the stored tags column into a tag set, always non-nil so a
// caller can range and marshal it without a nil check.
func decodeTags(stored string) ([]string, error) {
	tags := []string{}
	if stored == "" {
		return tags, nil
	}
	if err := json.Unmarshal([]byte(stored), &tags); err != nil {
		return nil, fmt.Errorf("decode tags %s: %w", strconv.Quote(stored), err)
	}
	if tags == nil {
		tags = []string{}
	}
	return tags, nil
}
