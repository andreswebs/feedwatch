package fetch_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/fetch"
	"github.com/andreswebs/feedwatch/internal/testsupport"
)

// TestFetchDecodesNonUTF8Bodies asserts that Fetch hands the parser UTF-8
// regardless of the source encoding, using the multi-encoding fixture corpus.
func TestFetchDecodesNonUTF8Bodies(t *testing.T) {
	tests := []struct {
		name        string
		fixture     string
		contentType string
		want        string
	}{
		{
			name:        "iso-8859-1 via content-type",
			fixture:     "iso8859-1.xml",
			contentType: "application/rss+xml; charset=ISO-8859-1",
			want:        "Feed café Latince",
		},
		{
			name:        "utf-16 via bom",
			fixture:     "utf16-bom.xml",
			contentType: "application/rss+xml",
			want:        "UTF-16 Feed",
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
			if !utf8.Valid(res.Body) {
				t.Fatalf("Fetch body is not valid UTF-8: % x", res.Body)
			}
			if !strings.Contains(string(res.Body), tc.want) {
				t.Errorf("body does not contain %q:\n%s", tc.want, res.Body)
			}
			if res.MIMEType != "application/rss+xml" {
				t.Errorf("mime = %q, want application/rss+xml", res.MIMEType)
			}
		})
	}
}
