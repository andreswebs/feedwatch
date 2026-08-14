package feedwatch_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/andreswebs/feedwatch"
	"github.com/andreswebs/feedwatch/core"
	"github.com/andreswebs/feedwatch/internal/testsupport"
)

// seedTagged subscribes url carrying tags, so a tag test starts from a known
// lane membership.
func seedTagged(t *testing.T, st *testsupport.InMemoryStore, url string, tags ...string) {
	t.Helper()
	if _, err := st.AddFeed(context.Background(), core.Feed{
		URL: url, Status: core.FeedActive, Tags: tags,
	}); err != nil {
		t.Fatalf("AddFeed(%s): %v", url, err)
	}
}

// tagWriteSpy records how many times a tag write reached the store, which is
// how a test observes the write-skip: the fixed clock leaves updated_at equal
// whether or not the row was rewritten.
type tagWriteSpy struct {
	*testsupport.InMemoryStore
	setTags int
}

func (s *tagWriteSpy) SetTags(ctx context.Context, url string, tags []string) error {
	s.setTags++
	return s.InMemoryStore.SetTags(ctx, url, tags)
}

// newSpyApp is newTestApp over a store that counts its tag writes.
func newSpyApp(t *testing.T) (*feedwatch.App, *tagWriteSpy) {
	t.Helper()

	clk := testsupport.FixedClock(fixedTestTime())
	st := &tagWriteSpy{InMemoryStore: testsupport.NewInMemoryStore(clk)}
	app, err := feedwatch.New(feedwatch.Defaults(),
		feedwatch.WithStore(st),
		feedwatch.WithClock(clk),
	)
	if err != nil {
		t.Fatalf("New = %v, want nil", err)
	}
	t.Cleanup(func() { _ = app.Close() })
	return app, st
}

// TestTagReadsWithoutWriting is the tracer: tag with no write flag reports the
// stored set with empty deltas and leaves the feed untouched.
func TestTagReadsWithoutWriting(t *testing.T) {
	app, st := newSpyApp(t)
	ctx := context.Background()

	const url = "https://a.example/feed.xml"
	seedTagged(t, st.InMemoryStore, url, "ai")

	res, err := app.Tag(ctx, feedwatch.TagRequest{Ref: url})
	if err != nil {
		t.Fatalf("Tag = %v, want nil", err)
	}
	if res.URL != url {
		t.Errorf("url = %q, want %q", res.URL, url)
	}
	if !slices.Equal(res.Tags, []string{"ai"}) {
		t.Errorf("tags = %v, want [ai]", res.Tags)
	}
	if len(res.Added) != 0 || len(res.Removed) != 0 {
		t.Errorf("read reported delta added=%v removed=%v, want both empty", res.Added, res.Removed)
	}
	if st.setTags != 0 {
		t.Errorf("read performed %d tag write(s), want none", st.setTags)
	}
}

// TestTagAddStoresCanonicalSet covers behavior 2: --add on an untagged feed
// stores the canonical set and reports it as the delta.
func TestTagAddStoresCanonicalSet(t *testing.T) {
	app, st, _ := newTestApp(t)
	ctx := context.Background()

	const url = "https://a.example/feed.xml"
	seedTagged(t, st, url)

	res, err := app.Tag(ctx, feedwatch.TagRequest{Ref: url, Add: []string{"AI", "agents"}})
	if err != nil {
		t.Fatalf("Tag = %v, want nil", err)
	}
	want := []string{"agents", "ai"}
	if !slices.Equal(res.Tags, want) {
		t.Errorf("tags = %v, want %v", res.Tags, want)
	}
	if !slices.Equal(res.Added, want) {
		t.Errorf("added = %v, want %v", res.Added, want)
	}
	if len(res.Removed) != 0 {
		t.Errorf("removed = %v, want empty", res.Removed)
	}

	stored, err := st.GetFeed(ctx, url)
	if err != nil {
		t.Fatalf("GetFeed: %v", err)
	}
	if !slices.Equal(stored.Tags, want) {
		t.Errorf("stored tags = %v, want %v", stored.Tags, want)
	}
}

