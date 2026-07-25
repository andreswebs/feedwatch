package parse_test

import (
	"context"
	"embed"
	"errors"
	"testing"
	"time"

	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/parse"
)

//go:embed testdata
var fixtures embed.FS

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := fixtures.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

func TestParseRSS2(t *testing.T) {
	p := parse.New()
	pf, err := p.Parse(context.Background(), readFixture(t, "rss2.xml"), "https://blog.example/")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(pf.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(pf.Items))
	}
	first := pf.Items[0]
	if first.Title != "First post" {
		t.Errorf("title = %q, want %q", first.Title, "First post")
	}
	if first.Link != "https://blog.example/first" {
		t.Errorf("link = %q", first.Link)
	}
	if first.GUID != "tag:blog.example,2026:first" {
		t.Errorf("guid = %q", first.GUID)
	}
	if first.Summary != "A short summary of the first post." {
		t.Errorf("summary = %q", first.Summary)
	}
	if want := []string{"go", "feeds"}; len(first.Categories) != 2 || first.Categories[0] != want[0] {
		t.Errorf("categories = %v, want %v", first.Categories, want)
	}
	if len(first.Enclosures) != 1 {
		t.Fatalf("enclosures = %d, want 1", len(first.Enclosures))
	}
	enc := first.Enclosures[0]
	if enc.URL != "https://blog.example/first.mp3" || enc.Type != "audio/mpeg" || enc.Length != 5768960 {
		t.Errorf("enclosure = %+v", enc)
	}
	if first.PublishedAt == nil || !first.PublishedAt.Equal(time.Date(2026, 6, 27, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("published_at = %v", first.PublishedAt)
	}
}

func TestParseRSS2TTL(t *testing.T) {
	p := parse.New()
	pf, err := p.Parse(context.Background(), readFixture(t, "rss2.xml"), "https://blog.example/")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if pf.TTL != 45*time.Minute {
		t.Errorf("ttl = %v, want 45m", pf.TTL)
	}
}

func TestParseAtom(t *testing.T) {
	p := parse.New()
	pf, err := p.Parse(context.Background(), readFixture(t, "atom.xml"), "https://atom.example/")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(pf.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(pf.Items))
	}
	it := pf.Items[0]
	if it.Title != "Atom entry one" {
		t.Errorf("title = %q", it.Title)
	}
	if it.GUID != "tag:atom.example,2026:one" {
		t.Errorf("guid = %q", it.GUID)
	}
	if it.Author != "Bob" {
		t.Errorf("author = %q, want Bob", it.Author)
	}
	if it.ContentHTML != `<p>Full <a href="/one">content</a>.</p>` {
		t.Errorf("content_html = %q", it.ContentHTML)
	}
	if it.Summary != "Summary of entry one." {
		t.Errorf("summary = %q", it.Summary)
	}
	if pf.TTL != 0 {
		t.Errorf("atom ttl = %v, want 0", pf.TTL)
	}
}

func TestParseJSONFeed(t *testing.T) {
	p := parse.New()
	pf, err := p.Parse(context.Background(), readFixture(t, "feed.json"), "https://json.example/")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(pf.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(pf.Items))
	}
	it := pf.Items[0]
	if it.Title != "JSON item one" {
		t.Errorf("title = %q", it.Title)
	}
	if it.GUID != "https://json.example/item-1" {
		t.Errorf("guid = %q", it.GUID)
	}
	if it.Author != "Carol" {
		t.Errorf("author = %q, want Carol", it.Author)
	}
}

func TestParseMalformedRecoverable(t *testing.T) {
	p := parse.New()
	pf, err := p.Parse(context.Background(), readFixture(t, "malformed.xml"), "https://sloppy.example/")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(pf.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(pf.Items))
	}
	if pf.Items[0].Title != "Recoverable post" {
		t.Errorf("title = %q", pf.Items[0].Title)
	}
}

func TestParseInvalidReturnsParseErr(t *testing.T) {
	p := parse.New()
	_, err := p.Parse(context.Background(), readFixture(t, "invalid.txt"), "https://bad.example/")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var fe *core.FeedError
	if !errors.As(err, &fe) {
		t.Fatalf("error is not *core.FeedError: %T", err)
	}
	if fe.Category != core.CatParse {
		t.Errorf("category = %q, want %q", fe.Category, core.CatParse)
	}
	if fe.FeedURL != "https://bad.example/" {
		t.Errorf("feed_url = %q", fe.FeedURL)
	}
}
