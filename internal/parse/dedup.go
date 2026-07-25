package parse

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/andreswebs/feedwatch/internal/core"
)

// DedupKey derives the per-item deduplication key the store pairs with the feed
// URL to enforce (feed_url, dedup_key) uniqueness. The precedence is the raw
// GUID (RSS guid / Atom id), then the item link, then the title. The result is
// stable and deterministic. There is deliberately no link+published rung: a
// re-dated item must keep the same key so it is not re-emitted as new.
func DedupKey(it core.Item) string {
	if it.GUID != "" {
		return it.GUID
	}
	if it.Link != "" {
		return it.Link
	}
	if it.Title != "" {
		return it.Title
	}
	// Last resort: an item with no GUID, link, or title still needs a stable,
	// non-empty key. Hash the (empty) title and link so the result is a fixed
	// deterministic string rather than "".
	sum := sha256.Sum256([]byte(it.Title + "\x00" + it.Link))
	return "sha256:" + hex.EncodeToString(sum[:])
}
