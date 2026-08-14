package command

import (
	"context"
	"fmt"

	cliv3 "github.com/urfave/cli/v3"

	"github.com/andreswebs/feedwatch"
	"github.com/andreswebs/feedwatch/core"
)

// tagsCommand registers the tags subcommand: report the lane vocabulary, every
// tag in use with the number of subscriptions carrying it. It is the read half
// of tag: tags lists which lanes exist, --tag on other commands filters by one.
func (d Deps) tagsCommand() *cliv3.Command {
	return &cliv3.Command{
		Name:      "tags",
		Usage:     "list every tag in use with the number of feeds carrying it",
		Arguments: argsFor(feedwatch.TagsRequest{}),
		Flags:     flagsFor(feedwatch.TagsRequest{}),
		Action:    d.tagsAction,
	}
}

// tagsAction delegates to the library's tags use case and renders its envelope.
// The request carries no fields today, so bind is a no-op; it is called anyway
// so a field added later is actually parsed rather than silently discarded. A
// store failure propagates to the boundary as a hard error (exit 69).
func (d Deps) tagsAction(ctx context.Context, cmd *cliv3.Command) error {
	app, err := d.app(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = app.Close() }()

	if arg := cmd.Args().First(); arg != "" {
		return &core.FeedError{
			Category: core.CatUsage,
			Message: fmt.Sprintf("tags takes no arguments, got %q; "+
				"run 'feedwatch list --tag %s' to list the feeds in a lane", arg, arg),
			Err: core.ErrUsage,
		}
	}

	var req feedwatch.TagsRequest
	if err := bind(cmd, &req); err != nil {
		return err
	}

	res, err := app.Tags(ctx, req)
	if err != nil {
		return err
	}
	return rendererFrom(ctx).Result(res)
}
