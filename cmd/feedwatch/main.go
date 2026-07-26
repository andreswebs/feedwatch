// Command feedwatch is the agent-first CLI for watching RSS and Atom feeds.
package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/andreswebs/feedwatch/internal/command"
	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/version"
)

func main() {
	// Capture SIGINT and SIGTERM into a channel and hand it to Run, which cancels
	// in-flight work and overrides the exit code with 128+signum (SIGINT -> 130,
	// SIGTERM -> 143). main touches only the process environment; Run owns the
	// exit boundary (ADR 0003).
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	os.Exit(command.Run(os.Args[1:], command.Deps{
		In:      os.Stdin,
		Out:     os.Stdout,
		Err:     os.Stderr,
		Clock:   core.SystemClock,
		Version: version.Current(),
		Signal:  sigCh,
	}))
}
