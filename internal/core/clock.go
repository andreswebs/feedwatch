package core

import "time"

// Clock supplies the current time. Components take a Clock rather than calling
// time.Now directly, so polling, backoff, and due calculations are
// deterministic under test.
type Clock func() time.Time

// SystemClock is the production Clock, reading the wall clock.
var SystemClock Clock = time.Now
