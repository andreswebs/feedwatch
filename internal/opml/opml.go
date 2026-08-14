package opml

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// Feed is a feed subscription extracted from an OPML outline.
type Feed struct {
	// XMLURL is the resolved feed URL, taken from the xmlUrl attribute and
	// falling back to the non-standard url attribute.
	XMLURL string
	// Title is the display label, taken from the text attribute and falling back
	// to title. It may be empty.
	Title string
	// Tags are the lane names carried by the category attribute. OPML 2.0 defines
	// category as a comma-separated list, and a feedwatch tag can hold no comma,
	// so a plain join needs no escaping. Slash-delimited hierarchy is not
	// interpreted: a tag containing a slash is one tag.
	Tags []string
}

// Invalid is a feed-like outline that carried no usable URL. It is reported
// separately so a single bad entry never fails the whole import.
type Invalid struct {
	Title  string
	Reason string
}

// document is the parsed OPML tree. Only the structure feedwatch consumes is
// modeled; unknown attributes and elements are ignored.
type document struct {
	XMLName xml.Name `xml:"opml"`
	Body    body     `xml:"body"`
}

type body struct {
	Outlines []outline `xml:"outline"`
}

type outline struct {
	Text     string    `xml:"text,attr"`
	Title    string    `xml:"title,attr"`
	Type     string    `xml:"type,attr"`
	XMLURL   string    `xml:"xmlUrl,attr"`
	URL      string    `xml:"url,attr"`
	Category string    `xml:"category,attr"`
	Outlines []outline `xml:"outline"`
}

// Parse reads an OPML 2.0 document and walks its outlines recursively, returning
// the feed entries found and any feed-like outlines that lacked a usable URL.
// Folder outlines (those with children and no feed identity) are traversed but
// not reported as feeds. A document that is not valid XML returns an error.
func Parse(r io.Reader) (feeds []Feed, invalid []Invalid, err error) {
	var doc document
	if err := xml.NewDecoder(r).Decode(&doc); err != nil {
		return nil, nil, fmt.Errorf("parse opml: %w", err)
	}
	walk(doc.Body.Outlines, &feeds, &invalid)
	return feeds, invalid, nil
}

// walk recurses through the outline tree, classifying each outline as a feed, an
// invalid feed-like entry, or a plain container.
func walk(outlines []outline, feeds *[]Feed, invalid *[]Invalid) {
	for _, o := range outlines {
		url := firstNonEmpty(o.XMLURL, o.URL)
		title := firstNonEmpty(o.Text, o.Title)

		if url != "" {
			*feeds = append(*feeds, Feed{XMLURL: url, Title: title, Tags: splitCategory(o.Category)})
		} else if isFeedType(o.Type) {
			*invalid = append(*invalid, Invalid{Title: title, Reason: "outline declares a feed type but has no xmlUrl or url"})
		}

		if len(o.Outlines) > 0 {
			walk(o.Outlines, feeds, invalid)
		}
	}
}

// splitCategory reads an OPML category attribute into tags, trimming each
// element and dropping empties, so a foreign exporter's "ai, agents," yields two
// usable tags. It returns nil for an absent or all-empty attribute, so an
// untagged outline is indistinguishable from one that predates the attribute.
func splitCategory(category string) []string {
	if category == "" {
		return nil
	}
	var tags []string
	for _, part := range strings.Split(category, ",") {
		if t := strings.TrimSpace(part); t != "" {
			tags = append(tags, t)
		}
	}
	return tags
}

// isFeedType reports whether an outline type attribute marks it as a feed, so a
// feed-like outline missing its URL can be flagged rather than silently ignored
// as a folder.
func isFeedType(t string) bool {
	switch strings.ToLower(t) {
	case "rss", "atom", "rdf":
		return true
	default:
		return false
	}
}

// exportDoc is the OPML 2.0 document feedwatch emits. It models only the head
// title and the flat outline list export produces; the richer tree that Parse
// accepts on import is not reconstructed here.
type exportDoc struct {
	XMLName xml.Name   `xml:"opml"`
	Version string     `xml:"version,attr"`
	Head    exportHead `xml:"head"`
	Body    exportBody `xml:"body"`
}

type exportHead struct {
	Title string `xml:"title"`
}

type exportBody struct {
	Outlines []exportOutline `xml:"outline"`
}

type exportOutline struct {
	Type   string `xml:"type,attr"`
	Text   string `xml:"text,attr"`
	Title  string `xml:"title,attr"`
	XMLURL string `xml:"xmlUrl,attr"`
	// Category follows XMLURL because encoding/xml emits attributes in field
	// order, and omitempty keeps an untagged feed's outline byte-identical to
	// what feedwatch emitted before tags existed.
	Category string `xml:"category,attr,omitempty"`
}

// Write serializes feeds as a valid OPML 2.0 document, one type="rss" outline
// per feed carrying its xmlUrl, its Title as both text and title, and its tags
// as a comma-joined category attribute omitted entirely when there are none. The
// output round-trips through Parse. encoding/xml handles attribute escaping, so titles
// and URLs bearing XML metacharacters stay well-formed.
func Write(w io.Writer, feeds []Feed) error {
	doc := exportDoc{
		Version: "2.0",
		Head:    exportHead{Title: "feedwatch subscriptions"},
		Body:    exportBody{Outlines: make([]exportOutline, 0, len(feeds))},
	}
	for _, f := range feeds {
		doc.Body.Outlines = append(doc.Body.Outlines, exportOutline{
			Type:     "rss",
			Text:     f.Title,
			Title:    f.Title,
			XMLURL:   f.XMLURL,
			Category: strings.Join(f.Tags, ","),
		})
	}

	if _, err := io.WriteString(w, xml.Header); err != nil {
		return fmt.Errorf("write opml header: %w", err)
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return fmt.Errorf("encode opml: %w", err)
	}
	if _, err := io.WriteString(w, "\n"); err != nil {
		return fmt.Errorf("write opml trailing newline: %w", err)
	}
	return nil
}

// firstNonEmpty returns the first non-empty string in vals, or "".
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
