package command

import (
	"context"

	cliv3 "github.com/urfave/cli/v3"

	"github.com/andreswebs/feedwatch"
)

// discoverCommand registers the discover subcommand: a read-only lister of
// candidate feeds for a URL, via rel="alternate" autodiscovery plus a bounded
// probe of common feed paths. It never writes to the store.
func (d Deps) discoverCommand() *cliv3.Command {
	return &cliv3.Command{
		Name:      "discover",
		Usage:     "list candidate feeds autodiscovered or probed from a URL (read-only)",
		ArgsUsage: "URL",
		Arguments: argsFor(feedwatch.DiscoverRequest{}),
		Flags:     flagsFor(feedwatch.DiscoverRequest{}),
		Action:    d.discoverAction,
	}
}

// discoverAction delegates to the library's discover use case and renders its
// envelope. A bad URL is a usage error (exit 64); a hard fetcher construction
// failure propagates to the boundary. The envelope is always a (possibly empty)
// candidates array, never null.
func (d Deps) discoverAction(ctx context.Context, cmd *cliv3.Command) error {
	app, err := d.app(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = app.Close() }()

	var req feedwatch.DiscoverRequest
	if err := bind(cmd, &req); err != nil {
		return err
	}

	res, err := app.Discover(ctx, req)
	if err != nil {
		return err
	}
	return rendererFrom(ctx).Result(res)
}
