package output_test

import (
	"os"
	"testing"

	"github.com/andreswebs/feedwatch/internal/output"
)

// nonTerminal returns an *os.File backed by a regular file, which is never a
// character device and so never reports as a terminal.
func nonTerminal(t *testing.T) *os.File {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stream")
	if err != nil {
		t.Fatalf("create temp stream: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

// Behavior 4: ResolveColor disables color for json format, the --no-color flag,
// NO_COLOR, TERM=dumb, and a non-terminal stream. The terminal-true path needs a
// real tty and is not asserted here.
func TestResolveColorDisablers(t *testing.T) {
	t.Run("non-text format", func(t *testing.T) {
		if output.ResolveColor(nonTerminal(t), "json", output.ColorPolicy{}) {
			t.Error("color enabled for json format")
		}
	})

	t.Run("no-color flag", func(t *testing.T) {
		if output.ResolveColor(nonTerminal(t), "text", output.ColorPolicy{NoColorFlag: true}) {
			t.Error("color enabled despite --no-color")
		}
	})

	t.Run("NO_COLOR set", func(t *testing.T) {
		t.Setenv("NO_COLOR", "1")
		if output.ResolveColor(nonTerminal(t), "text", output.ColorPolicy{}) {
			t.Error("color enabled despite NO_COLOR")
		}
	})

	t.Run("NO_COLOR empty value still disables", func(t *testing.T) {
		t.Setenv("NO_COLOR", "")
		if output.ResolveColor(nonTerminal(t), "text", output.ColorPolicy{}) {
			t.Error("color enabled despite NO_COLOR present with empty value")
		}
	})

	t.Run("TERM dumb", func(t *testing.T) {
		t.Setenv("TERM", "dumb")
		if output.ResolveColor(nonTerminal(t), "text", output.ColorPolicy{}) {
			t.Error("color enabled despite TERM=dumb")
		}
	})

	t.Run("non-terminal stream", func(t *testing.T) {
		t.Setenv("TERM", "xterm-256color")
		if output.ResolveColor(nonTerminal(t), "text", output.ColorPolicy{}) {
			t.Error("color enabled for a non-terminal stream")
		}
	})
}
