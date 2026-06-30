package output_test

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/output"
)

const ansiEsc = "\x1b["

// Behavior 3: a json-format Renderer never emits ANSI on either stream, even
// when color is forced on.
func TestRendererJSONNeverColorized(t *testing.T) {
	var out, errb bytes.Buffer
	r := &output.Renderer{
		Format:   "json",
		OutColor: true,
		ErrColor: true,
		Out:      &out,
		Err:      &errb,
	}

	if err := r.Result(map[string]any{"polled": 1}); err != nil {
		t.Fatalf("Result: %v", err)
	}
	if err := r.Error(core.HTTPErr("https://x.example/feed", 404, errors.New("boom"))); err != nil {
		t.Fatalf("Error: %v", err)
	}

	if strings.Contains(out.String(), ansiEsc) {
		t.Errorf("stdout JSON was colorized: %q", out.String())
	}
	if strings.Contains(errb.String(), ansiEsc) {
		t.Errorf("stderr JSON was colorized: %q", errb.String())
	}
}

// Behavior 5: text rendering of a result contains the field labels and emits no
// ANSI when color is off.
func TestRendererTextResultLabelsNoColor(t *testing.T) {
	type result struct {
		Polled   int `json:"polled"`
		NewItems int `json:"new_items"`
	}

	var out bytes.Buffer
	r := &output.Renderer{Format: "text", OutColor: false, Out: &out}
	if err := r.Result(result{Polled: 3, NewItems: 2}); err != nil {
		t.Fatalf("Result: %v", err)
	}

	s := out.String()
	if strings.Contains(s, ansiEsc) {
		t.Errorf("text result contains ANSI with color off: %q", s)
	}
	if !strings.Contains(s, "polled") || !strings.Contains(s, "new_items") {
		t.Errorf("text result missing field labels: %q", s)
	}
}

// Text error rendering pairs a symbol with color: the symbol survives when
// color is stripped, and ANSI appears only when color is on.
func TestRendererTextErrorSymbolAndColor(t *testing.T) {
	fe := func() *core.FeedError {
		return core.HTTPErr("https://x.example/feed", 404, errors.New("not found"))
	}

	var colored bytes.Buffer
	rc := &output.Renderer{Format: "text", ErrColor: true, Err: &colored}
	if err := rc.Error(fe()); err != nil {
		t.Fatalf("Error: %v", err)
	}
	cs := colored.String()
	if !strings.Contains(cs, "✗") {
		t.Errorf("colored error missing fail symbol: %q", cs)
	}
	if !strings.Contains(cs, ansiEsc) {
		t.Errorf("colored error missing ANSI: %q", cs)
	}

	var plain bytes.Buffer
	rp := &output.Renderer{Format: "text", ErrColor: false, Err: &plain}
	if err := rp.Error(fe()); err != nil {
		t.Fatalf("Error: %v", err)
	}
	ps := plain.String()
	if strings.Contains(ps, ansiEsc) {
		t.Errorf("plain error contains ANSI with color off: %q", ps)
	}
	if !strings.Contains(ps, "✗") {
		t.Errorf("plain error lost the fail symbol when color stripped: %q", ps)
	}
}

// Errors renders each failure; in text mode every line keeps its symbol.
func TestRendererTextErrorsEach(t *testing.T) {
	var errb bytes.Buffer
	r := &output.Renderer{Format: "text", ErrColor: false, Err: &errb}
	es := []*core.FeedError{
		core.HTTPErr("https://a.example/feed", 404, errors.New("not found")),
		core.NetworkErr("https://b.example/feed", errors.New("connection reset")),
	}
	if err := r.Errors(es); err != nil {
		t.Fatalf("Errors: %v", err)
	}
	if n := strings.Count(errb.String(), "✗"); n != 2 {
		t.Errorf("got %d fail symbols, want 2: %q", n, errb.String())
	}
}

// A TextRenderer result takes over its own text rendering under format text.
func TestRendererTextUsesTextRenderer(t *testing.T) {
	var out bytes.Buffer
	r := &output.Renderer{Format: "text", OutColor: false, Out: &out}
	if err := r.Result(customResult{}); err != nil {
		t.Fatalf("Result: %v", err)
	}
	if got := out.String(); got != "custom text\n" {
		t.Errorf("got %q, want custom RenderText output", got)
	}
}

type customResult struct{}

func (customResult) RenderText(w io.Writer, _ bool) error {
	_, err := w.Write([]byte("custom text\n"))
	return err
}
