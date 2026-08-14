package feedwatch

import (
	"bytes"
	"context"
	"encoding/json"

	"golang.org/x/sync/errgroup"

	"github.com/andreswebs/feedwatch/core"
	"github.com/andreswebs/feedwatch/internal/opml"
)

// ImportRequest carries the OPML document to subscribe from and whether each
// entry is proven to be a feed first. The document is bytes rather than a path
// or a reader because the library performs no I/O of its own: a CLI supplies a
// file or stdin, a server a request body.
//
// Validate is a field rather than the method every other request type carries,
// which is deliberate: import has nothing to validate syntactically (an
// unparseable document is reported by Import itself), and the flag reads
// naturally on the request. Note the polarity against the CLI's negative
// --no-validate flag, which the frontend inverts.
type ImportRequest struct {
	OPML     []byte `flag:"-"`
	Validate bool   `flag:"-"`
}

// ImportResult is the import result envelope: how many subscriptions were added,
// how many were skipped as already-subscribed duplicates, and the per-entry
// failures that did not abort the import.
type ImportResult struct {
	Head
	Added   int          `json:"added"`
	Skipped int          `json:"skipped"`
	Failed  []ImportFail `json:"failed"`
}

// MarshalJSON coalesces failed so it always serializes as [] rather than null.
func (r ImportResult) MarshalJSON() ([]byte, error) {
	type alias ImportResult
	a := alias(r)
	if a.Failed == nil {
		a.Failed = []ImportFail{}
	}
	return json.Marshal(a)
}

// ImportFail records one OPML entry that could not be imported, identified by
// its feed URL (empty when the entry carried none) and the reason.
type ImportFail struct {
	XMLURL string `json:"xmlUrl"`
	Reason string `json:"reason"`
}

// Import subscribes to every feed in an OPML outline, walking folders at any
// depth. It runs in three phases so concurrency stays confined to the network
// step while dedup and alias decisions remain deterministic: entries are
// classified sequentially against the existing subscriptions and URL syntax,
// the survivors are validated concurrently at the configured concurrency when
// the request asks for it, and the remaining candidates are subscribed
// sequentially so alias assignment is order-stable.
//
// A document that is not OPML is a usage-category failure, and so is a store
// that cannot list its subscriptions, since dedup and alias decisions depend on
// it. Everything else is per-entry result data: a bad entry is recorded in
// Failed and never aborts the import, and one validation failure never cancels
// its siblings.
//
// An outline's category attribute becomes the new subscription's tags. Tags are
// assigned only when the subscription is created, matching add's
// omitted-preserves rule: re-importing a backup leaves an already-subscribed
// feed's lanes as they are, which is why such a feed is reported as skipped.
func (a *App) Import(ctx context.Context, req ImportRequest) (ImportResult, error) {
	feeds, invalid, err := opml.Parse(bytes.NewReader(req.OPML))
	if err != nil {
		return ImportResult{}, usageErr("import source is not a valid OPML document")
	}

	st, err := a.resolveStore(ctx)
	if err != nil {
		return ImportResult{}, err
	}

	var fetcher Fetcher
	var parser Parser
	if req.Validate {
		if fetcher, err = a.resolveFetcher(); err != nil {
			return ImportResult{}, err
		}
		parser = a.resolveParser()
	}

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

	res := ImportResult{Head: OKHead(), Failed: make([]ImportFail, 0, len(invalid))}
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
		candidates = append(candidates, importCandidate{
			url:   feed.XMLURL,
			title: feed.Title,
			tags:  importTags(feed.Tags),
		})
	}

	validationErrs := a.validateCandidates(ctx, candidates, req.Validate, fetcher, parser)

	for i, c := range candidates {
		if validationErrs != nil && validationErrs[i] != nil {
			res.Failed = append(res.Failed, ImportFail{XMLURL: c.url, Reason: validationErrs[i].Error()})
			continue
		}

		alias := ""
		if c.title != "" && !aliases[c.title] {
			alias = c.title
		}

		if _, err := st.AddFeed(ctx, core.Feed{URL: c.url, Alias: alias, Tags: c.tags}); err != nil {
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

// importCandidate is one outline entry that passed dedup and syntax checks and
// is eligible to subscribe, preserving its outline order.
type importCandidate struct {
	url   string
	title string
	tags  []string
}

// importTags keeps the tags an outline's category attribute carried that
// feedwatch can actually store. OPML comes from foreign tools with no notion of
// feedwatch's tag rules, so an unusable name is dropped and its siblings still
// place the feed in their lanes, rather than one bad name failing the outline.
func importTags(tags []string) []string {
	kept := make([]string, 0, len(tags))
	for _, t := range tags {
		if core.ValidateTags([]string{t}) == nil {
			kept = append(kept, t)
		}
	}
	return core.CanonicalTags(kept)
}

// validateCandidates fetches and parses each candidate concurrently, returning a
// position-indexed slice whose entry is non-nil when that candidate failed
// validation. It returns nil when validation is disabled, so callers treat every
// candidate as valid without allocating. Each worker returns nil even on a
// validation failure, so one bad feed never cancels the group.
func (a *App) validateCandidates(ctx context.Context, candidates []importCandidate, validate bool, f Fetcher, p Parser) []error {
	if !validate || len(candidates) == 0 {
		return nil
	}

	errs := make([]error, len(candidates))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(a.cfg.Concurrency)
	for i, c := range candidates {
		g.Go(func() error {
			errs[i] = validateParsesAsFeed(gctx, f, p, c.url)
			return nil
		})
	}
	_ = g.Wait()
	return errs
}
