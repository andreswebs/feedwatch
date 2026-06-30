---
id: fee-l45i
status: closed
deps: [fee-cmkb]
links: []
created: 2026-06-29T15:36:08Z
type: task
priority: 0
assignee: Andre Silva
parent: fee-63n9
tags: [cli]
---

# Structured logging (slog) setup

Configure the structured logger: JSON handler to stderr by default, text handler
under format text, level from log-level, error-only under quiet. Logs never go to
stdout. Refs: docs/cli-design.md (Input and Output Contract, Error Handling and
Logging).

## Design

Package `src/internal/cli` (logging helper). Logs always go to stderr; never
stdout.

```go
func NewLogger(w io.Writer, format string, level slog.Level, quiet bool) *slog.Logger
// format == "text" -> slog.NewTextHandler, else slog.NewJSONHandler.
// quiet == true -> effective level = slog.LevelError (overrides level).
// the returned logger is passed explicitly to components (not via ctx.Value).
```

Level mapping from `--log-level`:

```text
error|warn|info|debug -> slog.LevelError|LevelWarn|LevelInfo|LevelDebug
```

TDD plan (inject a `bytes.Buffer` as `w`):

1. (tracer) `NewLogger` with `format=json` writes a JSON line containing the
   level and message.
2. `format=text` writes a non-JSON human line.
3. `quiet=true` suppresses info and debug, still emits error.
4. `level=warn` suppresses info, emits warn.

Deep-module note: a single constructor; handler choice is internal; output goes
only to the injected writer.

## Acceptance Criteria

- `NewLogger` behaves per behaviors 1-4 (JSON default, text under format text,
  quiet error-only, level honored).
- Writes only to the injected (stderr) writer; never stdout.
- Supports Req 2. `make validate` passes.

## Notes

**2026-06-29T20:38:12Z**

Implemented NewLogger(w io.Writer, format string, level slog.Level, quiet bool) \*slog.Logger in src/internal/cli/logger.go. format=="text" -> slog.NewTextHandler, anything else -> slog.NewJSONHandler (JSON is the default contract). quiet==true overrides level to slog.LevelError. Output goes only to the injected writer (cli layer passes os.Stderr); never stdout. TDD: 4 behaviors covered in logger_test.go (external cli_test package) - JSON default, text non-JSON, quiet error-only, level honored. No env/flag parsing here; the slog.Level and quiet bool are resolved upstream by config/cli. make build passes clean.
