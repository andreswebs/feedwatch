package output

import "os"

// ColorPolicy carries the color-related inputs that come from flags rather than
// the environment. NoColorFlag is the resolved value of --no-color.
type ColorPolicy struct {
	NoColorFlag bool
}

// ResolveColor reports whether a stream should be colorized. Color is enabled
// only when the format is text, the stream is a terminal, --no-color is unset,
// the NO_COLOR environment variable is absent, and TERM is not "dumb". stdout
// and stderr are evaluated independently by calling this once per stream.
func ResolveColor(stream *os.File, format string, p ColorPolicy) bool {
	if format != "text" {
		return false
	}
	if p.NoColorFlag {
		return false
	}
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		return false
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	return isTerminal(stream)
}

// isTerminal reports whether f refers to a character device, the portable
// stdlib signal that a stream is an interactive terminal. It avoids a cgo or
// third-party dependency for the common TTY check.
func isTerminal(f *os.File) bool {
	if f == nil {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
