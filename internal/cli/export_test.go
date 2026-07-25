package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cliv3 "github.com/urfave/cli/v3"

	"github.com/andreswebs/feedwatch/internal/config"
	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/opml"
	"github.com/andreswebs/feedwatch/internal/store"
	"github.com/andreswebs/feedwatch/internal/testsupport"
)

// runExport drives the export command through the root with an injected store
// double, capturing stdout, stderr, and the exit code.
func runExport(t *testing.T, st store.Store, args ...string) runResult {
	t.Helper()

	outF, errF := tempFile(t), tempFile(t)
	d := Deps{
		Cfg:     config.Defaults(),
		Clock:   testsupport.FixedClock(pollFixedTime()),
		Version: "1.2.3",
		Store:   st,
		Out:     outF,
		Err:     errF,
	}

	var res runResult
	oldExiter := cliv3.OsExiter
	cliv3.OsExiter = func(code int) {
		res.code = code
		res.exited = true
	}
	t.Cleanup(func() { cliv3.OsExiter = oldExiter })

	_ = NewRootCommand(d).Run(t.Context(), append([]string{"feedwatch", "export"}, args...))

	res.out = readFile(t, outF)
	res.err = readFile(t, errF)
	return res
}

// seedFeed adds a feed (URL plus optional alias) to the store, failing the test
// on error.
func seedFeed(t *testing.T, st store.Store, url, alias string) {
	t.Helper()
	if _, err := st.AddFeed(context.Background(), core.Feed{URL: url, Alias: alias}); err != nil {
		t.Fatalf("seed AddFeed %s: %v", url, err)
	}
}

// TestExportTwoFeedsToStdout covers behavior 1: exporting two subscriptions
// emits OPML to stdout with two outlines carrying their xmlUrl.
func TestExportTwoFeedsToStdout(t *testing.T) {
	st := testsupport.NewInMemoryStore(testsupport.FixedClock(pollFixedTime()))
	seedFeed(t, st, "https://a.example/feed.xml", "")
	seedFeed(t, st, "https://b.example/feed.xml", "")

	res := runExport(t, st)
	if res.exited {
		t.Fatalf("export should exit 0, got code %d (stderr %q)", res.code, res.err)
	}

	feeds, _, err := opml.Parse(strings.NewReader(res.out))
	if err != nil {
		t.Fatalf("stdout is not valid OPML: %v\ngot: %q", err, res.out)
	}
	got := make(map[string]bool, len(feeds))
	for _, f := range feeds {
		got[f.XMLURL] = true
	}
	if !got["https://a.example/feed.xml"] || !got["https://b.example/feed.xml"] {
		t.Errorf("exported URLs = %v, want both seeded feeds", got)
	}
}

// TestExportAliasPopulatesTitle covers behavior 2: a feed's alias becomes the
// outline text/title in the emitted OPML.
func TestExportAliasPopulatesTitle(t *testing.T) {
	st := testsupport.NewInMemoryStore(testsupport.FixedClock(pollFixedTime()))
	seedFeed(t, st, "https://go.example/feed.xml", "godev")

	res := runExport(t, st)
	if res.exited {
		t.Fatalf("export should exit 0, got code %d (stderr %q)", res.code, res.err)
	}

	feeds, _, err := opml.Parse(strings.NewReader(res.out))
	if err != nil {
		t.Fatalf("stdout is not valid OPML: %v\ngot: %q", err, res.out)
	}
	if len(feeds) != 1 {
		t.Fatalf("exported feeds = %d, want 1", len(feeds))
	}
	if feeds[0].Title != "godev" {
		t.Errorf("Title = %q, want the alias godev", feeds[0].Title)
	}
}

// TestExportToFile covers behavior 3: -o FILE writes the OPML to the named file
// and leaves stdout empty.
func TestExportToFile(t *testing.T) {
	st := testsupport.NewInMemoryStore(testsupport.FixedClock(pollFixedTime()))
	seedFeed(t, st, "https://a.example/feed.xml", "")

	out := filepath.Join(t.TempDir(), "backup.opml")
	res := runExport(t, st, "-o", out)
	if res.exited {
		t.Fatalf("export -o should exit 0, got code %d (stderr %q)", res.code, res.err)
	}
	if res.out != "" {
		t.Errorf("stdout = %q, want empty when -o is given", res.out)
	}

	b, err := os.ReadFile(out) //nolint:gosec // G304: path is a t.TempDir()-rooted test fixture, not external input
	if err != nil {
		t.Fatalf("read exported file: %v", err)
	}
	feeds, _, err := opml.Parse(strings.NewReader(string(b)))
	if err != nil {
		t.Fatalf("exported file is not valid OPML: %v", err)
	}
	if len(feeds) != 1 || feeds[0].XMLURL != "https://a.example/feed.xml" {
		t.Errorf("exported feeds = %+v, want the seeded feed", feeds)
	}
}

// TestExportRoundTripsWithImport covers behavior 4: OPML emitted by export
// re-imports cleanly into a fresh store, preserving URLs and aliases.
func TestExportRoundTripsWithImport(t *testing.T) {
	src := testsupport.NewInMemoryStore(testsupport.FixedClock(pollFixedTime()))
	seedFeed(t, src, "https://a.example/feed.xml", "Alpha")
	seedFeed(t, src, "https://b.example/feed.xml", "Beta")

	out := filepath.Join(t.TempDir(), "backup.opml")
	if res := runExport(t, src, "-o", out); res.exited {
		t.Fatalf("export -o should exit 0, got code %d (stderr %q)", res.code, res.err)
	}

	dst := testsupport.NewInMemoryStore(testsupport.FixedClock(pollFixedTime()))
	res := runImport(t, dst, nil, "--no-validate", out)
	if res.exited {
		t.Fatalf("import of exported OPML should exit 0, got code %d (stderr %q)", res.code, res.err)
	}
	if env := importEnv(t, res.out); env.Added != 2 {
		t.Errorf("re-import added = %d, want 2", env.Added)
	}

	feed, err := dst.GetFeed(context.Background(), "Alpha")
	if err != nil {
		t.Fatalf("GetFeed by round-tripped alias: %v", err)
	}
	if feed.URL != "https://a.example/feed.xml" {
		t.Errorf("URL = %q, want the round-tripped feed", feed.URL)
	}
}
