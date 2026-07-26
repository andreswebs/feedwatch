package command

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/testsupport"
)

// updateGolden regenerates the in-process golden files instead of comparing
// against them. After running with -update, inspect the regenerated goldens for
// correctness before committing them; the suite's value is that the golden
// content is a reviewed, pinned contract (ADR 0006).
var updateGolden = flag.Bool("update", false, "regenerate golden files")

// feedHostToken is the stable stand-in for a local feed server's volatile
// host:port, so goldens do not depend on the random httptest port.
//
//nolint:gosec // G101: a host placeholder token for golden normalization, not a credential.
const feedHostToken = "http://feedserver"

// dbToken is the stable stand-in for the per-scenario temp store path, which
// varies across runs and machines but surfaces in store-unavailable error
// messages.
const dbToken = "<db>"

// goldenRSS is a deterministic RSS 2.0 body. Every value an item carries
// (title, link, guid, pubDate) is fixed, so the only server-dependent value in
// the output is the subscription feed_url, which the normalizer rewrites.
const goldenRSS = `<?xml version="1.0" encoding="UTF-8"?>
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

// goldenDiscoverHTML is a homepage that autodiscovers the feed via a <link rel>.
const goldenDiscoverHTML = `<!DOCTYPE html>
<html><head>
<title>Example Home</title>
<link rel="alternate" type="application/rss+xml" title="Example Feed" href="/feed.xml">
</head><body>Hello</body></html>`

var (
	reCommit = regexp.MustCompile(`"commit":"[^"]*"`)
	reGo     = regexp.MustCompile(`"go":"[^"]*"`)
	// fetched_at is the wall-clock moment feedwatch first recorded an item, so it
	// is volatile across runs and normalized to a stable token.
	reFetchedAt = regexp.MustCompile(`"fetched_at":"[^"]*"`)
)

// goldenHarness drives the Run boundary in-process against one temp store and
// (optionally) one feed server, normalizing and golden-diffing every
// invocation's three artifacts: stdout, stderr, and the exit code (ADR 0006).
type goldenHarness struct {
	t     *testing.T
	db    string
	base  string // feed server base URL, "" when no server is involved
	clock core.Clock
}

// newGoldenHarness binds a harness to a fresh temp store path and a fixed clock,
// so fetched_at and any due/backoff calculation are deterministic. base is the
// feed server URL to normalize, or "" when the scenario touches no server.
func newGoldenHarness(t *testing.T, base string) goldenHarness {
	t.Helper()
	return goldenHarness{
		t:     t,
		db:    filepath.Join(t.TempDir(), "feedwatch.db"),
		base:  base,
		clock: testsupport.FixedClock(pollFixedTime()),
	}
}

// run invokes Run with --quiet (so only the structured error envelope, never
// info logs, reaches stderr) and a per-harness --db, asserts the exit code, and
// diffs normalized stdout/stderr against the named goldens. It records the
// observed exit code for the conformance test.
func (h goldenHarness) run(golden string, wantExit int, args ...string) {
	h.t.Helper()

	full := append([]string{"--quiet", "--db", h.db}, args...)
	var out, errb bytes.Buffer
	d := Deps{Clock: h.clock, Version: "1.2.3", Out: &out, Err: &errb}
	code := Run(full, d)

	if code != wantExit {
		h.t.Fatalf("%s: exit code = %d, want %d\nstdout: %s\nstderr: %s",
			golden, code, wantExit, out.String(), errb.String())
	}
	assertDeclared(h.t, golden, code)

	checkGolden(h.t, golden+".stdout", h.normalize(out.Bytes()))
	checkGolden(h.t, golden+".stderr", h.normalize(errb.Bytes()))
}

// runExit invokes Run like run but only asserts the exit code, for warm-up steps
// (such as the failing polls before an auto-disable crossing) that are not worth
// a pinned golden. The observed exit is still checked against the declared
// tables.
func (h goldenHarness) runExit(wantExit int, args ...string) {
	h.t.Helper()

	full := append([]string{"--quiet", "--db", h.db}, args...)
	var out, errb bytes.Buffer
	d := Deps{Clock: h.clock, Version: "1.2.3", Out: &out, Err: &errb}
	code := Run(full, d)

	if code != wantExit {
		h.t.Fatalf("warm-up %v: exit code = %d, want %d\nstdout: %s\nstderr: %s",
			args, code, wantExit, out.String(), errb.String())
	}
	assertDeclared(h.t, strings.Join(args, " "), code)
}

