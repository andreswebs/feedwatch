package command

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/andreswebs/feedwatch/core"
	"github.com/andreswebs/feedwatch/internal/testsupport"
	"github.com/andreswebs/feedwatch/store"
)

// runPrune drives the prune command through the root with an injected store
// double, capturing stdout, stderr, and the exit code the boundary selected.
func runPrune(t *testing.T, st store.Store, clk core.Clock, args ...string) runResult {
	t.Helper()

	d := Deps{Clock: clk, Version: "1.2.3", opts: storeOpts(st)}
	return drive(t, d, append([]string{"prune"}, args...)...)
}

// pruneEnvelope mirrors the stdout feedwatch.PruneResult shape for assertions.
type pruneEnvelope struct {
	Pruned int `json:"pruned"`
}

func parsePruneEnvelope(t *testing.T, out string) pruneEnvelope {
	t.Helper()
	var env pruneEnvelope
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("stdout is not a prune envelope: %v\ngot: %q", err, out)
	}
	return env
}

// TestPruneByKeepDays covers behavior 1 (tracer): prune --keep-days 90 tombstones
// items older than 90 days and reports the count.
func TestPruneByKeepDays(t *testing.T) {
	now := pollFixedTime()
	clk := testsupport.FixedClock(now)
	st := testsupport.NewInMemoryStore(clk)

	url := "https://x.example/feed.xml"
	seedItem(t, st, url, "old", "old", now.Add(-120*24*time.Hour), now.Add(-120*24*time.Hour))
	seedItem(t, st, url, "recent", "recent", now.Add(-10*24*time.Hour), now.Add(-10*24*time.Hour))

	res := runPrune(t, st, clk, "--keep-days", "90")
	if res.code != 0 {
		t.Fatalf("prune --keep-days should exit 0, got code %d (stderr=%q)", res.code, res.err)
	}
	env := parsePruneEnvelope(t, res.out)
	if env.Pruned != 1 {
		t.Errorf("pruned = %d, want 1\ngot: %q", env.Pruned, res.out)
	}
}

// TestPruneByMaxItems covers behavior 2: prune --max-items keeps the newest N per
// feed and tombstones the rest.
func TestPruneByMaxItems(t *testing.T) {
	now := pollFixedTime()
	clk := testsupport.FixedClock(now)
	st := testsupport.NewInMemoryStore(clk)

	url := "https://x.example/feed.xml"
	seedItem(t, st, url, "a", "first", now.Add(-3*time.Hour), now)
	seedItem(t, st, url, "b", "second", now.Add(-2*time.Hour), now)
	seedItem(t, st, url, "c", "third", now.Add(-1*time.Hour), now)

	res := runPrune(t, st, clk, "--max-items", "2")
	if res.code != 0 {
		t.Fatalf("prune --max-items should exit 0, got code %d (stderr=%q)", res.code, res.err)
	}
	env := parsePruneEnvelope(t, res.out)
	if env.Pruned != 1 {
		t.Errorf("pruned = %d, want 1\ngot: %q", env.Pruned, res.out)
	}
}

// TestPrunePreservesDedup covers behavior 3: after prune, items no longer returns
// the pruned rows and a re-upsert of a pruned key yields no new items (the dedup
// fingerprint is preserved).
func TestPrunePreservesDedup(t *testing.T) {
	now := pollFixedTime()
	clk := testsupport.FixedClock(now)
	st := testsupport.NewInMemoryStore(clk)

	url := "https://x.example/feed.xml"
	seedItem(t, st, url, "old", "old", now.Add(-120*24*time.Hour), now.Add(-120*24*time.Hour))
	seedItem(t, st, url, "recent", "recent", now.Add(-10*24*time.Hour), now.Add(-10*24*time.Hour))

	res := runPrune(t, st, clk, "--keep-days", "90")
	if res.code != 0 {
		t.Fatalf("prune should exit 0, got code %d", res.code)
	}

	resItems := runItems(t, st, clk)
	env := parseItemsEnvelope(t, resItems.out)
	for _, it := range env.Items {
		if it.Title == "old" {
			t.Errorf("items should not return pruned item; got %q", resItems.out)
		}
	}

	// A re-upsert of the pruned key is not re-emitted as new: the dedup
	// fingerprint survives the prune.
	reItem := core.Item{
		DedupKey:  "old",
		Title:     "old",
		Link:      url + "/old",
		FetchedAt: now,
	}
	added, err := st.UpsertItems(context.Background(), url, []core.Item{reItem})
	if err != nil {
		t.Fatalf("UpsertItems re-poll: %v", err)
	}
	if len(added) != 0 {
		t.Errorf("re-upsert of pruned key should yield no new items, got %d", len(added))
	}
}

