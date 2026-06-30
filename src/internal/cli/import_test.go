package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	cliv3 "github.com/urfave/cli/v3"

	"github.com/andreswebs/feedwatch/internal/config"
	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/store"
	"github.com/andreswebs/feedwatch/internal/testsupport"
)

// importEnvelope mirrors the stdout ImportResult shape for assertions.
type importEnvelope struct {
	Added   int `json:"added"`
	Skipped int `json:"skipped"`
	Failed  []struct {
		XMLURL string `json:"xmlUrl"`
		Reason string `json:"reason"`
	} `json:"failed"`
}

// runImport drives the import command through the root with an injected store
// double and optional stdin, capturing stdout, stderr, and the exit code.
func runImport(t *testing.T, st store.Store, in *os.File, args ...string) runResult {
	t.Helper()

	outF, errF := tempFile(t), tempFile(t)
	d := Deps{
		Cfg:     config.Defaults(),
		Clock:   testsupport.FixedClock(pollFixedTime()),
		Version: "1.2.3",
		Store:   st,
		In:      in,
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

	_ = NewRootCommand(d).Run(t.Context(), append([]string{"feedwatch", "import"}, args...))

	res.out = readFile(t, outF)
	res.err = readFile(t, errF)
	return res
}

// writeOPML writes content to a temp file and returns its path.
func writeOPML(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "subs.opml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write opml: %v", err)
	}
	return path
}

func importEnv(t *testing.T, out string) importEnvelope {
	t.Helper()
	var env importEnvelope
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("stdout is not an import envelope: %v\ngot: %q", err, out)
	}
	return env
}

const flatOPML = `<opml version="2.0"><body>
  <outline type="rss" text="Alpha" xmlUrl="https://a.example/feed.xml"/>
  <outline type="rss" text="Beta" xmlUrl="https://b.example/feed.xml"/>
</body></opml>`

// TestImportFlatAddsBoth covers behavior 1: a flat OPML with two feeds imports
// both, reporting added:2, and the feeds are persisted.
func TestImportFlatAddsBoth(t *testing.T) {
	st := testsupport.NewInMemoryStore(testsupport.FixedClock(pollFixedTime()))
	path := writeOPML(t, flatOPML)

	res := runImport(t, st, nil, path)

	if res.exited {
		t.Errorf("import should exit 0, got code %d (stderr %q)", res.code, res.err)
	}
	env := importEnv(t, res.out)
	if env.Added != 2 || env.Skipped != 0 || len(env.Failed) != 0 {
		t.Errorf("envelope = %+v, want added:2 skipped:0 failed:0", env)
	}

	feeds, err := st.ListFeeds(context.Background(), core.ListFilter{})
	if err != nil {
		t.Fatalf("ListFeeds: %v", err)
	}
	if len(feeds) != 2 {
		t.Fatalf("stored feeds = %d, want 2", len(feeds))
	}
}

// TestImportNestedFolders covers behavior 2: feeds nested in folders at depth >1
// are all found and imported.
func TestImportNestedFolders(t *testing.T) {
	st := testsupport.NewInMemoryStore(testsupport.FixedClock(pollFixedTime()))
	doc := `<opml version="2.0"><body>
    <outline text="Folder">
      <outline text="Sub">
        <outline type="rss" text="Deep" xmlUrl="https://deep.example/feed.xml"/>
      </outline>
    </outline>
  </body></opml>`
	path := writeOPML(t, doc)

	res := runImport(t, st, nil, path)
	if res.exited {
		t.Fatalf("import should exit 0, got code %d (stderr %q)", res.code, res.err)
	}
	env := importEnv(t, res.out)
	if env.Added != 1 {
		t.Errorf("added = %d, want 1", env.Added)
	}
	if _, err := st.GetFeed(context.Background(), "https://deep.example/feed.xml"); err != nil {
		t.Errorf("deep feed not stored: %v", err)
	}
}

// TestImportURLFallbackAndAlias covers behavior 3 plus aliasing: a missing
// xmlUrl falls back to url, and the outline text becomes the alias.
func TestImportURLFallbackAndAlias(t *testing.T) {
	st := testsupport.NewInMemoryStore(testsupport.FixedClock(pollFixedTime()))
	doc := `<opml version="2.0"><body>
    <outline type="rss" text="Legacy" url="https://legacy.example/feed.xml"/>
  </body></opml>`
	path := writeOPML(t, doc)

	res := runImport(t, st, nil, path)
	if res.exited {
		t.Fatalf("import should exit 0, got code %d (stderr %q)", res.code, res.err)
	}
	if env := importEnv(t, res.out); env.Added != 1 {
		t.Errorf("added = %d, want 1", env.Added)
	}

	feed, err := st.GetFeed(context.Background(), "Legacy")
	if err != nil {
		t.Fatalf("GetFeed by alias: %v", err)
	}
	if feed.URL != "https://legacy.example/feed.xml" {
		t.Errorf("URL = %q, want url fallback", feed.URL)
	}
	if feed.Alias != "Legacy" {
		t.Errorf("Alias = %q, want Legacy", feed.Alias)
	}
}

