package command

import (
	"context"
	"strings"

	cliv3 "github.com/urfave/cli/v3"

	"github.com/andreswebs/feedwatch"
	"github.com/andreswebs/feedwatch/core"
)

// itemsCommand registers the items subcommand: query stored item history with
// feed, time-window, substring, ordering, pagination, and projection filters.
func (d Deps) itemsCommand() *cliv3.Command {
	return &cliv3.Command{
		Name:      "items",
		Usage:     "query stored item history with filters, ordering, and pagination",
		Flags:     itemsFlags(),
		Arguments: argsFor(feedwatch.ItemsRequest{}),
		Action:    d.itemsAction,
	}
}

// itemsAction decodes the flags into an items request, delegates to the library,
// and renders the shape the request asked for. Unparseable --since/--until/--order
// values are usage errors (exit 64, empty stdout); a store failure propagates to
// the boundary.
func (d Deps) itemsAction(ctx context.Context, cmd *cliv3.Command) error {
	app, err := d.app(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = app.Close() }()

	var req feedwatch.ItemsRequest
	if err := bind(cmd, &req); err != nil {
		return err
	}

	res, err := app.Items(ctx, req)
	if err != nil {
		return err
	}

	if res.OmittedNoDate > 0 {
		loggerFrom(ctx).InfoContext(ctx, "excluded items with no publication date",
			"count", res.OmittedNoDate, "axis", "published")
	}
	return rendererFrom(ctx).Result(req.Envelope(res))
}

// itemsFlags projects the items request shape, then supplies the one usage string
// that cannot live in a struct tag: --fields enumerates the projectable field
// names, which are known only at runtime.
func itemsFlags() []cliv3.Flag {
	return withUsage(flagsFor(feedwatch.ItemsRequest{}), "fields",
		"project to a subset of item fields ("+strings.Join(core.ItemFieldNames(), ", ")+"); full item when omitted")
}
