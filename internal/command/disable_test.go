package command

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/store"
	"github.com/andreswebs/feedwatch/internal/testsupport"
)

// runDisable drives the disable command through the root with an injected store
// double, capturing stdout, stderr, and the exit code the boundary selected.
func runDisable(t *testing.T, st store.Store, clk core.Clock, args ...string) runResult {
	t.Helper()

	d := Deps{Clock: clk, Version: "1.2.3", store: st}
	return drive(t, d, append([]string{"disable"}, args...)...)
}

// disableEnvelope mirrors the stdout DisableResult shape for assertions.
type disableEnvelope struct {
	Feed FeedView `json:"feed"`
}

// TestDisableActiveFeed covers behavior 1: disable on an active feed sets status
// disabled and the feed is excluded from DueFeeds so poll skips it.
func TestDisableActiveFeed(t *testing.T) {
	clk := testsupport.FixedClock(pollFixedTime())
	st := testsupport.NewInMemoryStore(clk)

	url := "https://a.example/feed.xml"
	if _, err := st.AddFeed(context.Background(), core.Feed{URL: url, Status: core.FeedActive}); err != nil {
		t.Fatalf("AddFeed(%s): %v", url, err)
	}

	res := runDisable(t, st, clk, url)

	if res.code != 0 {
		t.Errorf("disable should exit 0 without invoking OsExiter, got code %d", res.code)
	}
	if res.err != "" {
		t.Errorf("stderr = %q, want empty", res.err)
	}

	var env disableEnvelope
	if err := json.Unmarshal([]byte(res.out), &env); err != nil {
		t.Fatalf("stdout is not a disable envelope: %v\ngot: %q", err, res.out)
	}
	if env.Feed.Status != string(core.FeedDisabled) {
		t.Errorf("reported status = %q, want %q", env.Feed.Status, core.FeedDisabled)
	}

	got, err := st.GetFeed(context.Background(), url)
	if err != nil {
		t.Fatalf("GetFeed: %v", err)
	}
	if got.Status != core.FeedDisabled {
		t.Errorf("stored status = %q, want %q", got.Status, core.FeedDisabled)
	}

	due, err := st.DueFeeds(context.Background(), pollFixedTime())
	if err != nil {
		t.Fatalf("DueFeeds: %v", err)
	}
	if len(due) != 0 {
		t.Errorf("DueFeeds = %v, want the disabled feed %q to be excluded", due, url)
	}
}

// TestDisableUnknownRef covers behavior 2: disable of an unknown ref is a usage
// error (exit 64) with a structured error object on stderr and no stdout result.
func TestDisableUnknownRef(t *testing.T) {
	clk := testsupport.FixedClock(pollFixedTime())
	st := testsupport.NewInMemoryStore(clk)

	res := runDisable(t, st, clk, "https://missing.example/feed.xml")

	if res.code != 64 {
		t.Errorf("disable of unknown ref should exit 64 (usage), got code=%d", res.code)
	}
	if res.out != "" {
		t.Errorf("stdout = %q, want empty on a usage failure", res.out)
	}

	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(res.err), &env); err != nil {
		t.Fatalf("stderr is not a JSON error object: %v\ngot: %q", err, res.err)
	}
	if env.Error.Code != core.ErrUsage.Code() {
		t.Errorf("code = %q, want %q", env.Error.Code, core.ErrUsage.Code())
	}
}

// TestDisableThenEnableRoundTrips covers behavior 3: disable then enable returns
// the feed to active, exercising the symmetric pair against one store.
func TestDisableThenEnableRoundTrips(t *testing.T) {
	clk := testsupport.FixedClock(pollFixedTime())
	st := testsupport.NewInMemoryStore(clk)

	url := "https://a.example/feed.xml"
	if _, err := st.AddFeed(context.Background(), core.Feed{URL: url, Status: core.FeedActive}); err != nil {
		t.Fatalf("AddFeed(%s): %v", url, err)
	}

	if res := runDisable(t, st, clk, url); res.code != 0 {
		t.Fatalf("disable should exit 0, got code %d (stderr %q)", res.code, res.err)
	}
	got, err := st.GetFeed(context.Background(), url)
	if err != nil {
		t.Fatalf("GetFeed after disable: %v", err)
	}
	if got.Status != core.FeedDisabled {
		t.Fatalf("status after disable = %q, want %q", got.Status, core.FeedDisabled)
	}

	if res := runEnable(t, st, clk, url); res.code != 0 {
		t.Fatalf("enable should exit 0, got code %d (stderr %q)", res.code, res.err)
	}
	got, err = st.GetFeed(context.Background(), url)
	if err != nil {
		t.Fatalf("GetFeed after enable: %v", err)
	}
	if got.Status != core.FeedActive {
		t.Errorf("status after enable = %q, want %q", got.Status, core.FeedActive)
	}
}
