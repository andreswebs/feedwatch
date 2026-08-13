package feedwatch_test

import (
	"context"
	"testing"
	"time"

	"github.com/andreswebs/feedwatch"
	"github.com/andreswebs/feedwatch/core"
)

// TestDisableLeavesFailureCountUntouched draws the contrast with auto-disable:
// disable is a manual switch over the lifecycle, not a part of it, so the
// failure history it reports survives the change.
func TestDisableLeavesFailureCountUntouched(t *testing.T) {
	app, st, now := newTestApp(t)
	ctx := context.Background()

	const url = "https://flaky.example/feed.xml"
	if _, err := st.AddFeed(ctx, core.Feed{URL: url, Status: core.FeedActive}); err != nil {
		t.Fatalf("AddFeed: %v", err)
	}
	if err := st.RecordFailure(ctx, url, core.CatNetwork, "dns: no such host", now, now.Add(time.Hour)); err != nil {
		t.Fatalf("RecordFailure: %v", err)
	}

	res, err := app.Disable(ctx, feedwatch.DisableRequest{Ref: url})
	if err != nil {
		t.Fatalf("Disable = %v, want nil", err)
	}
	if res.Feed.Status != string(core.FeedDisabled) {
		t.Errorf("status = %q, want %q", res.Feed.Status, core.FeedDisabled)
	}
	if res.Feed.Failures != 1 {
		t.Errorf("failures = %d, want the recorded 1 to survive disabling", res.Feed.Failures)
	}
	if res.Feed.LastError != "dns: no such host" {
		t.Errorf("last error = %q, want it to survive disabling", res.Feed.LastError)
	}
}

// TestDisableUnknownRefIsUsageError pins the classification an unknown ref gets.
func TestDisableUnknownRefIsUsageError(t *testing.T) {
	app, _, _ := newTestApp(t)

	_, err := app.Disable(context.Background(), feedwatch.DisableRequest{Ref: "nope"})
	wantUsageError(t, err, "feed not found")
}
