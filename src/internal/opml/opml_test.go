package opml_test

import (
	"strings"
	"testing"

	"github.com/andreswebs/feedwatch/internal/opml"
)

// TestParseFlatOutline covers a flat OPML with two feeds: both are extracted
// with their xmlUrl and title.
func TestParseFlatOutline(t *testing.T) {
	doc := `<?xml version="1.0" encoding="UTF-8"?>
<opml version="2.0">
  <body>
    <outline type="rss" text="Alpha" xmlUrl="https://a.example/feed.xml"/>
    <outline type="rss" text="Beta" xmlUrl="https://b.example/feed.xml"/>
  </body>
</opml>`

	feeds, invalid, err := opml.Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(invalid) != 0 {
		t.Errorf("invalid = %v, want none", invalid)
	}
	if len(feeds) != 2 {
		t.Fatalf("feeds = %d, want 2: %+v", len(feeds), feeds)
	}
	if feeds[0].XMLURL != "https://a.example/feed.xml" || feeds[0].Title != "Alpha" {
		t.Errorf("feeds[0] = %+v", feeds[0])
	}
	if feeds[1].XMLURL != "https://b.example/feed.xml" || feeds[1].Title != "Beta" {
		t.Errorf("feeds[1] = %+v", feeds[1])
	}
}

// TestParseNestedFolders covers folders nested deeper than one level: every feed
// is found regardless of depth, and the folder outlines themselves are not
// reported as feeds.
func TestParseNestedFolders(t *testing.T) {
	doc := `<opml version="2.0"><body>
    <outline text="Tech">
      <outline text="Languages">
        <outline type="rss" text="Go" xmlUrl="https://go.example/feed.xml"/>
        <outline text="Web">
          <outline type="rss" text="HTML" xmlUrl="https://html.example/feed.xml"/>
        </outline>
      </outline>
    </outline>
    <outline type="rss" text="Top" xmlUrl="https://top.example/feed.xml"/>
  </body></opml>`

	feeds, invalid, err := opml.Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(invalid) != 0 {
		t.Errorf("invalid = %v, want none", invalid)
	}
	got := make(map[string]string, len(feeds))
	for _, f := range feeds {
		got[f.XMLURL] = f.Title
	}
	want := map[string]string{
		"https://go.example/feed.xml":   "Go",
		"https://html.example/feed.xml": "HTML",
		"https://top.example/feed.xml":  "Top",
	}
	if len(got) != len(want) {
		t.Fatalf("feeds = %+v, want %+v", got, want)
	}
	for url, title := range want {
		if got[url] != title {
			t.Errorf("feed %s title = %q, want %q", url, got[url], title)
		}
	}
}

// TestParseURLFallback covers an outline missing xmlUrl: the URL falls back to
// the non-standard url attribute, and the title falls back to title when text
// is absent.
func TestParseURLFallback(t *testing.T) {
	doc := `<opml version="2.0"><body>
    <outline type="rss" title="Legacy" url="https://legacy.example/feed.xml"/>
  </body></opml>`

	feeds, invalid, err := opml.Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(invalid) != 0 {
		t.Errorf("invalid = %v, want none", invalid)
	}
	if len(feeds) != 1 {
		t.Fatalf("feeds = %d, want 1", len(feeds))
	}
	if feeds[0].XMLURL != "https://legacy.example/feed.xml" {
		t.Errorf("XMLURL = %q, want url fallback", feeds[0].XMLURL)
	}
	if feeds[0].Title != "Legacy" {
		t.Errorf("Title = %q, want title fallback", feeds[0].Title)
	}
}

