package parse

import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/mmcdole/gofeed"
	"github.com/mmcdole/gofeed/rss"
)

// GofeedParser implements Parser over github.com/mmcdole/gofeed, which
// auto-detects RSS 0.9x/1.0/2.0, Atom 1.0, and JSON Feed from a single parser,
// tolerates malformed XML, and resolves CDATA and entities. It performs raw
// field mapping only; date normalization, field precedence, base-URL
// resolution, and the dedup key are applied by later layers.
//
// GofeedParser is safe for concurrent use: a gofeed.Parser mutates shared
// translator state during a parse and is not concurrency-safe, so each Parse
// call constructs its own. The poll worker pool fetches and parses feeds in
// parallel, so this is a correctness requirement, not an optimization.
type GofeedParser struct{}

// Compile-time conformance: GofeedParser satisfies Parser.
var _ Parser = (*GofeedParser)(nil)

// New returns a GofeedParser ready to parse any supported feed format.
func New() *GofeedParser {
	return &GofeedParser{}
}

// Parse decodes a feed body into a ParsedFeed. A failure to parse returns a
// parse-category *core.FeedError scoped to baseURL.
func (p *GofeedParser) Parse(_ context.Context, body []byte, baseURL string) (ParsedFeed, error) {
	feed, err := gofeed.NewParser().Parse(bytes.NewReader(body))
	if err != nil {
		return ParsedFeed{}, core.ParseErr(baseURL, err)
	}

	feedBase := feed.Link
	feedAuthor := feedAuthorName(feed)

	pf := ParsedFeed{
		Title: feed.Title,
		TTL:   extractTTL(body, feed.FeedType),
		Items: make([]core.Item, 0, len(feed.Items)),
	}
	for _, it := range feed.Items {
		// The universal gofeed model drops an entry's xml:base and content
		// MIME type, so RawItem leaves them empty on this path; Normalize then
		// resolves the base URL from the item link, feed link, or feed URL.
		pf.Items = append(pf.Items, Normalize(RawItem{Item: it}, feedBase, baseURL, feedAuthor))
	}
	return pf, nil
}

// feedAuthorName returns the feed-level author (RSS managingEditor, or the Atom
// feed author, which gofeed unifies onto Authors), the middle tier of the item
// author cascade.
func feedAuthorName(feed *gofeed.Feed) string {
	if len(feed.Authors) > 0 && feed.Authors[0] != nil {
		return feed.Authors[0].Name
	}
	return ""
}

// parseLength converts an RSS enclosure length (a string of bytes) to int64,
// yielding 0 when the value is absent or unparseable.
func parseLength(s string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// extractTTL recovers the RSS <ttl> (in minutes) that the universal gofeed
// model discards. It is meaningful only for RSS; Atom and JSON Feed have no
// equivalent, so they yield 0. A re-parse with the RSS subparser is cheap for
// the small bodies feedwatch handles and keeps the universal path unchanged.
func extractTTL(body []byte, feedType string) time.Duration {
	if feedType != "rss" {
		return 0
	}
	rf, err := (&rss.Parser{}).Parse(bytes.NewReader(body))
	if err != nil || rf == nil {
		return 0
	}
	mins, err := strconv.Atoi(strings.TrimSpace(rf.TTL))
	if err != nil || mins <= 0 {
		return 0
	}
	return time.Duration(mins) * time.Minute
}
