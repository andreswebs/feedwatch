package command

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andreswebs/feedwatch/core"
	"github.com/andreswebs/feedwatch/internal/testsupport"
	"github.com/andreswebs/feedwatch/store"
)

// runExport drives the export command through the root with an injected store
// double, capturing stdout, stderr, and the exit code.
func runExport(t *testing.T, st store.Store, args ...string) runResult {
	t.Helper()

	d := Deps{Clock: testsupport.FixedClock(pollFixedTime()), Version: "1.2.3", opts: storeOpts(st)}
	return drive(t, d, append([]string{"export"}, args...)...)
}

// seedFeed adds a feed (URL plus optional alias) to the store, failing the test
// on error.
func seedFeed(t *testing.T, st store.Store, url, alias string) {
	t.Helper()
	if _, err := st.AddFeed(context.Background(), core.Feed{URL: url, Alias: alias}); err != nil {
		t.Fatalf("seed AddFeed %s: %v", url, err)
	}
}

// TestExportTwoFeedsToStdout covers behavior 1: the document the library
// produced reaches stdout carrying both subscriptions. Its OPML shape is the
// library's contract and is asserted there.
func TestExportTwoFeedsToStdout(t *testing.T) {
	st := testsupport.NewInMemoryStore(testsupport.FixedClock(pollFixedTime()))
	seedFeed(t, st, "https://a.example/feed.xml", "")
	seedFeed(t, st, "https://b.example/feed.xml", "")

	res := runExport(t, st)
	if res.code != 0 {
		t.Fatalf("export should exit 0, got code %d (stderr %q)", res.code, res.err)
	}

	for _, want := range []string{"<opml", "https://a.example/feed.xml", "https://b.example/feed.xml"} {
		if !strings.Contains(res.out, want) {
			t.Errorf("stdout = %q, want it to carry %q", res.out, want)
		}
	}
}

// TestExportToFile covers behavior 3: -o FILE writes the OPML to the named file
// and leaves stdout empty.
func TestExportToFile(t *testing.T) {
	st := testsupport.NewInMemoryStore(testsupport.FixedClock(pollFixedTime()))
	seedFeed(t, st, "https://a.example/feed.xml", "")

	out := filepath.Join(t.TempDir(), "backup.opml")
	res := runExport(t, st, "-o", out)
	if res.code != 0 {
		t.Fatalf("export -o should exit 0, got code %d (stderr %q)", res.code, res.err)
	}
	if res.out != "" {
		t.Errorf("stdout = %q, want empty when -o is given", res.out)
	}

	b, err := os.ReadFile(out) //nolint:gosec // G304: path is a t.TempDir()-rooted test fixture, not external input
	if err != nil {
		t.Fatalf("read exported file: %v", err)
	}
	if !strings.Contains(string(b), "https://a.example/feed.xml") {
		t.Errorf("exported file = %q, want it to carry the seeded feed", b)
	}
}

// TestExportRoundTripsWithImport covers behavior 4: OPML emitted by export
// re-imports cleanly into a fresh store, preserving URLs and aliases.
func TestExportRoundTripsWithImport(t *testing.T) {
	src := testsupport.NewInMemoryStore(testsupport.FixedClock(pollFixedTime()))
	seedFeed(t, src, "https://a.example/feed.xml", "Alpha")
	seedFeed(t, src, "https://b.example/feed.xml", "Beta")

	out := filepath.Join(t.TempDir(), "backup.opml")
	if res := runExport(t, src, "-o", out); res.code != 0 {
		t.Fatalf("export -o should exit 0, got code %d (stderr %q)", res.code, res.err)
	}

	dst := testsupport.NewInMemoryStore(testsupport.FixedClock(pollFixedTime()))
	res := runImport(t, dst, nil, "--no-validate", out)
	if res.code != 0 {
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

// seedTaggedFeed adds a feed carrying tags to the store, failing the test on
// error.
func seedTaggedFeed(t *testing.T, st store.Store, url string, tags ...string) {
	t.Helper()
	if _, err := st.AddFeed(context.Background(), core.Feed{URL: url, Tags: tags}); err != nil {
		t.Fatalf("seed AddFeed %s: %v", url, err)
	}
}

// TestExportFiltersByTag covers behavior 4 at the CLI boundary: --tag reaches
// the library, so only the named lane is written. It is the regression test for
// the missing bind call, which made the flag parse and be discarded.
func TestExportFiltersByTag(t *testing.T) {
	st := testsupport.NewInMemoryStore(testsupport.FixedClock(pollFixedTime()))
	seedTaggedFeed(t, st, "https://in.example/feed.xml", "ai")
	seedTaggedFeed(t, st, "https://out.example/feed.xml", "news")

	res := runExport(t, st, "--tag", "ai")
	if res.code != 0 {
		t.Fatalf("export should exit 0, got code %d (stderr %q)", res.code, res.err)
	}

	if !strings.Contains(res.out, "https://in.example/feed.xml") {
		t.Errorf("stdout = %q, want it to carry the in-lane feed", res.out)
	}
	if strings.Contains(res.out, "https://out.example/feed.xml") {
		t.Errorf("stdout = %q, want the out-of-lane feed absent", res.out)
	}
	if !strings.Contains(res.out, `category="ai"`) {
		t.Errorf("stdout = %q, want the tag written as a category attribute", res.out)
	}
}

// TestExportRejectsInvalidTagSelection covers behavior 9: an unknown --match
// value exits 64 and writes no document.
func TestExportRejectsInvalidTagSelection(t *testing.T) {
	st := testsupport.NewInMemoryStore(testsupport.FixedClock(pollFixedTime()))
	seedTaggedFeed(t, st, "https://in.example/feed.xml", "ai")

	res := runExport(t, st, "--tag", "ai", "--match", "bogus")
	if res.code != 64 {
		t.Fatalf("an invalid tag selection should exit 64 (usage), got code=%d\nstdout: %q", res.code, res.out)
	}
	if res.out != "" {
		t.Errorf("stdout = %q, want empty on a rejected export", res.out)
	}
}
