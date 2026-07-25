package fetch

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/andreswebs/feedwatch/internal/core"
)

func TestIsTransientClassification(t *testing.T) {
	netCause := &net.OpError{Op: "read", Err: errors.New("connection reset by peer")}
	tests := []struct {
		name   string
		status int
		err    error
		want   bool
	}{
		{"5xx status", http.StatusBadGateway, nil, true},
		{"503 status", http.StatusServiceUnavailable, nil, true},
		{"429 status", http.StatusTooManyRequests, nil, true},
		{"404 status", http.StatusNotFound, nil, false},
		{"400 status", http.StatusBadRequest, nil, false},
		{"timeout error", 0, core.TimeoutErr("u", context.DeadlineExceeded), true},
		{"network transport error", 0, core.NetworkErr("u", netCause), true},
		{"ssrf block (no transport cause)", 0, &core.FeedError{Category: core.CatNetwork, Message: "blocked"}, false},
		{"parse error", 0, core.ParseErr("u", errors.New("bad xml")), false},
		{"non-feed error", 0, errors.New("boom"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isTransient(tt.status, tt.err); got != tt.want {
				t.Errorf("isTransient(%d, %v) = %v, want %v", tt.status, tt.err, got, tt.want)
			}
		})
	}
}

func TestRetryAfterIsHonored(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte("<rss>ok</rss>"))
	}))
	defer srv.Close()

	f, err := New(WithRetry(2, time.Minute))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var waited []time.Duration
	f.sleep = func(_ context.Context, d time.Duration) error {
		waited = append(waited, d)
		return nil
	}

	res, err := f.Fetch(context.Background(), core.FetchRequest{URL: srv.URL})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if res.Status != http.StatusOK {
		t.Errorf("status = %d, want 200", res.Status)
	}
	if len(waited) != 1 {
		t.Fatalf("waits = %d, want 1", len(waited))
	}
	if waited[0] != time.Second {
		t.Errorf("wait = %s, want 1s (Retry-After honored over the fixed backoff)", waited[0])
	}
}

func TestParseRetryAfter(t *testing.T) {
	tests := []struct {
		in   string
		want time.Duration
	}{
		{"", 0},
		{"1", time.Second},
		{"30", 30 * time.Second},
		{"-5", 0},
		{"garbage", 0},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%q", tt.in), func(t *testing.T) {
			if got := parseRetryAfter(tt.in); got != tt.want {
				t.Errorf("parseRetryAfter(%q) = %s, want %s", tt.in, got, tt.want)
			}
		})
	}
}
