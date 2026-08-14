package command

import (
	"context"

	cliv3 "github.com/urfave/cli/v3"

	"github.com/andreswebs/feedwatch"
)

// rmCommand registers the rm subcommand: unsubscribe a feed resolved by its
// exact URL or unique alias, or every feed in a lane, cascading to their stored
// items.
func (d Deps) rmCommand() *cliv3.Command {
	return &cliv3.Command{
		Name:      "rm",
		Usage:     "unsubscribe a feed by URL or unique alias, or a whole lane by tag, removing their stored items",
		ArgsUsage: "URL|ALIAS",
		Arguments: argsFor(feedwatch.RemoveRequest{}),
		Flags:     flagsFor(feedwatch.RemoveRequest{}),
		Action:    d.rmAction,
	}
}

// rmAction delegates to the library's rm use case and renders its envelope. An
// unknown ref, and naming both a feed and a lane or neither, are usage failures
// (exit 64) that remove nothing; a store failure propagates to the boundary as a
// hard error. A lane no feed carries exits 0 having removed nothing.
func (d Deps) rmAction(ctx context.Context, cmd *cliv3.Command) error {
	app, err := d.app(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = app.Close() }()

	var req feedwatch.RemoveRequest
	if err := bind(cmd, &req); err != nil {
		return err
	}

	res, err := app.Remove(ctx, req)
	if err != nil {
		return err
	}
	return rendererFrom(ctx).Result(res)
}
