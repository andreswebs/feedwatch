package command

import (
	"context"

	cliv3 "github.com/urfave/cli/v3"

	"github.com/andreswebs/feedwatch"
)

// pruneCommand registers the prune subcommand: trim stored item history by age
// and/or per-feed count while preserving each item's dedup fingerprint.
func (d Deps) pruneCommand() *cliv3.Command {
	return &cliv3.Command{
		Name:      "prune",
		Usage:     "trim stored item history by age and/or per-feed count, preserving dedup",
		Flags:     flagsFor(feedwatch.PruneRequest{}),
		Arguments: argsFor(feedwatch.PruneRequest{}),
		Action:    d.pruneAction,
	}
}

// pruneAction decodes the flags into a prune request and delegates to the
// library. Each policy field is a pointer written only when its flag was given,
// so an explicit --keep-days 0 (prune everything older than now) stays
// distinguishable from the flag being absent. A bare invocation names no policy
// and is a usage error (exit 64) rather than a silent no-op; a store failure
// propagates to the boundary as a hard error.
func (d Deps) pruneAction(ctx context.Context, cmd *cliv3.Command) error {
	app, err := d.app(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = app.Close() }()

	var req feedwatch.PruneRequest
	if err := bind(cmd, &req); err != nil {
		return err
	}

	res, err := app.Prune(ctx, req)
	if err != nil {
		return err
	}
	return rendererFrom(ctx).Result(res)
}
