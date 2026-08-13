package feedwatch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/andreswebs/feedwatch/core"
)

// ListRequest selects the subscriptions to report. It carries no filters today:
// list reports every subscription, and narrowing it is a query concern the items
// use case already covers.
type ListRequest struct{}

// Validate reports whether the request is usable. A ListRequest has no fields to
// check, so it always succeeds; the method exists so every request type is
// validated uniformly by a frontend.
func (r ListRequest) Validate() error { return nil }

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
// optional alias, lifecycle status, consecutive failure count, and last error.
type FeedView struct {
	URL       string `json:"url"`
	Alias     string `json:"alias,omitempty"`
	Interval  string `json:"interval,omitempty"`
	Status    string `json:"status"`
	Failures  int    `json:"failures"`
	LastError string `json:"last_error,omitempty"`
}

// feedView projects a stored feed onto its agent-facing view. A zero interval
// means "use the configured default" and is reported as absent rather than as a
// literal zero duration.
func feedView(f core.Feed) FeedView {
	v := FeedView{
		URL:       f.URL,
		Alias:     f.Alias,
		Status:    string(f.Status),
		Failures:  f.FailureCount,
		LastError: f.LastError,
	}
	if f.Interval > 0 {
		v.Interval = f.Interval.String()
	}
	return v
}

// List reports every subscription with its status, alias, failure count, and
// last error. An empty store yields an empty list, not an error.
func (a *App) List(ctx context.Context, req ListRequest) (ListResult, error) {
	if err := req.Validate(); err != nil {
		return ListResult{}, err
	}
	st, err := a.resolveStore(ctx)
	if err != nil {
		return ListResult{}, err
	}

	feeds, err := st.ListFeeds(ctx, core.ListFilter{})
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
// struct fields directly. A dash stands in for an absent alias, interval, or
// last error so every column is present; status carries its own word, so no
// color is needed to convey meaning.
func (r ListResult) RenderText(w io.Writer, _ bool) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "URL\tALIAS\tINTERVAL\tSTATUS\tFAILURES\tLAST ERROR"); err != nil {
		return err
	}
	for _, f := range r.Feeds {
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\n",
			f.URL, dashIfEmpty(f.Alias), dashIfEmpty(f.Interval), f.Status, f.Failures, dashIfEmpty(f.LastError)); err != nil {
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
