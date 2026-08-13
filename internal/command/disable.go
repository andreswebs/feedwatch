package command

import (
	"context"

	cliv3 "github.com/urfave/cli/v3"

	"github.com/andreswebs/feedwatch"
)

// disableCommand registers the disable subcommand: manually disable a feed so
// poll skips it until it is re-enabled.
func (d Deps) disableCommand() *cliv3.Command {
	return &cliv3.Command{
		Name:      "disable",
		Usage:     "disable a feed so poll skips it until re-enabled",
		ArgsUsage: "URL|ALIAS",
		Arguments: argsFor(feedwatch.DisableRequest{}),
		Flags:     flagsFor(feedwatch.DisableRequest{}),
		Action:    d.disableAction,
	}
}

// disableAction delegates to the library's disable use case and renders its
// envelope. An unknown ref is a usage failure (exit 64); a store failure
// propagates to the boundary as a hard error.
func (d Deps) disableAction(ctx context.Context, cmd *cliv3.Command) error {
	app, err := d.app(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = app.Close() }()

	var req feedwatch.DisableRequest
	if err := bind(cmd, &req); err != nil {
		return err
	}

	res, err := app.Disable(ctx, req)
	if err != nil {
		return err
	}
	return rendererFrom(ctx).Result(res)
}
