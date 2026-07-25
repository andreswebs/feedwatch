package output_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/output"
)

// Behavior 1 (tracer): WriteJSON emits compact JSON with a trailing newline.
func TestWriteJSONCompactTrailingNewline(t *testing.T) {
	var buf bytes.Buffer
	v := map[string]any{"polled": 3, "new_items": 2}

	if err := output.WriteJSON(&buf, v); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}

	out := buf.String()
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("output %q lacks a trailing newline", out)
	}
	if n := strings.Count(out, "\n"); n != 1 {
		t.Errorf("output has %d newlines, want 1 (compact)", n)
	}
	if strings.Contains(out, "  ") {
		t.Errorf("output %q is indented, want compact", out)
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
}

// Behavior 2: WriteError emits {"error":{"category":"http","status":404,...}}.
func TestWriteErrorShape(t *testing.T) {
	var buf bytes.Buffer
	fe := core.HTTPErr("https://blog.example/feed.xml", 404, errors.New("not found"))

	if err := output.WriteError(&buf, fe); err != nil {
		t.Fatalf("WriteError: %v", err)
	}

	var env struct {
		Error struct {
			Category string `json:"category"`
			FeedURL  string `json:"feed_url"`
			Status   int    `json:"status"`
			Message  string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}

	if env.Error.Category != "http" {
		t.Errorf("category = %q, want http", env.Error.Category)
	}
	if env.Error.Status != 404 {
		t.Errorf("status = %d, want 404", env.Error.Status)
	}
	if env.Error.FeedURL != "https://blog.example/feed.xml" {
		t.Errorf("feed_url = %q, want the feed url", env.Error.FeedURL)
	}
	if env.Error.Message != "not found" {
		t.Errorf("message = %q, want %q", env.Error.Message, "not found")
	}
}

// A non-HTTP, whole-invocation error omits the zero status and empty feed URL.
func TestWriteErrorOmitsZeroFields(t *testing.T) {
	var buf bytes.Buffer
	fe := &core.FeedError{Category: core.CatConfig, Message: "bad db dsn"}

	if err := output.WriteError(&buf, fe); err != nil {
		t.Fatalf("WriteError: %v", err)
	}

	out := buf.String()
	if strings.Contains(out, "status") {
		t.Errorf("output %q includes a zero status", out)
	}
	if strings.Contains(out, "feed_url") {
		t.Errorf("output %q includes an empty feed_url", out)
	}
}

// WriteErrors wraps the per-feed failures under an "errors" array.
func TestWriteErrorsShape(t *testing.T) {
	var buf bytes.Buffer
	es := []*core.FeedError{
		core.HTTPErr("https://a.example/feed.xml", 404, errors.New("not found")),
		core.NetworkErr("https://b.example/feed.xml", errors.New("connection reset")),
	}

	if err := output.WriteErrors(&buf, es); err != nil {
		t.Fatalf("WriteErrors: %v", err)
	}

	var env struct {
		Errors []struct {
			Category string `json:"category"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if len(env.Errors) != 2 {
		t.Fatalf("got %d errors, want 2", len(env.Errors))
	}
	if env.Errors[0].Category != "http" || env.Errors[1].Category != "network" {
		t.Errorf("categories = %q, %q; want http, network",
			env.Errors[0].Category, env.Errors[1].Category)
	}
}
