package feedwatch

import (
	"context"
	"strings"

	"github.com/andreswebs/feedwatch/core"
	"github.com/andreswebs/feedwatch/internal/opml"
)

// ExportRequest selects what to export. Every subscription is exported, so it
// carries no fields; it exists so a frontend projects export exactly like every
// other use case.
type ExportRequest struct{}

// Validate reports whether the request is usable. It has no fields, so it always
// succeeds; the method exists so every request type is validated uniformly by a
// frontend.
func (r ExportRequest) Validate() error { return nil }

// ExportResult carries the OPML 2.0 document listing every subscription. It is
// deliberately not a JSON envelope with a schema head: the document itself is
// the payload, and a frontend writes it verbatim to a file or a stream.
type ExportResult struct {
	OPML string
}

// Export renders every subscription as an OPML 2.0 document. Each feed becomes a
// type="rss" outline whose label is its alias when set and its URL otherwise, so
// the document round-trips through Import. The library performs no filesystem
// I/O: where the document lands is the frontend's decision.
func (a *App) Export(ctx context.Context, req ExportRequest) (ExportResult, error) {
	if err := req.Validate(); err != nil {
		return ExportResult{}, err
	}
	st, err := a.resolveStore(ctx)
	if err != nil {
		return ExportResult{}, err
	}
	feeds, err := st.ListFeeds(ctx, core.ListFilter{})
	if err != nil {
		return ExportResult{}, err
	}

	outlines := make([]opml.Feed, 0, len(feeds))
	for _, f := range feeds {
		outlines = append(outlines, opml.Feed{XMLURL: f.URL, Title: exportTitle(f)})
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