// TestParseInvalidEntry covers a feed-like outline with no usable URL: it is
// reported as invalid while the surrounding valid feeds still parse.
func TestParseInvalidEntry(t *testing.T) {
	doc := `<opml version="2.0"><body>
    <outline type="rss" text="Good" xmlUrl="https://good.example/feed.xml"/>
    <outline type="rss" text="Broken"/>
  </body></opml>`

	feeds, invalid, err := opml.Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(feeds) != 1 || feeds[0].XMLURL != "https://good.example/feed.xml" {
		t.Fatalf("feeds = %+v, want only the good feed", feeds)
	}
	if len(invalid) != 1 {
		t.Fatalf("invalid = %d, want 1: %+v", len(invalid), invalid)
	}
	if invalid[0].Title != "Broken" || invalid[0].Reason == "" {
		t.Errorf("invalid[0] = %+v, want titled Broken with a reason", invalid[0])
	}
}

// TestParseMalformedXML covers a body that is not valid XML: Parse returns an
// error rather than silently yielding nothing.
func TestParseMalformedXML(t *testing.T) {
	_, _, err := opml.Parse(strings.NewReader("not xml <<<"))
	if err == nil {
		t.Fatal("Parse of malformed XML should error")
	}
}

// TestWriteRoundTrip covers Write emitting OPML that Parse reads back: two feeds
// with aliases round-trip their URLs and titles.
func TestWriteRoundTrip(t *testing.T) {
	feeds := []opml.Feed{
		{XMLURL: "https://a.example/feed.xml", Title: "Alpha"},
		{XMLURL: "https://b.example/feed.xml", Title: "Beta"},
	}

	var buf strings.Builder
	if err := opml.Write(&buf, feeds); err != nil {
		t.Fatalf("Write: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, `version="2.0"`) {
		t.Errorf("output missing OPML 2.0 version attribute:\n%s", out)
	}

	parsed, invalid, err := opml.Parse(strings.NewReader(out))
	if err != nil {
		t.Fatalf("Parse of emitted OPML: %v", err)
	}
	if len(invalid) != 0 {
		t.Errorf("invalid = %v, want none", invalid)
	}
	got := make(map[string]string, len(parsed))
	for _, f := range parsed {
		got[f.XMLURL] = f.Title
	}
	want := map[string]string{
		"https://a.example/feed.xml": "Alpha",
		"https://b.example/feed.xml": "Beta",
	}
	if len(got) != len(want) {
		t.Fatalf("parsed feeds = %+v, want %+v", got, want)
	}
	for url, title := range want {
		if got[url] != title {
			t.Errorf("feed %s title = %q, want %q", url, got[url], title)
		}
	}
}

// TestWriteEscapesSpecialChars covers a title carrying XML metacharacters: the
// emitted document stays well-formed and the title round-trips verbatim.
func TestWriteEscapesSpecialChars(t *testing.T) {
	feeds := []opml.Feed{
		{XMLURL: "https://x.example/feed.xml?a=1&b=2", Title: `Tom & "Jerry" <news>`},
	}

	var buf strings.Builder
	if err := opml.Write(&buf, feeds); err != nil {
		t.Fatalf("Write: %v", err)
	}

	parsed, _, err := opml.Parse(strings.NewReader(buf.String()))
	if err != nil {
		t.Fatalf("Parse of emitted OPML: %v", err)
	}
	if len(parsed) != 1 {
		t.Fatalf("parsed feeds = %d, want 1", len(parsed))
	}
	if parsed[0].XMLURL != "https://x.example/feed.xml?a=1&b=2" {
		t.Errorf("XMLURL = %q, want the ampersand-bearing URL", parsed[0].XMLURL)
	}
	if parsed[0].Title != `Tom & "Jerry" <news>` {
		t.Errorf("Title = %q, want the metacharacter title verbatim", parsed[0].Title)
	}
}

// TestWriteEmpty covers no subscriptions: Write emits a valid empty OPML
// document that Parse reads back with no feeds.
func TestWriteEmpty(t *testing.T) {
	var buf strings.Builder
	if err := opml.Write(&buf, nil); err != nil {
		t.Fatalf("Write: %v", err)
	}
	feeds, _, err := opml.Parse(strings.NewReader(buf.String()))
	if err != nil {
		t.Fatalf("Parse of emitted empty OPML: %v", err)
	}
	if len(feeds) != 0 {
		t.Errorf("feeds = %d, want 0", len(feeds))
	}
}
