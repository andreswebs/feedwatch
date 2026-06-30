package cli

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"text/tabwriter"

	cliv3 "github.com/urfave/cli/v3"

	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/discover"
)

// DiscoverResult is the discover stdout envelope: the candidate feeds found for a
// URL, each validated by parsing and tagged with how it was found.
type DiscoverResult struct {
	Candidates []discover.Candidate `json:"candidates"`
}

// discoverCommand registers the discover subcommand: a read-only lister of
// candidate feeds for a URL, via rel="alternate" autodiscovery plus a bounded
// probe of common feed paths. It never writes to the store.
func (d Deps) discoverCommand() *cliv3.Command {
	return &cliv3.Command{
		Name:      "discover",
		Usage:     "list candidate feeds autodiscovered or probed from a URL (read-only)",
		ArgsUsage: "URL",
		Arguments: []cliv3.Argument{&cliv3.StringArg{Name: "url"}},
		Action:    d.discoverAction,
	}
}

// discoverAction validates the URL, lists its candidate feeds, and writes the
// result envelope. A bad URL is a usage error (exit 1); a hard fetcher
// construction failure propagates to the boundary. The envelope is always a
// (possibly empty) candidates array, never null.
func (d Deps) discoverAction(ctx context.Context, cmd *cliv3.Command) error {
	cfg := configFrom(ctx)
	r := rendererFrom(ctx)

	pageURL := cmd.StringArg("url")
	if err := validateDiscoverURL(pageURL); err != nil {
		return err
	}

	rs := newResolver(d, cfg)
	defer rs.Close()
	fetcher, err := rs.Fetcher()
	if err != nil {
		return err
	}
	dd := discover.Deps{Fetcher: fetcher, Parser: rs.Parser()}

	candidates, err := discover.Discover(ctx, dd, pageURL)
	if err != nil {
		return err
	}
	return r.Result(DiscoverResult{Candidates: candidates})
}

// validateDiscoverURL rejects anything that is not an absolute http(s) URL so
// discover never tries to fetch a bare host or a non-web scheme.
func validateDiscoverURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return &core.FeedError{
			Category: core.CatUsage,
			Message:  "discover requires an absolute http(s) URL",
			Err:      core.ErrUsage,
		}
	}
	return nil
}

// RenderText writes the candidates as an aligned table under --format text. A
// dash stands in for an absent title or type so every column is present; the
// source word carries its own meaning, so no color is needed.
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
