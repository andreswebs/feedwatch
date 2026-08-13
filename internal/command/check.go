package command

import (
	"context"

	cliv3 "github.com/urfave/cli/v3"

	"github.com/andreswebs/feedwatch"
)

// checkCommand registers the check subcommand: fetch and parse each active
// feed (or the named feeds) without storing items or updating any state.
func (d Deps) checkCommand() *cliv3.Command {
	return &cliv3.Command{
		Name:      "check",
		Usage:     "validate feed reachability and parseability without storing items or updating state",
		ArgsUsage: "[FEED...]",
		Arguments: argsFor(feedwatch.CheckRequest{}),
		Flags:     flagsFor(feedwatch.CheckRequest{}),
		Action:    d.checkAction,
	}
}

// checkAction delegates to the library's check use case, renders its envelope,
// and returns an exitError carrying the outcome-derived exit code (2 when every
// checked feed failed, 3 when some did). An unknown feed reference is a hard
// error that leaves stdout empty.
func (d Deps) checkAction(ctx context.Context, cmd *cliv3.Command) error {
	app, err := d.app(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = app.Close() }()

	var req feedwatch.CheckRequest
	if err := bind(cmd, &req); err != nil {
		return err
	}

	res, err := app.Check(ctx, req)
	if err != nil {
		return err
	}
	if err := rendererFrom(ctx).Result(res); err != nil {
		return err
	}

	if code := res.ExitCode(); code != 0 {
		return exitError{code: code}
	}
	return nil
}
