package command

import (
	"context"

	cliv3 "github.com/urfave/cli/v3"

	"github.com/andreswebs/feedwatch"
)

// addCommand registers the add subcommand: subscribe to an explicit http(s)
// feed URL after validating it actually parses as a feed, with an optional
// alias and minimum poll interval. Adding an already-subscribed URL is an
// idempotent upsert of its alias and interval.
func (d Deps) addCommand() *cliv3.Command {
	return &cliv3.Command{
		Name:      "add",
		Usage:     "subscribe to an explicit feed URL after validating it parses as a feed",
		ArgsUsage: "URL",
		Arguments: argsFor(feedwatch.AddRequest{}),
		Flags:     flagsFor(feedwatch.AddRequest{}),
		Action:    d.addAction,
	}
}

// addAction decodes the flags into an add request and delegates to the library.
// A bad URL, an unfetchable URL, or a body that does not parse as a feed is a
// usage failure (exit 64) that points the agent at discover; a store failure
// propagates to the boundary as a hard error.
func (d Deps) addAction(ctx context.Context, cmd *cliv3.Command) error {
	app, err := d.app(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = app.Close() }()

	var req feedwatch.AddRequest
	if err := bind(cmd, &req); err != nil {
		return err
	}

	res, err := app.Add(ctx, req)
	if err != nil {
		return err
	}
	return rendererFrom(ctx).Result(res)
}
