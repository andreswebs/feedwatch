package cli

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	cliv3 "github.com/urfave/cli/v3"

	"github.com/andreswebs/feedwatch/internal/config"
	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/store"
	"github.com/andreswebs/feedwatch/internal/testsupport"
)

// runPrune drives the prune command through the root with an injected store
// double, capturing stdout, stderr, and the exit code the boundary selected.
func runPrune(t *testing.T, st store.Store, clk core.Clock, args ...string) runResult {
	t.Helper()

	outF, errF := tempFile(t), tempFile(t)
	d := Deps{
		Cfg:     config.Defaults(),
		Clock:   clk,
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

	_ = NewRootCommand(d).Run(t.Context(), append([]string{"feedwatch", "prune"}, args...))

	res.out = readFile(t, outF)
	res.err = readFile(t, errF)
	return res
}

// pruneEnvelope mirrors the stdout PruneResult shape for assertions.
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
	if res.exited {
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
	if res.exited {
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
	if res.exited {
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
// (exit 1) with no result on stdout, rather than a silent no-op.
func TestPruneRequiresBound(t *testing.T) {
	now := pollFixedTime()
	clk := testsupport.FixedClock(now)
	st := testsupport.NewInMemoryStore(clk)

	res := runPrune(t, st, clk)
	if !res.exited || res.code != 1 {
		t.Errorf("prune with no bound should exit 1, got exited=%v code=%d", res.exited, res.code)
	}
	if res.out != "" {
		t.Errorf("stdout should be empty on usage error, got %q", res.out)
	}
}
