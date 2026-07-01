package cli

import (
	"context"

	cliv3 "github.com/urfave/cli/v3"

	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/poll"
)

// PollFailure is one failed feed in the poll envelope: the feed URL, its error
// category, and the HTTP status when the category is http (omitted otherwise).
type PollFailure struct {
	FeedURL  string        `json:"feed_url"`
	Category core.Category `json:"category"`
	Status   int           `json:"status,omitempty"`
}

// PollResult is the poll stdout envelope: how many feeds were polled, succeeded,
// failed, and skipped, the count of newly-seen items, the items themselves, one
// entry per failed feed, and one entry per feed renamed by a permanent redirect.
// failures and renamed are always present, empty ([]) when nothing failed or was
// renamed, so a partial failure or a feed identity change is observable from
// stdout alone; the full per-feed detail (including the human message) is still
// written to stderr.
type PollResult struct {
	Polled    int               `json:"polled"`
	Succeeded int               `json:"succeeded"`
	Failed    int               `json:"failed"`
	Skipped   int               `json:"skipped"`
	NewItems  int               `json:"new_items"`
	Items     []core.Item       `json:"items" jsonschema:"opaque"`
	Failures  []PollFailure     `json:"failures"`
	Renamed   []core.FeedRename `json:"renamed"`
}

// pollCommand registers the poll subcommand: fetch the due feeds (or the named
// feeds), report newly-seen items, and update feed state. --force (alias --all)
// overrides scheduling and polls every active feed.
func (d Deps) pollCommand() *cliv3.Command {
	return &cliv3.Command{
		Name:      "poll",
		Usage:     "poll due feeds (or the named feeds) and report new items",
		ArgsUsage: "[FEED...]",
		Flags: []cliv3.Flag{
			&cliv3.BoolFlag{
				Name:    "force",
				Aliases: []string{"all"},
				Usage:   "poll every active feed, ignoring the schedule",
			},
		},
		Action: d.pollAction,
	}
}

// pollAction runs a poll, writes the result envelope to stdout, emits per-feed
// failures to stderr, and returns an exitError carrying the outcome-derived exit
// code (2 when all targeted feeds failed, 3 when some did). A hard failure (an
// unreachable store or a failed write) propagates to the boundary as a returned
// error mapping to exit 1, leaving stdout empty.
func (d Deps) pollAction(ctx context.Context, cmd *cliv3.Command) error {
	cfg := configFrom(ctx)
	r := rendererFrom(ctx)

	rs := newResolver(d, cfg)
	defer rs.Close()
	st, err := rs.Store(ctx)
	if err != nil {
		return err
	}
	fetcher, err := rs.Fetcher()
	if err != nil {
		return err
	}

	pd := poll.Deps{
		Store:            st,
		Fetcher:          fetcher,
		Parser:           rs.Parser(),
		Clock:            orSystemClock(d.Clock),
		Concurrency:      cfg.Concurrency,
		PerHostDelay:     cfg.PerHostDelay,
		DefaultInterval:  cfg.DefaultInterval,
		FailureThreshold: cfg.FailureThreshold,
		MaxBackoff:       cfg.MaxBackoff,
	}

	result, feedErrs, err := poll.Run(ctx, pd, cmd.Args().Slice(), cmd.Bool("force"))
	if err != nil {
		return err
	}

	failures := make([]PollFailure, 0, len(feedErrs))
	for _, fe := range feedErrs {
		failures = append(failures, PollFailure{
			FeedURL:  fe.FeedURL,
			Category: fe.Category,
			Status:   fe.Status,
		})
	}

	renamed := make([]core.FeedRename, 0, len(result.Renamed))
	renamed = append(renamed, result.Renamed...)

	if err := r.Result(PollResult{
		Polled:    result.Polled,
		Succeeded: result.Polled - result.Failed,
		Failed:    result.Failed,
		Skipped:   result.Skipped,
		NewItems:  result.NewItems,
		Items:     result.Items,
		Failures:  failures,
		Renamed:   renamed,
	}); err != nil {
		return err
	}

	if len(renamed) > 0 {
		loggerFrom(ctx).InfoContext(ctx, "renamed feeds after permanent redirect",
			"count", len(renamed))
	}

	if len(feedErrs) > 0 {
		if err := r.Errors(feedErrs); err != nil {
			return err
		}
	}

	if code := result.ExitCode(); code != 0 {
		return exitError{code: code}
	}
	return nil
}
