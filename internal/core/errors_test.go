package core_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/terr"
)

// Behavior 7: FeedError satisfies terr.Coded and terr.Detailed. Each Category
// delegates its class-level code, exit code, and hint to a per-category class
// sentinel, so errors.Is against that sentinel matches; ErrorDetails returns
// {feed_url, status} with the documented omissions.
func TestFeedErrorIsCoded(t *testing.T) {
	cases := []struct {
		cat      core.Category
		sentinel error
		code     string
		exit     int
	}{
		{core.CatUsage, core.ErrUsage, "usage_error", 64},
		{core.CatConfig, core.ErrConfig, "config_error", 78},
		{core.CatStore, core.ErrStoreUnavailable, "store_unavailable", 69},
		{core.CatInternal, core.ErrInternal, "internal_error", 70},
		{core.CatHTTP, core.ErrHTTP, "http_error", 70},
		{core.CatNetwork, core.ErrNetwork, "feed_unreachable", 70},
		{core.CatParse, core.ErrParse, "parse_error", 70},
		{core.CatTimeout, core.ErrTimeout, "timeout_error", 70},
	}
	for _, tc := range cases {
		t.Run(string(tc.cat), func(t *testing.T) {
			fe := &core.FeedError{Category: tc.cat}

			var coded terr.Coded
			if !errors.As(error(fe), &coded) {
				t.Fatal("FeedError does not satisfy terr.Coded")
			}
			if coded.Code() != tc.code {
				t.Errorf("Code() = %q, want %q", coded.Code(), tc.code)
			}
			if coded.ExitCode() != tc.exit {
				t.Errorf("ExitCode() = %d, want %d", coded.ExitCode(), tc.exit)
			}
			if coded.Hint() == "" {
				t.Error("Hint() is empty; every class sentinel documents a hint")
			}
			if !errors.Is(fe, tc.sentinel) {
				t.Errorf("errors.Is(FeedError{%s}, class sentinel) is false", tc.cat)
			}
		})
	}
}

func TestFeedErrorDetails(t *testing.T) {
	cases := []struct {
		name string
		fe   *core.FeedError
		want string
	}{
		{"url and status", core.HTTPErr("https://ex.test/feed.xml", 404, errors.New("nf")), `{"feed_url":"https://ex.test/feed.xml","status":404}`},
		{"url only", core.NetworkErr("https://ex.test/feed.xml", errors.New("dns")), `{"feed_url":"https://ex.test/feed.xml"}`},
		{"neither", &core.FeedError{Category: core.CatInternal}, `null`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var d terr.Detailed = tc.fe
			b, err := json.Marshal(d.ErrorDetails())
			if err != nil {
				t.Fatal(err)
			}
			if string(b) != tc.want {
				t.Errorf("ErrorDetails() marshaled = %s, want %s", b, tc.want)
			}
		})
	}
}

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
