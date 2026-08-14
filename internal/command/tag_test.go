package command

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/andreswebs/feedwatch/core"
	"github.com/andreswebs/feedwatch/internal/testsupport"
	"github.com/andreswebs/feedwatch/store"
)

// runTag drives the tag command through the root with an injected store double,
// capturing stdout, stderr, and the exit code the boundary selected.
func runTag(t *testing.T, st store.Store, clk core.Clock, args ...string) runResult {
	t.Helper()

	d := Deps{Clock: clk, Version: "1.2.3", opts: storeOpts(st)}
	return drive(t, d, append([]string{"tag"}, args...)...)
}

// tagEnvelope mirrors the stdout feedwatch.TagResult shape for assertions.
type tagEnvelope struct {
	URL     string   `json:"url"`
	Tags    []string `json:"tags"`
	Added   []string `json:"added"`
	Removed []string `json:"removed"`
}

// seedTagFeed subscribes url carrying tags in a fresh in-memory store.
func seedTagFeed(t *testing.T, url string, tags ...string) (*testsupport.InMemoryStore, core.Clock) {
	t.Helper()

	clk := testsupport.FixedClock(pollFixedTime())
	st := testsupport.NewInMemoryStore(clk)
	if _, err := st.AddFeed(context.Background(), core.Feed{
		URL: url, Alias: "tagged", Status: core.FeedActive, Tags: tags,
	}); err != nil {
		t.Fatalf("AddFeed(%s): %v", url, err)
	}
	return st, clk
}

// decodeTag decodes a successful tag envelope, failing on any other outcome.
func decodeTag(t *testing.T, res runResult) tagEnvelope {
	t.Helper()

	if res.code != 0 {
		t.Fatalf("tag should exit 0, got code %d; stderr=%q", res.code, res.err)
	}
	var env tagEnvelope
	if err := json.Unmarshal([]byte(res.out), &env); err != nil {
		t.Fatalf("stdout is not a tag envelope: %v\ngot: %q", err, res.out)
	}
	return env
}

// TestTagReadsCurrentSet is the tracer: tag with no write flag reports the
// stored set with empty deltas and exits 0.
func TestTagReadsCurrentSet(t *testing.T) {
	url := "https://a.example/feed.xml"
	st, clk := seedTagFeed(t, url, "ai")

	env := decodeTag(t, runTag(t, st, clk, url))

	if env.URL != url {
		t.Errorf("url = %q, want %q", env.URL, url)
	}
	if !slices.Equal(env.Tags, []string{"ai"}) {
		t.Errorf("tags = %v, want [ai]", env.Tags)
	}
	if len(env.Added) != 0 || len(env.Removed) != 0 {
		t.Errorf("read reported added=%v removed=%v, want both empty", env.Added, env.Removed)
	}
}

// TestTagResolvesAlias covers that REF is a URL or a unique alias, as it is for
// enable, disable, and rm.
func TestTagResolvesAlias(t *testing.T) {
	url := "https://a.example/feed.xml"
	st, clk := seedTagFeed(t, url, "ai")

	env := decodeTag(t, runTag(t, st, clk, "tagged"))

	if env.URL != url {
		t.Errorf("url = %q, want the canonical %q", env.URL, url)
	}
}

// TestTagAddAndRemoveThroughCLI covers the edit path end to end, including the
// slice flag's two accepted spellings and the delta the envelope reports.
func TestTagAddAndRemoveThroughCLI(t *testing.T) {
	url := "https://a.example/feed.xml"
	st, clk := seedTagFeed(t, url)

	added := decodeTag(t, runTag(t, st, clk, url, "--add", "AI", "--add", "agents"))
	want := []string{"agents", "ai"}
	if !slices.Equal(added.Tags, want) {
		t.Errorf("tags = %v, want %v", added.Tags, want)
	}
	if !slices.Equal(added.Added, want) {
		t.Errorf("added = %v, want %v", added.Added, want)
	}

	stored, err := st.GetFeed(context.Background(), url)
	if err != nil {
		t.Fatalf("GetFeed: %v", err)
	}
	if !slices.Equal(stored.Tags, want) {
		t.Errorf("stored tags = %v, want %v", stored.Tags, want)
	}

	removed := decodeTag(t, runTag(t, st, clk, url, "--remove", "agents"))
	if !slices.Equal(removed.Tags, []string{"ai"}) {
		t.Errorf("tags = %v, want [ai]", removed.Tags)
	}
	if !slices.Equal(removed.Removed, []string{"agents"}) {
		t.Errorf("removed = %v, want [agents]", removed.Removed)
	}
}

