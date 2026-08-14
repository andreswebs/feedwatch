package feedwatch_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/andreswebs/feedwatch"
	"github.com/andreswebs/feedwatch/core"
)

// importDoc is a two-entry OPML outline, the smallest document that exercises
// both a fresh subscription and a sibling that is unaffected by it.
const importDoc = `<opml version="2.0"><body>
  <outline type="rss" text="Alpha" xmlUrl="https://a.example/feed.xml"/>
  <outline type="rss" text="Beta" xmlUrl="https://b.example/feed.xml"/>
</body></opml>`

// TestImportRoundTripsExport covers the pair as one contract: a document Export
// produced re-imports into the same store as all-skipped, adding nothing and
// failing nothing, so a backup restored twice is a no-op.
func TestImportRoundTripsExport(t *testing.T) {
	app, st, _ := newTestApp(t)
	ctx := context.Background()
	seedSubscription(t, st, "https://a.example/feed.xml", "Alpha")
	seedSubscription(t, st, "https://b.example/feed.xml", "")

	exported, err := app.Export(ctx, feedwatch.ExportRequest{})
	if err != nil {
		t.Fatalf("Export = %v, want nil", err)
	}

	res, err := app.Import(ctx, feedwatch.ImportRequest{OPML: []byte(exported.OPML)})
	if err != nil {
		t.Fatalf("Import = %v, want nil", err)
	}
	if res.Added != 0 || res.Skipped != 2 || len(res.Failed) != 0 {
		t.Errorf("added/skipped/failed = %d/%d/%v, want 0/2/none", res.Added, res.Skipped, res.Failed)
	}
}

// TestImportRejectsNonOPML covers a document that is not OPML at all: the whole
// use case fails with a usage-category error rather than reporting per-entry
// failures for a document it never understood.
func TestImportRejectsNonOPML(t *testing.T) {
	app, _, _ := newTestApp(t)

	_, err := app.Import(context.Background(), feedwatch.ImportRequest{OPML: []byte("not xml at all")})
	wantUsageError(t, err, "import source is not a valid OPML document")
}

// TestImportSkipsSubscribedURL covers an entry whose URL is already subscribed:
// it counts as skipped, not failed, since re-importing a backup is expected.
func TestImportSkipsSubscribedURL(t *testing.T) {
	app, st, _ := newTestApp(t)
	seedSubscription(t, st, "https://a.example/feed.xml", "")

	res, err := app.Import(context.Background(), feedwatch.ImportRequest{OPML: []byte(importDoc)})
	if err != nil {
		t.Fatalf("Import = %v, want nil", err)
	}
	if res.Added != 1 || res.Skipped != 1 || len(res.Failed) != 0 {
		t.Errorf("added/skipped/failed = %d/%d/%v, want 1/1/none", res.Added, res.Skipped, res.Failed)
	}
}

// TestImportSkipsInDocumentDuplicate covers a URL listed twice inside one
// document: the second occurrence is skipped rather than attempted again, which
// is what the URL reservation in phase 1 exists for.
func TestImportSkipsInDocumentDuplicate(t *testing.T) {
	app, _, _ := newTestApp(t)
	doc := `<opml version="2.0"><body>
    <outline type="rss" text="Once" xmlUrl="https://dup.example/feed.xml"/>
    <outline type="rss" text="Twice" xmlUrl="https://dup.example/feed.xml"/>
  </body></opml>`

	res, err := app.Import(context.Background(), feedwatch.ImportRequest{OPML: []byte(doc)})
	if err != nil {
		t.Fatalf("Import = %v, want nil", err)
	}
	if res.Added != 1 || res.Skipped != 1 || len(res.Failed) != 0 {
		t.Errorf("added/skipped/failed = %d/%d/%v, want 1/1/none", res.Added, res.Skipped, res.Failed)
	}
}

// TestImportRejectsNonHTTPURL covers an entry whose URL is not absolute http(s):
// it lands in failed with a reason and never aborts the import, so its valid
// sibling still subscribes.
func TestImportRejectsNonHTTPURL(t *testing.T) {
	app, st, _ := newTestApp(t)
	doc := `<opml version="2.0"><body>
    <outline type="rss" text="Good" xmlUrl="https://good.example/feed.xml"/>
    <outline type="rss" text="Bad" xmlUrl="file:///etc/passwd"/>
  </body></opml>`

	res, err := app.Import(context.Background(), feedwatch.ImportRequest{OPML: []byte(doc)})
	if err != nil {
		t.Fatalf("Import = %v, want nil", err)
	}
	if res.Added != 1 {
		t.Errorf("added = %d, want 1 (the valid sibling)", res.Added)
	}
	if len(res.Failed) != 1 ||
		res.Failed[0].XMLURL != "file:///etc/passwd" ||
		res.Failed[0].Reason != "outline xmlUrl/url is not an absolute http(s) URL" {
		t.Fatalf("failed = %+v, want the non-http entry with its reason", res.Failed)
	}
	if _, err := st.GetFeed(context.Background(), "file:///etc/passwd"); err == nil {
		t.Error("the non-http entry was subscribed, want it routed to failed")
	}
}