// runBadDB is run for a scenario whose store path is deliberately unopenable, so
// the path in the error message is normalized to dbToken rather than the
// harness's own db. It uses badDB as the --db value and normalizes that path.
func (h goldenHarness) runBadDB(golden string, wantExit int, badDB string, args ...string) {
	h.t.Helper()

	full := append([]string{"--quiet", "--db", badDB}, args...)
	var out, errb bytes.Buffer
	d := Deps{Clock: h.clock, Version: "1.2.3", Out: &out, Err: &errb}
	code := Run(full, d)

	if code != wantExit {
		h.t.Fatalf("%s: exit code = %d, want %d\nstdout: %s\nstderr: %s",
			golden, code, wantExit, out.String(), errb.String())
	}
	assertDeclared(h.t, golden, code)

	norm := func(b []byte) []byte {
		return h.normalize([]byte(strings.ReplaceAll(string(b), badDB, dbToken)))
	}
	checkGolden(h.t, golden+".stdout", norm(out.Bytes()))
	checkGolden(h.t, golden+".stderr", norm(errb.Bytes()))
}

// runDecode invokes Run like run, but instead of golden-diffing stdout it
// asserts the exit code and decodes stdout as JSON into v. It is for scenarios
// where the response scales with the feed count and a pinned golden is not
// worth it (a behavioral regression rather than a stream-contract pin).
func (h goldenHarness) runDecode(v any, wantExit int, args ...string) {
	h.t.Helper()

	full := append([]string{"--quiet", "--db", h.db}, args...)
	var out, errb bytes.Buffer
	d := Deps{Clock: h.clock, Version: "1.2.3", Out: &out, Err: &errb}
	code := Run(full, d)

	if code != wantExit {
		h.t.Fatalf("exit code = %d, want %d\nstdout: %s\nstderr: %s", code, wantExit, out.String(), errb.String())
	}
	assertDeclared(h.t, strings.Join(args, " "), code)
	if err := json.Unmarshal(out.Bytes(), v); err != nil {
		h.t.Fatalf("decode stdout as JSON: %v\nstdout: %s", err, out.String())
	}
}

// normalize rewrites the volatile parts of an output stream into stable tokens:
// the feed server's random host:port, the build-stamped commit and Go toolchain
// reported by --version, and item fetched_at timestamps. Everything else
// (timestamps from the fixed fixture, counts, categories) is deterministic and
// left intact.
func (h goldenHarness) normalize(b []byte) []byte {
	s := string(b)
	if h.base != "" {
		s = strings.ReplaceAll(s, h.base, feedHostToken)
	}
	s = reCommit.ReplaceAllString(s, `"commit":"<commit>"`)
	s = reGo.ReplaceAllString(s, `"go":"<go>"`)
	s = reFetchedAt.ReplaceAllString(s, `"fetched_at":"<fetched_at>"`)
	return []byte(s)
}

// declaredExitCodes is the union of every command's exit-code table: the set of
// codes any command is allowed to exit with (ADR 0001). The golden harness
// asserts every observed exit is a member of this set, so an exit the tables do
// not declare fails the suite.
func declaredExitCodes() map[string]bool {
	union := map[string]bool{}
	for _, tbl := range []map[string]string{defaultExitCodes(), pollExitCodes(), checkExitCodes()} {
		for code := range tbl {
			union[code] = true
		}
	}
	return union
}

// assertDeclared fails the scenario if the observed exit code is not a member of
// any command's declared exit-code table.
func assertDeclared(t *testing.T, scenario string, code int) {
	t.Helper()
	if !declaredExitCodes()[strconv.Itoa(code)] {
		t.Errorf("%s: observed exit %d is not declared in any command's exit-code table", scenario, code)
	}
}

// checkGolden compares got against testdata/<name>, or rewrites it under
// -update.
func checkGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)

	if *updateGolden {
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

// startGoldenFeed starts a feed server serving the standard RSS body at
// /feed.xml and returns it with its base URL for normalization.
func startGoldenFeed(t *testing.T) (*testsupport.FeedServer, string) {
	t.Helper()
	srv := testsupport.NewFeedServer()
	srv.Register("/feed.xml", testsupport.Endpoint{Body: goldenRSS})
	t.Cleanup(srv.Close)
	return srv, srv.URL("")
}

// TestGoldenVersion covers the tracer behavior (ADR 0006 TDD step 1): --version
// through Run with buffers golden-diffs stdout, stderr, and exit 0.
func TestGoldenVersion(t *testing.T) {
	h := newGoldenHarness(t, "")
	h.run("version", 0, "--version")
}
