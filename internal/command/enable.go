package command

import (
	"context"

	cliv3 "github.com/urfave/cli/v3"

	"github.com/andreswebs/feedwatch"
)

// enableCommand registers the enable subcommand: re-enable an auto-disabled or
// manually disabled feed, resetting its failure lifecycle so it is due again.
func (d Deps) enableCommand() *cliv3.Command {
	return &cliv3.Command{
		Name:      "enable",
		Usage:     "re-enable a disabled feed and reset its failure lifecycle so it is due again",
		ArgsUsage: "URL|ALIAS",
		Arguments: argsFor(feedwatch.EnableRequest{}),
		Flags:     flagsFor(feedwatch.EnableRequest{}),
		Action:    d.enableAction,
	}
}

// enableAction delegates to the library's enable use case and renders its
// envelope. An unknown ref is a usage failure (exit 64); a store failure
// propagates to the boundary as a hard error.
func (d Deps) enableAction(ctx context.Context, cmd *cliv3.Command) error {
	app, err := d.app(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = app.Close() }()

	var req feedwatch.EnableRequest
	if err := bind(cmd, &req); err != nil {
		return err
	}

	res, err := app.Enable(ctx, req)
	if err != nil {
		return err
	}
	return rendererFrom(ctx).Result(res)
}
