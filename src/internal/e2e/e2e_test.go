package e2e_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/andreswebs/feedwatch/internal/cli"
	"github.com/andreswebs/feedwatch/internal/testsupport"
)

// update regenerates the golden files instead of comparing against them. After
// running with -update, inspect the regenerated goldens for correctness before
// committing them; the suite's value is that the golden content is a reviewed,
// pinned contract.
var update = flag.Bool("update", false, "regenerate golden files")

// binPath is the freshly built feedwatch binary the suite drives. It is built
// once in TestMain so every scenario exercises the same artifact a user runs.
var binPath string

// feedHostToken is the stable stand-in for the local feed server's volatile
// host:port, so goldens do not depend on the random httptest port.
//
//nolint:gosec // G101: a host placeholder token for golden normalization, not a credential.
const feedHostToken = "http://feedserver"

// rssFeed is a deterministic RSS 2.0 body. Every value an item carries (title,
// link, guid, pubDate) is fixed, so the only server-dependent value in the
// output is the subscription feed_url, which the normalizer rewrites.
const rssFeed = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>Example Feed</title>
    <link>https://example.test/</link>
    <description>An example feed for end-to-end tests</description>
    <item>
      <title>First post</title>
      <link>https://example.test/first</link>
      <description>The first post.</description>
      <guid isPermaLink="false">urn:example:1</guid>
      <pubDate>Sat, 27 Jun 2026 10:00:00 +0000</pubDate>
    </item>
    <item>
      <title>Second post</title>
      <link>https://example.test/second</link>
      <description>The second post.</description>
      <guid isPermaLink="false">urn:example:2</guid>
      <pubDate>Sun, 28 Jun 2026 12:30:00 +0000</pubDate>
    </item>
  </channel>
</rss>`

// discoverHTML is a homepage that autodiscovers the feed via a <link rel>.
const discoverHTML = `<!DOCTYPE html>
<html><head>
<title>Example Home</title>
<link rel="alternate" type="application/rss+xml" title="Example Feed" href="/feed.xml">
</head><body>Hello</body></html>`

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "feedwatch-e2e-bin")
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e: mkdir temp:", err)
		os.Exit(1)
	}
	binPath = filepath.Join(dir, "feedwatch")

	//nolint:gosec // G204: building the module's own command with a fixed import path and a temp output.
	build := exec.Command("go", "build", "-o", binPath, "github.com/andreswebs/feedwatch/cmd/feedwatch")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "e2e: build feedwatch:", err)
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}

	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// harness drives the built binary against one temp store and (optionally) one
// feed server, normalizing and golden-diffing every invocation.
type harness struct {
	t    *testing.T
	db   string
	base string // feed server base URL, "" when no server is involved
}

func newHarness(t *testing.T, base string) harness {
	t.Helper()
	return harness{t: t, db: filepath.Join(t.TempDir(), "feedwatch.db"), base: base}
}

// run invokes the binary with --quiet (so only the structured error envelope,
// never info logs, reaches stderr) and a per-harness --db, asserts the exit
// code, and diffs normalized stdout/stderr against the named goldens.
func (h harness) run(golden string, wantExit int, args ...string) {
	h.t.Helper()

	full := append([]string{"--quiet", "--db", h.db}, args...)
	//nolint:gosec // G204: the suite runs the binary it just built with test-controlled args.
	cmd := exec.Command(binPath, full...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()

	if code := exitCodeOf(err); code != wantExit {
		h.t.Fatalf("%s: exit code = %d, want %d\nstdout: %s\nstderr: %s",
			golden, code, wantExit, out.String(), errb.String())
	}

	checkGolden(h.t, golden+".stdout", normalize(out.Bytes(), h.base))
	checkGolden(h.t, golden+".stderr", normalize(errb.Bytes(), h.base))
}

// runJSON invokes the binary like run, but instead of golden-diffing stdout it
// asserts the exit code and decodes stdout as JSON into v. It is for scenarios
// where the response is too large or too input-dependent (e.g. many feeds) for
// a pinned golden to be practical.
func (h harness) runJSON(v any, wantExit int, args ...string) {
	h.t.Helper()

	full := append([]string{"--quiet", "--db", h.db}, args...)
	//nolint:gosec // G204: the suite runs the binary it just built with test-controlled args.
	cmd := exec.Command(binPath, full...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()

	if code := exitCodeOf(err); code != wantExit {
		h.t.Fatalf("exit code = %d, want %d\nstdout: %s\nstderr: %s", code, wantExit, out.String(), errb.String())
	}
	if err := json.Unmarshal(out.Bytes(), v); err != nil {
		h.t.Fatalf("decode stdout as JSON: %v\nstdout: %s", err, out.String())
	}
}

// exitCodeOf extracts a process exit code from a *exec.ExitError, returning 0
// for a clean exit and -1 for a failure to launch the process at all.
func exitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

var (
	reCommit = regexp.MustCompile(`"commit":"[^"]*"`)
	reGo     = regexp.MustCompile(`"go":"[^"]*"`)
	// fetched_at is the wall-clock moment feedwatch first recorded an item, so it
	// is volatile across runs and normalized to a stable token.
	reFetchedAt = regexp.MustCompile(`"fetched_at":"[^"]*"`)
)

