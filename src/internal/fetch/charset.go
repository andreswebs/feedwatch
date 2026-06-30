package fetch

import (
	"bytes"
	"mime"
	"regexp"

	"golang.org/x/net/html/charset"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/unicode"
)

// replacement is the Unicode replacement character used when falling back to a
// lossy UTF-8 decode of bytes that no resolved encoding could handle.
var replacement = []byte("�")

// xmlDeclEncodingRe extracts the encoding name from an XML declaration's
// encoding attribute when the declaration leads the document. It is matched
// against the leading bytes only, treated as ASCII (an XML declaration is ASCII
// even when the document body is in another single-byte encoding).
var xmlDeclEncodingRe = regexp.MustCompile(`(?i)^\s*<\?xml\b[^>]*\bencoding\s*=\s*["']([^"']+)["']`)

// decodeBody converts a raw response body to UTF-8 by an explicit precedence:
// a byte-order mark, then the XML declaration encoding, then the HTTP
// Content-Type charset, then a lossy UTF-8 fallback. It returns the decoded
// bytes and the bare media type (charset parameter stripped) from the
// Content-Type header, so the parser always receives UTF-8.
func decodeBody(raw []byte, contentType string) (utf8Body []byte, mediaTyp string) {
	mt := mediaType(contentType)

	if enc, bomLen := bomEncoding(raw); enc != nil {
		if out, err := enc.NewDecoder().Bytes(raw[bomLen:]); err == nil {
			return canonicalizeXMLDeclEncoding(out), mt
		}
		return bytes.ToValidUTF8(raw, replacement), mt
	}

	if name := xmlDeclEncoding(raw); name != "" {
		if out, ok := decodeWith(name, raw); ok {
			return canonicalizeXMLDeclEncoding(out), mt
		}
	}

	if cs := contentTypeCharset(contentType); cs != "" {
		if out, ok := decodeWith(cs, raw); ok {
			return canonicalizeXMLDeclEncoding(out), mt
		}
	}

	return bytes.ToValidUTF8(raw, replacement), mt
}

// canonicalizeXMLDeclEncoding rewrites a leading XML declaration's encoding
// value to UTF-8 on a body already decoded to UTF-8, so a downstream XML reader
// (gofeed installs golang.org/x/net/html/charset as encoding/xml's
// CharsetReader) does not re-decode the bytes per the now-stale source
// declaration. Without this, an ISO-8859-1 declaration double-encodes the body
// to mojibake and a UTF-16 declaration makes detection fail outright. It is a
// no-op when there is no leading declaration, and only the leading head window
// (the same one xmlDeclEncoding inspects) is examined so a later occurrence is
// never rewritten.
func canonicalizeXMLDeclEncoding(utf8Body []byte) []byte {
	head := utf8Body
	if len(head) > 1024 {
		head = head[:1024]
	}
	loc := xmlDeclEncodingRe.FindSubmatchIndex(head)
	if loc == nil {
		return utf8Body
	}
	start, end := loc[2], loc[3] // submatch 1 = the encoding value
	out := make([]byte, 0, len(utf8Body)+len("UTF-8"))
	out = append(out, utf8Body[:start]...)
	out = append(out, "UTF-8"...)
	out = append(out, utf8Body[end:]...)
	return out
}

// bomEncoding reports the encoding implied by a leading byte-order mark, if any,
// along with the BOM length to skip. The BOM is consumed by the caller so the
// decoded output never retains a stray U+FEFF.
func bomEncoding(raw []byte) (encoding.Encoding, int) {
	switch {
	case bytes.HasPrefix(raw, []byte{0xEF, 0xBB, 0xBF}):
		return unicode.UTF8, 3
	case bytes.HasPrefix(raw, []byte{0xFE, 0xFF}):
		return unicode.UTF16(unicode.BigEndian, unicode.IgnoreBOM), 2
	case bytes.HasPrefix(raw, []byte{0xFF, 0xFE}):
		return unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM), 2
	}
	return nil, 0
}

// xmlDeclEncoding returns the encoding name declared in a leading XML
// declaration, or the empty string when there is none.
func xmlDeclEncoding(raw []byte) string {
	head := raw
	if len(head) > 1024 {
		head = head[:1024]
	}
	m := xmlDeclEncodingRe.FindSubmatch(head)
	if m == nil {
		return ""
	}
	return string(m[1])
}

// contentTypeCharset extracts the charset parameter from a Content-Type header.
func contentTypeCharset(contentType string) string {
	if contentType == "" {
		return ""
	}
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return ""
	}
	return params["charset"]
}

// decodeWith looks up the named charset and decodes raw to UTF-8 with it. It
// reports false when the name is unknown or decoding fails, so the caller can
// fall through to the next precedence rung.
func decodeWith(name string, raw []byte) ([]byte, bool) {
	enc, _ := charset.Lookup(name)
	if enc == nil {
		return nil, false
	}
	out, err := enc.NewDecoder().Bytes(raw)
	if err != nil {
		return nil, false
	}
	return out, true
}
