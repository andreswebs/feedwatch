package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	cliv3 "github.com/urfave/cli/v3"

	"github.com/andreswebs/feedwatch/internal/config"
	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/fetch"
	"github.com/andreswebs/feedwatch/internal/parse"
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
// double and optional stdin, capturing stdout, stderr, and the exit code. It
// injects no fetcher or parser, so validating imports build production
// collaborators; classification tests pass --no-validate to stay offline.
func runImport(t *testing.T, st store.Store, in *os.File, args ...string) runResult {
	t.Helper()
	return runImportWith(t, st, nil, nil, in, args...)
}

// runImportWith is runImport with explicit fetcher and parser doubles, for
// exercising the default validating path without touching the network.
func runImportWith(t *testing.T, st store.Store, fetcher fetch.Fetcher, parser parse.Parser, in *os.File, args ...string) runResult {
	t.Helper()

	outF, errF := tempFile(t), tempFile(t)
	d := Deps{
		Cfg:     config.Defaults(),
		Clock:   testsupport.FixedClock(pollFixedTime()),
		Version: "1.2.3",
		Store:   st,
		Fetch:   fetcher,
		Parse:   parser,
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

	res := runImport(t, st, nil, "--no-validate", path)

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

	res := runImport(t, st, nil, "--no-validate", path)
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

	res := runImport(t, st, nil, "--no-validate", path)
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

	res := runImport(t, st, nil, "--no-validate", path)
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

	res := runImport(t, st, nil, "--no-validate", path)
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

	res := runImport(t, st, nil, "--no-validate", path)
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

	res := runImport(t, st, in, "--no-validate", "-")
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

// validateMixOPML pairs a good feed, an unreachable URL, and a reachable but
// non-feed (HTML) URL, to exercise the default validating import.
const validateMixOPML = `<opml version="2.0"><body>
  <outline type="rss" text="Good" xmlUrl="https://good.example/feed.xml"/>
  <outline type="rss" text="Dead" xmlUrl="https://dead.example/feed.xml"/>
  <outline type="rss" text="HTML" xmlUrl="https://html.example/page"/>
</body></opml>`

// TestImportValidatesByDefault covers the breaking change: a default import
// fetches and parses each feed, subscribing only the one that resolves and
// parses, recording the unreachable and the non-feed URLs in failed with
// reasons, and never aborting.
func TestImportValidatesByDefault(t *testing.T) {
	st := testsupport.NewInMemoryStore(testsupport.FixedClock(pollFixedTime()))
	path := writeOPML(t, validateMixOPML)

	ff := testsupport.NewFakeFetcher()
	ff.Register("https://good.example/feed.xml", core.FetchResult{Body: []byte("<rss/>")})
	ff.RegisterError("https://dead.example/feed.xml",
		core.NetworkErr("https://dead.example/feed.xml", errors.New("no such host")))
	ff.Register("https://html.example/page", core.FetchResult{Body: []byte("<html></html>")})

	fp := testsupport.NewFakeParser()
	fp.Register("https://good.example/feed.xml", parse.ParsedFeed{Title: "Good"})
	fp.RegisterError("https://html.example/page",
		core.ParseErr("https://html.example/page", errors.New("not a feed")))

	res := runImportWith(t, st, ff, fp, nil, path)
	if res.exited {
		t.Fatalf("import should exit 0 despite validation failures, got code %d (stderr %q)", res.code, res.err)
	}

	env := importEnv(t, res.out)
	if env.Added != 1 {
		t.Errorf("added = %d, want 1 (only the resolvable feed)", env.Added)
	}
	if len(env.Failed) != 2 {
		t.Fatalf("failed = %+v, want two entries (unreachable + non-feed)", env.Failed)
	}
	failedByURL := map[string]string{}
	for _, f := range env.Failed {
		failedByURL[f.XMLURL] = f.Reason
	}
	if r := failedByURL["https://dead.example/feed.xml"]; !strings.Contains(r, "could not fetch") {
		t.Errorf("dead-feed reason = %q, want a fetch failure", r)
	}
	if r := failedByURL["https://html.example/page"]; !strings.Contains(r, "does not parse as a feed") {
		t.Errorf("html reason = %q, want a parse failure", r)
	}

	if _, err := st.GetFeed(context.Background(), "https://good.example/feed.xml"); err != nil {
		t.Errorf("good feed not subscribed: %v", err)
	}
	if _, err := st.GetFeed(context.Background(), "https://dead.example/feed.xml"); err == nil {
		t.Errorf("unreachable feed was subscribed, want it routed to failed")
	}
}

// TestImportNoValidateSkipsFetch covers --no-validate: every syntactically valid
// feed is subscribed without any fetch, so a successful import implies nothing
// about reachability. The injected fetcher errors on every URL, proving the path
// is never taken.
func TestImportNoValidateSkipsFetch(t *testing.T) {
	st := testsupport.NewInMemoryStore(testsupport.FixedClock(pollFixedTime()))
	path := writeOPML(t, validateMixOPML)

	ff := testsupport.NewFakeFetcher() // unregistered URLs error if fetched
	fp := testsupport.NewFakeParser()

	res := runImportWith(t, st, ff, fp, nil, "--no-validate", path)
	if res.exited {
		t.Fatalf("--no-validate import should exit 0, got code %d (stderr %q)", res.code, res.err)
	}

	env := importEnv(t, res.out)
	if env.Added != 3 || len(env.Failed) != 0 {
		t.Errorf("envelope = %+v, want added:3 failed:0 without fetching", env)
	}
	for _, u := range []string{
		"https://good.example/feed.xml",
		"https://dead.example/feed.xml",
		"https://html.example/page",
	} {
		if got := ff.Requests(u); len(got) != 0 {
			t.Errorf("fetcher was called for %s (%d times), want no fetch under --no-validate", u, len(got))
		}
	}
}

// gateFetcher blocks every fetch until `need` are in flight, recording the peak
// concurrency, so a test can prove import validates candidates concurrently.
type gateFetcher struct {
	need   int
	mu     sync.Mutex
	cur    int
	max    int
	closed bool
	gate   chan struct{}
}

func newGateFetcher(need int) *gateFetcher {
	return &gateFetcher{need: need, gate: make(chan struct{})}
}

func (g *gateFetcher) Fetch(ctx context.Context, req core.FetchRequest) (core.FetchResult, error) {
	g.mu.Lock()
	g.cur++
	if g.cur > g.max {
		g.max = g.cur
	}
	if g.cur >= g.need && !g.closed {
		g.closed = true
		close(g.gate)
	}
	g.mu.Unlock()

	select {
	case <-g.gate:
	case <-ctx.Done():
		return core.FetchResult{}, ctx.Err()
	}

	g.mu.Lock()
	g.cur--
	g.mu.Unlock()
	return core.FetchResult{FinalURL: req.URL, Body: []byte("ok")}, nil
}

func (g *gateFetcher) peak() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.max
}

// TestImportValidatesConcurrently proves validation runs candidates in parallel
// up to the concurrency limit and that one validation failure does not cancel
// its siblings: four feeds must all reach the fetcher before any is released,
// and the single feed whose parse fails lands in failed while the rest import.
func TestImportValidatesConcurrently(t *testing.T) {
	st := testsupport.NewInMemoryStore(testsupport.FixedClock(pollFixedTime()))
	doc := `<opml version="2.0"><body>
    <outline type="rss" text="One" xmlUrl="https://one.example/feed.xml"/>
    <outline type="rss" text="Two" xmlUrl="https://two.example/feed.xml"/>
    <outline type="rss" text="Three" xmlUrl="https://three.example/feed.xml"/>
    <outline type="rss" text="Four" xmlUrl="https://four.example/feed.xml"/>
  </body></opml>`
	path := writeOPML(t, doc)

	urls := []string{
		"https://one.example/feed.xml",
		"https://two.example/feed.xml",
		"https://three.example/feed.xml",
		"https://four.example/feed.xml",
	}
	gf := newGateFetcher(len(urls))
	fp := testsupport.NewFakeParser()
	for _, u := range urls {
		fp.Register(u, parse.ParsedFeed{})
	}
	fp.RegisterError("https://three.example/feed.xml", core.ParseErr("https://three.example/feed.xml", errors.New("not a feed")))

	res := runImportWith(t, st, gf, fp, nil, path)
	if res.exited {
		t.Fatalf("import should exit 0, got code %d (stderr %q)", res.code, res.err)
	}
	if peak := gf.peak(); peak != len(urls) {
		t.Errorf("peak concurrent fetches = %d, want %d (validation should be concurrent)", peak, len(urls))
	}

	env := importEnv(t, res.out)
	if env.Added != 3 {
		t.Errorf("added = %d, want 3 (one sibling failed, others unaffected)", env.Added)
	}
	if len(env.Failed) != 1 || env.Failed[0].XMLURL != "https://three.example/feed.xml" {
		t.Errorf("failed = %+v, want only the non-feed sibling", env.Failed)
	}
}
