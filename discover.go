package feedwatch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/andreswebs/feedwatch/core"
	"github.com/andreswebs/feedwatch/internal/discover"
)

// DiscoverRequest names the page to list candidate feeds for. Discovery is
// read-only: it fetches the page and probes its origin, and never touches the
// store.
type DiscoverRequest struct {
	URL string `arg:"url"`
}

// Validate rejects anything that is not an absolute http(s) URL, so discover
// never tries to fetch a bare host or a non-web scheme.
func (r DiscoverRequest) Validate() error {
	if !isAbsoluteHTTPURL(r.URL) {
		return usageErr("discover requires an absolute http(s) URL")
	}
	return nil
}

// DiscoverResult is the discover result envelope: the candidate feeds found for
// a URL, each validated by parsing and tagged with how it was found.
type DiscoverResult struct {
	Head
	Candidates []core.Candidate `json:"candidates"`
}

// MarshalJSON coalesces candidates so it always serializes as [] rather than null.
func (r DiscoverResult) MarshalJSON() ([]byte, error) {
	type alias DiscoverResult
	a := alias(r)
	if a.Candidates == nil {
		a.Candidates = []core.Candidate{}
	}
	return json.Marshal(a)
}

// Discover lists the candidate feeds for a page: the feeds it declares through
// rel="alternate" autodiscovery, then a bounded probe of common feed paths
// against its origin. Every candidate is fetched and parse-validated, so
// non-feeds are dropped, and the returned slice is never nil. Discovery opens no
// store, which is what makes it safe to run against an arbitrary URL before
// subscribing to anything.
func (a *App) Discover(ctx context.Context, req DiscoverRequest) (DiscoverResult, error) {
	if err := req.Validate(); err != nil {
		return DiscoverResult{}, err
	}
	fetcher, err := a.resolveFetcher()
	if err != nil {
		return DiscoverResult{}, err
	}

	candidates, err := discover.Discover(ctx, discover.Deps{
		Fetcher: fetcher,
		Parser:  a.resolveParser(),
	}, req.URL)
	if err != nil {
		return DiscoverResult{}, err
	}
	return DiscoverResult{Head: OKHead(), Candidates: candidates}, nil
}

// RenderText is the optional human-text projection of the same values: the
// candidates as an aligned table. A dash stands in for an absent title or type
// so every column is present; the source word carries its own meaning, so no
// color is needed.
func (r DiscoverResult) RenderText(w io.Writer, _ bool) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "SOURCE\tTYPE\tTITLE\tURL"); err != nil {
		return err
	}
	for _, c := range r.Candidates {
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
			c.Source, dashIfEmpty(c.Type), dashIfEmpty(c.Title), c.URL); err != nil {
			return err
		}
	}
	return tw.Flush()
}
