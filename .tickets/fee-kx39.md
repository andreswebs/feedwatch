---
id: fee-kx39
status: closed
deps: [fee-e487]
links: []
created: 2026-06-29T18:45:23Z
type: task
priority: 1
assignee: Andre Silva
parent: fee-2heq
tags: [parse]
---

# Item normalization (dates, precedence, base URL, MIME)

Normalize parsed items into core.Item: content/summary/author precedence, fixed-width UTC dates with the null-on-unparseable rule, base-URL resolution from xml:base, MIME capture, and multi-enclosure preservation. Refs: docs/cli-design.md (Item Model and Content, Parsing and Robustness).

## Design

Normalize parsed items into the documented `core.Item` shape.

Field precedence and rules:

- Content: `content_html` from `content:encoded` or Atom `content`, falling back
  to the description when content is empty. `summary` from the description or
  iTunes summary. `content_text` derived from `content_html` (tags stripped).
- Author cascade: item author -> feed `managingEditor` -> Dublin Core
  `dc:creator`.
- Dates: parse to fixed-width RFC3339 UTC; an unparseable date yields a nil
  `PublishedAt` (never fabricated). `UpdatedAt` likewise nil when absent.
- `BaseURL`: from `xml:base`, else the item link, else the feed URL (used to
  resolve relative links later).
- `ContentMIMEType`: from the source content type where the feed provides it.
- `Categories`/`Enclosures`: copied through; multiple enclosures preserved.

```go
func Normalize(raw RawItem, feedBase, feedURL, feedAuthor string) core.Item
// RawItem is the parser's intermediate (gofeed item + extensions).
```

TDD plan (table-driven over crafted raw items):

1. (tracer) `content:encoded` present -> `content_html` uses it; empty content
   falls back to description.
2. unparseable pubdate -> `PublishedAt == nil` (not fabricated).
3. author cascade picks item author, else managingEditor, else `dc:creator`.
4. `xml:base` populates `BaseURL`; absent -> item link; absent -> feed URL.
5. multiple enclosures are all preserved.
6. `content_text` is the de-tagged form of `content_html`.

Deep-module note: pure mapping function; deterministic and table-tested.

## Acceptance Criteria

- Implements the documented content/summary/author precedence, UTC/null date
  rule, base-URL resolution, MIME capture, and multi-enclosure preservation.
- `PublishedAt` is nil (never fabricated) for unparseable dates.
- Behaviors 1-6 covered table-driven.
- Supports Req 12 and the date part of Req 14. `make validate` passes.

## Notes

**2026-06-29T22:02:32Z**

Implemented parse.Normalize(raw RawItem, feedBase, feedURL, feedAuthor) core.Item as a pure, table-tested mapping and wired it into GofeedParser.Parse (replacing the raw mapItem). Covers: content_html from content:encoded/Atom content falling back to description; summary from description else iTunes summary; author cascade item->feed managingEditor->dc:creator; dates normalized to UTC with nil-on-unparseable (never fabricated); base URL precedence xml:base->item link->feed link(feedBase)->feed URL; ContentMIMEType passthrough; content_text de-tagged via golang.org/x/net/html (now a direct dep). Behaviors 1-6 table-tested in normalize_test.go. NOTE: gofeed's universal model drops an entry's xml:base AND the Atom content type, so on the gofeed path RawItem.XMLBase/ContentType are always empty and BaseURL effectively resolves to item link->feed link->feed URL; Normalize still implements the full precedence so a stricter parser can populate them. FeedURL and DedupKey remain unset here (store sets FeedURL in UpsertItems; poll/fee-q6t3 sets DedupKey via parse.DedupKey). make build green (lint 0 issues).