// TestTagSetAcceptsCommaSpelling covers that --set a,b and --set a --set b are
// the same request, since a slice flag splits on commas itself.
func TestTagSetAcceptsCommaSpelling(t *testing.T) {
	url := "https://a.example/feed.xml"
	st, clk := seedTagFeed(t, url, "security")

	env := decodeTag(t, runTag(t, st, clk, url, "--set", "ai,research"))

	if !slices.Equal(env.Tags, []string{"ai", "research"}) {
		t.Errorf("tags = %v, want [ai research]", env.Tags)
	}
	if !slices.Equal(env.Removed, []string{"security"}) {
		t.Errorf("removed = %v, want [security]", env.Removed)
	}
}

// TestTagClearThroughCLI covers --clear: the stored set is emptied and every
// prior tag is reported as removed.
func TestTagClearThroughCLI(t *testing.T) {
	url := "https://a.example/feed.xml"
	st, clk := seedTagFeed(t, url, "ai", "agents")

	env := decodeTag(t, runTag(t, st, clk, url, "--clear"))

	if len(env.Tags) != 0 {
		t.Errorf("tags = %v, want empty", env.Tags)
	}
	if !slices.Equal(env.Removed, []string{"agents", "ai"}) {
		t.Errorf("removed = %v, want [agents ai]", env.Removed)
	}

	stored, err := st.GetFeed(context.Background(), url)
	if err != nil {
		t.Fatalf("GetFeed: %v", err)
	}
	if len(stored.Tags) != 0 {
		t.Errorf("stored tags = %v, want empty", stored.Tags)
	}
}

// TestTagUsageFailures covers the usage half: mutually exclusive write flags,
// unstorable tag names, and an unknown ref all exit 64 with empty stdout and a
// structured error object on stderr.
func TestTagUsageFailures(t *testing.T) {
	url := "https://a.example/feed.xml"

	cases := map[string][]string{
		"clear with add": {url, "--clear", "--add", "ai"},
		"set with add":   {url, "--set", "a", "--add", "b"},
		"empty tag":      {url, "--add", ""},
		"comma tag":      {url, "--add", "a,,b"},
		"whitespace tag": {url, "--add", "two words"},
		"unknown ref":    {"https://missing.example/feed.xml", "--add", "ai"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			st, clk := seedTagFeed(t, url, "ai")

			res := runTag(t, st, clk, args...)

			if res.code != 64 {
				t.Errorf("exit code = %d, want 64 (usage)", res.code)
			}
			if res.out != "" {
				t.Errorf("stdout = %q, want empty on a usage failure", res.out)
			}
			var env errEnvelope
			if err := json.Unmarshal([]byte(res.err), &env); err != nil {
				t.Fatalf("stderr is not a JSON error object: %v\ngot: %q", err, res.err)
			}
			if env.Error.Code != core.ErrUsage.Code() {
				t.Errorf("code = %q, want %q", env.Error.Code, core.ErrUsage.Code())
			}
		})
	}
}

// TestTagEnvelopeCollectionsNeverNull asserts on the raw stdout bytes that the
// three collections are always arrays, which decoding into a struct would hide.
func TestTagEnvelopeCollectionsNeverNull(t *testing.T) {
	url := "https://a.example/feed.xml"
	st, clk := seedTagFeed(t, url)

	res := runTag(t, st, clk, url)
	if res.code != 0 {
		t.Fatalf("tag should exit 0, got %d; stderr=%q", res.code, res.err)
	}
	for _, want := range []string{`"tags":[]`, `"added":[]`, `"removed":[]`} {
		if !strings.Contains(res.out, want) {
			t.Errorf("stdout %q does not contain %s", res.out, want)
		}
	}
}
