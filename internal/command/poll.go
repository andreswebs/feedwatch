package command

import (
	"context"
	"encoding/json"

	cliv3 "github.com/urfave/cli/v3"

	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/output"
	"github.com/andreswebs/feedwatch/internal/poll"
)

// PollFailure is one failed feed in the poll envelope: the feed URL, its error
// category, the HTTP status when the category is http (omitted otherwise), and
// the bare human detail of the failure (always present).
type PollFailure struct {
	FeedURL  string        `json:"feed_url"`
	Category core.Category `json:"category"`
	Status   int           `json:"status,omitempty"`
	Message  string        `json:"message"`
}

// PollResult is the poll stdout envelope: how many feeds were polled, succeeded,
// failed, and skipped, the count of items parsed from the wire (fetched), the
// count of newly-seen items (new_items), the count of wire items that were
// already known (deduped = fetched - new_items), the items themselves, one
// entry per failed feed, and one entry per feed renamed by a permanent redirect.
// failures and renamed are always present, empty ([]) when nothing failed or was
// renamed, so a partial failure or a feed identity change is observable from
// stdout alone; the full per-feed detail (including the human message) is still
// written to stderr.
type PollResult struct {
	output.Head
	Polled    int               `json:"polled"`
	Succeeded int               `json:"succeeded"`
	Failed    int               `json:"failed"`
	Skipped   int               `json:"skipped"`
	Fetched   int               `json:"fetched"`
	NewItems  int               `json:"new_items"`
	Deduped   int               `json:"deduped"`
	Items     []core.Item       `json:"items"`
	Failures  []PollFailure     `json:"failures"`
	Renamed   []core.FeedRename `json:"renamed"`
}

// MarshalJSON coalesces the owned collections so items, failures, and renamed
// always serialize as [] rather than null.
func (r PollResult) MarshalJSON() ([]byte, error) {
	type alias PollResult
	a := alias(r)
	if a.Items == nil {
		a.Items = []core.Item{}
	}
	if a.Failures == nil {
		a.Failures = []PollFailure{}
	}
	if a.Renamed == nil {
		a.Renamed = []core.FeedRename{}
	}
	return json.Marshal(a)
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
// code (2 when all targeted feeds failed, 3 when some did). A hard failure
// propagates to the boundary as a returned error mapping to a sysexits failure
// code (an unreachable store is 69; an unclassified mid-persist write failure is
// 70); an early hard failure leaves stdout empty.
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
		Warn:             r.Warn,
	}

	result, feedErrs, err := poll.Run(ctx, pd, cmd.Args().Slice(), cmd.Bool("force"))
	if err != nil {
		// result.Polled > 0 distinguishes a mid-persist failure (some feeds'
		// writes already committed) from an early hard failure such as an
		// unreachable store, where stdout must stay empty.
		if result.Polled > 0 {
			_ = r.Result(shapePollResult(result, feedErrs))
		}
		return err
	}

	if err := r.Result(shapePollResult(result, feedErrs)); err != nil {
		return err
	}

	if len(result.Renamed) > 0 {
		loggerFrom(ctx).InfoContext(ctx, "renamed feeds after permanent redirect",
			"count", len(result.Renamed))
	}

	if code := result.ExitCode(); code != 0 {
		return exitError{code: code}
	}
	return nil
}

// shapePollResult builds the stdout envelope from a poll outcome and its
// per-feed errors, shared by the success and mid-persist-failure paths so
// they cannot drift.
func shapePollResult(result poll.Result, feedErrs []*core.FeedError) PollResult {
	var failures []PollFailure
	for _, fe := range feedErrs {
		failures = append(failures, PollFailure{
			FeedURL:  fe.FeedURL,
			Category: fe.Category,
			Status:   fe.Status,
			Message:  fe.Detail(),
		})
	}

	return PollResult{
		Head:      output.OKHead(),
		Polled:    result.Polled,
		Succeeded: result.Polled - result.Failed,
		Failed:    result.Failed,
		Skipped:   result.Skipped,
		Fetched:   result.Fetched,
		NewItems:  result.NewItems,
		Deduped:   result.Deduped,
		Items:     result.Items,
		Failures:  failures,
		Renamed:   result.Renamed,
	}
}
