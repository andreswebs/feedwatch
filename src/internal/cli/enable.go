package cli

import (
	"context"

	cliv3 "github.com/urfave/cli/v3"

	"github.com/andreswebs/feedwatch/internal/core"
)

// EnableResult is the enable stdout envelope: the feed's state after it has been
// re-enabled and its failure lifecycle reset.
type EnableResult struct {
	Feed FeedView `json:"feed"`
}

// enableCommand registers the enable subcommand: re-enable an auto-disabled or
// manually disabled feed, resetting its failure lifecycle so it is due again.
func (d Deps) enableCommand() *cliv3.Command {
	return &cliv3.Command{
		Name:      "enable",
		Usage:     "re-enable a disabled feed and reset its failure lifecycle so it is due again",
		ArgsUsage: "URL|ALIAS",
		Arguments: []cliv3.Argument{&cliv3.StringArg{Name: "ref"}},
		Action:    d.enableAction,
	}
}

// enableAction resolves the ref, sets the feed active, and resets its failure
// lifecycle (failure count, last error, and backed-off schedule) so poll treats
// it as due again. Resetting via the store's success path makes enable
// idempotent on an already-active feed. An unknown ref is a usage failure (exit
// 1); a store failure propagates to the boundary as a hard error.
func (d Deps) enableAction(ctx context.Context, cmd *cliv3.Command) error {
	cfg := configFrom(ctx)
	r := rendererFrom(ctx)

	ref := cmd.StringArg("ref")

	rs := newResolver(d, cfg)
	defer rs.Close()
	st, err := rs.Store(ctx)
	if err != nil {
		return err
	}

	feed, err := st.GetFeed(ctx, ref)
	if err != nil {
		return err
	}

	if err := st.SetStatus(ctx, feed.URL, core.FeedActive); err != nil {
		return err
	}

	// Reset the failure lifecycle through the same path a successful poll uses:
	// it clears the failure count, last error, and last-error time. The next-due
	// is set to now so the cleared backoff leaves the feed immediately due rather
	// than waiting out the disabled feed's backed-off schedule.
	now := orSystemClock(d.Clock)()
	if _, err := st.RecordSuccess(ctx, feed.URL, now, now, ""); err != nil {
		return err
	}

	updated, err := st.GetFeed(ctx, feed.URL)
	if err != nil {
		return err
	}
	return r.Result(EnableResult{Feed: FeedView{
		URL:       updated.URL,
		Alias:     updated.Alias,
		Status:    string(updated.Status),
		Failures:  updated.FailureCount,
		LastError: updated.LastError,
	}})
}
