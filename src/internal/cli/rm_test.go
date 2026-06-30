package cli

import (
	"context"
	"encoding/json"
	"testing"

	cliv3 "github.com/urfave/cli/v3"

	"github.com/andreswebs/feedwatch/internal/config"
	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/store"
	"github.com/andreswebs/feedwatch/internal/testsupport"
)

// runRm drives the rm command through the root with an injected store double,
// capturing stdout, stderr, and the exit code the boundary selected.
func runRm(t *testing.T, st store.Store, clk core.Clock, args ...string) runResult {
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

	_ = NewRootCommand(d).Run(t.Context(), append([]string{"feedwatch", "rm"}, args...))

	res.out = readFile(t, outF)
	res.err = readFile(t, errF)
	return res
}

// rmEnvelope mirrors the stdout RmResult shape for assertions.
type rmEnvelope struct {
	Removed string `json:"removed"`
}

// TestRmByURL covers behavior 1: rm by URL removes the subscription and its
// items, reports the removed URL, and exits 0.
func TestRmByURL(t *testing.T) {
	clk := testsupport.FixedClock(pollFixedTime())
	st := testsupport.NewInMemoryStore(clk)

	url := "https://a.example/feed.xml"
	if _, err := st.AddFeed(context.Background(), core.Feed{URL: url, Status: core.FeedActive}); err != nil {
		t.Fatalf("AddFeed(%s): %v", url, err)
	}
	if _, err := st.UpsertItems(context.Background(), url, []core.Item{
		{DedupKey: "k1", Title: "one"},
	}); err != nil {
		t.Fatalf("UpsertItems: %v", err)
	}

	res := runRm(t, st, clk, url)

	if res.exited {
		t.Errorf("rm should exit 0 without invoking OsExiter, got code %d", res.code)
	}
	if res.err != "" {
		t.Errorf("stderr = %q, want empty", res.err)
	}

	var env rmEnvelope
	if err := json.Unmarshal([]byte(res.out), &env); err != nil {
		t.Fatalf("stdout is not an rm envelope: %v\ngot: %q", err, res.out)
	}
	if env.Removed != url {
		t.Errorf("removed = %q, want %q", env.Removed, url)
	}

	if _, err := st.GetFeed(context.Background(), url); err == nil {
		t.Errorf("feed %q still present after rm", url)
	}
	items, err := st.QueryItems(context.Background(), core.ItemQuery{Feeds: []string{url}})
	if err != nil {
		t.Fatalf("QueryItems: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("len(items) = %d after rm, want 0 (items should cascade)", len(items))
	}
}

// TestRmByAlias covers behavior 2: rm resolves a unique alias and removes the
// feed, reporting its canonical URL.
func TestRmByAlias(t *testing.T) {
	clk := testsupport.FixedClock(pollFixedTime())
	st := testsupport.NewInMemoryStore(clk)

	url := "https://a.example/feed.xml"
	if _, err := st.AddFeed(context.Background(), core.Feed{URL: url, Alias: "aye", Status: core.FeedActive}); err != nil {
		t.Fatalf("AddFeed(%s): %v", url, err)
	}

	res := runRm(t, st, clk, "aye")

	if res.exited {
		t.Errorf("rm should exit 0, got code %d", res.code)
	}

	var env rmEnvelope
	if err := json.Unmarshal([]byte(res.out), &env); err != nil {
		t.Fatalf("stdout is not an rm envelope: %v\ngot: %q", err, res.out)
	}
	if env.Removed != url {
		t.Errorf("removed = %q, want canonical url %q", env.Removed, url)
	}
	if _, err := st.GetFeed(context.Background(), url); err == nil {
		t.Errorf("feed %q still present after rm by alias", url)
	}
}

// TestRmUnknownRef covers behavior 3: rm of an unknown ref is a usage error
// (exit 1) with a structured error object on stderr and no stdout result.
func TestRmUnknownRef(t *testing.T) {
	clk := testsupport.FixedClock(pollFixedTime())
	st := testsupport.NewInMemoryStore(clk)

	res := runRm(t, st, clk, "https://missing.example/feed.xml")

	if !res.exited || res.code != 1 {
		t.Errorf("rm of unknown ref should exit 1, got exited=%v code=%d", res.exited, res.code)
	}
	if res.out != "" {
		t.Errorf("stdout = %q, want empty on a usage failure", res.out)
	}

	var env struct {
		Error struct {
			Category string `json:"category"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(res.err), &env); err != nil {
		t.Fatalf("stderr is not a JSON error object: %v\ngot: %q", err, res.err)
	}
	if env.Error.Category != string(core.CatUsage) {
		t.Errorf("error category = %q, want %q", env.Error.Category, core.CatUsage)
	}
}
