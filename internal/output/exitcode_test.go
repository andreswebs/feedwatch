package output_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/output"
)

// Behavior 8: ExitCodeFor resolves the coded error at the boundary via
// errors.As and returns its exit code, falling back to 70 for anything
// unclassified. The whole-invocation classes map exactly as the ADR 0001
// conformance guard requires (64/65/69/78/70). A feed-scoped FeedError reaching
// the boundary is a bug path that classifies loudly as exit 70 (the
// internal-error class), never a silent 0.
func TestExitCodeFor(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"usage sentinel", core.ErrUsage, 64},
		{"schema too new sentinel", core.ErrSchemaTooNew, 65},
		{"store unavailable sentinel", core.ErrStoreUnavailable, 69},
		{"config sentinel", core.ErrConfig, 78},
		{"internal sentinel", core.ErrInternal, 70},
		{"wrapped usage", fmt.Errorf("x: %w", core.ErrUsage), 64},
		{"wrapped config", fmt.Errorf("x: %w", core.ErrConfig), 78},
		{"wrapped store", fmt.Errorf("x: %w", core.ErrStoreUnavailable), 69},
		{"feed-scoped usage", &core.FeedError{Category: core.CatUsage}, 64},
		{"feed-scoped config", &core.FeedError{Category: core.CatConfig}, 78},
		{"feed-scoped store", &core.FeedError{Category: core.CatStore}, 69},
		{"feed-scoped internal", &core.FeedError{Category: core.CatInternal}, 70},
		{"unclassified fallback", errors.New("boom"), 70},
		{"feed-scoped http", core.HTTPErr("u", 404, errors.New("nf")), 70},
		{"feed-scoped network", core.NetworkErr("u", errors.New("net")), 70},
		{"feed-scoped parse", core.ParseErr("u", errors.New("bad")), 70},
		{"feed-scoped timeout", core.TimeoutErr("u", errors.New("slow")), 70},
		{"wrapped feed-scoped", fmt.Errorf("x: %w", core.NetworkErr("u", errors.New("net"))), 70},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := output.ExitCodeFor(tc.err); got != tc.want {
				t.Errorf("ExitCodeFor(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}
