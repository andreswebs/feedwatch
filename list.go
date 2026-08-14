package feedwatch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/andreswebs/feedwatch/core"
)

// ListRequest selects the subscriptions to report. An empty Tags reports every
// subscription; naming tags narrows to a lane, combined per Match. A lane is a
// property of the subscription, so it is filtered here rather than by the items
// use case, which narrows over item history instead.
type ListRequest struct {
	Tags  []string `flag:"tag" usage:"tag to filter by (repeatable); all feeds when omitted"`
	Match string   `flag:"match" default:"all" usage:"multi-tag semantics: 'all' (default) or 'any'"`
}

// Validate reports whether the request's tag selection is usable, returning a
// usage-category error naming the first problem. It resolves the request into a
// filter and discards it, so the rules are stated once in filter and validation
// cannot drift from resolution.
func (r ListRequest) Validate() error {
	_, err := r.filter()
	return err
}

// filter resolves the request into the store filter that narrows the listing.
func (r ListRequest) filter() (core.ListFilter, error) {
	return tagFilter(r.Tags, r.Match)
}

// ListResult is the list result envelope: one view per subscription.
type ListResult struct {
	Head
	Feeds []FeedView `json:"feeds"`
}

// MarshalJSON coalesces feeds so it always serializes as [] rather than null.
func (r ListResult) MarshalJSON() ([]byte, error) {
	type alias ListResult
	a := alias(r)
	if a.Feeds == nil {
		a.Feeds = []FeedView{}
	}
	return json.Marshal(a)
}

// FeedView is the agent-facing summary of one subscription: its canonical URL,
// optional alias, lane tags, lifecycle status, consecutive failure count, and
// last error. Tags are part of the contract rather than an optional extra, so
// the key is always present and an agent never has to tell "no tags" apart from
// a binary that predates them.
type FeedView struct {
	URL       string   `json:"url"`
	Alias     string   `json:"alias,omitempty"`
	Interval  string   `json:"interval,omitempty"`
	Tags      []string `json:"tags"`
	Status    string   `json:"status"`
	Failures  int      `json:"failures"`
	LastError string   `json:"last_error,omitempty"`
}

// MarshalJSON coalesces tags so it always serializes as [] rather than null.
// The view is nested in the list, enable, and disable envelopes, so the rule
// lives here rather than in each parent.
func (v FeedView) MarshalJSON() ([]byte, error) {
	type alias FeedView
	a := alias(v)
	if a.Tags == nil {
		a.Tags = []string{}
	}
	return json.Marshal(a)
}

// feedView projects a stored feed onto its agent-facing view. A zero interval
// means "use the configured default" and is reported as absent rather than as a
// literal zero duration.
func feedView(f core.Feed) FeedView {
	v := FeedView{
		URL:       f.URL,
		Alias:     f.Alias,
		Tags:      f.Tags,
		Status:    string(f.Status),
		Failures:  f.FailureCount,
		LastError: f.LastError,
	}
	if f.Interval > 0 {
		v.Interval = f.Interval.String()
	}
	return v
}

// List reports the subscriptions the request selects with their status, alias,
// tags, failure count, and last error. An empty store, or a lane no feed
// carries, yields an empty list rather than an error.
func (a *App) List(ctx context.Context, req ListRequest) (ListResult, error) {
	filter, err := req.filter()
	if err != nil {
		return ListResult{}, err
	}
	st, err := a.resolveStore(ctx)
	if err != nil {
		return ListResult{}, err
	}

	feeds, err := st.ListFeeds(ctx, filter)
	if err != nil {
		return ListResult{}, err
	}

	res := ListResult{Head: OKHead(), Feeds: make([]FeedView, 0, len(feeds))}
	for _, f := range feeds {
		res.Feeds = append(res.Feeds, feedView(f))
	}
	return res, nil
}

// RenderText is the optional human-text projection of the same values: the
// subscriptions as an aligned table. A frontend may ignore it and read the
// struct fields directly. A dash stands in for an absent alias, interval, tag
// set, or last error so every column is present; status carries its own word, so no
// color is needed to convey meaning.
func (r ListResult) RenderText(w io.Writer, _ bool) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "URL\tALIAS\tINTERVAL\tTAGS\tSTATUS\tFAILURES\tLAST ERROR"); err != nil {
		return err
	}
	for _, f := range r.Feeds {
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%d\t%s\n",
			f.URL, dashIfEmpty(f.Alias), dashIfEmpty(f.Interval), dashIfEmpty(strings.Join(f.Tags, ", ")),
			f.Status, f.Failures, dashIfEmpty(f.LastError)); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// dashIfEmpty renders an absent optional column as a dash so a text table stays
// rectangular and an empty value is visibly distinct from a missing column.
func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
