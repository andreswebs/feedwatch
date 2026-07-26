package output_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/andreswebs/feedwatch/internal/output"
)

// warnEnvelope mirrors the ADR 0005 warning envelope so tests can decode it.
type warnEnvelope struct {
	SchemaVersion int    `json:"schema_version"`
	Level         string `json:"level"`
	Code          string `json:"code"`
	Message       string `json:"message"`
	Hint          string `json:"hint"`
	Details       struct {
		FeedURL  string `json:"feed_url"`
		Failures int    `json:"failures"`
	} `json:"details"`
}

// Behavior 1 (tracer): EmitWarning writes exactly one newline-terminated object
// carrying level "warning" and the passed code, message, hint, and details.
func TestEmitWarningTracer(t *testing.T) {
	var buf bytes.Buffer
	details := map[string]any{"feed_url": "http://feedserver/feed.xml", "failures": 5}
	output.EmitWarning(&buf, "feed_auto_disabled", "feed disabled after 5 consecutive failures",
		"re-enable with: feedwatch enable <feed>", details)

	out := buf.String()
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("output %q lacks a trailing newline", out)
	}
	if n := strings.Count(out, "\n"); n != 1 {
		t.Errorf("output has %d newlines, want 1", n)
	}
	if strings.Contains(out, `"ok"`) {
		t.Errorf("output %q carries an ok field; a warning must use level instead", out)
	}

	var env warnEnvelope
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if env.Level != "warning" {
		t.Errorf("level = %q, want warning", env.Level)
	}
	if env.SchemaVersion != output.SchemaVersion {
		t.Errorf("schema_version = %d, want %d", env.SchemaVersion, output.SchemaVersion)
	}
	if env.Code != "feed_auto_disabled" {
		t.Errorf("code = %q, want feed_auto_disabled", env.Code)
	}
	if env.Message != "feed disabled after 5 consecutive failures" {
		t.Errorf("message = %q, want the disabled message", env.Message)
	}
	if env.Hint != "re-enable with: feedwatch enable <feed>" {
		t.Errorf("hint = %q, want the enable hint", env.Hint)
	}
	if env.Details.FeedURL != "http://feedserver/feed.xml" || env.Details.Failures != 5 {
		t.Errorf("details = %+v, want feed_url and failures", env.Details)
	}
}

// Behavior 2: hint and details are omitted when empty.
func TestEmitWarningOmitsEmptyHintAndDetails(t *testing.T) {
	var buf bytes.Buffer
	output.EmitWarning(&buf, "some_code", "a message", "", nil)

	out := buf.String()
	if strings.Contains(out, "hint") {
		t.Errorf("output %q includes an empty hint", out)
	}
	if strings.Contains(out, "details") {
		t.Errorf("output %q includes empty details", out)
	}
}

// Behavior 3: two successive calls produce two lines, each independently
// decodable (a valid NDJSON stream).
func TestEmitWarningNDJSONStream(t *testing.T) {
	var buf bytes.Buffer
	output.EmitWarning(&buf, "first", "one", "", nil)
	output.EmitWarning(&buf, "second", "two", "", nil)

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2: %q", len(lines), buf.String())
	}
	for i, line := range lines {
		var env warnEnvelope
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			t.Fatalf("line %d is not valid JSON: %v", i, err)
		}
		if env.Level != "warning" {
			t.Errorf("line %d level = %q, want warning", i, env.Level)
		}
	}
}
