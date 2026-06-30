// Command feedwatch is the agent-first CLI for watching RSS and Atom feeds.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	cliv3 "github.com/urfave/cli/v3"

	"github.com/andreswebs/feedwatch/internal/cli"
	"github.com/andreswebs/feedwatch/internal/config"
	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/version"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Capture the signal explicitly rather than via signal.NotifyContext so the
	// actual signal is known: an interrupt is a graceful stop whose exit code is
	// 128+signum (SIGINT -> 130, SIGTERM -> 143), not the error-derived code.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	caught := make(chan os.Signal, 1)
	go func() {
		if s, ok := <-sigCh; ok {
			caught <- s // buffered; recorded before the cancel propagates
			cancel()
		}
	}()

	// The cli boundary exits through cliv3.OsExiter from inside Run (its
	// ExitErrHandler maps a feed-outcome ExitCoder to a code). Wrap it so a
	// caught signal overrides that code with 128+signum: an interrupt is a
	// graceful stop (SIGINT -> 130, SIGTERM -> 143), and the poll layer has
	// already persisted the completed work and written its envelope to stdout.
	// The caught send happens-before the cancel that lets Run unwind into this
	// exiter, so the signal is observable here.
	osExit := cliv3.OsExiter
	cliv3.OsExiter = func(code int) {
		select {
		case s := <-caught:
			if sig, ok := s.(syscall.Signal); ok {
				osExit(128 + int(sig))
				return
			}
		default:
		}
		osExit(code)
	}

	deps := cli.Deps{
		Cfg:     config.Defaults(),
		Clock:   core.SystemClock,
		Version: version.Current(),
		In:      os.Stdin,
		Out:     os.Stdout,
		Err:     os.Stderr,
	}

	err := cli.NewRootCommand(deps).Run(ctx, os.Args)

	// A clean, signalled run (no ExitCoder error, so OsExiter was never called)
	// still owes the 128+signum exit code.
	select {
	case s := <-caught:
		if sig, ok := s.(syscall.Signal); ok {
			os.Exit(128 + int(sig))
		}
	default:
	}
	cliv3.HandleExitCoder(err)
}
