---
id: fee-d974
status: closed
deps: [fee-yigg]
links: []
created: 2026-06-30T03:42:48Z
type: bug
priority: 2
tags: [parse]
---

# parse: charset decoding broken for ISO-8859-1 and UTF-16LE+BOM

Non-UTF-8 feed decoding is broken: an ISO-8859-1 feed with a matching XML declaration is stored as double-encoded mojibake, and a UTF-16LE feed with a BOM does not parse at all. Found during manual QA (TC-PARSE-003); see `docs/qa.result.bak.md` BUG-006.

## Design

Required charset precedence (REQ 14): BOM > XML declaration > `Content-Type` > UTF-8.

ISO-8859-1 case: `latin1.xml` is genuine ISO-8859-1 (byte `0xE9` for `e-acute`) and declares `encoding="ISO-8859-1"`. The stored title `Cafe item` (with an accented e) is bytes `43 61 66 c3 83 c2 a9 20 ...` ("CafA©") instead of the correct `43 61 66 c3 a9 20 ...`. This is double-encoded mojibake: the declared encoding is not honored.

UTF-16LE + BOM case: `utf16le-bom.xml` fails to parse entirely ("does not parse as a feed"), even without the conflicting `charset=utf-8` header, so the "BOM wins" rule cannot hold and UTF-16 is effectively unsupported.

Garbage-charset case works correctly (lossy fallback, no crash, exit 0).

Repro:

```sh
feedwatch --db "$DB" add "$FIX/feeds/latin1.xml"
feedwatch --db "$DB" poll --all
feedwatch --db "$DB" items | jq -r ".items[0].title" | od -An -tx1   # 43 61 66 c3 83 c2 a9 ... (wrong)

feedwatch --db "$DB2" add "$FIX/feeds/utf16le-bom.xml"               # exit 1: does not parse
```

## Acceptance Criteria

- An ISO-8859-1 feed with a matching XML declaration decodes to correct UTF-8 (`Cafe item` title stored as `43 61 66 c3 a9 ...`).
- A UTF-16LE feed with a BOM parses and yields items; the BOM wins over a conflicting `charset` header.
- A garbage/unknown charset still falls back lossily without crashing (unchanged).

## Implementation Plan

`decodeBody` in `src/internal/fetch/charset.go` already converts the body to correct UTF-8 by the required precedence (BOM > XML declaration > `Content-Type` > UTF-8). The defect is downstream double-decoding: the decoded body still carries its original XML declaration (`encoding="ISO-8859-1"` / `encoding="UTF-16"`), and gofeed installs `golang.org/x/net/html/charset` as the `encoding/xml` `CharsetReader`, so it re-decodes the already-UTF-8 bytes per that stale declaration (mojibake for Latin-1; detection failure for UTF-16). The fix keeps the layering (the `Content-Type` rung is only knowable at the fetch layer, since gofeed never sees HTTP headers) by neutralizing the declaration so gofeed's reader becomes a no-op.

1. Add a helper to `src/internal/fetch/charset.go` that rewrites a leading XML declaration's `encoding="..."` value to `UTF-8`, using `xmlDeclEncodingRe` to locate the encoding submatch and splicing only that span (a no-op when there is no declaration):

   ```go
   func canonicalizeXMLDeclEncoding(utf8Body []byte) []byte {
       loc := xmlDeclEncodingRe.FindSubmatchIndex(utf8Body)
       if loc == nil {
           return utf8Body
       }
       start, end := loc[2], loc[3] // submatch 1 = the encoding value
       out := make([]byte, 0, len(utf8Body)+4)
       out = append(out, utf8Body[:start]...)
       out = append(out, "UTF-8"...)
       out = append(out, utf8Body[end:]...)
       return out
   }
   ```

2. Apply it to the output of each successful non-UTF-8 decode branch in `decodeBody` (the BOM, XML-declaration, and `Content-Type` rungs) before returning. Do NOT apply it on the lossy `bytes.ToValidUTF8` fallback paths, so the garbage/unknown-charset case (which currently exits `0`) is unchanged.

3. The matcher only inspects the leading bytes (as `xmlDeclEncoding` already caps to the first 1024 bytes); keep the splice operating on that same head window so a stray later occurrence cannot be rewritten.

Verification:

- New parser-level test (the gap that let this through: `charset_test.go` stops at the byte level): decode the `latin1.xml` and `utf16le-bom.xml` fixtures through the parser and assert the stored title decodes to `43 61 66 c3 a9 ...` ("Café") and that the UTF-16LE+BOM feed parses and yields items.
- Re-confirm the garbage-charset fixture still parses lossily and exits `0`.
- `make build` green; learnings entry.

## Notes

**2026-06-30T03:42:48Z**

Source: manual QA report docs/qa.result.bak.md BUG-006 (TC-PARSE-003). Severity Medium-High, Priority P1.

**2026-06-30T13:46:29Z**

Fixed double-decoding of non-UTF-8 feeds. decodeBody already produced correct UTF-8, but the decoded body retained its source XML declaration (encoding=ISO-8859-1/UTF-16); gofeed's CharsetReader then re-decoded the UTF-8 bytes per that stale declaration (Latin-1 mojibake; UTF-16 detection failure). Added canonicalizeXMLDeclEncoding in src/internal/fetch/charset.go to rewrite the leading decl's encoding to UTF-8, applied only to the three successful non-UTF-8 decode rungs (BOM/XML-decl/Content-Type), NOT the lossy fallback (garbage-charset case unchanged, exit 0). New end-to-end test charset_parse_test.go (fetch_test) runs fetched bodies through parse.New() — the gap the byte-level tests missed. Verified live: latin1 item title now '43 61 66 c3 a9...' (Café), utf16le-bom adds+polls exit 0 with items. make build green.
