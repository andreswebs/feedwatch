package command

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite" // registers the pure-Go "sqlite" driver for stamping

	"github.com/andreswebs/feedwatch/internal/testsupport"
)

// TestGoldenLifecycle mirrors the exec lifecycle: add then poll surfaces the new
// items and exits 0; an immediate second forced poll surfaces nothing new and
// still exits 0; the persisted history is then re-queryable via items and the
// subscription via list.
func TestGoldenLifecycle(t *testing.T) {
	srv, base := startGoldenFeed(t)
	h := newGoldenHarness(t, base)
	feedURL := srv.URL("/feed.xml")

	h.run("lifecycle/add", 0, "add", feedURL, "--alias", "example")
	h.run("lifecycle/poll_first", 0, "poll", "--force")
	h.run("lifecycle/poll_again", 0, "poll", "--force")
	h.run("lifecycle/items", 0, "items")
	h.run("lifecycle/list", 0, "list")
}

// TestGoldenAllFailed covers a poll where every targeted feed fails: exit 2, the
// per-feed failure in the stdout failures array, and an empty (valid) result
// envelope. The feed is added while it parses, then flipped to a deterministic
// 404 so the failure is immediate (not retried).
func TestGoldenAllFailed(t *testing.T) {
	srv, base := startGoldenFeed(t)
	h := newGoldenHarness(t, base)
	feedURL := srv.URL("/feed.xml")

	h.run("all_failed/add", 0, "add", feedURL)
	srv.Register("/feed.xml", testsupport.Endpoint{Status: 404, Body: "not found"})
	h.run("all_failed/poll", 2, "poll", "--force")
}

// TestGoldenPartial covers a poll where some feeds succeed and some fail: exit 3.
// Two feeds are added while both parse, then one is flipped to 404.
func TestGoldenPartial(t *testing.T) {
	srv := testsupport.NewFeedServer()
	srv.Register("/ok.xml", testsupport.Endpoint{Body: goldenRSS})
	srv.Register("/bad.xml", testsupport.Endpoint{Body: goldenRSS})
	t.Cleanup(srv.Close)
	h := newGoldenHarness(t, srv.URL(""))

	h.run("partial/add_ok", 0, "add", srv.URL("/ok.xml"))
	h.run("partial/add_bad", 0, "add", srv.URL("/bad.xml"))
	srv.Register("/bad.xml", testsupport.Endpoint{Status: 404})
	h.run("partial/poll", 3, "poll", "--force")
}

// TestGoldenAutoDisableWarns covers the ADR 0005 NDJSON warning channel: a feed
// that fails consecutively up to the failure threshold is auto-disabled, and the
// crossing poll emits exactly one warning line on stderr (even under --quiet,
// which suppresses logs but never warnings) while the exit code stays the
// unchanged all-failed 2.
func TestGoldenAutoDisableWarns(t *testing.T) {
	srv, base := startGoldenFeed(t)
	h := newGoldenHarness(t, base)
	feedURL := srv.URL("/feed.xml")

	h.run("auto_disable/add", 0, "add", feedURL)
	srv.Register("/feed.xml", testsupport.Endpoint{Status: 404, Body: "not found"})

	// The default failure threshold is 10 (config.Defaults). The tenth failure
	// crosses the threshold, disables the feed, and raises the advisory. Warm-up
	// polls only assert the unchanged exit code; the crossing poll is golden.
	const threshold = 10
	for range threshold - 1 {
		h.runExit(2, "poll", "--force")
	}
	h.run("auto_disable/poll", 2, "poll", "--force")

	// The feed is now disabled, so a further forced poll targets nothing and
	// exits 0 with no warning.
	h.run("auto_disable/poll_after", 0, "poll", "--force")
}

// TestGoldenMigrateStatus covers migrate --status on a fresh store: it ensures
// the schema and reports the current version, zero pending, and the backend.
func TestGoldenMigrateStatus(t *testing.T) {
	h := newGoldenHarness(t, "")
	h.run("migrate_status", 0, "migrate", "--status")
}

// TestGoldenDiscover exercises read-only discovery: a homepage autodiscovers its
// feed via a <link rel="alternate">.
func TestGoldenDiscover(t *testing.T) {
	srv := testsupport.NewFeedServer()
	srv.Register("/", testsupport.Endpoint{Body: goldenDiscoverHTML, ContentType: "text/html"})
	srv.Register("/feed.xml", testsupport.Endpoint{Body: goldenRSS})
	t.Cleanup(srv.Close)
	h := newGoldenHarness(t, srv.URL(""))

	h.run("discover", 0, "discover", srv.URL("/"))
}

