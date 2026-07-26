package command

import (
	"context"
	"errors"
	"io"
	"os"
	"syscall"

	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/fetch"
	"github.com/andreswebs/feedwatch/internal/output"
	"github.com/andreswebs/feedwatch/internal/parse"
	"github.com/andreswebs/feedwatch/internal/store"
	"github.com/andreswebs/feedwatch/internal/terr"
)

// Deps carries the injected process environment. It is the framework-free
// contract of ADR 0003: main constructs it, Run consumes it. In, Out, and Err
// are the process streams; Clock and Version are the only extra seams a test
// must fake. Signal delivers caught termination signals so Run can override its
// exit code with 128+signum for a graceful stop; main wires it from
// signal.Notify and tests leave it nil.
//
// The store, fetcher, and parser are resolved lazily by resolve.go and are not
// part of the contract; the unexported fields below are the same-package test
// seam that injects fakes, left nil in production.
type Deps struct {
	In      io.Reader
	Out     io.Writer
	Err     io.Writer
	Clock   core.Clock
	Version string
	Signal  <-chan os.Signal

	store store.Store
	fetch fetch.Fetcher
	parse parse.Parser
}

// Run is the single exit boundary (ADR 0003). It builds and runs the command
// tree, emits at most one error envelope to deps.Err, chooses the exit code from
// the coded-error taxonomy (ADR 0001), translates a feed-outcome exitError, and
// applies the signal override. main calls os.Exit on the returned code and never
// inspects errors itself.
func Run(args []string, deps Deps) int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	caught := watchSignal(cancel, deps.Signal)

	r, err := runRoot(ctx, args, deps)
	return finish(r, err, caught)
}

// finish maps a run outcome to an exit code. It emits the classified error
// envelope through the renderer (format-aware, so --format text still yields a
// human line), then lets a caught signal override the code with 128+signum: an
// interrupt is a graceful stop whose code wins over any error the interrupted
// command returned. A feed-outcome exitError is already reported on stdout, so
// it only sets the code and emits nothing.
func finish(r *output.Renderer, err error, caught <-chan os.Signal) int {
	code := 0
	if err != nil {
		var ee exitError
		if errors.As(err, &ee) {
			code = ee.code
		} else {
			e := boundaryError(err)
			_ = r.Error(e)
			code = output.ExitCodeFor(e)
		}
	}

	if sig, ok := caughtSignal(caught); ok {
		return 128 + int(sig)
	}
	return code
}

// watchSignal starts a goroutine that, on the first caught termination signal,
// records it into a buffered channel and cancels the context so in-flight work
// aborts. Recording the signal happens-before the cancel, so once runRoot
// unwinds the recorded signal is observable to finish. It is a no-op when no
// signal channel is wired (the common test case), returning a nil channel that
// caughtSignal reads as "no signal".
func watchSignal(cancel context.CancelFunc, sig <-chan os.Signal) <-chan os.Signal {
	if sig == nil {
		return nil
	}
	caught := make(chan os.Signal, 1)
	go func() {
		if s, ok := <-sig; ok {
			caught <- s // buffered; recorded before the cancel propagates
			cancel()
		}
	}()
	return caught
}

// caughtSignal reports the caught termination signal, if any, without blocking.
func caughtSignal(caught <-chan os.Signal) (syscall.Signal, bool) {
	select {
	case s := <-caught:
		if sig, ok := s.(syscall.Signal); ok {
			return sig, true
		}
	default:
	}
	return 0, false
}

// boundaryError normalizes an error for stderr rendering by the renderer, which
// classifies any terr.Coded error directly. A residual cancellation or deadline
// is a graceful interrupt, not an internal failure (the signal override owns the
// 128+signum exit code), so an uncoded one is wrapped as a timeout-category
// error to keep it from surfacing as internal_error on stderr. Every other error
// is passed through unchanged for the renderer to classify.
func boundaryError(err error) error {
	var coded terr.Coded
	if errors.As(err, &coded) {
		return err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return &core.FeedError{Category: core.CatTimeout, Message: err.Error(), Err: err}
	}
	return err
}
