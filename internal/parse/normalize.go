package parse

import (
	"strings"
	"time"

	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/mmcdole/gofeed"
	"golang.org/x/net/html"
)

// RawItem is the parser's intermediate: a gofeed item plus the few signals the
// universal gofeed model discards. The universal model drops an entry's
// xml:base and the source content's MIME type (e.g. the Atom content type
// attribute), so a parser that can recover them supplies them here; the gofeed
// path leaves them empty. Keeping them on RawItem rather than re-deriving them
// inside Normalize is what lets Normalize stay a pure, table-tested mapping.
type RawItem struct {
	Item        *gofeed.Item
	XMLBase     string // entry-level xml:base, when known
	ContentType string // source content MIME, when the feed declares it
}

// Normalize maps a RawItem onto the documented core.Item shape, applying the
// content, summary, and author precedence cascades, the UTC/null date rule, and
// base-URL resolution. It is pure and deterministic; the dedup key and feed-URL
// assignment are applied by later layers (poll and the store).
//
// feedBase is the feed-level base URL (a feed's link or xml:base) used as a
// fallback before feedURL, the canonical subscription URL. feedAuthor is the
// feed's managing editor, the middle tier of the author cascade.
func Normalize(raw RawItem, feedBase, feedURL, feedAuthor string) core.Item {
	it := raw.Item
	out := core.Item{
		GUID:            it.GUID,
		Title:           it.Title,
		Link:            it.Link,
		ContentMIMEType: raw.ContentType,
		Categories:      it.Categories,
		PublishedAt:     toUTC(it.PublishedParsed),
		UpdatedAt:       toUTC(it.UpdatedParsed),
	}

	out.ContentHTML = it.Content
	if out.ContentHTML == "" {
		out.ContentHTML = it.Description
	}
	out.ContentText = htmlToText(out.ContentHTML)

	out.Summary = it.Description
	if out.Summary == "" && it.ITunesExt != nil {
		out.Summary = it.ITunesExt.Summary
	}

	out.Author = firstNonEmpty(itemAuthor(it), feedAuthor, dcCreator(it))

	out.BaseURL = firstNonEmpty(raw.XMLBase, it.Link, feedBase, feedURL)

	for _, e := range it.Enclosures {
		if e == nil {
			continue
		}
		out.Enclosures = append(out.Enclosures, core.Enclosure{
			URL:    e.URL,
			Type:   e.Type,
			Length: parseLength(e.Length),
		})
	}
	return out
}

// toUTC normalizes a parsed timestamp to UTC, preserving a nil (unparseable or
// absent) value so dates are never fabricated.
func toUTC(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

// itemAuthor returns the entry's own author name, or "" when absent.
func itemAuthor(it *gofeed.Item) string {
	if len(it.Authors) > 0 && it.Authors[0] != nil {
		return it.Authors[0].Name
	}
	return ""
}

// dcCreator returns the first Dublin Core dc:creator, the last tier of the
// author cascade, or "" when absent.
func dcCreator(it *gofeed.Item) string {
	if it.DublinCoreExt != nil && len(it.DublinCoreExt.Creator) > 0 {
		return it.DublinCoreExt.Creator[0]
	}
	return ""
}

// firstNonEmpty returns the first argument that is not the empty string.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// blockElements are HTML elements whose boundaries introduce a line break when
// rendering content to plaintext, so adjacent blocks do not run together.
var blockElements = map[string]bool{
	"address": true, "article": true, "blockquote": true, "br": true,
	"div": true, "dd": true, "dt": true, "footer": true, "h1": true,
	"h2": true, "h3": true, "h4": true, "h5": true, "h6": true, "header": true,
	"hr": true, "li": true, "ol": true, "p": true, "pre": true, "section": true,
	"table": true, "tr": true, "ul": true,
}

// htmlToText renders content HTML to readable plaintext: it strips tags,
// resolves entities, drops script and style content, and separates block
// elements with newlines. An unparseable body falls back to its trimmed self.
func htmlToText(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	doc, err := html.Parse(strings.NewReader(s))
	if err != nil {
		return strings.TrimSpace(s)
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style") {
			return
		}
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		block := n.Type == html.ElementNode && blockElements[n.Data]
		if block {
			b.WriteByte('\n')
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if block {
			b.WriteByte('\n')
		}
	}
	walk(doc)
	return collapseLines(b.String())
}

// collapseLines trims and collapses interior whitespace per line, drops blank
// lines, and joins the survivors with single newlines.
func collapseLines(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, ln := range lines {
		if ln = strings.Join(strings.Fields(ln), " "); ln != "" {
			out = append(out, ln)
		}
	}
	return strings.Join(out, "\n")
}
