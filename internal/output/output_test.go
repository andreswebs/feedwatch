package output_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/output"
	"github.com/andreswebs/feedwatch/internal/terr"
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

// errEnvelope mirrors the ADR 0005 error envelope so tests can decode it.
type errEnvelope struct {
	SchemaVersion int  `json:"schema_version"`
	OK            bool `json:"ok"`
	Error         struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Hint    string `json:"hint"`
		Details struct {
			FeedURL string `json:"feed_url"`
			Status  int    `json:"status"`
		} `json:"details"`
	} `json:"error"`
}

// Behavior 1 (tracer): EmitError on a registered sentinel writes exactly the
// ADR 0005 envelope with schema_version, ok:false, code, message, and hint, and
// a single trailing newline.
func TestEmitErrorSentinelShape(t *testing.T) {
	var buf bytes.Buffer
	output.EmitError(&buf, core.ErrUsage)

	out := buf.String()
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("output %q lacks a trailing newline", out)
	}
	if n := strings.Count(out, "\n"); n != 1 {
		t.Errorf("output has %d newlines, want 1", n)
	}

	var env errEnvelope
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if env.SchemaVersion != output.SchemaVersion {
		t.Errorf("schema_version = %d, want %d", env.SchemaVersion, output.SchemaVersion)
	}
	if env.OK {
		t.Errorf("ok = true, want false for an error envelope")
	}
	if env.Error.Code != "usage_error" {
		t.Errorf("code = %q, want usage_error", env.Error.Code)
	}
	if env.Error.Message != core.ErrUsage.Error() {
		t.Errorf("message = %q, want %q", env.Error.Message, core.ErrUsage.Error())
	}
	if env.Error.Hint != core.ErrUsage.Hint() {
		t.Errorf("hint = %q, want %q", env.Error.Hint, core.ErrUsage.Hint())
	}
}

// Behavior 2: hint and details are omitted when empty.
func TestEmitErrorOmitsEmptyHintAndDetails(t *testing.T) {
	var buf bytes.Buffer
	output.EmitError(&buf, errors.New("plain error"))

	out := buf.String()
	if strings.Contains(out, "hint") {
		t.Errorf("output %q includes an empty hint", out)
	}
	if strings.Contains(out, "details") {
		t.Errorf("output %q includes empty details", out)
	}
}

// A whole-invocation FeedError with no feed scope omits the details field
// rather than rendering an empty object.
func TestEmitErrorWholeInvocationOmitsDetails(t *testing.T) {
	var buf bytes.Buffer
	fe := &core.FeedError{Category: core.CatConfig, Message: "bad db dsn", Err: core.ErrConfig}
	output.EmitError(&buf, fe)

	out := buf.String()
	if strings.Contains(out, "details") {
		t.Errorf("output %q includes empty details for a whole-invocation error", out)
	}
	var env errEnvelope
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if env.Error.Code != "config_error" {
		t.Errorf("code = %q, want config_error", env.Error.Code)
	}
}

// Behavior 3: a *core.FeedError renders feed_url and status under error.details.
func TestEmitErrorFeedDetails(t *testing.T) {
	var buf bytes.Buffer
	fe := core.HTTPErr("https://blog.example/feed.xml", 404, errors.New("not found"))
	output.EmitError(&buf, fe)

	var env errEnvelope
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if env.Error.Code != "http_error" {
		t.Errorf("code = %q, want http_error", env.Error.Code)
	}
	if env.Error.Details.FeedURL != "https://blog.example/feed.xml" {
		t.Errorf("details.feed_url = %q, want the feed url", env.Error.Details.FeedURL)
	}
	if env.Error.Details.Status != 404 {
		t.Errorf("details.status = %d, want 404", env.Error.Details.Status)
	}
}

// Behavior 4: an unclassified error renders code internal_error and exits 70.
func TestEmitErrorUnclassifiedIsInternal(t *testing.T) {
	var buf bytes.Buffer
	err := errors.New("something odd")
	output.EmitError(&buf, err)

	var env errEnvelope
	if jerr := json.Unmarshal(buf.Bytes(), &env); jerr != nil {
		t.Fatalf("output is not valid JSON: %v", jerr)
	}
	if env.Error.Code != "internal_error" {
		t.Errorf("code = %q, want internal_error", env.Error.Code)
	}
	if got := output.ExitCodeFor(err); got != 70 {
		t.Errorf("ExitCodeFor = %d, want 70", got)
	}
}

// Behavior 5: details that cannot marshal degrade to the envelope without them
// rather than producing no output.
func TestEmitErrorUnmarshalableDetailsDegrade(t *testing.T) {
	var buf bytes.Buffer
	bad := terr.New("bad_details_code", 70, "hint", "boom").WithDetails(func() {})
	output.EmitError(&buf, bad)

	out := buf.String()
	if out == "" {
		t.Fatal("EmitError produced no output for unmarshalable details")
	}
	var env errEnvelope
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("degraded output is not valid JSON: %v\ngot: %q", err, out)
	}
	if env.Error.Code != "bad_details_code" {
		t.Errorf("code = %q, want bad_details_code", env.Error.Code)
	}
}
