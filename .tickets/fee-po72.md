---
id: fee-po72
status: closed
deps: [fee-qnv1, fee-4m17]
links: []
created: 2026-06-29T15:36:08Z
type: task
priority: 0
assignee: Andre Silva
parent: fee-63n9
tags: [cli]
---

# Output envelope, renderers and color gating

Define the result rendering layer: JSON (default) and human text, with per-stream
color gating, and structured FeedError rendering to stderr. JSON output is never
colorized. Refs: docs/cli-design.md (Input and Output Contract, Error Handling
and Logging).

## Design

Package `src/internal/output`. Commands build a typed result value; output
renders it as JSON (default) or human text, and renders FeedError objects to
stderr.

```go
func WriteJSON(w io.Writer, v any) error                  // compact, newline-terminated
func WriteError(w io.Writer, e *core.FeedError) error     // {"error":{...}}
func WriteErrors(w io.Writer, es []*core.FeedError) error // {"errors":[...]}

type ColorPolicy struct{ NoColorFlag bool }

// ResolveColor reports whether to colorize a stream:
//   format == "text" AND isatty(stream) AND NO_COLOR unset AND TERM != "dumb".
// stdout and stderr are evaluated independently.
func ResolveColor(stream *os.File, format string, p ColorPolicy) bool

type Renderer struct {
    Format             string
    OutColor, ErrColor bool
    Out, Err           io.Writer
}

func NewRenderer(format string, out, err *os.File, p ColorPolicy) *Renderer
func (r *Renderer) Result(v any) error            // JSON or text -> Out
func (r *Renderer) Error(e *core.FeedError) error // JSON or text -> Err
func (r *Renderer) Errors(es []*core.FeedError) error
```

Rules:

- JSON output is never colorized on either stream, regardless of color state.
- Text status markers pair a symbol with color (never color alone): a check for
  ok, a cross for failure, so stripping color loses no information.

TDD plan (inject buffers and a non-terminal stream):

1. (tracer) `WriteJSON` emits compact JSON with a trailing newline for a sample
   result value.
2. `WriteError` emits `{"error":{"category":"http","status":404,...}}`.
3. A Renderer with `Format=="json"` never emits ANSI even if color is enabled.
4. `ResolveColor`: `NO_COLOR` set -> false; `TERM=dumb` -> false; format not
   text -> false; non-terminal stream -> false.
5. Text rendering of a small result contains the field labels and no ANSI when
   color is false.

Deep-module note: callers use `Renderer.Result`/`Error`(s); the never-color-JSON
rule is enforced inside.

## Acceptance Criteria

- `WriteJSON`, `WriteError`(s), Renderer, `ResolveColor` defined.
- JSON output never contains ANSI (behavior 3); color gating per Req 2 and the
  cli-design color rules (behavior 4).
- Compact JSON with trailing newline (behavior 1).
- Text status pairs symbol with color (never color alone).
- `make validate` passes.

## Notes

**2026-06-29T20:36:05Z**

Implemented internal/output: WriteJSON/WriteError/WriteErrors, ColorPolicy+ResolveColor, Renderer (NewRenderer/Result/Error/Errors). JSON via json.Encoder (compact + trailing newline). FeedError stderr shape is an unexported errorPayload struct {category, feed_url omitempty, status omitempty, message}; message prefers e.Message then e.Err (drops the category/url/status prefix that the structured fields already carry). ResolveColor gates on format==text AND tty AND no NoColorFlag AND NO_COLOR absent AND TERM!=dumb; tty check is pure stdlib via os.ModeCharDevice (no x/term dep). NewRenderer takes \*os.File (resolves per-stream color); Renderer struct uses io.Writer so tests inject buffers and set OutColor/ErrColor directly. Text results: optional TextRenderer interface, else a reflection field-dump using json tags as labels. Text errors prefix SymbolFail (cross), add red only when colored, so the symbol survives color stripping. All 4 ticket behaviors + extras covered; make build green.
