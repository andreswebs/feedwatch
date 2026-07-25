package cli_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/andreswebs/feedwatch/internal/cli"
)

func TestNewLoggerJSON(t *testing.T) {
	var buf bytes.Buffer
	logger := cli.NewLogger(&buf, "json", slog.LevelInfo, false)

	logger.Info("hello", "key", "value")

	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("output is not a JSON line: %v\ngot: %q", err, buf.String())
	}
	if line[slog.LevelKey] != "INFO" {
		t.Errorf("level = %v, want INFO", line[slog.LevelKey])
	}
	if line[slog.MessageKey] != "hello" {
		t.Errorf("msg = %v, want hello", line[slog.MessageKey])
	}
	if line["key"] != "value" {
		t.Errorf("key = %v, want value", line["key"])
	}
}

func TestNewLoggerText(t *testing.T) {
	var buf bytes.Buffer
	logger := cli.NewLogger(&buf, "text", slog.LevelInfo, false)

	logger.Info("hello", "key", "value")

	out := buf.String()
	if json.Valid(bytes.TrimSpace(buf.Bytes())) {
		t.Errorf("text output should not be valid JSON, got: %q", out)
	}
	if !strings.Contains(out, "hello") {
		t.Errorf("text output missing message, got: %q", out)
	}
	if !strings.Contains(out, "key=value") {
		t.Errorf("text output missing attribute, got: %q", out)
	}
}

func TestNewLoggerQuiet(t *testing.T) {
	var buf bytes.Buffer
	logger := cli.NewLogger(&buf, "json", slog.LevelDebug, true)

	logger.Debug("debug-msg")
	logger.Info("info-msg")
	logger.Warn("warn-msg")
	if buf.Len() != 0 {
		t.Errorf("quiet logger emitted below-error output: %q", buf.String())
	}

	logger.Error("error-msg")
	if !strings.Contains(buf.String(), "error-msg") {
		t.Errorf("quiet logger suppressed error, got: %q", buf.String())
	}
}

func TestNewLoggerLevelHonored(t *testing.T) {
	var buf bytes.Buffer
	logger := cli.NewLogger(&buf, "json", slog.LevelWarn, false)

	logger.Info("info-msg")
	if buf.Len() != 0 {
		t.Errorf("warn-level logger emitted info: %q", buf.String())
	}

	logger.Warn("warn-msg")
	if !strings.Contains(buf.String(), "warn-msg") {
		t.Errorf("warn-level logger suppressed warn, got: %q", buf.String())
	}
}