// TestTagAddIsIdempotent covers behavior 3: adding a tag the feed already
// carries changes nothing, reports an empty delta, and performs no write.
func TestTagAddIsIdempotent(t *testing.T) {
	app, st := newSpyApp(t)
	ctx := context.Background()

	const url = "https://a.example/feed.xml"
	seedTagged(t, st.InMemoryStore, url, "ai")

	res, err := app.Tag(ctx, feedwatch.TagRequest{Ref: url, Add: []string{"ai"}})
	if err != nil {
		t.Fatalf("Tag = %v, want nil", err)
	}
	if !slices.Equal(res.Tags, []string{"ai"}) {
		t.Errorf("tags = %v, want [ai]", res.Tags)
	}
	if len(res.Added) != 0 {
		t.Errorf("added = %v, want empty on an idempotent add", res.Added)
	}
	if st.setTags != 0 {
		t.Errorf("no-op edit performed %d tag write(s), want none", st.setTags)
	}
}

// TestTagRemove covers behavior 4: --remove drops a carried tag and reports it,
// while removing an absent tag is a no-op with an empty delta.
func TestTagRemove(t *testing.T) {
	app, st, _ := newTestApp(t)
	ctx := context.Background()

	const url = "https://a.example/feed.xml"
	seedTagged(t, st, url, "ai", "agents")

	res, err := app.Tag(ctx, feedwatch.TagRequest{Ref: url, Remove: []string{"agents"}})
	if err != nil {
		t.Fatalf("Tag = %v, want nil", err)
	}
	if !slices.Equal(res.Tags, []string{"ai"}) {
		t.Errorf("tags = %v, want [ai]", res.Tags)
	}
	if !slices.Equal(res.Removed, []string{"agents"}) {
		t.Errorf("removed = %v, want [agents]", res.Removed)
	}

	absent, err := app.Tag(ctx, feedwatch.TagRequest{Ref: url, Remove: []string{"security"}})
	if err != nil {
		t.Fatalf("Tag = %v, want nil", err)
	}
	if !slices.Equal(absent.Tags, []string{"ai"}) {
		t.Errorf("tags = %v, want [ai]", absent.Tags)
	}
	if len(absent.Removed) != 0 {
		t.Errorf("removed = %v, want empty when the tag was absent", absent.Removed)
	}
}

// TestTagAddBeforeRemove pins the documented composition order: a tag named in
// both --add and --remove ends up removed.
func TestTagAddBeforeRemove(t *testing.T) {
	app, st, _ := newTestApp(t)
	ctx := context.Background()

	const url = "https://a.example/feed.xml"
	seedTagged(t, st, url)

	res, err := app.Tag(ctx, feedwatch.TagRequest{
		Ref: url, Add: []string{"ai", "agents"}, Remove: []string{"ai"},
	})
	if err != nil {
		t.Fatalf("Tag = %v, want nil", err)
	}
	if !slices.Equal(res.Tags, []string{"agents"}) {
		t.Errorf("tags = %v, want [agents]", res.Tags)
	}
	if !slices.Equal(res.Added, []string{"agents"}) {
		t.Errorf("added = %v, want [agents]", res.Added)
	}
}

// TestTagSetReplacesWholeSet covers behavior 5: --set replaces the set and
// reports both the additions and the removals it caused.
func TestTagSetReplacesWholeSet(t *testing.T) {
	app, st, _ := newTestApp(t)
	ctx := context.Background()

	const url = "https://a.example/feed.xml"
	seedTagged(t, st, url, "ai", "security")

	res, err := app.Tag(ctx, feedwatch.TagRequest{Ref: url, Set: []string{"ai", "research"}})
	if err != nil {
		t.Fatalf("Tag = %v, want nil", err)
	}
	if !slices.Equal(res.Tags, []string{"ai", "research"}) {
		t.Errorf("tags = %v, want [ai research]", res.Tags)
	}
	if !slices.Equal(res.Added, []string{"research"}) {
		t.Errorf("added = %v, want [research]", res.Added)
	}
	if !slices.Equal(res.Removed, []string{"security"}) {
		t.Errorf("removed = %v, want [security]", res.Removed)
	}
}