// TestImportAssignsAliasOnlyWhenFree covers alias assignment: an outline label
// becomes the alias when nothing holds it, and is dropped rather than colliding
// when it is already taken.
func TestImportAssignsAliasOnlyWhenFree(t *testing.T) {
	app, st, _ := newTestApp(t)
	ctx := context.Background()
	seedSubscription(t, st, "https://taken.example/feed.xml", "Alpha")

	res, err := app.Import(ctx, feedwatch.ImportRequest{OPML: []byte(importDoc)})
	if err != nil {
		t.Fatalf("Import = %v, want nil", err)
	}
	if res.Added != 2 {
		t.Fatalf("added = %d, want 2", res.Added)
	}

	contested, err := st.GetFeed(ctx, "https://a.example/feed.xml")
	if err != nil {
		t.Fatalf("GetFeed contested: %v", err)
	}
	if contested.Alias != "" {
		t.Errorf("alias = %q, want empty: Alpha is already held by another feed", contested.Alias)
	}

	free, err := st.GetFeed(ctx, "https://b.example/feed.xml")
	if err != nil {
		t.Fatalf("GetFeed free: %v", err)
	}
	if free.Alias != "Beta" {
		t.Errorf("alias = %q, want the free outline label Beta", free.Alias)
	}
}

// TestImportReportsInvalidEntry covers a feed-like outline carrying no URL: the
// parser reports it as invalid, and it surfaces in failed with an empty xmlUrl
// rather than being silently dropped.
func TestImportReportsInvalidEntry(t *testing.T) {
	app, _, _ := newTestApp(t)
	doc := `<opml version="2.0"><body>
    <outline type="rss" text="Broken"/>
  </body></opml>`

	res, err := app.Import(context.Background(), feedwatch.ImportRequest{OPML: []byte(doc)})
	if err != nil {
		t.Fatalf("Import = %v, want nil", err)
	}
	if len(res.Failed) != 1 || res.Failed[0].XMLURL != "" || res.Failed[0].Reason == "" {
		t.Errorf("failed = %+v, want one entry with no xmlUrl and a reason", res.Failed)
	}
}

