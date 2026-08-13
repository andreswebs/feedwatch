package feedwatch_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/andreswebs/feedwatch"
	"github.com/andreswebs/feedwatch/core"
)

// TestItemsRequestValidateMessages pins the wording of every items usage error,
// which the CLI goldens compare byte for byte.
func TestItemsRequestValidateMessages(t *testing.T) {
	cases := []struct {
		name string
		req  feedwatch.ItemsRequest
		want string
	}{
		{
			"unknown field with a near match",
			feedwatch.ItemsRequest{Fields: []string{"tilte"}},
			`--fields: unknown field "tilte"; did you mean "title"?; valid fields: ` + strings.Join(core.ItemFieldNames(), ", "),
		},
		{
			"malformed order",
			feedwatch.ItemsRequest{Order: "published desc extra"},
			`--order: want '<published|fetched> [asc|desc]', got "published desc extra"`,
		},
		{
			"unknown order field",
			feedwatch.ItemsRequest{Order: "author"},
			`--order field must be 'published' or 'fetched', got "author"`,
		},
		{
			"unknown order direction",
			feedwatch.ItemsRequest{Order: "published sideways"},
			`--order direction must be 'asc' or 'desc', got "sideways"`,
		},
		{
			"unknown time-field",
			feedwatch.ItemsRequest{TimeField: "created"},
			`--time-field must be 'published' or 'fetched', got "created"`,
		},
		{
			"unparseable since",
			feedwatch.ItemsRequest{Since: "yesterday"},
			`--since: invalid time "yesterday": want RFC3339 or relative such as 24h or 7d`,
		},
		{
			"unparseable until",
			feedwatch.ItemsRequest{Until: "soon"},
			`--until: invalid time "soon": want RFC3339 or relative such as 24h or 7d`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wantUsageError(t, tc.req.Validate(), tc.want)
		})
	}
}

// TestItemsRequestAcceptsFeedURLField covers that naming the always-present
// identity field in a projection is a no-op rather than an error.
func TestItemsRequestAcceptsFeedURLField(t *testing.T) {
	req := feedwatch.ItemsRequest{Fields: []string{"feed_url", "title"}}
	if err := req.Validate(); err != nil {
		t.Errorf("Validate = %v, want nil: feed_url is accepted as a no-op", err)
	}
}

// TestItemsRelativeAndAbsoluteTimeBounds covers that a relative bound resolves
// against the injected clock and an RFC3339 bound passes through verbatim,
// asserted through which items the window selects.
func TestItemsRelativeAndAbsoluteTimeBounds(t *testing.T) {
	app, st, now := newTestApp(t)
	ctx := context.Background()

	const url = "https://blog.example/feed.xml"
	seedItem(t, st, url, "recent", now.Add(-2*time.Hour))
	seedItem(t, st, url, "older", now.Add(-10*24*time.Hour))

	cases := map[string]string{
		"days":    "7d",
		"hours":   "24h",
		"rfc3339": now.Add(-24 * time.Hour).Format(time.RFC3339),
	}
	for name, since := range cases {
		t.Run(name, func(t *testing.T) {
			res, err := app.Items(ctx, feedwatch.ItemsRequest{Since: since})
			if err != nil {
				t.Fatalf("Items = %v, want nil", err)
			}
			if len(res.Items) != 1 || res.Items[0].DedupKey != "recent" {
				t.Errorf("Items = %v, want only the recent item within the window", res.Items)
			}
		})
	}
}

