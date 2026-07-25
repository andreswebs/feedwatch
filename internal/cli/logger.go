package cli

import (
	"io"
	"log/slog"
)

// NewLogger builds the structured logger handed explicitly to components.
//
// Output always goes to w (the cli layer passes os.Stderr); logs never touch
// stdout, so a result stream piped into jq stays clean. When format is "text" a
// human-friendly text handler is used; any other value selects the JSON handler
// that is the default contract. quiet raises the effective floor to errors only,
// overriding level; otherwise level is honored as given.
func NewLogger(w io.Writer, format string, level slog.Level, quiet bool) *slog.Logger {
	if quiet {
		level = slog.LevelError
	}

	opts := &slog.HandlerOptions{Level: level}

	var handler slog.Handler
	if format == "text" {
		handler = slog.NewTextHandler(w, opts)
	} else {
		handler = slog.NewJSONHandler(w, opts)
	}

	return slog.New(handler)
}
