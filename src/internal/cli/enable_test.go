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

// runEnable drives the enable command through the root with an injected store
// double, capturing stdout, stderr, and the exit code the boundary selected.
func runEnable(t *testing.T, st store.Store, clk core.Clock, args ...string) runResult {
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

	_ = NewRootCommand(d).Run(t.Context(), append([]string{"feedwatch", "enable"}, args...))

	res.out = readFile(t, outF)
	res.err = readFile(t, errF)
	return res
}

// enableEnvelope mirrors the stdout EnableResult shape for assertions.
type enableEnvelope struct {
	Feed FeedView `json:"feed"`
}

// TestEnableDisabledFeed covers behavior 1: enable on a disabled feed sets
// status active, clears the failure lifecycle, and makes the feed due again.
func TestEnableDisabledFeed(t *testing.T) {
	clk := testsupport.FixedClock(pollFixedTime())
	st := testsupport.NewInMemoryStore(clk)

	url := "https://flaky.example/feed.xml"
	future := pollFixedTime().Add(24 * time.Hour)
	if _, err := st.AddFeed(context.Background(), core.Feed{
		URL:          url,
		Status:       core.FeedDisabled,
		FailureCount: 12,
		LastError:    "dns: no such host",
		LastErrorAt:  &future,
		NextDueAt:    &future,
	}); err != nil {
		t.Fatalf("AddFeed(%s): %v", url, err)
	}

	res := runEnable(t, st, clk, url)

	if res.exited {
		t.Errorf("enable should exit 0 without invoking OsExiter, got code %d", res.code)
	}
	if res.err != "" {
		t.Errorf("stderr = %q, want empty", res.err)
	}

	var env enableEnvelope
	if err := json.Unmarshal([]byte(res.out), &env); err != nil {
		t.Fatalf("stdout is not an enable envelope: %v\ngot: %q", err, res.out)
	}
	if env.Feed.Status != string(core.FeedActive) {
		t.Errorf("reported status = %q, want %q", env.Feed.Status, core.FeedActive)
	}
	if env.Feed.Failures != 0 {
		t.Errorf("reported failures = %d, want 0", env.Feed.Failures)
	}
	if env.Feed.LastError != "" {
		t.Errorf("reported last_error = %q, want empty", env.Feed.LastError)
	}

	got, err := st.GetFeed(context.Background(), url)
	if err != nil {
		t.Fatalf("GetFeed: %v", err)
	}
	if got.Status != core.FeedActive {
		t.Errorf("stored status = %q, want %q", got.Status, core.FeedActive)
	}
	if got.FailureCount != 0 {
		t.Errorf("stored failure_count = %d, want 0", got.FailureCount)
	}
	if got.LastError != "" {
		t.Errorf("stored last_error = %q, want cleared", got.LastError)
	}

	due, err := st.DueFeeds(context.Background(), pollFixedTime())
	if err != nil {
		t.Fatalf("DueFeeds: %v", err)
	}
	if len(due) != 1 || due[0].URL != url {
		t.Errorf("DueFeeds = %v, want the enabled feed %q to be due", due, url)
	}
}

// TestEnableUnknownRef covers behavior 2: enable of an unknown ref is a usage
// error (exit 1) with a structured error object on stderr and no stdout result.
func TestEnableUnknownRef(t *testing.T) {
	clk := testsupport.FixedClock(pollFixedTime())
	st := testsupport.NewInMemoryStore(clk)

	res := runEnable(t, st, clk, "https://missing.example/feed.xml")

	if !res.exited || res.code != 1 {
		t.Errorf("enable of unknown ref should exit 1, got exited=%v code=%d", res.exited, res.code)
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

// TestEnableAlreadyActive covers behavior 3: enable on an already-active feed is
// idempotent, exits 0, and leaves the feed active.
func TestEnableAlreadyActive(t *testing.T) {
	clk := testsupport.FixedClock(pollFixedTime())
	st := testsupport.NewInMemoryStore(clk)

	url := "https://a.example/feed.xml"
	if _, err := st.AddFeed(context.Background(), core.Feed{URL: url, Status: core.FeedActive}); err != nil {
		t.Fatalf("AddFeed(%s): %v", url, err)
	}

	res := runEnable(t, st, clk, url)

	if res.exited {
		t.Errorf("enable of an active feed should exit 0, got code %d", res.code)
	}

	var env enableEnvelope
	if err := json.Unmarshal([]byte(res.out), &env); err != nil {
		t.Fatalf("stdout is not an enable envelope: %v\ngot: %q", err, res.out)
	}
	if env.Feed.Status != string(core.FeedActive) {
		t.Errorf("reported status = %q, want %q", env.Feed.Status, core.FeedActive)
	}

	got, err := st.GetFeed(context.Background(), url)
	if err != nil {
		t.Fatalf("GetFeed: %v", err)
	}
	if got.Status != core.FeedActive {
		t.Errorf("stored status = %q, want %q", got.Status, core.FeedActive)
	}
}