// normalize rewrites the volatile parts of an output stream into stable tokens:
// the feed server's random host:port, and the build-stamped commit and Go
// toolchain reported by --version. Everything else (timestamps from the fixed
// fixture, counts, categories) is deterministic and left intact.
func normalize(b []byte, serverBase string) []byte {
	s := string(b)
	if serverBase != "" {
		s = strings.ReplaceAll(s, serverBase, feedHostToken)
	}
	s = reCommit.ReplaceAllString(s, `"commit":"<commit>"`)
	s = reGo.ReplaceAllString(s, `"go":"<go>"`)
	s = reFetchedAt.ReplaceAllString(s, `"fetched_at":"<fetched_at>"`)
	return []byte(s)
}

// checkGolden compares got against testdata/<name>, or rewrites it under -update.
func checkGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)

	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatalf("mkdir golden dir: %v", err)
		}
		//nolint:gosec // G306: goldens are human-reviewed test fixtures, not secrets.
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("write golden %s: %v", name, err)
		}
		return
	}

	//nolint:gosec // G304: the golden path is built from a test-literal name.
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (run the suite with -update to create it)", name, err)
	}
	if !bytes.Equal(want, got) {
		t.Errorf("golden %s mismatch\n--- want ---\n%s\n--- got ---\n%s", name, want, got)
	}
}

// startFeedServer starts a feed server serving the standard RSS body at
// /feed.xml and returns it with its base URL for normalization.
func startFeedServer(t *testing.T) (*testsupport.FeedServer, string) {
	t.Helper()
	srv := testsupport.NewFeedServer()
	srv.Register("/feed.xml", testsupport.Endpoint{Body: rssFeed})
	t.Cleanup(srv.Close)
	return srv, srv.URL("")
}

// TestLifecycle covers behavior 1: add then poll surfaces the new items and
// exits 0; an immediate second poll surfaces nothing new and still exits 0; the
// persisted history is then re-queryable via items and the subscription via list.
func TestLifecycle(t *testing.T) {
	srv, base := startFeedServer(t)
	h := newHarness(t, base)
	feedURL := srv.URL("/feed.xml")

	h.run("lifecycle/add", 0, "add", feedURL, "--alias", "example")
	h.run("lifecycle/poll_first", 0, "poll", "--force")
	h.run("lifecycle/poll_again", 0, "poll", "--force")
	h.run("lifecycle/items", 0, "items")
	h.run("lifecycle/list", 0, "list")
}

// TestAllFailed covers behavior 2: a poll where every targeted feed fails exits
// 2, emits the per-feed error envelope on stderr, and still writes a valid
// (empty) result envelope on stdout. The feed is added while it parses, then
// flipped to a deterministic 404 so the failure is immediate (not retried).
func TestAllFailed(t *testing.T) {
	srv, base := startFeedServer(t)
	h := newHarness(t, base)
	feedURL := srv.URL("/feed.xml")

	h.run("all_failed/add", 0, "add", feedURL)
	srv.Register("/feed.xml", testsupport.Endpoint{Status: 404, Body: "not found"})
	h.run("all_failed/poll", 2, "poll", "--force")
}

// TestPartial covers behavior 3: a poll where some feeds succeed and some fail
// exits 3. Two feeds are added while both parse, then one is flipped to 404.
func TestPartial(t *testing.T) {
	srv := testsupport.NewFeedServer()
	srv.Register("/ok.xml", testsupport.Endpoint{Body: rssFeed})
	srv.Register("/bad.xml", testsupport.Endpoint{Body: rssFeed})
	t.Cleanup(srv.Close)
	h := newHarness(t, srv.URL(""))

	h.run("partial/add_ok", 0, "add", srv.URL("/ok.xml"))
	h.run("partial/add_bad", 0, "add", srv.URL("/bad.xml"))
	srv.Register("/bad.xml", testsupport.Endpoint{Status: 404})
	h.run("partial/poll", 3, "poll", "--force")
}

