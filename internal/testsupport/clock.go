package testsupport

import (
	"time"

	"github.com/andreswebs/feedwatch/internal/core"
)

// FixedClock returns a core.Clock that always reports t, so backoff, due, and
// date calculations are deterministic under test.
func FixedClock(t time.Time) core.Clock {
	return func() time.Time { return t }
}
