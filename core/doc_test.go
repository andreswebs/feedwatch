package core_test

import (
	"testing"
	"time"

	"github.com/andreswebs/feedwatch/core"
)

// TestPublicSurfaceIsReachable pins the invariant that the types every public
// signature is written in terms of are constructible from outside the package.
// A compile failure here means a later change pushed part of the surface back
// out of reach of an external embedder.
func TestPublicSurfaceIsReachable(t *testing.T) {
	pf := core.ParsedFeed{
		Title: "Blog",
		TTL:   time.Hour,
		Items: []core.Item{{FeedURL: "https://blog.example/feed.xml", Title: "Item"}},
	}
	if pf.Title != "Blog" || len(pf.Items) != 1 {
		t.Errorf("ParsedFeed = %+v, want one item titled Blog", pf)
	}

	c := core.Candidate{
		Title:  "Blog",
		URL:    "https://blog.example/feed.xml",
		Type:   "application/atom+xml",
		Source: core.SourceAutodiscovery,
	}
	if c.Source != "autodiscovery" || core.SourceProbe != "probe" {
		t.Errorf("Candidate = %+v, probe source = %q", c, core.SourceProbe)
	}
}
