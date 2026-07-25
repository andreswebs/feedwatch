package fetch

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestDecodeBodyContentTypeCharset covers behavior 1: an ISO-8859-1 body with a
// matching Content-Type charset and no XML declaration decodes via the
// Content-Type rung.
func TestDecodeBodyContentTypeCharset(t *testing.T) {
	raw := []byte("caf\xe9") // 0xE9 is é in ISO-8859-1
	out, mime := decodeBody(raw, "text/xml; charset=ISO-8859-1")

	if got := string(out); got != "café" {
		t.Errorf("body = %q, want %q", got, "café")
	}
	if !utf8.Valid(out) {
		t.Errorf("body is not valid UTF-8: % x", out)
	}
	if mime != "text/xml" {
		t.Errorf("mime = %q, want text/xml (charset stripped)", mime)
	}
}

// TestDecodeBodyBOMWinsOverContentType covers behavior 2: a UTF-16LE BOM wins
// over a conflicting Content-Type charset.
func TestDecodeBodyBOMWinsOverContentType(t *testing.T) {
	// "café" in UTF-16LE, prefixed with the LE BOM (0xFF 0xFE).
	raw := []byte{0xff, 0xfe, 'c', 0, 'a', 0, 'f', 0, 0xe9, 0}
	out, _ := decodeBody(raw, "text/xml; charset=utf-8")

	if got := string(out); got != "café" {
		t.Errorf("body = %q, want %q (BOM must win over Content-Type)", got, "café")
	}
	if strings.ContainsRune(string(out), '\uFEFF') {
		t.Errorf("decoded body retained a BOM: %q", out)
	}
}

// TestDecodeBodyXMLDeclaration covers behavior 3: with no BOM and no
// Content-Type charset, the XML declaration encoding is honored.
func TestDecodeBodyXMLDeclaration(t *testing.T) {
	raw := []byte("<?xml version=\"1.0\" encoding=\"ISO-8859-1\"?>\n<r>caf\xe9</r>")
	out, _ := decodeBody(raw, "application/xml") // no charset parameter

	if got := string(out); !strings.Contains(got, "café") {
		t.Errorf("body = %q, want it to contain %q", got, "café")
	}
	if !utf8.Valid(out) {
		t.Errorf("body is not valid UTF-8: % x", out)
	}
}

// TestDecodeBodyGarbageFallsBackToLossyUTF8 covers behavior 4: an unknown
// charset with invalid bytes falls back to lossy UTF-8 without error, yielding
// valid UTF-8.
func TestDecodeBodyGarbageFallsBackToLossyUTF8(t *testing.T) {
	raw := []byte("ok \x80\x81 bad")
	out, mime := decodeBody(raw, "text/plain; charset=definitely-not-a-real-charset")

	if !utf8.Valid(out) {
		t.Errorf("fallback body is not valid UTF-8: % x", out)
	}
	if !strings.Contains(string(out), "ok ") || !strings.Contains(string(out), " bad") {
		t.Errorf("fallback body = %q, want surrounding ASCII preserved", out)
	}
	if mime != "text/plain" {
		t.Errorf("mime = %q, want text/plain", mime)
	}
}

// TestDecodeBodyPlainUTF8 is a regression guard: a valid UTF-8 body with no
// declared encoding passes through unchanged.
func TestDecodeBodyPlainUTF8(t *testing.T) {
	raw := []byte("<rss>café ok</rss>")
	out, _ := decodeBody(raw, "application/rss+xml")
	if got := string(out); got != "<rss>café ok</rss>" {
		t.Errorf("body = %q, want unchanged", got)
	}
}
