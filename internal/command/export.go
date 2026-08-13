package command

import (
	"context"
	"io"
	"os"

	cliv3 "github.com/urfave/cli/v3"

	"github.com/andreswebs/feedwatch"
	"github.com/andreswebs/feedwatch/core"
)

// exportCommand registers the export subcommand: serialize the current
// subscriptions as OPML 2.0 to a file or stdout. -o is hand-declared: the output
// destination is a frontend concern, not a field of the request.
func (d Deps) exportCommand() *cliv3.Command {
	return &cliv3.Command{
		Name:      "export",
		Usage:     "export subscriptions as OPML 2.0 to a file or stdout",
		Arguments: argsFor(feedwatch.ExportRequest{}),
		Flags: append(flagsFor(feedwatch.ExportRequest{}),
			&cliv3.StringFlag{
				Name:      "o",
				Usage:     "write OPML to this file instead of stdout",
				TakesFile: true,
			}),
		Action: d.exportAction,
	}
}

// exportAction delegates to the library and writes the OPML document it returns.
// The document is the result payload, not a JSON envelope: it goes to the -o file
// when given, otherwise to stdout. Choosing the destination is the frontend's
// job, so an unwritable output file is a usage error (exit 64) raised here; a
// store failure (exit 69) propagates from the library to the boundary.
func (d Deps) exportAction(ctx context.Context, cmd *cliv3.Command) error {
	app, err := d.app(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = app.Close() }()

	res, err := app.Export(ctx, feedwatch.ExportRequest{})
	if err != nil {
		return err
	}

	w, closeOut, err := exportDest(cmd.String("o"), rendererFrom(ctx).Out)
	if err != nil {
		return err
	}
	defer closeOut()

	if _, err := io.WriteString(w, res.OPML); err != nil {
		return err
	}
	return nil
}

// exportDest resolves where the OPML is written: the named file when path is
// non-empty, otherwise the stdout writer. The returned closer closes a file this
// function opened and is a no-op for stdout. An unwritable file is a usage
// failure (exit 64).
func exportDest(path string, stdout io.Writer) (io.Writer, func(), error) {
	if path == "" {
		return stdout, func() {}, nil
	}
	f, err := os.Create(path) //nolint:gosec // G304: operator-supplied OPML output path, not network/item input
	if err != nil {
		return nil, func() {}, &core.FeedError{
			Category: core.CatUsage,
			Message:  "cannot create OPML file " + path,
			Err:      core.ErrUsage,
		}
	}
	return f, func() { _ = f.Close() }, nil
}
