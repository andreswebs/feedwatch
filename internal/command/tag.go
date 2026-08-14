package command

import (
	"context"

	cliv3 "github.com/urfave/cli/v3"

	"github.com/andreswebs/feedwatch"
)

// tagCommand registers the tag subcommand: read one feed's lane tags, or edit
// them with --add/--remove, --set, or --clear.
func (d Deps) tagCommand() *cliv3.Command {
	return &cliv3.Command{
		Name:      "tag",
		Usage:     "read or edit the tags on a subscription",
		ArgsUsage: "URL|ALIAS",
		Arguments: argsFor(feedwatch.TagRequest{}),
		Flags:     flagsFor(feedwatch.TagRequest{}),
		Action:    d.tagAction,
	}
}

// tagAction delegates to the library's tag use case and renders its envelope.
// An unknown ref, a mutually exclusive pair of write flags, and an unstorable
// tag name are all usage failures (exit 64); a store failure propagates to the
// boundary as a hard error.
func (d Deps) tagAction(ctx context.Context, cmd *cliv3.Command) error {
	app, err := d.app(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = app.Close() }()

	var req feedwatch.TagRequest
	if err := bind(cmd, &req); err != nil {
		return err
	}

	res, err := app.Tag(ctx, req)
	if err != nil {
		return err
	}
	return rendererFrom(ctx).Result(res)
}
