package cli

import (
	"context"
	"io"
	"os"

	cliv3 "github.com/urfave/cli/v3"

	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/opml"
	"github.com/andreswebs/feedwatch/internal/store"
)

// ImportResult is the import stdout envelope: how many subscriptions were added,
// how many were skipped as already-subscribed duplicates, and the per-entry
// failures that did not abort the import.
type ImportResult struct {
	Added   int          `json:"added"`
	Skipped int          `json:"skipped"`
	Failed  []ImportFail `json:"failed"`
}

// ImportFail records one OPML entry that could not be imported, identified by
// its feed URL (empty when the entry carried none) and the reason.
type ImportFail struct {
	XMLURL string `json:"xmlUrl"`
	Reason string `json:"reason"`
}

// importCommand registers the import subcommand: add subscriptions from an OPML
// outline read from a file or stdin.
func (d Deps) importCommand() *cliv3.Command {
	return &cliv3.Command{
		Name:      "import",
		Usage:     "add subscriptions from an OPML outline read from a file or stdin",
		ArgsUsage: "FILE|-",
		Arguments: []cliv3.Argument{&cliv3.StringArg{Name: "file"}},
		Action:    d.importAction,
	}
}

// importAction reads OPML from the file argument (or stdin when it is "-"),
// walks its outlines recursively, and adds each feed: falling back xmlUrl to
// url, using the outline text or title as the alias when free, skipping
// already-subscribed feeds, and collecting per-entry failures without aborting.
// A missing or unreadable source, or one that is not valid OPML, is a hard
// usage failure (exit 1); a store-open failure propagates to the boundary.
func (d Deps) importAction(ctx context.Context, cmd *cliv3.Command) error {
	cfg := configFrom(ctx)
	r := rendererFrom(ctx)

	src, closeSrc, err := d.importSource(cmd.StringArg("file"))
	if err != nil {
		return err
	}
	defer closeSrc()

	feeds, invalid, err := opml.Parse(src)
	if err != nil {
		return &core.FeedError{
			Category: core.CatUsage,
			Message:  "import source is not a valid OPML document",
			Err:      core.ErrUsage,
		}
	}

	rs := newResolver(d, cfg)
	defer rs.Close()
	st, err := rs.Store(ctx)
	if err != nil {
		return err
	}

	res, err := importFeeds(ctx, st, feeds, invalid)
	if err != nil {
		return err
	}
	return r.Result(res)
}

// importFeeds adds each parsed feed to the store, deduplicating against the
// existing subscriptions and assigning a free alias from the outline title. It
// never returns an error for a single bad entry: a store-level add failure is
// recorded in the result. A failure reading the existing subscriptions is a
// hard error, since the dedup and alias decisions depend on it.
func importFeeds(ctx context.Context, st store.Store, feeds []opml.Feed, invalid []opml.Invalid) (ImportResult, error) {
	existing, err := st.ListFeeds(ctx, core.ListFilter{})
	if err != nil {
		return ImportResult{}, err
	}

	urls := make(map[string]bool, len(existing))
	aliases := make(map[string]bool, len(existing))
	for _, f := range existing {
		urls[f.URL] = true
		if f.Alias != "" {
			aliases[f.Alias] = true
		}
	}

	res := ImportResult{Failed: make([]ImportFail, 0, len(invalid))}
	for _, iv := range invalid {
		res.Failed = append(res.Failed, ImportFail{Reason: iv.Reason})
	}

	for _, feed := range feeds {
		if urls[feed.XMLURL] {
			res.Skipped++
			continue
		}

		if !isAbsoluteHTTPURL(feed.XMLURL) {
			res.Failed = append(res.Failed, ImportFail{
				XMLURL: feed.XMLURL,
				Reason: "outline xmlUrl/url is not an absolute http(s) URL",
			})
			continue
		}

		alias := ""
		if feed.Title != "" && !aliases[feed.Title] {
			alias = feed.Title
		}

		if _, err := st.AddFeed(ctx, core.Feed{URL: feed.XMLURL, Alias: alias}); err != nil {
			res.Failed = append(res.Failed, ImportFail{XMLURL: feed.XMLURL, Reason: err.Error()})
			continue
		}

		urls[feed.XMLURL] = true
		if alias != "" {
			aliases[alias] = true
		}
		res.Added++
	}

	return res, nil
}

// importSource opens the OPML source: stdin when the argument is "-", otherwise
// the named file. An empty argument or a file that cannot be opened is a usage
// failure. The returned closer is a no-op for stdin.
func (d Deps) importSource(arg string) (io.Reader, func(), error) {
	if arg == "" {
		return nil, func() {}, &core.FeedError{
			Category: core.CatUsage,
			Message:  "import requires a file path or '-' to read OPML from stdin",
			Err:      core.ErrUsage,
		}
	}

	if arg == "-" {
		in := d.In
		if in == nil {
			in = os.Stdin
		}
		return in, func() {}, nil
	}

	f, err := os.Open(arg) //nolint:gosec // G304: operator-supplied OPML path, not network/item input
	if err != nil {
		return nil, func() {}, &core.FeedError{
			Category: core.CatUsage,
			Message:  "cannot open OPML file " + arg,
			Err:      core.ErrUsage,
		}
	}
	return f, func() { _ = f.Close() }, nil
}
