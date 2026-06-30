package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/andreswebs/feedwatch/internal/core"
)

// UpsertItems inserts items for a feed in one transaction, returning only those
// whose (feed_url, dedup_key) was previously absent. An existing live row has
// its mutable content refreshed; a tombstoned row is left untouched so a pruned
// item is never resurrected or re-emitted as new.
func (s *Store) UpsertItems(ctx context.Context, feedURL string, items []core.Item) ([]core.Item, error) {
	if len(items) == 0 {
		return nil, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin upsert: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var newItems []core.Item
	for _, it := range items {
		it.FeedURL = feedURL
		isNew, err := upsertOne(ctx, tx, s.now(), it)
		if err != nil {
			return nil, err
		}
		if isNew {
			newItems = append(newItems, it)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit upsert: %w", err)
	}
	committed = true
	return newItems, nil
}

// upsertOne classifies and writes a single item within the transaction.
func upsertOne(ctx context.Context, tx *sql.Tx, now time.Time, it core.Item) (bool, error) {
	var tombstoned int
	err := tx.QueryRowContext(ctx,
		`SELECT tombstoned FROM items WHERE feed_url = ? AND dedup_key = ?`,
		it.FeedURL, it.DedupKey).Scan(&tombstoned)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return true, insertItem(ctx, tx, now, it)
	case err != nil:
		return false, fmt.Errorf("lookup item %q: %w", it.DedupKey, err)
	case tombstoned == 1:
		return false, nil // pruned fingerprint: never resurrect
	default:
		return false, refreshItem(ctx, tx, it)
	}
}

func insertItem(ctx context.Context, tx *sql.Tx, now time.Time, it core.Item) error {
	fetchedAt := it.FetchedAt
	if fetchedAt.IsZero() {
		fetchedAt = now
	}
	cats, encs, err := encodeItemJSON(it)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO items (feed_url, dedup_key, guid, title, link, summary,
			content_html, content_text, content_mime_type, base_url, author,
			categories, enclosures, published_at, updated_at, fetched_at, seen)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1)`,
		it.FeedURL, it.DedupKey, it.GUID, it.Title, it.Link, it.Summary,
		it.ContentHTML, it.ContentText, it.ContentMIMEType, it.BaseURL, it.Author,
		cats, encs, formatTimePtr(it.PublishedAt), formatTimePtr(it.UpdatedAt),
		formatTime(fetchedAt)); err != nil {
		return fmt.Errorf("insert item %q: %w", it.DedupKey, err)
	}
	return nil
}

func refreshItem(ctx context.Context, tx *sql.Tx, it core.Item) error {
	cats, encs, err := encodeItemJSON(it)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE items SET guid = ?, title = ?, link = ?, summary = ?,
			content_html = ?, content_text = ?, content_mime_type = ?, base_url = ?,
			author = ?, categories = ?, enclosures = ?, published_at = ?, updated_at = ?
		 WHERE feed_url = ? AND dedup_key = ?`,
		it.GUID, it.Title, it.Link, it.Summary, it.ContentHTML, it.ContentText,
		it.ContentMIMEType, it.BaseURL, it.Author, cats, encs,
		formatTimePtr(it.PublishedAt), formatTimePtr(it.UpdatedAt),
		it.FeedURL, it.DedupKey); err != nil {
		return fmt.Errorf("refresh item %q: %w", it.DedupKey, err)
	}
	return nil
}

// encodeItemJSON renders categories and enclosures as JSON text, normalizing
// nil slices to empty arrays.
func encodeItemJSON(it core.Item) (categories, enclosures string, err error) {
	cats := it.Categories
	if cats == nil {
		cats = []string{}
	}
	cb, err := json.Marshal(cats)
	if err != nil {
		return "", "", fmt.Errorf("encode categories: %w", err)
	}
	encs := it.Enclosures
	if encs == nil {
		encs = []core.Enclosure{}
	}
	eb, err := json.Marshal(encs)
	if err != nil {
		return "", "", fmt.Errorf("encode enclosures: %w", err)
	}
	return string(cb), string(eb), nil
}

// itemColumns lists every selectable item column with the projectable field
// name agents pass via --fields. Identity and ordering columns have no field
// name and are always selected.
var itemColumns = []string{
	"feed_url", "dedup_key", "guid", "title", "link", "summary", "content_html",
	"content_text", "content_mime_type", "base_url", "author", "categories",
	"enclosures", "published_at", "updated_at", "fetched_at",
}

// fieldColumns maps an agent-facing field name to its physical column.
var fieldColumns = map[string]string{
	"id":                "guid",
	"title":             "title",
	"link":              "link",
	"summary":           "summary",
	"content_html":      "content_html",
	"content_text":      "content_text",
	"content_mime_type": "content_mime_type",
	"base_url":          "base_url",
	"author":            "author",
	"categories":        "categories",
	"enclosures":        "enclosures",
	"published_at":      "published_at",
	"updated_at":        "updated_at",
}

// alwaysColumns are selected regardless of projection: identity plus the
// columns ordering and date filters coalesce over.
var alwaysColumns = map[string]bool{
	"feed_url": true, "dedup_key": true, "published_at": true, "fetched_at": true,
}

