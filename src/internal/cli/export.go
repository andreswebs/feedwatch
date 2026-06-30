package cli

import (
	"context"
	"io"
	"os"

	cliv3 "github.com/urfave/cli/v3"

	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/opml"
)

// exportCommand registers the export subcommand: serialize the current
// subscriptions as OPML 2.0 to a file or stdout.
func (d Deps) exportCommand() *cliv3.Command {
	return &cliv3.Command{
		Name:  "export",
		Usage: "export subscriptions as OPML 2.0 to a file or stdout",
		Flags: []cliv3.Flag{
			&cliv3.StringFlag{
				Name:      "o",
				Usage:     "write OPML to this file instead of stdout",
				TakesFile: true,
			},
		},
		Action: d.exportAction,
	}
}

// exportAction reads every subscription and writes it as an OPML 2.0 document.
// Each feed becomes a type="rss" outline whose text/title is the alias when set,
// falling back to the URL, so the document round-trips through import. The OPML
// is the result payload: it goes to the -o file when given, otherwise to stdout,
// rather than being wrapped in the JSON envelope. A store failure or an
// unwritable output file propagates to the boundary as a hard error (exit 1).
func (d Deps) exportAction(ctx context.Context, cmd *cliv3.Command) error {
	cfg := configFrom(ctx)
	r := rendererFrom(ctx)

	rs := newResolver(d, cfg)
	defer rs.Close()
	st, err := rs.Store(ctx)
	if err != nil {
		return err
	}

	feeds, err := st.ListFeeds(ctx, core.ListFilter{})
	if err != nil {
		return err
	}

	out := make([]opml.Feed, 0, len(feeds))
	for _, f := range feeds {
		out = append(out, opml.Feed{XMLURL: f.URL, Title: exportTitle(f)})
	}

	w, closeOut, err := exportDest(cmd.String("o"), r.Out)
	if err != nil {
		return err
	}
	defer closeOut()

	if err := opml.Write(w, out); err != nil {
		return err
	}
	return nil
}

// exportTitle picks the outline label for a feed: its alias when set, otherwise
// its URL, so every outline carries the OPML-required text without inventing a
// name.
func exportTitle(f core.Feed) string {
	if f.Alias != "" {
		return f.Alias
	}
	return f.URL
}

// exportDest resolves where the OPML is written: the named file when path is
// non-empty, otherwise the stdout writer. The returned closer closes a file this
// function opened and is a no-op for stdout. An unwritable file is a usage
// failure (exit 1).
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
