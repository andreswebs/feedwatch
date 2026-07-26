package command

import (
	"context"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	cliv3 "github.com/urfave/cli/v3"

	"github.com/andreswebs/feedwatch/internal/core"
)

// TestRunSignalOverridesExitCode covers TDD behavior 6: a caught SIGINT or
// SIGTERM makes the boundary return 128+signum regardless of the error the
// interrupted command returned. The signal channel is pre-filled and the stub
// action blocks on context cancellation, so the record-then-cancel ordering in
// watchSignal makes the caught signal deterministically observable to finish.
func TestRunSignalOverridesExitCode(t *testing.T) {
	cases := []struct {
		name string
		sig  syscall.Signal
		want int
	}{
		{"SIGINT", syscall.SIGINT, 130},
		{"SIGTERM", syscall.SIGTERM, 143},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sigCh := make(chan os.Signal, 1)
			sigCh <- tc.sig

			db := filepath.Join(t.TempDir(), "state.db")
			outF, errF := tempFile(t), tempFile(t)
			d := Deps{Clock: core.SystemClock, Version: "1.2.3", Out: outF, Err: errF, Signal: sigCh}

			action := func(ctx context.Context, _ *cliv3.Command) error {
				<-ctx.Done() // unblocks only after watchSignal cancels
				return &core.FeedError{Category: core.CatConfig, Message: "would be 78 without the override"}
			}
			customize := func(root *cliv3.Command) {
				root.Commands = append(root.Commands, &cliv3.Command{Name: "stub", Action: action})
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			caught := watchSignal(cancel, d.Signal)
			r, err := runCustom(ctx, []string{"--db", db, "stub"}, d, customize)
			code := finish(r, err, caught)

			if code != tc.want {
				t.Errorf("exit code = %d, want %d (signal override)", code, tc.want)
			}
		})
	}
}

// TestExitErrorIsNotFrameworkExitCoder pins ADR 0003's requirement that the
// feed-outcome type is a plain error the boundary recognizes with errors.As, not
// a framework ExitCoder the framework would act on.
func TestExitErrorIsNotFrameworkExitCoder(t *testing.T) {
	var err error = exitError{code: 2}
	if _, ok := err.(interface {
		ExitCode() int
		Exit() string
	}); ok {
		t.Error("exitError still implements the cli.ExitCoder shape; the framework must not see it")
	}
}

// TestContractFilesDoNotImportFramework enforces the no-leak rule: the
// framework-free contract files (run.go, exit.go) must never import urfave/cli;
// the framework belongs only to the interior files (root.go and the per-command
// files).
func TestContractFilesDoNotImportFramework(t *testing.T) {
	for _, name := range []string{"run.go", "exit.go"} {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, imp := range f.Imports {
			if strings.Contains(imp.Path.Value, "urfave/cli") {
				t.Errorf("%s imports the framework (%s); the contract must stay framework-free", name, imp.Path.Value)
			}
		}
	}
}