// QueryItems returns stored, non-tombstoned items matching the query, honoring
// since/until/contains filters, ordering, pagination, and field projection.
func (s *Store) QueryItems(ctx context.Context, q core.ItemQuery) ([]core.Item, error) {
	cols := projectedColumns(q.Fields)
	where, args := itemFilters(q)

	var b strings.Builder
	b.WriteString("SELECT ")
	b.WriteString(strings.Join(cols, ", "))
	b.WriteString(" FROM items")
	b.WriteString(where)
	b.WriteString(itemOrder(q.Order))
	if q.Limit > 0 {
		b.WriteString(" LIMIT ? OFFSET ?")
		args = append(args, q.Limit, q.Offset)
	}

	rows, err := s.db.QueryContext(ctx, b.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("query items: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []core.Item
	for rows.Next() {
		it, err := scanItem(rows, cols)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate items: %w", err)
	}
	return out, nil
}

// projectedColumns resolves the column list for a query: all columns when no
// projection is requested, else the always-selected columns plus the mapped
// requested fields, preserving the canonical column order.
func projectedColumns(fields []string) []string {
	if len(fields) == 0 {
		return itemColumns
	}
	want := map[string]bool{}
	for c := range alwaysColumns {
		want[c] = true
	}
	for _, f := range fields {
		if col, ok := fieldColumns[f]; ok {
			want[col] = true
		}
	}
	cols := make([]string, 0, len(want))
	for _, c := range itemColumns {
		if want[c] {
			cols = append(cols, c)
		}
	}
	return cols
}

// itemFilters builds the WHERE clause (always excluding tombstoned rows) and
// its bound arguments.
func itemFilters(q core.ItemQuery) (string, []any) {
	clauses := []string{"tombstoned = 0"}
	var args []any

	if len(q.Feeds) > 0 {
		placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(q.Feeds)), ", ")
		clauses = append(clauses, fmt.Sprintf(
			`feed_url IN (SELECT url FROM feeds WHERE url IN (%s) OR alias IN (%[1]s))`,
			placeholders))
		for _, f := range q.Feeds {
			args = append(args, f)
		}
		for _, f := range q.Feeds {
			args = append(args, f)
		}
	}
	if q.Since != nil {
		clauses = append(clauses, "COALESCE(published_at, fetched_at) >= ?")
		args = append(args, formatTime(*q.Since))
	}
	if q.Until != nil {
		clauses = append(clauses, "COALESCE(published_at, fetched_at) <= ?")
		args = append(args, formatTime(*q.Until))
	}
	if q.Contains != "" {
		clauses = append(clauses, "(title LIKE ? OR content_text LIKE ? OR content_html LIKE ?)")
		like := "%" + q.Contains + "%"
		args = append(args, like, like, like)
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

// itemOrder renders the ORDER BY clause; "fetched" sorts on fetched_at,
// anything else on the published-or-fetched coalesce.
func itemOrder(o core.ItemOrder) string {
	expr := "COALESCE(published_at, fetched_at)"
	if o.Field == "fetched" {
		expr = "fetched_at"
	}
	dir := "ASC"
	if o.Desc {
		dir = "DESC"
	}
	return " ORDER BY " + expr + " " + dir + ", dedup_key " + dir
}

// scanItem reads one row into a core.Item, populating only the projected
// columns and leaving unselected fields zero.
func scanItem(rows *sql.Rows, cols []string) (core.Item, error) {
	var (
		it                                    core.Item
		guid, title, link, summary            string
		contentHTML, contentText, contentMIME string
		baseURL, author, categories           string
		enclosures                            string
		publishedAt, updatedAt                sql.NullString
		fetchedAt                             sql.NullString
	)
	targets := map[string]any{
		"feed_url": &it.FeedURL, "dedup_key": &it.DedupKey, "guid": &guid,
		"title": &title, "link": &link, "summary": &summary,
		"content_html": &contentHTML, "content_text": &contentText,
		"content_mime_type": &contentMIME, "base_url": &baseURL, "author": &author,
		"categories": &categories, "enclosures": &enclosures,
		"published_at": &publishedAt, "updated_at": &updatedAt, "fetched_at": &fetchedAt,
	}
	dest := make([]any, len(cols))
	for i, c := range cols {
		dest[i] = targets[c]
	}
	if err := rows.Scan(dest...); err != nil {
		return core.Item{}, fmt.Errorf("scan item: %w", err)
	}

	it.GUID, it.Title, it.Link, it.Summary = guid, title, link, summary
	it.ContentHTML, it.ContentText, it.ContentMIMEType = contentHTML, contentText, contentMIME
	it.BaseURL, it.Author = baseURL, author

	if categories != "" {
		if err := json.Unmarshal([]byte(categories), &it.Categories); err != nil {
			return core.Item{}, fmt.Errorf("decode categories: %w", err)
		}
	}
	if enclosures != "" {
		if err := json.Unmarshal([]byte(enclosures), &it.Enclosures); err != nil {
			return core.Item{}, fmt.Errorf("decode enclosures: %w", err)
		}
	}

	var err error
	if it.PublishedAt, err = parseTimePtr(publishedAt); err != nil {
		return core.Item{}, err
	}
	if it.UpdatedAt, err = parseTimePtr(updatedAt); err != nil {
		return core.Item{}, err
	}
	if fetchedAt.Valid {
		t, perr := time.Parse(timeLayout, fetchedAt.String)
		if perr != nil {
			return core.Item{}, fmt.Errorf("parse fetched_at: %w", perr)
		}
		it.FetchedAt = t.UTC()
	}
	return it, nil
}
