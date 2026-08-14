package feedwatch_test

import (
	"context"
	"strings"
	"testing"

	"github.com/andreswebs/feedwatch"
	"github.com/andreswebs/feedwatch/core"
	"github.com/andreswebs/feedwatch/internal/opml"
)

// seedSubscription subscribes url with an optional alias, failing the test on
// error. Unlike check's seedFeed it wires no network doubles, since the OPML use
// cases read and write subscriptions rather than fetching them.
func seedSubscription(t *testing.T, st interface {
	AddFeed(context.Context, core.Feed) (core.Feed, error)
}, url, alias string,
) {
	t.Helper()
	if _, err := st.AddFeed(context.Background(), core.Feed{URL: url, Alias: alias}); err != nil {
		t.Fatalf("seed AddFeed %s: %v", url, err)
	}
}

// TestExportEmptyStoreIsValidOPML covers an export with nothing subscribed: the
// document still parses as OPML and carries no outlines, so a frontend always
// has something well-formed to write.
func TestExportEmptyStoreIsValidOPML(t *testing.T) {
	app, _, _ := newTestApp(t)

	res, err := app.Export(context.Background(), feedwatch.ExportRequest{})
	if err != nil {
		t.Fatalf("Export = %v, want nil", err)
	}

	feeds, _, err := opml.Parse(strings.NewReader(res.OPML))
	if err != nil {
		t.Fatalf("export is not valid OPML: %v\ngot: %q", err, res.OPML)
	}
	if len(feeds) != 0 {
		t.Errorf("outlines = %d, want 0 on an empty store", len(feeds))
	}
}

// TestExportLabelsOutlines covers the outline label rule: a feed's alias when it
// has one, its URL when it does not, so every outline carries the OPML-required
// text without inventing a name.
func TestExportLabelsOutlines(t *testing.T) {
	app, st, _ := newTestApp(t)
	seedSubscription(t, st, "https://aliased.example/feed.xml", "godev")
	seedSubscription(t, st, "https://bare.example/feed.xml", "")

	res, err := app.Export(context.Background(), feedwatch.ExportRequest{})
	if err != nil {
		t.Fatalf("Export = %v, want nil", err)
	}

	feeds, _, err := opml.Parse(strings.NewReader(res.OPML))
	if err != nil {
		t.Fatalf("export is not valid OPML: %v\ngot: %q", err, res.OPML)
	}
	titles := make(map[string]string, len(feeds))
	for _, f := range feeds {
		titles[f.XMLURL] = f.Title
	}
	if got := titles["https://aliased.example/feed.xml"]; got != "godev" {
		t.Errorf("aliased outline title = %q, want the alias godev", got)
	}
	if got := titles["https://bare.example/feed.xml"]; got != "https://bare.example/feed.xml" {
		t.Errorf("unaliased outline title = %q, want the feed URL", got)
	}
}

// seedTaggedSubscription subscribes url with the given tags, failing the test on
// error. Tags are set at creation time because the store's upsert assigns them
// only on create.
func seedTaggedSubscription(t *testing.T, st interface {
	AddFeed(context.Context, core.Feed) (core.Feed, error)
}, url string, tags ...string,
) {
	t.Helper()
	if _, err := st.AddFeed(context.Background(), core.Feed{URL: url, Tags: tags}); err != nil {
		t.Fatalf("seed AddFeed %s: %v", url, err)
	}
}

// TestExportCarriesFeedTags covers behavior 3: each outline carries its feed's
// tags, so a lane assignment survives the document.
func TestExportCarriesFeedTags(t *testing.T) {
	app, st, _ := newTestApp(t)
	seedTaggedSubscription(t, st, "https://tagged.example/feed.xml", "ai", "agents")
	seedTaggedSubscription(t, st, "https://bare.example/feed.xml")

	res, err := app.Export(context.Background(), feedwatch.ExportRequest{})
	if err != nil {
		t.Fatalf("Export = %v, want nil", err)
	}

	feeds, _, err := opml.Parse(strings.NewReader(res.OPML))
	if err != nil {
		t.Fatalf("export is not valid OPML: %v\ngot: %q", err, res.OPML)
	}
	tags := make(map[string]string, len(feeds))
	for _, f := range feeds {
		tags[f.XMLURL] = strings.Join(f.Tags, "|")
	}
	if got, want := tags["https://tagged.example/feed.xml"], "agents|ai"; got != want {
		t.Errorf("tagged outline tags = %q, want %q (canonicalized)", got, want)
	}
	if got := tags["https://bare.example/feed.xml"]; got != "" {
		t.Errorf("untagged outline tags = %q, want none", got)
	}
}

// TestExportFiltersByTag covers behavior 4: --tag narrows the exported set to
// one lane, and an out-of-lane feed is absent from the document entirely.
func TestExportFiltersByTag(t *testing.T) {
	app, st, _ := newTestApp(t)
	seedTaggedSubscription(t, st, "https://in.example/feed.xml", "ai")
	seedTaggedSubscription(t, st, "https://out.example/feed.xml", "news")

	res, err := app.Export(context.Background(), feedwatch.ExportRequest{Tags: []string{"ai"}})
	if err != nil {
		t.Fatalf("Export = %v, want nil", err)
	}

	feeds, _, err := opml.Parse(strings.NewReader(res.OPML))
	if err != nil {
		t.Fatalf("export is not valid OPML: %v\ngot: %q", err, res.OPML)
	}
	if len(feeds) != 1 {
		t.Fatalf("feeds = %d, want only the in-lane one: %+v", len(feeds), feeds)
	}
	if feeds[0].XMLURL != "https://in.example/feed.xml" {
		t.Errorf("exported feed = %q, want the in-lane feed", feeds[0].XMLURL)
	}
}

// TestExportRejectsBadMatch covers the shared tag-filter rules reaching export:
// an unknown --match value is a usage error and nothing is exported.
func TestExportRejectsBadMatch(t *testing.T) {
	app, _, _ := newTestApp(t)

	_, err := app.Export(context.Background(), feedwatch.ExportRequest{Match: "bogus"})
	wantUsageError(t, err, `match must be 'all' or 'any', got "bogus"`)
}
