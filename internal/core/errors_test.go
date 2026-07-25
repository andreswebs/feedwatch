package core_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/andreswebs/feedwatch/internal/core"
)

// Behavior 1 (tracer): errors.As recovers a *FeedError through a %w-wrapped
// chain, and its Category and Status are readable.
func TestFeedErrorRecoveredThroughWrap(t *testing.T) {
	cause := errors.New("connection reset")
	wrapped := fmt.Errorf("polling failed: %w", core.HTTPErr("https://blog.example/feed.xml", 404, cause))

	var fe *core.FeedError
	if !errors.As(wrapped, &fe) {
		t.Fatal("errors.As did not recover *FeedError through the wrap chain")
	}
	if fe.Category != core.CatHTTP {
		t.Errorf("Category = %q, want %q", fe.Category, core.CatHTTP)
	}
	if fe.Status != 404 {
		t.Errorf("Status = %d, want 404", fe.Status)
	}
	if fe.FeedURL != "https://blog.example/feed.xml" {
		t.Errorf("FeedURL = %q, want the feed url", fe.FeedURL)
	}
	if !errors.Is(wrapped, cause) {
		t.Error("Unwrap chain does not reach the underlying cause")
	}
}

func TestFeedErrorConstructorsSetCategory(t *testing.T) {
	cause := errors.New("boom")
	cases := []struct {
		name string
		err  *core.FeedError
		want core.Category
	}{
		{"network", core.NetworkErr("u", cause), core.CatNetwork},
		{"http", core.HTTPErr("u", 500, cause), core.CatHTTP},
		{"parse", core.ParseErr("u", cause), core.CatParse},
		{"timeout", core.TimeoutErr("u", cause), core.CatTimeout},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.err.Category != tc.want {
				t.Errorf("Category = %q, want %q", tc.err.Category, tc.want)
			}
			if tc.err.FeedURL != "u" {
				t.Errorf("FeedURL = %q, want %q", tc.err.FeedURL, "u")
			}
			if !errors.Is(tc.err, cause) {
				t.Error("constructor did not wrap the cause")
			}
		})
	}
}

// Behavior 2: errors.Is matches each sentinel through wrapping.
func TestSentinelsMatchThroughWrap(t *testing.T) {
	sentinels := []error{
		core.ErrUsage,
		core.ErrConfig,
		core.ErrStoreUnavailable,
		core.ErrSchemaTooNew,
	}
	for _, s := range sentinels {
		wrapped := fmt.Errorf("context: %w", s)
		if !errors.Is(wrapped, s) {
			t.Errorf("errors.Is did not match sentinel %v through wrapping", s)
		}
	}
}

// Behavior 3: ExitCodeFor maps whole-invocation sentinels and FeedError
// categories to the sysexits.h failure classes of ADR 0001, and a purely
// feed-scoped *FeedError to 0; nil maps to 0. No whole-invocation failure
// returns 1.
func TestExitCodeFor(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, 0},
		{"usage sentinel", core.ErrUsage, 64},
		{"config sentinel", core.ErrConfig, 78},
		{"store unavailable sentinel", core.ErrStoreUnavailable, 69},
		{"schema too new sentinel", core.ErrSchemaTooNew, 65},
		{"wrapped usage", fmt.Errorf("x: %w", core.ErrUsage), 64},
		{"wrapped config", fmt.Errorf("x: %w", core.ErrConfig), 78},
		{"wrapped store", fmt.Errorf("x: %w", core.ErrStoreUnavailable), 69},
		{"feed-scoped usage", &core.FeedError{Category: core.CatUsage}, 64},
		{"feed-scoped config", &core.FeedError{Category: core.CatConfig}, 78},
		{"feed-scoped store", &core.FeedError{Category: core.CatStore}, 69},
		{"feed-scoped internal", &core.FeedError{Category: core.CatInternal}, 70},
		{"unclassified fallback", errors.New("boom"), 70},
		{"feed-scoped http", core.HTTPErr("u", 404, errors.New("nf")), 0},
		{"feed-scoped network", core.NetworkErr("u", errors.New("net")), 0},
		{"feed-scoped parse", core.ParseErr("u", errors.New("bad")), 0},
		{"feed-scoped timeout", core.TimeoutErr("u", errors.New("slow")), 0},
		{"wrapped feed-scoped", fmt.Errorf("x: %w", core.NetworkErr("u", errors.New("net"))), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := core.ExitCodeFor(tc.err); got != tc.want {
				t.Errorf("ExitCodeFor(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}

// Behavior 4: Error() strings lead lowercase and carry no trailing punctuation
// (Go style), and include the category, url, and message.
func TestFeedErrorStringStyle(t *testing.T) {
	cases := []*core.FeedError{
		core.HTTPErr("https://blog.example/feed.xml", 404, errors.New("not found")),
		core.NetworkErr("https://blog.example/feed.xml", errors.New("dns: no such host")),
		core.ParseErr("https://blog.example/feed.xml", errors.New("unexpected eof")),
		core.TimeoutErr("https://blog.example/feed.xml", errors.New("deadline exceeded")),
	}
	for _, fe := range cases {
		msg := fe.Error()
		if msg == "" {
			t.Fatal("empty error string")
		}
		if r := rune(msg[0]); r >= 'A' && r <= 'Z' {
			t.Errorf("error string %q must lead lowercase", msg)
		}
		if strings.HasSuffix(msg, ".") || strings.HasSuffix(msg, "!") {
			t.Errorf("error string %q must not have trailing punctuation", msg)
		}
		if !strings.Contains(msg, string(fe.Category)) {
			t.Errorf("error string %q should contain category %q", msg, fe.Category)
		}
	}
}

// Behavior 5: Detail() returns the bare human detail without the category/URL/status head.
func TestFeedErrorDetail(t *testing.T) {
	withMsg := &core.FeedError{
		Category: core.CatParse,
		FeedURL:  "u",
		Message:  "explicit message",
		Err:      errors.New("inner"),
	}
	if got := withMsg.Detail(); got != "explicit message" {
		t.Errorf("Detail() = %q, want %q", got, "explicit message")
	}

	noMsg := &core.FeedError{
		Category: core.CatNetwork,
		FeedURL:  "u",
		Err:      errors.New("inner cause"),
	}
	if got := noMsg.Detail(); got != "inner cause" {
		t.Errorf("Detail() = %q, want %q", got, "inner cause")
	}

	neither := &core.FeedError{Category: core.CatHTTP, FeedURL: "u", Status: 500}
	if got := neither.Detail(); got != "" {
		t.Errorf("Detail() = %q, want empty string for FeedError with no Message or Err", got)
	}
}

// The Message field is preferred over the wrapped cause when present, and falls
// back to the cause's text when Message is empty.
func TestFeedErrorMessageFallback(t *testing.T) {
	withMsg := &core.FeedError{Category: core.CatParse, FeedURL: "u", Message: "explicit message", Err: errors.New("inner")}
	if !strings.Contains(withMsg.Error(), "explicit message") {
		t.Errorf("error %q should use the explicit Message", withMsg.Error())
	}

	noMsg := &core.FeedError{Category: core.CatParse, FeedURL: "u", Err: errors.New("inner cause")}
	if !strings.Contains(noMsg.Error(), "inner cause") {
		t.Errorf("error %q should fall back to the wrapped cause", noMsg.Error())
	}
}
