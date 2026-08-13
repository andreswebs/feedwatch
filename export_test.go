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