// TestItemsReportsOmittedNoDate covers the honest-exclusion contract: a
// publication-axis window drops items with a null publication time and says how
// many it dropped.
func TestItemsReportsOmittedNoDate(t *testing.T) {
	app, st, now := newTestApp(t)
	ctx := context.Background()

	const url = "https://blog.example/feed.xml"
	seedItem(t, st, url, "dated", now.Add(-2*time.Hour))
	seedItem(t, st, url, "dateless", time.Time{})

	res, err := app.Items(ctx, feedwatch.ItemsRequest{Since: "24h"})
	if err != nil {
		t.Fatalf("Items = %v, want nil", err)
	}
	if res.OmittedNoDate != 1 {
		t.Errorf("OmittedNoDate = %d, want 1", res.OmittedNoDate)
	}

	onFetchAxis, err := app.Items(ctx, feedwatch.ItemsRequest{Since: "24h", TimeField: "fetched"})
	if err != nil {
		t.Fatalf("Items = %v, want nil", err)
	}
	if onFetchAxis.OmittedNoDate != 0 {
		t.Errorf("OmittedNoDate on the fetch axis = %d, want 0", onFetchAxis.OmittedNoDate)
	}
	if len(onFetchAxis.Items) != 2 {
		t.Errorf("fetch axis matched %d item(s), want 2: fetched_at is never null", len(onFetchAxis.Items))
	}
}

// TestItemsEnvelopeSelectsProjection covers that Envelope is where the
// projected-versus-full choice is made, and that a projected row leads with the
// always-on feed_url identity field.
func TestItemsEnvelopeSelectsProjection(t *testing.T) {
	app, st, now := newTestApp(t)
	ctx := context.Background()

	const url = "https://blog.example/feed.xml"
	seedItem(t, st, url, "one", now.Add(-time.Hour))

	res, err := app.Items(ctx, feedwatch.ItemsRequest{})
	if err != nil {
		t.Fatalf("Items = %v, want nil", err)
	}

	full := feedwatch.ItemsRequest{}.Envelope(res)
	if _, ok := full.(feedwatch.ItemsResult); !ok {
		t.Errorf("Envelope with no fields returned %T, want feedwatch.ItemsResult", full)
	}

	projected := feedwatch.ItemsRequest{Fields: []string{"title"}}.Envelope(res)
	if _, ok := projected.(feedwatch.ProjectedItemsResult); !ok {
		t.Fatalf("Envelope with fields returned %T, want feedwatch.ProjectedItemsResult", projected)
	}

	b, err := json.Marshal(projected)
	if err != nil {
		t.Fatalf("Marshal = %v, want nil", err)
	}
	if !strings.Contains(string(b), `"items":[{"feed_url":`) {
		t.Errorf("projected JSON = %s, want each row to lead with feed_url", b)
	}
	if strings.Contains(string(b), `"link"`) {
		t.Errorf("projected JSON = %s, want only the requested fields", b)
	}
}

// TestItemsResultMarshalsEmptyCollection pins the collection-coalescing rule for
// both envelope shapes.
func TestItemsResultMarshalsEmptyCollection(t *testing.T) {
	for name, v := range map[string]any{
		"full":      feedwatch.ItemsResult{Head: feedwatch.OKHead()},
		"projected": feedwatch.ItemsResult{Head: feedwatch.OKHead()}.Project([]string{"title"}),
	} {
		t.Run(name, func(t *testing.T) {
			b, err := json.Marshal(v)
			if err != nil {
				t.Fatalf("Marshal = %v, want nil", err)
			}
			if !strings.Contains(string(b), `"items":[]`) {
				t.Errorf("marshaled result = %s, want items as []", b)
			}
		})
	}
}

// seedItem stores one item under feedURL. A zero published time is stored as
// null, with the fetch time set, so the publication-axis exclusion path can be
// exercised.
func seedItem(t *testing.T, st interface {
	AddFeed(context.Context, core.Feed) (core.Feed, error)
	UpsertItems(context.Context, string, []core.Item) ([]core.Item, error)
}, feedURL, key string, published time.Time,
) {
	t.Helper()

	ctx := context.Background()
	if _, err := st.AddFeed(ctx, core.Feed{URL: feedURL, Status: core.FeedActive}); err != nil {
		t.Fatalf("AddFeed(%s): %v", feedURL, err)
	}
	it := core.Item{DedupKey: key, Title: key, Link: feedURL + "/" + key, FetchedAt: fixedTestTime()}
	if !published.IsZero() {
		p := published
		it.PublishedAt = &p
	}
	if _, err := st.UpsertItems(ctx, feedURL, []core.Item{it}); err != nil {
		t.Fatalf("UpsertItems(%s/%s): %v", feedURL, key, err)
	}
}
