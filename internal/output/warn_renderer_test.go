package output_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/andreswebs/feedwatch/internal/output"
)

// Behavior 4: Renderer.Warn in JSON mode writes one NDJSON warning line to
// stderr and nothing to stdout.
func TestRendererWarnJSONToStderrOnly(t *testing.T) {
	var out, err bytes.Buffer
	r := &output.Renderer{Format: "json", Out: &out, Err: &err}
	r.Warn("feed_auto_disabled", "feed disabled after 5 consecutive failures",
		"re-enable with: feedwatch enable <feed>",
		map[string]any{"feed_url": "http://feedserver/feed.xml", "failures": 5})

	if out.Len() != 0 {
		t.Errorf("Warn wrote %q to stdout, want nothing", out.String())
	}
	var env warnEnvelope
	if jerr := json.Unmarshal(bytes.TrimRight(err.Bytes(), "\n"), &env); jerr != nil {
		t.Fatalf("stderr is not valid JSON: %v", jerr)
	}
	if env.Level != "warning" || env.Code != "feed_auto_disabled" {
		t.Errorf("envelope = %+v, want warning/feed_auto_disabled", env)
	}
}

// Behavior 4 (text): Renderer.Warn in text mode writes a marked human line whose
// marker survives color being off, is not the failure symbol, and writes nothing
// to stdout.
func TestRendererWarnTextMarkerAndColor(t *testing.T) {
	var colOut, colErr bytes.Buffer
	rc := &output.Renderer{Format: "text", ErrColor: true, Out: &colOut, Err: &colErr}
	rc.Warn("feed_auto_disabled", "feed disabled after 5 consecutive failures", "", nil)

	if colOut.Len() != 0 {
		t.Errorf("Warn wrote %q to stdout, want nothing", colOut.String())
	}
	cs := colErr.String()
	if strings.Contains(cs, output.SymbolFail) {
		t.Errorf("warning uses the failure symbol: %q", cs)
	}
	if !strings.Contains(cs, output.SymbolWarn) {
		t.Errorf("colored warning missing warn symbol: %q", cs)
	}
	if !strings.Contains(cs, ansiEsc) {
		t.Errorf("colored warning missing ANSI: %q", cs)
	}

	var plainErr bytes.Buffer
	rp := &output.Renderer{Format: "text", ErrColor: false, Err: &plainErr}
	rp.Warn("feed_auto_disabled", "feed disabled after 5 consecutive failures", "", nil)
	ps := plainErr.String()
	if strings.Contains(ps, ansiEsc) {
		t.Errorf("plain warning contains ANSI with color off: %q", ps)
	}
	if !strings.Contains(ps, output.SymbolWarn) {
		t.Errorf("plain warning lost the warn symbol when color stripped: %q", ps)
	}
}
