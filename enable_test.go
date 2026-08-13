package feedwatch_test

import (
	"context"
	"testing"
	"time"

	"github.com/andreswebs/feedwatch"
	"github.com/andreswebs/feedwatch/core"
)

// TestEnableResetsFailureLifecycle covers the substance of enable: a feed that
// was auto-disabled and backed off comes back active, with its failure count and
// last error cleared and its schedule due again.
func TestEnableResetsFailureLifecycle(t *testing.T) {
	app, st, now := newTestApp(t)
	ctx := context.Background()

	const url = "https://flaky.example/feed.xml"
	if _, err := st.AddFeed(ctx, core.Feed{URL: url, Status: core.FeedActive}); err != nil {
		t.Fatalf("AddFeed: %v", err)
	}
	if err := st.RecordFailure(ctx, url, core.CatNetwork, "dns: no such host", now, now.Add(24*time.Hour)); err != nil {
		t.Fatalf("RecordFailure: %v", err)
	}
	if err := st.SetStatus(ctx, url, core.FeedDisabled); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	res, err := app.Enable(ctx, feedwatch.EnableRequest{Ref: url})
	if err != nil {
		t.Fatalf("Enable = %v, want nil", err)
	}
	if res.Feed.Status != string(core.FeedActive) {
		t.Errorf("status = %q, want %q", res.Feed.Status, core.FeedActive)
	}
	if res.Feed.Failures != 0 {
		t.Errorf("failures = %d, want 0", res.Feed.Failures)
	}
	if res.Feed.LastError != "" {
		t.Errorf("last error = %q, want empty", res.Feed.LastError)
	}

	due, err := st.DueFeeds(ctx, now)
	if err != nil {
		t.Fatalf("DueFeeds: %v", err)
	}
	if len(due) != 1 || due[0].URL != url {
		t.Errorf("DueFeeds = %v, want the re-enabled feed to be immediately due", due)
	}
}

// TestEnableIsIdempotent covers that enabling an already-active feed succeeds
// and reports the same state, so an agent can call it unconditionally.
func TestEnableIsIdempotent(t *testing.T) {
	app, st, _ := newTestApp(t)
	ctx := context.Background()

	const url = "https://ok.example/feed.xml"
	if _, err := st.AddFeed(ctx, core.Feed{URL: url, Alias: "ok", Status: core.FeedActive}); err != nil {
		t.Fatalf("AddFeed: %v", err)
	}

	first, err := app.Enable(ctx, feedwatch.EnableRequest{Ref: "ok"})
	if err != nil {
		t.Fatalf("first Enable = %v, want nil", err)
	}
	second, err := app.Enable(ctx, feedwatch.EnableRequest{Ref: url})
	if err != nil {
		t.Fatalf("second Enable = %v, want nil", err)
	}
	if first.Feed != second.Feed {
		t.Errorf("enable is not idempotent: %+v then %+v", first.Feed, second.Feed)
	}
}

// TestEnableUnknownRefIsUsageError pins the classification an unknown ref gets.
func TestEnableUnknownRefIsUsageError(t *testing.T) {
	app, _, _ := newTestApp(t)

	_, err := app.Enable(context.Background(), feedwatch.EnableRequest{Ref: "nope"})
	wantUsageError(t, err, "feed not found")
}
