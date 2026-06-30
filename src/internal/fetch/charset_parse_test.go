package fetch_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/fetch"
	"github.com/andreswebs/feedwatch/internal/parse"
	"github.com/andreswebs/feedwatch/internal/testsupport"
)

// TestFetchThenParseNonUTF8 fetches a non-UTF-8 feed and parses the decoded body
// through gofeed, closing the gap that byte-level charset tests missed: the
// decoded body still carried its original XML declaration (encoding="ISO-8859-1"
// / "UTF-16"), so gofeed's CharsetReader re-decoded the already-UTF-8 bytes per
// that stale declaration (mojibake for Latin-1; detection failure for UTF-16).
func TestFetchThenParseNonUTF8(t *testing.T) {
	tests := []struct {
		name        string
		fixture     string
		contentType string
		wantFeed    string
		wantItem    string
	}{
		{
			name:        "iso-8859-1 via xml declaration",
			fixture:     "iso8859-1.xml",
			contentType: "application/rss+xml", // no charset: the XML-decl rung must fire
			wantFeed:    "Feed café Latince",
			wantItem:    "Élément un, déjà vu",
		},
		{
			name:        "utf-16le via bom",
			fixture:     "utf16-bom.xml",
			contentType: "application/rss+xml",
			wantFeed:    "UTF-16 Feed",
			wantItem:    "UTF-16 item one",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := testsupport.Fixture(t, tc.fixture)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				_, _ = w.Write(body)
			}))
			defer srv.Close()

			f, err := fetch.New()
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			res, err := f.Fetch(context.Background(), core.FetchRequest{URL: srv.URL})
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}

			pf, err := parse.New().Parse(context.Background(), res.Body, srv.URL)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if pf.Title != tc.wantFeed {
				t.Errorf("feed title = %q (% x), want %q", pf.Title, pf.Title, tc.wantFeed)
			}
			if len(pf.Items) == 0 {
				t.Fatalf("parsed feed yielded no items")
			}
			if got := pf.Items[0].Title; got != tc.wantItem {
				t.Errorf("item title = %q (% x), want %q", got, got, tc.wantItem)
			}
		})
	}
}
