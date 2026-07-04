package cli

import (
	"context"
	"errors"

	cliv3 "github.com/urfave/cli/v3"
	"golang.org/x/sync/errgroup"

	"github.com/andreswebs/feedwatch/internal/core"
)

// CheckFailure is one failed feed in the check envelope: the feed URL, its
// error category, the HTTP status when the category is http (omitted
// otherwise), and the human detail of the failure (always present).
type CheckFailure struct {
	FeedURL  string        `json:"feed_url"`
	Category core.Category `json:"category"`
	Status   int           `json:"status,omitempty"`
	Message  string        `json:"message"`
}

// CheckResult is the check stdout envelope: how many feeds were checked, how
// many passed, how many failed, and one entry per failed feed. failures is
// always present, empty ([]) when nothing failed.
type CheckResult struct {
	Checked  int            `json:"checked"`
	OK       int            `json:"ok"`
	Failed   int            `json:"failed"`
	Failures []CheckFailure `json:"failures"`
}

// ExitCode derives the process exit code from the outcome: 0 when nothing was
// checked or every feed passed, 2 when every feed failed, 3 when some passed
// and some failed.
func (r CheckResult) ExitCode() int {
	if r.Checked == 0 || r.Failed == 0 {
		return 0
	}
	if r.Failed == r.Checked {
		return 2
	}
	return 3
}

// checkCommand registers the check subcommand: fetch and parse each active
// feed (or the named feeds) without storing items or updating any state.
func (d Deps) checkCommand() *cliv3.Command {
	return &cliv3.Command{
		Name:      "check",
		Usage:     "validate feed reachability and parseability without storing items or updating state",
		ArgsUsage: "[FEED...]",
		Action:    d.checkAction,
	}
}

// checkAction fetches and parses each targeted feed concurrently with no store
// writes. It writes the result envelope to stdout, emits per-feed failures to
// stderr, and returns an exitError for exit 2 or 3 on failures.
func (d Deps) checkAction(ctx context.Context, cmd *cliv3.Command) error {
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
	parser := rs.Parser()

	// Select target feeds: named refs or all active feeds.
	var feeds []core.Feed
	names := cmd.Args().Slice()
	if len(names) > 0 {
		feeds = make([]core.Feed, 0, len(names))
		for _, ref := range names {
			f, err := st.GetFeed(ctx, ref)
			if err != nil {
				return err
			}
			feeds = append(feeds, f)
		}
	} else {
		feeds, err = st.ListFeeds(ctx, core.ListFilter{Status: core.FeedActive})
		if err != nil {
			return err
		}
	}

	// Fetch and parse each feed concurrently; results into position-indexed slots.
	feedErrs := make([]*core.FeedError, len(feeds))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(cfg.Concurrency)
	for i, f := range feeds {
		g.Go(func() error {
			res, err := fetcher.Fetch(gctx, core.FetchRequest{URL: f.URL})
			if err != nil {
				feedErrs[i] = checkFeedError(f.URL, err)
				return nil
			}
			if _, err := parser.Parse(gctx, res.Body, f.URL); err != nil {
				feedErrs[i] = checkFeedError(f.URL, err)
			}
			return nil
		})
	}
	_ = g.Wait()

	// Build the envelope; failures always present as a list.
	result := CheckResult{
		Checked:  len(feeds),
		Failures: make([]CheckFailure, 0),
	}
	var collectedErrs []*core.FeedError
	for _, fe := range feedErrs {
		if fe != nil {
			result.Failed++
			result.Failures = append(result.Failures, CheckFailure{
				FeedURL:  fe.FeedURL,
				Category: fe.Category,
				Status:   fe.Status,
				Message:  fe.Detail(),
			})
			collectedErrs = append(collectedErrs, fe)
		}
	}
	result.OK = result.Checked - result.Failed

	if err := r.Result(result); err != nil {
		return err
	}

	if len(collectedErrs) > 0 {
		if err := r.Errors(collectedErrs); err != nil {
			return err
		}
	}

	if code := result.ExitCode(); code != 0 {
		return exitError{code: code}
	}
	return nil
}

// checkFeedError classifies a fetch or parse error as a *core.FeedError,
// extracting the existing one from the chain or falling back to network.
func checkFeedError(feedURL string, err error) *core.FeedError {
	var fe *core.FeedError
	if errors.As(err, &fe) {
		return fe
	}
	return core.NetworkErr(feedURL, err)
}
