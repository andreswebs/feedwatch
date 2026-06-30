---
id: fee-fp2h
status: closed
deps: [fee-b91x]
links: []
created: 2026-06-29T18:45:23Z
type: task
priority: 1
assignee: Andre Silva
parent: fee-8cau
tags: [http]
---

# Charset decode to UTF-8

Decode response bodies to UTF-8 by explicit precedence (BOM, XML declaration, HTTP Content-Type charset, lossy UTF-8 fallback) so the parser always receives UTF-8, recording the resolved MIME type. Refs: docs/cli-design.md (Parsing and Robustness, Fetching and HTTP).

## Design

Decode the response body to UTF-8 before it reaches the parser, by an explicit
precedence.

Precedence:

1. Byte-order mark (UTF-8/UTF-16 BOM).
2. XML declaration `encoding="..."`.
3. HTTP `Content-Type` charset parameter.
4. Lossy UTF-8 fallback.

```go
// decodeBody converts raw bytes + the HTTP Content-Type into UTF-8 bytes,
// recording the resolved MIME type into core.FetchResult.MIMEType.
func decodeBody(raw []byte, contentType string) (utf8Body []byte, mime string)
// uses golang.org/x/net/html/charset and golang.org/x/text/encoding.
```

The decoded UTF-8 body is placed in `core.FetchResult.Body`; `MIMEType` is set
from the `Content-Type` (sans charset). gofeed then parses UTF-8 only.

TDD plan (fixtures in several encodings):

1. (tracer) an ISO-8859-1 document with a matching `Content-Type` charset
   decodes to correct UTF-8.
2. a UTF-16 document with a BOM decodes correctly (BOM wins over Content-Type).
3. an XML declaration `encoding` is honored when no BOM is present.
4. unknown/garbage encoding falls back to lossy UTF-8 without error.

Deep-module note: decoding is internal to the fetch layer; the parser always
receives UTF-8.

## Acceptance Criteria

- Body decoded to UTF-8 by precedence BOM > XML declaration > Content-Type >
  lossy UTF-8; `MIMEType` recorded on the result.
- Behaviors 1-4 covered with multi-encoding fixtures.
- Supports the charset part of Req 14. `make validate` passes.

## Notes

**2026-06-29T22:09:09Z**

Implemented decodeBody in internal/fetch/charset.go: converts raw response bytes to UTF-8 by explicit precedence BOM > XML declaration > Content-Type charset > lossy UTF-8 fallback, returning the bare media type alongside. Wired into Client.Fetch (replaces the old raw body + mediaType()). BOM detection (UTF-8/UTF-16LE/UTF-16BE) consumes the BOM via golang.org/x/text/encoding/unicode IgnoreBOM decoders on the post-BOM slice, so no stray U+FEFF survives. XML decl and Content-Type rungs use charset.Lookup; any unknown name or decode error falls through to bytes.ToValidUTF8. go mod tidy promoted golang.org/x/text from indirect to direct. Tests: 5 white-box decodeBody unit cases (charset_internal_test.go) covering all 4 ticket behaviors + a UTF-8 passthrough regression, plus a black-box Fetch e2e (charset_test.go) over the iso8859-1 and utf16-bom fixtures. All green under -race. Note: precedence is implemented explicitly rather than via charset.DetermineEncoding, which orders Content-Type before the XML declaration and would violate the ticket's required ordering.
