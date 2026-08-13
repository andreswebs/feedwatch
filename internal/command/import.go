package command

import (
	"context"
	"io"
	"os"

	cliv3 "github.com/urfave/cli/v3"

	"github.com/andreswebs/feedwatch"
	"github.com/andreswebs/feedwatch/core"
)

// importCommand registers the import subcommand: add subscriptions from an OPML
// outline read from a file or stdin. The projected surface is empty, since the
// request carries the document as bytes rather than a path, so the file argument
// and the negative --no-validate flag are the frontend's own: reading the source
// is the CLI's job, and inverting a boolean in a tag would be the first step
// toward a validation mini-language (ADR 0007).
func (d Deps) importCommand() *cliv3.Command {
	return &cliv3.Command{
		Name:      "import",
		Usage:     "add subscriptions from an OPML outline read from a file or stdin",
		ArgsUsage: "FILE|-",
		Arguments: append(argsFor(feedwatch.ImportRequest{}),
			&cliv3.StringArg{Name: "file"}),
		Flags: append(flagsFor(feedwatch.ImportRequest{}),
			&cliv3.BoolFlag{
				Name:  "no-validate",
				Usage: "subscribe without fetching each feed (fast bulk-add; a successful import does not imply reachability)",
			}),
		Action: d.importAction,
	}
}

// importAction reads the OPML source (the file argument, or stdin when it is
// "-"), hands the document to the library, and renders the per-entry result.
// Reading the source is the frontend's job, so a missing argument and an
// unopenable or unreadable file are usage failures (exit 64) raised here; a
// document that is not OPML is a usage failure from the library, and a store
// failure propagates to the boundary. The CLI flag is the negative
// --no-validate, so the request's positive Validate is its inverse: validation
// stays the default.
func (d Deps) importAction(ctx context.Context, cmd *cliv3.Command) error {
	src, closeSrc, err := d.importSource(cmd.StringArg("file"))
	if err != nil {
		return err
	}
	defer closeSrc()

	doc, err := io.ReadAll(src)
	if err != nil {
		return &core.FeedError{
			Category: core.CatUsage,
			Message:  "cannot read the OPML source",
			Err:      core.ErrUsage,
		}
	}

	app, err := d.app(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = app.Close() }()

	res, err := app.Import(ctx, feedwatch.ImportRequest{
		OPML:     doc,
		Validate: !cmd.Bool("no-validate"),
	})
	if err != nil {
		return err
	}
	return rendererFrom(ctx).Result(res)
}

// importSource opens the OPML source: stdin when the argument is "-", otherwise
// the named file. An empty argument or a file that cannot be opened is a usage
// failure. The returned closer is a no-op for stdin.
func (d Deps) importSource(arg string) (io.Reader, func(), error) {
	if arg == "" {
		return nil, func() {}, &core.FeedError{
			Category: core.CatUsage,
			Message:  "import requires a file path or '-' to read OPML from stdin",
			Err:      core.ErrUsage,
		}
	}

	if arg == "-" {
		in := d.In
		if in == nil {
			in = os.Stdin
		}
		return in, func() {}, nil
	}

	f, err := os.Open(arg) //nolint:gosec // G304: operator-supplied OPML path, not network/item input
	if err != nil {
		return nil, func() {}, &core.FeedError{
			Category: core.CatUsage,
			Message:  "cannot open OPML file " + arg,
			Err:      core.ErrUsage,
		}
	}
	return f, func() { _ = f.Close() }, nil
}
