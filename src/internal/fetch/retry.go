package fetch

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/andreswebs/feedwatch/internal/core"
)

// isTransient reports whether a fetch outcome may succeed on a retry within the
// same invocation. A 5xx or 429 status is transient; every other status is
// deterministic. An error is transient only when it is a timeout or a genuine
// transport failure (one wrapping a net.Error); a parse error or an
// SSRF-blocked redirect (a network FeedError with no transport cause) is not.
func isTransient(status int, err error) bool {
	if err != nil {
		var fe *core.FeedError
		if !errors.As(err, &fe) {
			return false
		}
		switch fe.Category {
		case core.CatTimeout:
			return true
		case core.CatNetwork:
			var ne net.Error
			return errors.As(fe.Err, &ne)
		default:
			return false
		}
	}
	if status == http.StatusTooManyRequests {
		return true
	}
	return status >= 500 && status <= 599
}

// parseRetryAfter interprets a Retry-After header value, supporting both the
// delta-seconds and HTTP-date forms. It returns 0 for an absent, malformed, or
// past value so the caller falls back to its fixed backoff.
func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if secs, err := strconv.Atoi(value); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(value); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

// sleepContext waits for d or until ctx is done, whichever comes first. This is
// what caps a retry delay (including a large Retry-After) by the overall
// deadline: when ctx expires first it returns the context error.
func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
