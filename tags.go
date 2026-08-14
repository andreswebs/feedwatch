package feedwatch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/andreswebs/feedwatch/core"
)

// TagsRequest asks for the tag vocabulary. It carries no fields: the command
// reports every tag, and narrowing is a job for list --tag. It exists so a
// frontend projects tags exactly like every other use case.
type TagsRequest struct{}

// Validate reports whether the request is usable. It has no fields, so it
// always succeeds; the method exists so every request type is validated
// uniformly by a frontend.
func (r TagsRequest) Validate() error { return nil }

// TagsResult is the tags result envelope: every distinct tag with the number of
// subscriptions carrying it, sorted by tag.
type TagsResult struct {
	Head
	Tags []core.TagCount `json:"tags"`
}

// MarshalJSON coalesces tags so it always serializes as [] rather than null.
func (r TagsResult) MarshalJSON() ([]byte, error) {
	type alias TagsResult
	a := alias(r)
	if a.Tags == nil {
		a.Tags = []core.TagCount{}
	}
	return json.Marshal(a)
}

// Tags reports the lane vocabulary: every distinct tag in use with the number
// of subscriptions carrying it, sorted by tag. Feeds of any status are counted,
// so a lane whose feeds have all been disabled stays discoverable; an agent that
// wants the active-only count filters with list --tag. A store carrying no tags
// yields an empty list rather than an error.
func (a *App) Tags(ctx context.Context, req TagsRequest) (TagsResult, error) {
	if err := req.Validate(); err != nil {
		return TagsResult{}, err
	}
	st, err := a.resolveStore(ctx)
	if err != nil {
		return TagsResult{}, err
	}

	counts, err := st.TagCounts(ctx)
	if err != nil {
		return TagsResult{}, err
	}
	return TagsResult{Head: OKHead(), Tags: counts}, nil
}

// RenderText is the optional human-text projection of the same values: the
// vocabulary as an aligned two-column table, following ListResult.RenderText.
func (r TagsResult) RenderText(w io.Writer, _ bool) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "TAG\tFEEDS"); err != nil {
		return err
	}
	for _, c := range r.Tags {
		if _, err := fmt.Fprintf(tw, "%s\t%d\n", c.Tag, c.Feeds); err != nil {
			return err
		}
	}
	return tw.Flush()
}