// TestGoldenImportExportPrune exercises the OPML and retention surface: import a
// subscription from an OPML file, poll it, export the subscriptions back as
// OPML, then prune the stored history by per-feed count.
func TestGoldenImportExportPrune(t *testing.T) {
	srv, base := startGoldenFeed(t)
	h := newGoldenHarness(t, base)
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

// TestGoldenUsageBadFlag covers exit 64 (EX_USAGE): an undefined flag is a usage
// error with the error envelope on stderr and empty stdout.
func TestGoldenUsageBadFlag(t *testing.T) {
	h := newGoldenHarness(t, "")
	h.run("err/usage_bad_flag", 64, "--bogus-flag")
}

// TestGoldenConfigError covers exit 78 (EX_CONFIG): an invalid --concurrency is a
// configuration error with the error envelope on stderr and empty stdout.
func TestGoldenConfigError(t *testing.T) {
	h := newGoldenHarness(t, "")
	h.run("err/config_concurrency", 78, "--concurrency", "0", "list")
}

// TestGoldenStoreUnavailable covers exit 69 (EX_UNAVAILABLE): a --db whose parent
// directory does not exist cannot be opened, so the store-unavailable failure
// maps to 69 with the error envelope on stderr and empty stdout. The bad path is
// normalized so the message is stable across temp dirs.
func TestGoldenStoreUnavailable(t *testing.T) {
	h := newGoldenHarness(t, "")
	bad := filepath.Join(t.TempDir(), "missing-dir", "feedwatch.db")
	h.runBadDB("err/store_unavailable", 69, bad, "migrate", "--status")
}

// TestGoldenSchemaTooNew covers exit 65 (EX_DATAERR): a store stamped with a
// schema version beyond what the binary understands is refused rather than
// migrated, mapping to 65 with the error envelope on stderr and empty stdout.
func TestGoldenSchemaTooNew(t *testing.T) {
	h := newGoldenHarness(t, "")

	// Migrate a fresh store to the current version, then stamp a future version
	// directly into schema_migrations, mimicking a db written by a newer binary.
	h.runExit(0, "migrate")
	stampFutureSchema(t, h.db)

	h.run("err/schema_too_new", 65, "migrate", "--status")
}

// stampFutureSchema opens the sqlite store file directly and records a migration
// version one beyond the highest the binary applied, so the next command sees a
// too-new schema. The store is closed by the preceding Run, so this second
// connection does not contend with it.
func stampFutureSchema(t *testing.T, dbPath string) {
	t.Helper()

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite for stamping: %v", err)
	}
	defer func() { _ = db.Close() }()

	var maxVersion int
	if err := db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&maxVersion); err != nil {
		t.Fatalf("read max schema version: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
		maxVersion+1, "2026-06-29T12:00:00Z",
	); err != nil {
		t.Fatalf("stamp future schema version: %v", err)
	}
}

// TestGoldenFirstPollReportsAllNewItems is the customer regression for fee-udsl,
// moved in-process from the exec suite: a first poll across many feeds must
// report every stored item as new, never new_items=0 with items[] empty while
// the store nonetheless holds them. It decodes JSON rather than golden-diffing,
// since the response scales with the feed count and per-feed URLs are not worth
// normalizing into a pinned golden.
func TestGoldenFirstPollReportsAllNewItems(t *testing.T) {
	const numFeeds = 20
	const itemsPerFeed = 2

	// Each feed gets its own httptest server (its own host:port), not one server
	// with many paths: poll applies a per-host politeness delay between same-host
	// requests, so sharing one host would serialize all 20 fetches.
	var opml strings.Builder
	opml.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	opml.WriteString(`<opml version="2.0"><head><title>subs</title></head><body>` + "\n")
	for i := range numFeeds {
		srv := testsupport.NewFeedServer()
		t.Cleanup(srv.Close)
		srv.Register("/feed.xml", testsupport.Endpoint{Body: goldenRSS})
		fmt.Fprintf(&opml, `<outline text="feed%d" type="rss" xmlUrl="%s"/>`+"\n", i, srv.URL("/feed.xml"))
	}
	opml.WriteString(`</body></opml>`)

	opmlPath := filepath.Join(t.TempDir(), "subs.opml")
	if err := os.WriteFile(opmlPath, []byte(opml.String()), 0o600); err != nil {
		t.Fatalf("write opml: %v", err)
	}

	h := newGoldenHarness(t, "")
	h.runExit(0, "import", opmlPath, "--no-validate")

	var poll PollResult
	h.runDecode(&poll, 0, "poll", "--force")

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

	var items ItemsResult
	h.runDecode(&items, 0, "items", "--limit", "0")
	if len(items.Items) != wantItems {
		t.Fatalf("items: len(items)=%d, want %d", len(items.Items), wantItems)
	}
}

// TestGoldenExitCodeConformance is the ADR 0006 conformance obligation over the
// scenario table: every code in every command's declared exit-code table is
// exercised by at least one golden scenario, and (via assertDeclared in the
// harness) every observed exit is a member of the declared tables.
//
// Exit 70 (EX_SOFTWARE, internal/unclassified) is intentionally uncovered: it is
// produced only by an unclassified Go error or a recovered panic in main,
// neither of which a real command emits deterministically, so no reachable
// scenario exists to exercise it. This is the explicit note ADR 0006 asks for in
// place of silently leaving it uncovered.
func TestGoldenExitCodeConformance(t *testing.T) {
	// Each declared code paired with the scenario(s) that exercise it. The
	// scenarios themselves run in the sibling tests above; this map is the
	// coverage ledger, and it must name a scenario for every declared code.
	exercised := map[string]string{
		"0":  "version, lifecycle/*, migrate_status, discover, opml/*, auto_disable/poll_after",
		"2":  "all_failed/poll, auto_disable/poll",
		"3":  "partial/poll",
		"64": "err/usage_bad_flag",
		"65": "err/schema_too_new",
		"69": "err/store_unavailable",
		"78": "err/config_concurrency",
	}
	const uncoveredInternal = "70"

	union := declaredExitCodes()
	for code := range union {
		if code == uncoveredInternal {
			continue
		}
		if _, ok := exercised[code]; !ok {
			t.Errorf("declared exit code %q is exercised by no golden scenario", code)
		}
	}
	for code := range exercised {
		if !union[code] {
			t.Errorf("scenario ledger claims exit %q that no command's table declares", code)
		}
	}
	if !union[uncoveredInternal] {
		t.Errorf("exit %q is documented as uncovered but no table declares it; the note is stale", uncoveredInternal)
	}
}
