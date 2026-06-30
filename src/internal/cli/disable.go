package cli

import (
	"context"

	cliv3 "github.com/urfave/cli/v3"

	"github.com/andreswebs/feedwatch/internal/core"
)

// DisableResult is the disable stdout envelope: the feed's state after it has
// been manually disabled so poll skips it.
type DisableResult struct {
	Feed FeedView `json:"feed"`
}

// disableCommand registers the disable subcommand: manually disable a feed so
// poll skips it until it is re-enabled.
func (d Deps) disableCommand() *cliv3.Command {
	return &cliv3.Command{
		Name:      "disable",
		Usage:     "disable a feed so poll skips it until re-enabled",
		ArgsUsage: "URL|ALIAS",
		Arguments: []cliv3.Argument{&cliv3.StringArg{Name: "ref"}},
		Action:    d.disableAction,
	}
}

// disableAction resolves the ref and sets the feed disabled so poll's due
// selection excludes it. Unlike auto-disable (driven by the failure lifecycle),
// this is a manual switch and leaves the failure count untouched; the feed is
// re-enabled with enable. Setting an already-disabled feed disabled again is
// idempotent. An unknown ref is a usage failure (exit 1); a store failure
// propagates to the boundary as a hard error.
func (d Deps) disableAction(ctx context.Context, cmd *cliv3.Command) error {
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

	if err := st.SetStatus(ctx, feed.URL, core.FeedDisabled); err != nil {
		return err
	}

	updated, err := st.GetFeed(ctx, feed.URL)
	if err != nil {
		return err
	}
	return r.Result(DisableResult{Feed: FeedView{
		URL:       updated.URL,
		Alias:     updated.Alias,
		Status:    string(updated.Status),
		Failures:  updated.FailureCount,
		LastError: updated.LastError,
	}})
}