// TestVersion covers behavior 4a: --version prints the {version,commit,go} JSON
// contract and exits 0.
func TestVersion(t *testing.T) {
	h := newHarness(t, "")
	h.run("version", 0, "--version")
}

// TestMigrateStatus covers behavior 4b: migrate --status on a fresh store
// ensures the schema and reports the current version, zero pending, and backend.
func TestMigrateStatus(t *testing.T) {
	h := newHarness(t, "")
	h.run("migrate_status", 0, "migrate", "--status")
}

// TestDiscover exercises read-only discovery: a homepage autodiscovers its feed.
func TestDiscover(t *testing.T) {
	srv := testsupport.NewFeedServer()
	srv.Register("/", testsupport.Endpoint{Body: discoverHTML, ContentType: "text/html"})
	srv.Register("/feed.xml", testsupport.Endpoint{Body: rssFeed})
	t.Cleanup(srv.Close)
	h := newHarness(t, srv.URL(""))

	h.run("discover", 0, "discover", srv.URL("/"))
}

// TestImportExportPrune exercises the OPML and retention surface end-to-end:
// import a subscription from an OPML file, poll it, export the subscriptions
// back as OPML, then prune the stored history by per-feed count.
func TestImportExportPrune(t *testing.T) {
	srv, base := startFeedServer(t)
	h := newHarness(t, base)
	feedURL := srv.URL("/feed.xml")

	opmlPath := filepath.Join(t.TempDir(), "subs.opml")
	opmlDoc := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<opml version="2.0"><head><title>subs</title></head><body>
<outline text="example" type="rss" xmlUrl="%s"/>
</body></opml>`, feedURL)
	if err := os.WriteFile(opmlPath, []byte(opmlDoc), 0o600); err != nil {
		t.Fatalf("write opml: %v", err)
	}

	h.run("opml/import", 0, "import", opmlPath)
	h.run("opml/poll", 0, "poll", "--force")
	h.run("opml/export", 0, "export")
	h.run("opml/prune", 0, "prune", "--max-items", "1")
}

// TestFirstPollReportsAllNewItems is the customer regression for fee-udsl: a
// first poll across many feeds must report every stored item as new, never
// new_items=0 with items[] empty while the store nonetheless holds them. It
// decodes JSON rather than golden-diffing, since the response scales with the
// feed count and per-feed URLs are not worth normalizing into a pinned golden.
func TestFirstPollReportsAllNewItems(t *testing.T) {
	const numFeeds = 20
	const itemsPerFeed = 2

	// Each feed gets its own httptest server (its own host:port), not one server
	// with many paths: poll applies a per-host politeness delay between
	// same-host requests, so sharing one host would serialize all 20 fetches
	// and make the test take ~20s for no reason relevant to what it verifies.
	var opml strings.Builder
	opml.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	opml.WriteString(`<opml version="2.0"><head><title>subs</title></head><body>` + "\n")
	for i := range numFeeds {
		srv := testsupport.NewFeedServer()
		t.Cleanup(srv.Close)
		srv.Register("/feed.xml", testsupport.Endpoint{Body: rssFeed})
		fmt.Fprintf(&opml, `<outline text="feed%d" type="rss" xmlUrl="%s"/>`+"\n", i, srv.URL("/feed.xml"))
	}
	opml.WriteString(`</body></opml>`)

	opmlPath := filepath.Join(t.TempDir(), "subs.opml")
	if err := os.WriteFile(opmlPath, []byte(opml.String()), 0o600); err != nil {
		t.Fatalf("write opml: %v", err)
	}

	h := newHarness(t, "")
	h.run("first_poll/import", 0, "import", opmlPath, "--no-validate")

	var poll cli.PollResult
	h.runJSON(&poll, 0, "poll", "--force")

	wantItems := numFeeds * itemsPerFeed
	if poll.Polled != numFeeds || poll.Failed != 0 {
		t.Fatalf("poll: polled=%d failed=%d, want polled=%d failed=0", poll.Polled, poll.Failed, numFeeds)
	}
	if poll.NewItems != wantItems {
		t.Fatalf("poll: new_items=%d, want %d", poll.NewItems, wantItems)
	}
	if len(poll.Items) != wantItems {
		t.Fatalf("poll: len(items)=%d, want %d", len(poll.Items), wantItems)
	}

	var items cli.ItemsResult
	h.runJSON(&items, 0, "items", "--limit", "0")
	if len(items.Items) != wantItems {
		t.Fatalf("items: len(items)=%d, want %d", len(items.Items), wantItems)
	}
}
