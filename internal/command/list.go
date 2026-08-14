package command

import (
	"context"

	cliv3 "github.com/urfave/cli/v3"

	"github.com/andreswebs/feedwatch"
)

// listCommand registers the list subcommand: report every subscription, or the
// ones in a lane, with its status, alias, tags, failure count, and last error.
func (d Deps) listCommand() *cliv3.Command {
	return &cliv3.Command{
		Name:      "list",
		Usage:     "list subscriptions with status, alias, tags, failure count, and last error",
		Flags:     flagsFor(feedwatch.ListRequest{}),
		Arguments: argsFor(feedwatch.ListRequest{}),
		Action:    d.listAction,
	}
}

// listAction decodes the tag selection into a list request and delegates to the
// library. An empty store, or a lane no feed carries, yields an empty list; an
// invalid tag name or --match value is a usage error (exit 64), and a store
// failure propagates to the boundary as a hard error (exit 69).
func (d Deps) listAction(ctx context.Context, cmd *cliv3.Command) error {
	app, err := d.app(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = app.Close() }()

	var req feedwatch.ListRequest
	if err := bind(cmd, &req); err != nil {
		return err
	}

	res, err := app.List(ctx, req)
	if err != nil {
		return err
	}
	return rendererFrom(ctx).Result(res)
}
