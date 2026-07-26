package command

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/terr"
)

// A residual context cancellation reaching the error boundary must not be
// rendered as an internal-category error: an interrupt is a graceful stop, and
// main owns its 130/143 exit code. boundaryError classifies it as a timeout so
// it never surfaces as internal_error on stderr. This hardens every store write
// site against leaking "context canceled" as an internal failure.
func TestBoundaryErrorContextCancellationIsNotInternal(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"canceled", context.Canceled},
		{"deadline", context.DeadlineExceeded},
		{"wrapped canceled", fmt.Errorf("persist validators: %w", context.Canceled)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var coded terr.Coded
			if !errors.As(boundaryError(tc.err), &coded) {
				t.Fatalf("boundaryError(%v) is not coded", tc.err)
			}
			if coded.Code() == core.ErrInternal.Code() {
				t.Fatalf("boundaryError(%v) code = internal_error, want non-internal", tc.err)
			}
		})
	}
}

// An explicit coded error in the chain still wins over the context-error
// mapping: boundaryError passes it through unchanged for EmitError to classify.
func TestBoundaryErrorExplicitCodedWins(t *testing.T) {
	want := core.HTTPErr("https://x.example/feed.xml", 503, context.Canceled)
	got := boundaryError(want)
	var coded terr.Coded
	if !errors.As(got, &coded) {
		t.Fatalf("boundaryError returned an uncoded error: %v", got)
	}
	if coded.Code() != core.ErrHTTP.Code() {
		t.Fatalf("boundaryError honored context error over explicit coded error: code = %q", coded.Code())
	}
}
