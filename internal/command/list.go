package command

import (
	"context"

	cliv3 "github.com/urfave/cli/v3"

	"github.com/andreswebs/feedwatch"
)

// listCommand registers the list subcommand: report every subscription with its
// status, alias, failure count, and last error.
func (d Deps) listCommand() *cliv3.Command {
	return &cliv3.Command{
		Name:      "list",
		Usage:     "list subscriptions with status, alias, failure count, and last error",
		Flags:     flagsFor(feedwatch.ListRequest{}),
		Arguments: argsFor(feedwatch.ListRequest{}),
		Action:    d.listAction,
	}
}

// listAction delegates to the library's list use case and renders its envelope.
// An empty store yields an empty list. A store failure propagates to the
// boundary as a hard error (exit 69, store unavailable).
func (d Deps) listAction(ctx context.Context, _ *cliv3.Command) error {
	app, err := d.app(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = app.Close() }()

	res, err := app.List(ctx, feedwatch.ListRequest{})
	if err != nil {
		return err
	}
	return rendererFrom(ctx).Result(res)
}