// TestImportFailedMarshalsAsList covers the output contract: an import that
// failed nothing still serializes failed as [], never null.
func TestImportFailedMarshalsAsList(t *testing.T) {
	app, _, _ := newTestApp(t)

	res, err := app.Import(context.Background(), feedwatch.ImportRequest{OPML: []byte(importDoc)})
	if err != nil {
		t.Fatalf("Import = %v, want nil", err)
	}
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"failed":[]`) {
		t.Errorf("envelope = %s, want failed serialized as []", b)
	}
}

// TestImportValidatesBeforeSubscribing covers Validate true: each entry is
// fetched and parsed the way add does, so a body that is not a feed lands in
// failed and is never subscribed while its healthy sibling imports.
func TestImportValidatesBeforeSubscribing(t *testing.T) {
	app, st, fetcher, parser := newNetworkApp(t)
	ctx := context.Background()
	registerFeed(fetcher, parser, "https://a.example/feed.xml", "Alpha")
	fetcher.Register("https://b.example/feed.xml", htmlPage("<html></html>"))
	parser.RegisterError("https://b.example/feed.xml",
		core.ParseErr("https://b.example/feed.xml", errors.New("not a feed")))

	res, err := app.Import(ctx, feedwatch.ImportRequest{OPML: []byte(importDoc), Validate: true})
	if err != nil {
		t.Fatalf("Import = %v, want nil", err)
	}
	if res.Added != 1 {
		t.Errorf("added = %d, want 1 (only the feed that parses)", res.Added)
	}
	if len(res.Failed) != 1 || !strings.Contains(res.Failed[0].Reason, "does not parse as a feed") {
		t.Fatalf("failed = %+v, want the non-feed entry with a parse reason", res.Failed)
	}
	if _, err := st.GetFeed(ctx, "https://b.example/feed.xml"); err == nil {
		t.Error("the non-feed entry was subscribed, want it routed to failed")
	}
}

// TestImportWithoutValidationNeverFetches covers Validate false: every
// syntactically valid entry subscribes without a single fetch, so a successful
// import implies nothing about reachability.
func TestImportWithoutValidationNeverFetches(t *testing.T) {
	app, _, fetcher, _ := newNetworkApp(t)

	res, err := app.Import(context.Background(), feedwatch.ImportRequest{OPML: []byte(importDoc)})
	if err != nil {
		t.Fatalf("Import = %v, want nil", err)
	}
	if res.Added != 2 || len(res.Failed) != 0 {
		t.Errorf("added/failed = %d/%v, want 2/none without validation", res.Added, res.Failed)
	}
	for _, u := range []string{"https://a.example/feed.xml", "https://b.example/feed.xml"} {
		if n := len(fetcher.Requests(u)); n != 0 {
			t.Errorf("fetcher was called %d times for %s, want none", n, u)
		}
	}
}

// TestImportAssignsTagsFromCategory covers behavior 5: an outline's category
// attribute becomes the subscription's tags, canonicalized.
func TestImportAssignsTagsFromCategory(t *testing.T) {
	app, st, _ := newTestApp(t)
	doc := `<opml version="2.0"><body>
    <outline type="rss" text="Alpha" xmlUrl="https://a.example/feed.xml" category="AI, agents"/>
  </body></opml>`

	res, err := app.Import(context.Background(), feedwatch.ImportRequest{OPML: []byte(doc)})
	if err != nil {
		t.Fatalf("Import = %v, want nil", err)
	}
	if res.Added != 1 {
		t.Fatalf("added = %d, want 1 (failed %v)", res.Added, res.Failed)
	}
	if got, want := strings.Join(storedTags(t, st, "https://a.example/feed.xml"), "|"), "agents|ai"; got != want {
		t.Errorf("stored tags = %q, want %q", got, want)
	}
}

// TestImportDropsInvalidTag covers behavior 6: OPML comes from foreign tools, so
// an unusable tag is dropped rather than failing the whole outline; the feed is
// still subscribed with its valid tags.
func TestImportDropsInvalidTag(t *testing.T) {
	app, st, _ := newTestApp(t)
	doc := `<opml version="2.0"><body>
    <outline type="rss" text="Alpha" xmlUrl="https://a.example/feed.xml" category="ai,two words"/>
  </body></opml>`

	res, err := app.Import(context.Background(), feedwatch.ImportRequest{OPML: []byte(doc)})
	if err != nil {
		t.Fatalf("Import = %v, want nil", err)
	}
	if res.Added != 1 || len(res.Failed) != 0 {
		t.Fatalf("added/failed = %d/%v, want 1/none", res.Added, res.Failed)
	}
	if got, want := strings.Join(storedTags(t, st, "https://a.example/feed.xml"), "|"), "ai"; got != want {
		t.Errorf("stored tags = %q, want %q (the whitespace tag dropped)", got, want)
	}
}

// TestImportPreservesTagsOfSubscribedFeed covers behavior 8: an already
// subscribed feed is skipped, and its stored tags are left alone rather than
// overwritten by the document's.
func TestImportPreservesTagsOfSubscribedFeed(t *testing.T) {
	app, st, _ := newTestApp(t)
	seedTaggedSubscription(t, st, "https://a.example/feed.xml", "mine")
	doc := `<opml version="2.0"><body>
    <outline type="rss" text="Alpha" xmlUrl="https://a.example/feed.xml" category="theirs"/>
  </body></opml>`

	res, err := app.Import(context.Background(), feedwatch.ImportRequest{OPML: []byte(doc)})
	if err != nil {
		t.Fatalf("Import = %v, want nil", err)
	}
	if res.Added != 0 || res.Skipped != 1 {
		t.Fatalf("added/skipped = %d/%d, want 0/1", res.Added, res.Skipped)
	}
	if got, want := strings.Join(storedTags(t, st, "https://a.example/feed.xml"), "|"), "mine"; got != want {
		t.Errorf("stored tags = %q, want %q untouched", got, want)
	}
}

// TestExportImportRoundTripsTags covers behavior 7: two differently tagged feeds
// exported and imported into a fresh store keep their lanes exactly.
func TestExportImportRoundTripsTags(t *testing.T) {
	source, srcStore, _ := newTestApp(t)
	ctx := context.Background()
	seedTaggedSubscription(t, srcStore, "https://a.example/feed.xml", "ai", "agents")
	seedTaggedSubscription(t, srcStore, "https://b.example/feed.xml", "news")
	seedSubscription(t, srcStore, "https://c.example/feed.xml", "")

	exported, err := source.Export(ctx, feedwatch.ExportRequest{})
	if err != nil {
		t.Fatalf("Export = %v, want nil", err)
	}

	target, dstStore, _ := newTestApp(t)
	res, err := target.Import(ctx, feedwatch.ImportRequest{OPML: []byte(exported.OPML)})
	if err != nil {
		t.Fatalf("Import = %v, want nil", err)
	}
	if res.Added != 3 || len(res.Failed) != 0 {
		t.Fatalf("added/failed = %d/%v, want 3/none", res.Added, res.Failed)
	}

	want := map[string]string{
		"https://a.example/feed.xml": "agents|ai",
		"https://b.example/feed.xml": "news",
		"https://c.example/feed.xml": "",
	}
	for url, tags := range want {
		if got := strings.Join(storedTags(t, dstStore, url), "|"); got != tags {
			t.Errorf("feed %s tags = %q, want %q", url, got, tags)
		}
	}
}

// storedTags reads back one feed's stored tags, failing the test when the feed
// is not subscribed.
func storedTags(t *testing.T, st interface {
	ListFeeds(context.Context, core.ListFilter) ([]core.Feed, error)
}, url string,
) []string {
	t.Helper()
	feeds, err := st.ListFeeds(context.Background(), core.ListFilter{})
	if err != nil {
		t.Fatalf("ListFeeds: %v", err)
	}
	for _, f := range feeds {
		if f.URL == url {
			return f.Tags
		}
	}
	t.Fatalf("feed %s is not subscribed", url)
	return nil
}
