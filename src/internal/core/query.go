package core

import "time"

// ListFilter narrows a feed listing. A zero value matches every feed.
type ListFilter struct {
	Status FeedStatus // "" matches any status
}

// ItemOrder controls the sort of an item query.
type ItemOrder struct {
	Field string // "published" | "fetched"
	Desc  bool
}

// ItemQuery is the filter, projection, sort, and pagination for item history.
type ItemQuery struct {
	Feeds    []string // url or alias; empty matches all
	Since    *time.Time
	Until    *time.Time
	Contains string
	Limit    int
	Offset   int
	Order    ItemOrder
	Fields   []string // projection; empty selects all fields
}

// ValidItemFields is the set of field names accepted by `items --fields`. It is
// the single source of truth for projection validity; the store's
// name-to-column mapping is keyed by these same names. `feed_url` is the
// always-on identity field and is not selectable (it is emitted regardless).
var ValidItemFields = map[string]bool{
	"id": true, "title": true, "link": true, "summary": true,
	"content_html": true, "content_text": true, "content_mime_type": true,
	"base_url": true, "author": true, "categories": true, "enclosures": true,
	"published_at": true, "updated_at": true,
}

// ProjectItem renders it into a map keyed by the requested field names, always
// including the `feed_url` identity field. A requested field is always present
// in the result (even when empty), so the projection is deterministic; unknown
// names are ignored here and are rejected earlier at the CLI boundary. The keys
// match the agent-facing JSON names in Item's tags.
func ProjectItem(it Item, fields []string) map[string]any {
	out := map[string]any{"feed_url": it.FeedURL}
	for _, f := range fields {
		switch f {
		case "id":
			out["id"] = it.GUID
		case "title":
			out["title"] = it.Title
		case "link":
			out["link"] = it.Link
		case "summary":
			out["summary"] = it.Summary
		case "content_html":
			out["content_html"] = it.ContentHTML
		case "content_text":
			out["content_text"] = it.ContentText
		case "content_mime_type":
			out["content_mime_type"] = it.ContentMIMEType
		case "base_url":
			out["base_url"] = it.BaseURL
		case "author":
			out["author"] = it.Author
		case "categories":
			out["categories"] = it.Categories
		case "enclosures":
			out["enclosures"] = it.Enclosures
		case "published_at":
			out["published_at"] = it.PublishedAt
		case "updated_at":
			out["updated_at"] = it.UpdatedAt
		}
	}
	return out
}

// PrunePolicy bounds stored history by age, per-feed count, or both. A pruned
// item's dedup fingerprint is preserved so it is never re-emitted as new.
type PrunePolicy struct {
	KeepBefore *time.Time // delete items older than this
	MaxPerFeed int        // keep at most this many per feed; 0 disables
}