// TestImportSkipsDuplicate covers behavior 4: a feed already subscribed is
// skipped and counted, not re-added.
func TestImportSkipsDuplicate(t *testing.T) {
	st := testsupport.NewInMemoryStore(testsupport.FixedClock(pollFixedTime()))
	if _, err := st.AddFeed(context.Background(), core.Feed{URL: "https://a.example/feed.xml"}); err != nil {
		t.Fatalf("seed AddFeed: %v", err)
	}
	path := writeOPML(t, flatOPML)

	res := runImport(t, st, nil, path)
	if res.exited {
		t.Fatalf("import should exit 0, got code %d (stderr %q)", res.code, res.err)
	}
	env := importEnv(t, res.out)
	if env.Added != 1 || env.Skipped != 1 {
		t.Errorf("envelope = %+v, want added:1 skipped:1", env)
	}
}

// TestImportReportsMalformed covers behavior 5: a malformed entry (feed-like but
// no URL) is reported in failed while the rest import.
func TestImportReportsMalformed(t *testing.T) {
	st := testsupport.NewInMemoryStore(testsupport.FixedClock(pollFixedTime()))
	doc := `<opml version="2.0"><body>
    <outline type="rss" text="Good" xmlUrl="https://good.example/feed.xml"/>
    <outline type="rss" text="Broken"/>
  </body></opml>`
	path := writeOPML(t, doc)

	res := runImport(t, st, nil, path)
	if res.exited {
		t.Fatalf("import should exit 0 despite a bad entry, got code %d (stderr %q)", res.code, res.err)
	}
	env := importEnv(t, res.out)
	if env.Added != 1 {
		t.Errorf("added = %d, want 1", env.Added)
	}
	if len(env.Failed) != 1 || env.Failed[0].Reason == "" {
		t.Errorf("failed = %+v, want one entry with a reason", env.Failed)
	}
}

// TestImportRejectsMalformedURL covers URL validation parity with add: an
// outline whose xmlUrl is present but not an absolute http(s) URL is routed to
// failed rather than stored, while a valid sibling still imports.
func TestImportRejectsMalformedURL(t *testing.T) {
	st := testsupport.NewInMemoryStore(testsupport.FixedClock(pollFixedTime()))
	doc := `<opml version="2.0"><body>
    <outline type="rss" text="Good" xmlUrl="https://good.example/feed.xml"/>
    <outline type="rss" text="Bad" xmlUrl="not-a-valid-url"/>
  </body></opml>`
	path := writeOPML(t, doc)

	res := runImport(t, st, nil, path)
	if res.exited {
		t.Fatalf("import should exit 0 despite a malformed URL, got code %d (stderr %q)", res.code, res.err)
	}
	env := importEnv(t, res.out)
	if env.Added != 1 {
		t.Errorf("added = %d, want 1", env.Added)
	}
	if len(env.Failed) != 1 || env.Failed[0].XMLURL != "not-a-valid-url" || env.Failed[0].Reason == "" {
		t.Errorf("failed = %+v, want one entry for the malformed URL with a reason", env.Failed)
	}
	if _, err := st.GetFeed(context.Background(), "not-a-valid-url"); err == nil {
		t.Errorf("malformed URL was stored, want it routed to failed")
	}
}

// TestImportFromStdin covers behavior 6: import - reads the outline from stdin.
func TestImportFromStdin(t *testing.T) {
	st := testsupport.NewInMemoryStore(testsupport.FixedClock(pollFixedTime()))

	in := tempFile(t)
	if _, err := in.WriteString(flatOPML); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	if _, err := in.Seek(0, 0); err != nil {
		t.Fatalf("seek stdin: %v", err)
	}

	res := runImport(t, st, in, "-")
	if res.exited {
		t.Fatalf("import - should exit 0, got code %d (stderr %q)", res.code, res.err)
	}
	if env := importEnv(t, res.out); env.Added != 2 {
		t.Errorf("added = %d, want 2", env.Added)
	}
}

// TestImportMissingFileIsConfigError covers a missing file path: the whole
// invocation fails (exit 1) with a structured error on stderr.
func TestImportMissingFileIsConfigError(t *testing.T) {
	st := testsupport.NewInMemoryStore(testsupport.FixedClock(pollFixedTime()))

	res := runImport(t, st, nil, filepath.Join(t.TempDir(), "nope.opml"))
	if !res.exited || res.code != 1 {
		t.Errorf("missing file should exit 1, got exited=%v code=%d", res.exited, res.code)
	}
	if res.out != "" {
		t.Errorf("stdout = %q, want empty on a hard failure", res.out)
	}
}
