package feedwatch_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/andreswebs/feedwatch"
	"github.com/andreswebs/feedwatch/core"
	"github.com/andreswebs/feedwatch/internal/testsupport"
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

// seedLaneItems seeds the three-feed lane fixture the tag tests share (one feed
// in both lanes, one in a single lane, one untagged) and gives each feed four
// items at distinct publication times. Every item's key encodes its age in
// hours, so a lane's expected order and page are hand-computable: the ai lane
// holds k00, k01, k03, k04, k06, k07, k09 and k10 in descending publication
// order.
func seedLaneItems(t *testing.T, st *testsupport.InMemoryStore, now time.Time) (both, one, none string) {
	t.Helper()

	both, one, none = seedLaneFeeds(t, st)
	for feedIdx, url := range []string{both, one, none} {
		for j := range 4 {
			age := time.Duration(3*j+feedIdx) * time.Hour
			seedItem(t, st, url, fmt.Sprintf("k%02d", int(age.Hours())), now.Add(-age))
		}
	}
	return both, one, none
}

// itemKeys projects an items result onto its dedup keys, which is what every
// lane assertion compares.
func itemKeys(res feedwatch.ItemsResult) []string {
	keys := make([]string, 0, len(res.Items))
	for _, it := range res.Items {
		keys = append(keys, it.DedupKey)
	}
	return keys
}

// TestItemsFiltersByTag covers behaviors 1, 2, 3, 4, 5 and 8 on the library
// side: a tag narrows item history to a lane, the default match is all, --tag
// composes as an intersection with --feed, the time window and --contains, the
// page is taken over the filtered set, and an empty lane is an empty result
// rather than an error.
func TestItemsFiltersByTag(t *testing.T) {
	app, st, now := newTestApp(t)
	both, _, none := seedLaneItems(t, st, now)

	tests := []struct {
		name string
		req  feedwatch.ItemsRequest
		want []string
	}{
		{
			"one tag narrows to the lane",
			feedwatch.ItemsRequest{Tags: []string{"ai"}},
			[]string{"k00", "k01", "k03", "k04", "k06", "k07", "k09", "k10"},
		},
		{
			"two tags default to match all",
			feedwatch.ItemsRequest{Tags: []string{"ai", "agents"}},
			[]string{"k00", "k03", "k06", "k09"},
		},
		{
			"match any unions the lanes",
			feedwatch.ItemsRequest{Tags: []string{"ai", "agents"}, Match: "any"},
			[]string{"k00", "k01", "k03", "k04", "k06", "k07", "k09", "k10"},
		},
		{
			"tag and feed intersect rather than union",
			feedwatch.ItemsRequest{Tags: []string{"ai"}, Feeds: []string{both}},
			[]string{"k00", "k03", "k06", "k09"},
		},
		{
			"a feed outside the lane matches nothing",
			feedwatch.ItemsRequest{Tags: []string{"ai"}, Feeds: []string{none}},
			[]string{},
		},
		{
			"tag composes with the time window",
			feedwatch.ItemsRequest{Tags: []string{"ai"}, Since: "5h"},
			[]string{"k00", "k01", "k03", "k04"},
		},
		{
			"tag composes with contains",
			feedwatch.ItemsRequest{Tags: []string{"ai"}, Contains: "k04"},
			[]string{"k04"},
		},
		{
			"page is taken over the filtered set",
			feedwatch.ItemsRequest{Tags: []string{"ai"}, Limit: 2, Offset: 2},
			[]string{"k03", "k04"},
		},
		{
			"an empty lane is not an error",
			feedwatch.ItemsRequest{Tags: []string{"nosuchlane"}},
			[]string{},
		},
		{
			"no tags query every feed",
			feedwatch.ItemsRequest{Limit: 3},
			[]string{"k00", "k01", "k02"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := app.Items(context.Background(), tt.req)
			if err != nil {
				t.Fatalf("Items = %v, want nil", err)
			}
			got := itemKeys(res)
			if len(got) != len(tt.want) {
				t.Fatalf("keys = %v, want %v", got, tt.want)
			}
			for i, k := range tt.want {
				if got[i] != k {
					t.Fatalf("keys = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

// TestItemsTagOmittedNoDateCountsOnlyInLane covers behavior 6: the
// honest-exclusion count is computed over the filtered set, so an undated item
// outside the lane is never counted against a lane query.
func TestItemsTagOmittedNoDateCountsOnlyInLane(t *testing.T) {
	app, st, now := newTestApp(t)
	_, one, none := seedLaneItems(t, st, now)
	seedItem(t, st, one, "in-lane-dateless", time.Time{})
	seedItem(t, st, none, "out-of-lane-dateless", time.Time{})

	res, err := app.Items(context.Background(), feedwatch.ItemsRequest{Tags: []string{"ai"}, Since: "24h"})
	if err != nil {
		t.Fatalf("Items = %v, want nil", err)
	}
	if res.OmittedNoDate != 1 {
		t.Errorf("OmittedNoDate = %d, want 1: only the in-lane undated item counts", res.OmittedNoDate)
	}
}

// TestItemsTagProjects covers behavior 7: --tag narrows the rows a projection
// renders without changing the projected envelope's shape.
func TestItemsTagProjects(t *testing.T) {
	app, st, now := newTestApp(t)
	seedLaneItems(t, st, now)

	req := feedwatch.ItemsRequest{Tags: []string{"ai", "agents"}, Fields: []string{"title"}}
	res, err := app.Items(context.Background(), req)
	if err != nil {
		t.Fatalf("Items = %v, want nil", err)
	}

	projected, ok := req.Envelope(res).(feedwatch.ProjectedItemsResult)
	if !ok {
		t.Fatalf("Envelope returned %T, want feedwatch.ProjectedItemsResult", req.Envelope(res))
	}
	if len(projected.Items) != 4 {
		t.Fatalf("projected %d row(s), want 4: only the both-lane feed's items", len(projected.Items))
	}
	for _, row := range projected.Items {
		if len(row) != 2 || row["title"] == nil || row["feed_url"] == nil {
			t.Errorf("row = %v, want exactly feed_url and title", row)
		}
	}
}

// TestItemsRejectsInvalidTagSelection covers behavior 9: an unknown --match
// value and an unstorable tag name are usage errors, reported by both Validate
// and the use case so a frontend can reject before dialing the store.
func TestItemsRejectsInvalidTagSelection(t *testing.T) {
	tests := []struct {
		name string
		req  feedwatch.ItemsRequest
		msg  string
	}{
		{"unknown match", feedwatch.ItemsRequest{Tags: []string{"ai"}, Match: "bogus"}, `match must be 'all' or 'any', got "bogus"`},
		{"empty tag", feedwatch.ItemsRequest{Tags: []string{""}}, `tag must not be empty, got ""`},
		{"tag with whitespace", feedwatch.ItemsRequest{Tags: []string{"a b"}}, `tag must not contain whitespace, got "a b"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app, st, now := newTestApp(t)
			seedLaneItems(t, st, now)

			wantUsageError(t, tt.req.Validate(), tt.msg)

			_, err := app.Items(context.Background(), tt.req)
			wantUsageError(t, err, tt.msg)
		})
	}
}
