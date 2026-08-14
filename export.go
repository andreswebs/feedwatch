package feedwatch

import (
	"context"
	"strings"

	"github.com/andreswebs/feedwatch/core"
	"github.com/andreswebs/feedwatch/internal/opml"
)

// ExportRequest selects the subscriptions to export. An empty Tags exports every
// subscription; naming tags exports one lane, combined per Match, so a lane can
// be handed to another reader on its own.
type ExportRequest struct {
	Tags  []string `flag:"tag" usage:"export only feeds carrying this tag (repeatable); all feeds when omitted"`
	Match string   `flag:"match" default:"all" usage:"multi-tag semantics: 'all' (default) or 'any'"`
}

// Validate reports whether the request's tag selection is usable, returning a
// usage-category error naming the first problem. It resolves the request into a
// filter and discards it, so the rules are stated once in filter.
func (r ExportRequest) Validate() error {
	_, err := r.filter()
	return err
}

// filter resolves the request into the store filter that narrows the export.
func (r ExportRequest) filter() (core.ListFilter, error) {
	return tagFilter(r.Tags, r.Match)
}

// ExportResult carries the OPML 2.0 document listing every subscription. It is
// deliberately not a JSON envelope with a schema head: the document itself is
// the payload, and a frontend writes it verbatim to a file or a stream.
type ExportResult struct {
	OPML string
}

// Export renders the selected subscriptions as an OPML 2.0 document. Each feed
// becomes a type="rss" outline whose label is its alias when set and its URL
// otherwise, carrying its tags in the standard category attribute, so both
// identity and lane round-trip through Import. The library performs no
// filesystem I/O: where the document lands is the frontend's decision.
func (a *App) Export(ctx context.Context, req ExportRequest) (ExportResult, error) {
	filter, err := req.filter()
	if err != nil {
		return ExportResult{}, err
	}
	st, err := a.resolveStore(ctx)
	if err != nil {
		return ExportResult{}, err
	}
	feeds, err := st.ListFeeds(ctx, filter)
	if err != nil {
		return ExportResult{}, err
	}

	outlines := make([]opml.Feed, 0, len(feeds))
	for _, f := range feeds {
		outlines = append(outlines, opml.Feed{XMLURL: f.URL, Title: exportTitle(f), Tags: f.Tags})
	}

	var doc strings.Builder
	if err := opml.Write(&doc, outlines); err != nil {
		return ExportResult{}, err
	}
	return ExportResult{OPML: doc.String()}, nil
}

// exportTitle picks the outline label for a feed: its alias when set, otherwise
// its URL, so every outline carries the OPML-required text without inventing a
// name.
func exportTitle(f core.Feed) string {
	if f.Alias != "" {
		return f.Alias
	}
	return f.URL
}
