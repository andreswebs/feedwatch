package output

import (
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"

	"github.com/andreswebs/feedwatch/internal/core"
)

// Text-mode status markers. Each pairs a symbol with color so that stripping
// color never removes meaning: the symbol alone still distinguishes outcomes.
const (
	// SymbolOK marks a successful outcome in text output.
	SymbolOK = "✓"
	// SymbolFail marks a failed outcome in text output.
	SymbolFail = "✗"
)

const (
	ansiReset = "\x1b[0m"
	ansiRed   = "\x1b[31m"
)

// TextRenderer is implemented by result values that render their own
// human-friendly text under --format text. Values that do not implement it fall
// back to a generic field dump. JSON output never consults this interface.
type TextRenderer interface {
	RenderText(w io.Writer, color bool) error
}

// Renderer writes a command's result to stdout and its per-feed failures to
// stderr in the selected format. The never-colorize-JSON rule and per-stream
// color gating are enforced here, so callers only choose Result, Error, or
// Errors.
type Renderer struct {
	Format             string
	OutColor, ErrColor bool
	Out, Err           io.Writer
}

// NewRenderer builds a Renderer for the given format, resolving per-stream color
// from the real terminal streams and the color policy. out and err are the
// process streams (typically os.Stdout and os.Stderr).
func NewRenderer(format string, out, err *os.File, p ColorPolicy) *Renderer {
	return &Renderer{
		Format:   format,
		OutColor: ResolveColor(out, format, p),
		ErrColor: ResolveColor(err, format, p),
		Out:      out,
		Err:      err,
	}
}

// Result writes the result envelope to stdout: compact JSON by default, or text
// when the format is text. JSON is never colorized.
func (r *Renderer) Result(v any) error {
	if r.Format != "text" {
		return WriteJSON(r.Out, v)
	}
	if tr, ok := v.(TextRenderer); ok {
		return tr.RenderText(r.Out, r.OutColor)
	}
	return renderText(r.Out, v)
}

// Error writes a single per-feed failure to stderr as a structured JSON object,
// or as a symbol-marked text line when the format is text.
func (r *Renderer) Error(e *core.FeedError) error {
	if r.Format != "text" {
		return WriteError(r.Err, e)
	}
	return r.textError(e)
}

// Errors writes a batch of per-feed failures to stderr, one structured object
// under "errors" in JSON, or one symbol-marked line each in text.
func (r *Renderer) Errors(es []*core.FeedError) error {
	if r.Format != "text" {
		return WriteErrors(r.Err, es)
	}
	for _, e := range es {
		if err := r.textError(e); err != nil {
			return err
		}
	}
	return nil
}

// textError writes one failure line. The fail symbol is always present; red is
// added only when the stream is colorized, so color is never the sole carrier
// of the failure signal.
func (r *Renderer) textError(e *core.FeedError) error {
	line := SymbolFail + " " + e.Error()
	if r.ErrColor {
		line = ansiRed + line + ansiReset
	}
	_, err := fmt.Fprintln(r.Err, line)
	return err
}

// renderText is the generic text fallback for result values that do not
// implement TextRenderer. It prints one "label: value" line per exported struct
// field, using the JSON tag as the label, and falls back to a plain value for
// non-struct results. It never emits color.
func renderText(w io.Writer, v any) error {
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			_, err := fmt.Fprintln(w, "<nil>")
			return err
		}
		rv = rv.Elem()
	}

	if rv.Kind() != reflect.Struct {
		_, err := fmt.Fprintf(w, "%v\n", rv.Interface())
		return err
	}

	rt := rv.Type()
	for i := range rt.NumField() {
		f := rt.Field(i)
		if !f.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = f.Name
		}
		if _, err := fmt.Fprintf(w, "%s: %v\n", name, rv.Field(i).Interface()); err != nil {
			return err
		}
	}
	return nil
}
