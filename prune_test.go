package feedwatch_test

import (
	"context"
	"testing"
	"time"

	"github.com/andreswebs/feedwatch"
	"github.com/andreswebs/feedwatch/core"
)

// TestPruneRequestValidate pins the wording of every prune usage error, which
// the CLI goldens compare byte for byte.
func TestPruneRequestValidate(t *testing.T) {
	zero, negative := 0, -1
	cases := []struct {
		name string
		req  feedwatch.PruneRequest
		want string
	}{
		{"neither policy", feedwatch.PruneRequest{}, "prune requires --keep-days and/or --max-items"},
		{"negative keep-days", feedwatch.PruneRequest{KeepDays: &negative}, "--keep-days must not be negative"},
		{"negative max-items", feedwatch.PruneRequest{MaxItems: &negative}, "--max-items must not be negative"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wantUsageError(t, tc.req.Validate(), tc.want)
		})
	}

	t.Run("explicit zero keep-days is a policy", func(t *testing.T) {
		if err := (feedwatch.PruneRequest{KeepDays: &zero}).Validate(); err != nil {
			t.Errorf("Validate = %v, want nil: an explicit --keep-days 0 is a policy", err)
		}
	})
}

// TestPruneKeepDaysZeroCutsAtNow covers the reason the request fields are
// pointers: an explicit zero prunes everything older than now, which is a
// different outcome from the flag being absent.
func TestPruneKeepDaysZeroCutsAtNow(t *testing.T) {
	app, st, now := newTestApp(t)
	ctx := context.Background()

	const url = "https://blog.example/feed.xml"
	if _, err := st.AddFeed(ctx, core.Feed{URL: url, Status: core.FeedActive}); err != nil {
		t.Fatalf("AddFeed: %v", err)
	}
	old := now.Add(-48 * time.Hour)
	if _, err := st.UpsertItems(ctx, url, []core.Item{
		{DedupKey: "one", Title: "One", PublishedAt: &old, FetchedAt: old},
	}); err != nil {
		t.Fatalf("UpsertItems: %v", err)
	}

	zero := 0
	res, err := app.Prune(ctx, feedwatch.PruneRequest{KeepDays: &zero})
	if err != nil {
		t.Fatalf("Prune = %v, want nil", err)
	}
	if res.Pruned != 1 {
		t.Errorf("Pruned = %d, want 1 with a cutoff at now", res.Pruned)
	}

	qr, err := st.QueryItems(ctx, core.ItemQuery{})
	if err != nil {
		t.Fatalf("QueryItems: %v", err)
	}
	if len(qr.Items) != 0 {
		t.Errorf("store still holds %d item(s) after pruning, want 0", len(qr.Items))
	}
}
