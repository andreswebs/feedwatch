package command

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/andreswebs/feedwatch/core"
	"github.com/andreswebs/feedwatch/internal/testsupport"
	"github.com/andreswebs/feedwatch/store"
)

// runTags drives the tags command through the root with an injected store
// double, capturing stdout, stderr, and the exit code the boundary selected.
func runTags(t *testing.T, st store.Store, clk core.Clock, args ...string) runResult {
	t.Helper()

	d := Deps{Clock: clk, Version: "1.2.3", opts: storeOpts(st)}
	return drive(t, d, append([]string{"tags"}, args...)...)
}

// tagsEnvelope mirrors the stdout feedwatch.TagsResult shape for assertions.
type tagsEnvelope struct {
	Tags []struct {
		Tag   string `json:"tag"`
		Feeds int    `json:"feeds"`
	} `json:"tags"`
}

// seedLanes subscribes the lane fixture the tags command tests share: one feed
// in two lanes, one in a single lane, and one untagged.
func seedLanes(t *testing.T, st *testsupport.InMemoryStore) {
	t.Helper()

	ctx := context.Background()
	feeds := []core.Feed{
		{URL: "https://a.example/feed.xml", Tags: []string{"ai", "agents"}, Status: core.FeedActive},
		{URL: "https://b.example/feed.xml", Tags: []string{"ai"}, Status: core.FeedActive},
		{URL: "https://c.example/feed.xml", Status: core.FeedActive},
	}
	for _, f := range feeds {
		if _, err := st.AddFeed(ctx, f); err != nil {
			t.Fatalf("AddFeed(%s): %v", f.URL, err)
		}
	}
}

// TestTagsReportsLaneVocabulary covers behavior 1: tags reports every distinct
// tag with its feed count, sorted by tag, exit 0.
func TestTagsReportsLaneVocabulary(t *testing.T) {
	clk := testsupport.FixedClock(pollFixedTime())
	st := testsupport.NewInMemoryStore(clk)
	seedLanes(t, st)

	res := runTags(t, st, clk)

	if res.code != 0 {
		t.Errorf("tags should exit 0, got code %d\nstderr: %s", res.code, res.err)
	}
	if res.err != "" {
		t.Errorf("stderr = %q, want empty", res.err)
	}

	var env tagsEnvelope
	if err := json.Unmarshal([]byte(res.out), &env); err != nil {
		t.Fatalf("stdout is not a tags envelope: %v\ngot: %q", err, res.out)
	}
	if len(env.Tags) != 2 {
		t.Fatalf("len(tags) = %d, want 2\ngot: %q", len(env.Tags), res.out)
	}
	if env.Tags[0].Tag != "agents" || env.Tags[0].Feeds != 1 {
		t.Errorf("tags[0] = %+v, want {agents 1}", env.Tags[0])
	}
	if env.Tags[1].Tag != "ai" || env.Tags[1].Feeds != 2 {
		t.Errorf("tags[1] = %+v, want {ai 2}", env.Tags[1])
	}
}

// TestTagsEmptyVocabularyIsAList covers behavior 2: an untagged store reports
// an empty list on stdout, asserted on the raw bytes so a null regression is
// caught rather than absorbed by a decoder.
func TestTagsEmptyVocabularyIsAList(t *testing.T) {
	clk := testsupport.FixedClock(pollFixedTime())
	st := testsupport.NewInMemoryStore(clk)

	res := runTags(t, st, clk)

	if res.code != 0 {
		t.Errorf("tags should exit 0, got code %d\nstderr: %s", res.code, res.err)
	}
	if !strings.Contains(res.out, `"tags":[]`) {
		t.Errorf("stdout = %q, want tags as []", res.out)
	}
}

// TestTagsCountsDisabledFeeds covers behavior 3: tags counts subscriptions of
// any status, so a lane that has gone entirely disabled is still reported.
func TestTagsCountsDisabledFeeds(t *testing.T) {
	clk := testsupport.FixedClock(pollFixedTime())
	st := testsupport.NewInMemoryStore(clk)

	if _, err := st.AddFeed(context.Background(), core.Feed{
		URL: "https://dead.example/feed.xml", Tags: []string{"archive"}, Status: core.FeedDisabled,
	}); err != nil {
		t.Fatalf("AddFeed: %v", err)
	}

	res := runTags(t, st, clk)

	var env tagsEnvelope
	if err := json.Unmarshal([]byte(res.out), &env); err != nil {
		t.Fatalf("stdout is not a tags envelope: %v\ngot: %q", err, res.out)
	}
	if len(env.Tags) != 1 || env.Tags[0].Tag != "archive" || env.Tags[0].Feeds != 1 {
		t.Errorf("tags = %+v, want [{archive 1}]", env.Tags)
	}
}

// TestTagsRendersTextTable covers behavior 4: --format text renders the
// two-column table with its header.
func TestTagsRendersTextTable(t *testing.T) {
	clk := testsupport.FixedClock(pollFixedTime())
	st := testsupport.NewInMemoryStore(clk)
	seedLanes(t, st)

	res := runTags(t, st, clk, "--format", "text")

	if res.code != 0 {
		t.Errorf("tags should exit 0, got code %d\nstderr: %s", res.code, res.err)
	}
	for _, want := range []string{"TAG", "FEEDS", "agents", "ai"} {
		if !strings.Contains(res.out, want) {
			t.Errorf("text output = %q, want it to contain %q", res.out, want)
		}
	}
}

// TestTagsRejectsPositionalArgument covers behavior 5: tags takes no argument,
// and a stray positional is a usage error rather than a silently ignored word.
func TestTagsRejectsPositionalArgument(t *testing.T) {
	clk := testsupport.FixedClock(pollFixedTime())
	st := testsupport.NewInMemoryStore(clk)
	seedLanes(t, st)

	res := runTags(t, st, clk, "extra")

	if res.code != 64 {
		t.Errorf("exit = %d, want 64\nstdout: %s\nstderr: %s", res.code, res.out, res.err)
	}
	if res.out != "" {
		t.Errorf("stdout = %q, want empty on a usage failure", res.out)
	}
}
