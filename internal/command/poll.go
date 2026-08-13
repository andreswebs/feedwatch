package command

import (
	"context"

	cliv3 "github.com/urfave/cli/v3"

	"github.com/andreswebs/feedwatch"
)

// pollCommand registers the poll subcommand: fetch the due feeds (or the named
// feeds), report newly-seen items, and update feed state. --force (alias --all)
// overrides scheduling and polls every active feed.
func (d Deps) pollCommand() *cliv3.Command {
	return &cliv3.Command{
		Name:      "poll",
		Usage:     "poll due feeds (or the named feeds) and report new items",
		ArgsUsage: "[FEED...]",
		Arguments: argsFor(feedwatch.PollRequest{}),
		Flags:     flagsFor(feedwatch.PollRequest{}),
		Action:    d.pollAction,
	}
}

// pollAction delegates to the library's poll use case, writes the result
// envelope to stdout, and returns an exitError carrying the outcome-derived exit
// code (2 when all targeted feeds failed, 3 when some did). A hard failure
// propagates to the boundary as a returned error mapping to a sysexits failure
// code (an unreachable store is 69; an unclassified mid-persist write failure is
// 70); an early hard failure leaves stdout empty, while a mid-persist one still
// emits the envelope covering the feeds already persisted.
func (d Deps) pollAction(ctx context.Context, cmd *cliv3.Command) error {
	app, err := d.app(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = app.Close() }()

	var req feedwatch.PollRequest
	if err := bind(cmd, &req); err != nil {
		return err
	}

	r := rendererFrom(ctx)
	res, err := app.Poll(ctx, req)
	if err != nil {
		// A populated result means the failure struck partway through
		// persisting: that work is durable and would otherwise never be
		// reported, so the partial envelope is written before the error
		// propagates. An early hard failure leaves stdout empty.
		if res.Polled > 0 {
			_ = r.Result(res)
		}
		return err
	}

	if err := r.Result(res); err != nil {
		return err
	}

	if len(res.Renamed) > 0 {
		loggerFrom(ctx).InfoContext(ctx, "renamed feeds after permanent redirect",
			"count", len(res.Renamed))
	}

	if code := res.ExitCode(); code != 0 {
		return exitError{code: code}
	}
	return nil
}