// TestTagClearEmptiesTheSet covers behavior 6: --clear removes every tag and
// reports each prior tag as removed.
func TestTagClearEmptiesTheSet(t *testing.T) {
	app, st, _ := newTestApp(t)
	ctx := context.Background()

	const url = "https://a.example/feed.xml"
	seedTagged(t, st, url, "ai", "agents")

	res, err := app.Tag(ctx, feedwatch.TagRequest{Ref: url, Clear: true})
	if err != nil {
		t.Fatalf("Tag = %v, want nil", err)
	}
	if len(res.Tags) != 0 {
		t.Errorf("tags = %v, want empty", res.Tags)
	}
	if !slices.Equal(res.Removed, []string{"agents", "ai"}) {
		t.Errorf("removed = %v, want [agents ai]", res.Removed)
	}

	stored, err := st.GetFeed(ctx, url)
	if err != nil {
		t.Fatalf("GetFeed: %v", err)
	}
	if len(stored.Tags) != 0 {
		t.Errorf("stored tags = %v, want empty", stored.Tags)
	}
}

// TestTagMutuallyExclusiveFlags covers behavior 7: --clear and --set each
// exclude every other write flag, as usage errors.
func TestTagMutuallyExclusiveFlags(t *testing.T) {
	app, st, _ := newTestApp(t)
	seedTagged(t, st, "https://a.example/feed.xml")

	cases := map[string]struct {
		req  feedwatch.TagRequest
		want string
	}{
		"clear with add":    {feedwatch.TagRequest{Ref: "https://a.example/feed.xml", Clear: true, Add: []string{"ai"}}, "--clear cannot be combined with --add, --remove, or --set"},
		"clear with remove": {feedwatch.TagRequest{Ref: "https://a.example/feed.xml", Clear: true, Remove: []string{"ai"}}, "--clear cannot be combined with --add, --remove, or --set"},
		"clear with set":    {feedwatch.TagRequest{Ref: "https://a.example/feed.xml", Clear: true, Set: []string{"ai"}}, "--clear cannot be combined with --add, --remove, or --set"},
		"set with add":      {feedwatch.TagRequest{Ref: "https://a.example/feed.xml", Set: []string{"a"}, Add: []string{"b"}}, "--set cannot be combined with --add or --remove"},
		"set with remove":   {feedwatch.TagRequest{Ref: "https://a.example/feed.xml", Set: []string{"a"}, Remove: []string{"b"}}, "--set cannot be combined with --add or --remove"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := app.Tag(context.Background(), tc.req)
			wantUsageError(t, err, tc.want)
		})
	}
}

// TestTagInvalidTagNames covers behavior 8: an unstorable tag name is a usage
// error wherever it appears, checked before the feed is even resolved.
func TestTagInvalidTagNames(t *testing.T) {
	app, st, _ := newTestApp(t)
	seedTagged(t, st, "https://a.example/feed.xml")

	cases := map[string]struct {
		req  feedwatch.TagRequest
		want string
	}{
		"empty add":       {feedwatch.TagRequest{Ref: "https://a.example/feed.xml", Add: []string{""}}, `tag must not be empty, got ""`},
		"comma in add":    {feedwatch.TagRequest{Ref: "https://a.example/feed.xml", Add: []string{"a,b"}}, `tag must not contain a comma, got "a,b"`},
		"space in add":    {feedwatch.TagRequest{Ref: "https://a.example/feed.xml", Add: []string{"two words"}}, `tag must not contain whitespace, got "two words"`},
		"space in remove": {feedwatch.TagRequest{Ref: "https://a.example/feed.xml", Remove: []string{"two words"}}, `tag must not contain whitespace, got "two words"`},
		"space in set":    {feedwatch.TagRequest{Ref: "https://a.example/feed.xml", Set: []string{"two words"}}, `tag must not contain whitespace, got "two words"`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := app.Tag(context.Background(), tc.req)
			wantUsageError(t, err, tc.want)
		})
	}
}

// TestTagUnknownRefIsUsageError covers behavior 9: an unresolvable ref carries
// the store's usage-category "feed not found", as it does for enable.
func TestTagUnknownRefIsUsageError(t *testing.T) {
	app, _, _ := newTestApp(t)

	_, err := app.Tag(context.Background(), feedwatch.TagRequest{Ref: "nope", Add: []string{"ai"}})
	wantUsageError(t, err, "feed not found")
}

// TestTagResultCollectionsNeverNull covers behavior 10: the envelope's three
// collections serialize as [] even from the zero value, asserted on the raw
// bytes because decoding into a struct hides the difference.
func TestTagResultCollectionsNeverNull(t *testing.T) {
	b, err := json.Marshal(feedwatch.TagResult{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"tags", "added", "removed"} {
		if got := string(m[key]); got != "[]" {
			t.Errorf("%q = %s, want []", key, got)
		}
	}
}
