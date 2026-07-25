package parse_test

import (
	"testing"
	"time"

	"github.com/andreswebs/feedwatch/internal/parse"
	"github.com/mmcdole/gofeed"
	ext "github.com/mmcdole/gofeed/extensions"
)

func TestNormalizeContentFallback(t *testing.T) {
	withContent := parse.Normalize(parse.RawItem{Item: &gofeed.Item{
		Content:     "<p>full body</p>",
		Description: "the description",
	}}, "", "https://feed.example/", "")
	if withContent.ContentHTML != "<p>full body</p>" {
		t.Errorf("content_html = %q, want the content", withContent.ContentHTML)
	}

	emptyContent := parse.Normalize(parse.RawItem{Item: &gofeed.Item{
		Content:     "",
		Description: "the description",
	}}, "", "https://feed.example/", "")
	if emptyContent.ContentHTML != "the description" {
		t.Errorf("content_html = %q, want fallback to description", emptyContent.ContentHTML)
	}
}

func TestNormalizeDates(t *testing.T) {
	// Absent/unparseable dates are never fabricated.
	none := parse.Normalize(parse.RawItem{Item: &gofeed.Item{}}, "", "u", "")
	if none.PublishedAt != nil {
		t.Errorf("published_at = %v, want nil for absent date", none.PublishedAt)
	}
	if none.UpdatedAt != nil {
		t.Errorf("updated_at = %v, want nil for absent date", none.UpdatedAt)
	}

	// A parsed non-UTC date is normalized to UTC.
	loc := time.FixedZone("PST", -8*3600)
	pub := time.Date(2026, 6, 27, 2, 0, 0, 0, loc)
	got := parse.Normalize(parse.RawItem{Item: &gofeed.Item{PublishedParsed: &pub}}, "", "u", "")
	if got.PublishedAt == nil {
		t.Fatal("published_at is nil, want a value")
	}
	if loc, off := got.PublishedAt.Zone(); off != 0 {
		t.Errorf("published_at zone = %q offset %d, want UTC", loc, off)
	}
	if !got.PublishedAt.Equal(time.Date(2026, 6, 27, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("published_at = %v, want the UTC instant", got.PublishedAt)
	}
}

func TestNormalizeAuthorCascade(t *testing.T) {
	itemAuthor := &gofeed.Item{
		Authors:       []*gofeed.Person{{Name: "Item Writer"}},
		DublinCoreExt: &ext.DublinCoreExtension{Creator: []string{"DC Creator"}},
	}
	if got := parse.Normalize(parse.RawItem{Item: itemAuthor}, "", "u", "Feed Editor").Author; got != "Item Writer" {
		t.Errorf("author = %q, want item author to win", got)
	}

	feedEditor := &gofeed.Item{
		DublinCoreExt: &ext.DublinCoreExtension{Creator: []string{"DC Creator"}},
	}
	if got := parse.Normalize(parse.RawItem{Item: feedEditor}, "", "u", "Feed Editor").Author; got != "Feed Editor" {
		t.Errorf("author = %q, want feed managingEditor", got)
	}

	dcOnly := &gofeed.Item{
		DublinCoreExt: &ext.DublinCoreExtension{Creator: []string{"DC Creator"}},
	}
	if got := parse.Normalize(parse.RawItem{Item: dcOnly}, "", "u", "").Author; got != "DC Creator" {
		t.Errorf("author = %q, want dc:creator fallback", got)
	}
}

func TestNormalizeBaseURL(t *testing.T) {
	cases := []struct {
		name    string
		raw     parse.RawItem
		feedURL string
		want    string
	}{
		{"xml:base wins", parse.RawItem{Item: &gofeed.Item{Link: "https://i/link"}, XMLBase: "https://base/"}, "https://feed/", "https://base/"},
		{"item link next", parse.RawItem{Item: &gofeed.Item{Link: "https://i/link"}}, "https://feed/", "https://i/link"},
		{"feed url last", parse.RawItem{Item: &gofeed.Item{}}, "https://feed/", "https://feed/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parse.Normalize(tc.raw, "", tc.feedURL, "").BaseURL; got != tc.want {
				t.Errorf("base_url = %q, want %q", got, tc.want)
			}
		})
	}

	// feedBase is the tier between the item link and the canonical feed URL.
	noLink := parse.RawItem{Item: &gofeed.Item{}}
	if got := parse.Normalize(noLink, "https://feedbase/", "https://feedurl/", "").BaseURL; got != "https://feedbase/" {
		t.Errorf("base_url = %q, want feed base before feed url", got)
	}
}

func TestNormalizeMultipleEnclosures(t *testing.T) {
	it := &gofeed.Item{Enclosures: []*gofeed.Enclosure{
		{URL: "https://e/1.mp3", Type: "audio/mpeg", Length: "5768960"},
		nil,
		{URL: "https://e/2.pdf", Type: "application/pdf", Length: "not-a-number"},
	}}
	got := parse.Normalize(parse.RawItem{Item: it}, "", "u", "").Enclosures
	if len(got) != 2 {
		t.Fatalf("enclosures = %d, want 2 (nil dropped)", len(got))
	}
	if got[0].Length != 5768960 {
		t.Errorf("enclosure[0] length = %d, want 5768960", got[0].Length)
	}
	if got[1].URL != "https://e/2.pdf" || got[1].Length != 0 {
		t.Errorf("enclosure[1] = %+v, want pdf with length 0", got[1])
	}
}

func TestNormalizeContentText(t *testing.T) {
	it := &gofeed.Item{Content: `<p>Hello <b>world</b> &amp; friends</p><p>Second line</p>`}
	got := parse.Normalize(parse.RawItem{Item: it}, "", "u", "").ContentText
	want := "Hello world & friends\nSecond line"
	if got != want {
		t.Errorf("content_text = %q, want %q", got, want)
	}
}

func TestNormalizeSummaryITunesFallback(t *testing.T) {
	it := &gofeed.Item{ITunesExt: &ext.ITunesItemExtension{Summary: "podcast summary"}}
	if got := parse.Normalize(parse.RawItem{Item: it}, "", "u", "").Summary; got != "podcast summary" {
		t.Errorf("summary = %q, want iTunes summary fallback", got)
	}
}

func TestNormalizeContentMIME(t *testing.T) {
	it := &gofeed.Item{Content: "<p>x</p>"}
	got := parse.Normalize(parse.RawItem{Item: it, ContentType: "application/xhtml+xml"}, "", "u", "")
	if got.ContentMIMEType != "application/xhtml+xml" {
		t.Errorf("content_mime_type = %q, want passthrough from RawItem", got.ContentMIMEType)
	}
}
