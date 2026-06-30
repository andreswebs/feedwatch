package cli

import (
	"context"
	"fmt"
	"io"
	"text/tabwriter"

	cliv3 "github.com/urfave/cli/v3"

	"github.com/andreswebs/feedwatch/internal/core"
)

// ListResult is the list stdout envelope: one view per subscription.
type ListResult struct {
	Feeds []FeedView `json:"feeds"`
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

// listCommand registers the list subcommand: report every subscription with its
// status, alias, failure count, and last error.
func (d Deps) listCommand() *cliv3.Command {
	return &cliv3.Command{
		Name:   "list",
		Usage:  "list subscriptions with status, alias, failure count, and last error",
		Action: d.listAction,
	}
}

// listAction reads every subscription and writes the result envelope. An empty
// store yields an empty list. A store failure propagates to the boundary as a
// hard error (exit 1).
func (d Deps) listAction(ctx context.Context, _ *cliv3.Command) error {
	cfg := configFrom(ctx)
	r := rendererFrom(ctx)

	rs := newResolver(d, cfg)
	defer rs.Close()
	st, err := rs.Store(ctx)
	if err != nil {
		return err
	}

	feeds, err := st.ListFeeds(ctx, core.ListFilter{})
	if err != nil {
		return err
	}

	res := ListResult{Feeds: make([]FeedView, 0, len(feeds))}
	for _, f := range feeds {
		fv := FeedView{
			URL:       f.URL,
			Alias:     f.Alias,
			Status:    string(f.Status),
			Failures:  f.FailureCount,
			LastError: f.LastError,
		}
		if f.Interval > 0 {
			fv.Interval = f.Interval.String()
		}
		res.Feeds = append(res.Feeds, fv)
	}
	return r.Result(res)
}

// RenderText writes the subscriptions as an aligned table under --format text.
// A dash stands in for an absent alias or last error so every column is present.
// Status carries its own word, so no color is needed to convey meaning.
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

// dashIfEmpty renders an absent optional column as a dash so the table stays
// rectangular and an empty value is visibly distinct from a missing column.
func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
