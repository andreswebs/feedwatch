package cli

import (
	"context"
	"io"
	"os"

	cliv3 "github.com/urfave/cli/v3"
	"golang.org/x/sync/errgroup"

	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/fetch"
	"github.com/andreswebs/feedwatch/internal/opml"
	"github.com/andreswebs/feedwatch/internal/parse"
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
		Flags: []cliv3.Flag{
			&cliv3.BoolFlag{
				Name:  "no-validate",
				Usage: "subscribe without fetching each feed (fast bulk-add; a successful import does not imply reachability)",
			},
		},
		Action: d.importAction,
	}
}

// importAction reads OPML from the file argument (or stdin when it is "-"),
// walks its outlines recursively, and adds each feed: falling back xmlUrl to
// url, using the outline text or title as the alias when free, skipping
// already-subscribed feeds, and collecting per-entry failures without aborting.
// By default it validates each feed by fetching and parsing it the way add does,
// so a reported add means the feed actually resolves; --no-validate restores the
// fast bulk-add that subscribes without fetching. A missing or unreadable source,
// or one that is not valid OPML, is a hard usage failure (exit 1); a store-open
// failure propagates to the boundary.
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

	validate := !cmd.Bool("no-validate")
	var fetcher fetch.Fetcher
	var parser parse.Parser
	if validate {
		if fetcher, err = rs.Fetcher(); err != nil {
			return err
		}
		parser = rs.Parser()
	}

	res, err := importFeeds(ctx, st, feeds, invalid, importOpts{
		validate:    validate,
		fetcher:     fetcher,
		parser:      parser,
		concurrency: cfg.Concurrency,
	})
	if err != nil {
		return err
	}
	return r.Result(res)
}

// importOpts carries the validation collaborators for importFeeds. When validate
// is false the fetcher and parser are unused and may be nil.
type importOpts struct {
	validate    bool
	fetcher     fetch.Fetcher
	parser      parse.Parser
	concurrency int
}

// importCandidate is one outline entry that passed dedup and syntax checks and
// is eligible to subscribe, preserving its outline order.
type importCandidate struct {
	url   string
	title string
}

// importFeeds adds each parsed feed to the store in three phases so concurrency
// stays confined to the network step while dedup and alias decisions remain
// deterministic. Phase 1 classifies each entry sequentially against the existing
// subscriptions and URL syntax. Phase 2 validates the surviving candidates
// concurrently (only when opts.validate), one failure never cancelling another.
// Phase 3 subscribes the candidates that were not validated away, sequentially,
// so alias assignment is order-stable. It never returns an error for a single bad
// entry; a failure reading the existing subscriptions is a hard error, since the
// dedup and alias decisions depend on it.
func importFeeds(ctx context.Context, st store.Store, feeds []opml.Feed, invalid []opml.Invalid, opts importOpts) (ImportResult, error) {
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

	candidates := make([]importCandidate, 0, len(feeds))
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
		urls[feed.XMLURL] = true // reserve so an OPML-internal duplicate is skipped
		candidates = append(candidates, importCandidate{url: feed.XMLURL, title: feed.Title})
	}

	validationErrs := validateCandidates(ctx, candidates, opts)

	for i, c := range candidates {
		if validationErrs != nil && validationErrs[i] != nil {
			res.Failed = append(res.Failed, ImportFail{XMLURL: c.url, Reason: validationErrs[i].Error()})
			continue
		}

		alias := ""
		if c.title != "" && !aliases[c.title] {
			alias = c.title
		}

		if _, err := st.AddFeed(ctx, core.Feed{URL: c.url, Alias: alias}); err != nil {
			res.Failed = append(res.Failed, ImportFail{XMLURL: c.url, Reason: err.Error()})
			continue
		}

		if alias != "" {
			aliases[alias] = true
		}
		res.Added++
	}

	return res, nil
}

// validateCandidates fetches and parses each candidate concurrently, returning a
// position-indexed slice whose entry is non-nil when that candidate failed
// validation. It returns nil when validation is disabled, so callers treat every
// candidate as valid without allocating. Each worker returns nil even on a
// validation failure, so one bad feed never cancels the group.
func validateCandidates(ctx context.Context, candidates []importCandidate, opts importOpts) []error {
	if !opts.validate || len(candidates) == 0 {
		return nil
	}

	errs := make([]error, len(candidates))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(opts.concurrency)
	for i, c := range candidates {
		g.Go(func() error {
			errs[i] = validateParsesAsFeed(gctx, opts.fetcher, opts.parser, c.url)
			return nil
		})
	}
	_ = g.Wait()
	return errs
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