// TestPruneRequiresBound covers behavior 4: prune with no bound is a usage error
// (exit 64) with no result on stdout, rather than a silent no-op.
func TestPruneRequiresBound(t *testing.T) {
	now := pollFixedTime()
	clk := testsupport.FixedClock(now)
	st := testsupport.NewInMemoryStore(clk)

	res := runPrune(t, st, clk)
	if res.code != 64 {
		t.Errorf("prune with no bound should exit 64 (usage), got code=%d", res.code)
	}
	if res.out != "" {
		t.Errorf("stdout should be empty on usage error, got %q", res.out)
	}
}

// TestPruneExplicitZeroKeepDays pins the set-versus-unset asymmetry that the
// reflection projector has to preserve (ADR 0007): --keep-days 0 names a policy
// (tombstone everything older than now) and succeeds, while the flag being absent
// names none and is the usage error above. A projector that bound the flag's zero
// value unconditionally would collapse the two.
func TestPruneExplicitZeroKeepDays(t *testing.T) {
	now := pollFixedTime()
	clk := testsupport.FixedClock(now)
	st := testsupport.NewInMemoryStore(clk)

	url := "https://x.example/feed.xml"
	seedItem(t, st, url, "old", "old", now.Add(-time.Hour), now.Add(-time.Hour))

	res := runPrune(t, st, clk, "--keep-days", "0")
	if res.code != 0 {
		t.Fatalf("prune --keep-days 0 should exit 0, got code %d (stderr=%q)", res.code, res.err)
	}
	if env := parsePruneEnvelope(t, res.out); env.Pruned != 1 {
		t.Errorf("pruned = %d, want 1\ngot: %q", env.Pruned, res.out)
	}
}

// TestPruneByTagScopesToLane covers behavior 1 of the lane-scoped prune: an
// in-lane per-feed prune tombstones only in-lane items, while an out-of-lane
// feed carrying more items than the cutoff keeps every one of them.
func TestPruneByTagScopesToLane(t *testing.T) {
	now := pollFixedTime()
	clk := testsupport.FixedClock(now)
	st := testsupport.NewInMemoryStore(clk)

	ai, other := "https://a.example/feed.xml", "https://b.example/feed.xml"
	if _, err := st.AddFeed(context.Background(), core.Feed{URL: ai, Tags: []string{"ai"}, Status: core.FeedActive}); err != nil {
		t.Fatalf("AddFeed(%s): %v", ai, err)
	}
	if _, err := st.AddFeed(context.Background(), core.Feed{URL: other, Status: core.FeedActive}); err != nil {
		t.Fatalf("AddFeed(%s): %v", other, err)
	}
	for _, url := range []string{ai, other} {
		seedItem(t, st, url, "a", "first", now.Add(-3*time.Hour), now)
		seedItem(t, st, url, "b", "second", now.Add(-2*time.Hour), now)
		seedItem(t, st, url, "c", "third", now.Add(-1*time.Hour), now)
	}

	res := runPrune(t, st, clk, "--tag", "ai", "--max-items", "1")
	if res.code != 0 {
		t.Fatalf("prune --tag should exit 0, got code %d (stderr=%q)", res.code, res.err)
	}
	if env := parsePruneEnvelope(t, res.out); env.Pruned != 2 {
		t.Errorf("pruned = %d, want 2 (only the in-lane feed's surplus)\ngot: %q", env.Pruned, res.out)
	}
	if got := countItems(t, st, ai); got != 1 {
		t.Errorf("in-lane feed holds %d item(s), want 1", got)
	}
	if got := countItems(t, st, other); got != 3 {
		t.Errorf("out-of-lane feed holds %d item(s), want 3 untouched", got)
	}
}

// TestPruneTagAloneStillRequiresBound covers that --tag narrows a prune without
// authorizing one: a bare prune --tag names no bound and stays a usage error.
func TestPruneTagAloneStillRequiresBound(t *testing.T) {
	now := pollFixedTime()
	clk := testsupport.FixedClock(now)
	st := testsupport.NewInMemoryStore(clk)

	url := "https://a.example/feed.xml"
	if _, err := st.AddFeed(context.Background(), core.Feed{URL: url, Tags: []string{"ai"}, Status: core.FeedActive}); err != nil {
		t.Fatalf("AddFeed: %v", err)
	}
	seedItem(t, st, url, "a", "first", now.Add(-time.Hour), now)

	res := runPrune(t, st, clk, "--tag", "ai")
	if res.code != 64 {
		t.Errorf("prune --tag with no bound should exit 64 (usage), got code=%d", res.code)
	}
	if res.out != "" {
		t.Errorf("stdout should be empty on usage error, got %q", res.out)
	}
	if got := countItems(t, st, url); got != 1 {
		t.Errorf("feed holds %d item(s), want 1 untouched", got)
	}
}

// countItems reports how many items the store still holds for url.
func countItems(t *testing.T, st store.Store, url string) int {
	t.Helper()

	qr, err := st.QueryItems(context.Background(), core.ItemQuery{Feeds: []string{url}})
	if err != nil {
		t.Fatalf("QueryItems(%s): %v", url, err)
	}
	return len(qr.Items)
}
