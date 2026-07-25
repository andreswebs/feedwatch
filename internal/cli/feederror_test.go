package cli

import (
	"context"
	"fmt"
	"testing"

	"github.com/andreswebs/feedwatch/internal/core"
)

// A residual context cancellation reaching the error boundary must not be
// rendered as an internal-category error: an interrupt is a graceful stop, and
// main owns its 130/143 exit code. This hardens every store write site against
// leaking "context canceled" as an internal failure.
func TestFeedErrorForContextCancellationIsNotInternal(t *testing.T) {
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
			fe := feedErrorFor(tc.err)
			if fe.Category == core.CatInternal {
				t.Fatalf("feedErrorFor(%v).Category = internal, want non-internal", tc.err)
			}
		})
	}
}

// An explicit *FeedError in the chain still wins over the context-error mapping.
func TestFeedErrorForExplicitFeedErrorWins(t *testing.T) {
	want := core.HTTPErr("https://x.example/feed.xml", 503, context.Canceled)
	if got := feedErrorFor(want); got.Category != core.CatHTTP {
		t.Fatalf("feedErrorFor honored context error over explicit FeedError: %v", got.Category)
	}
}
