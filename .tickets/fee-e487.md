---
id: fee-e487
status: closed
deps: [fee-63n9, fee-chr5]
links: []
created: 2026-06-29T18:45:23Z
type: task
priority: 1
assignee: Andre Silva
parent: fee-2heq
tags: [parse]
---

# Parser: gofeed wrapper and format coverage

Implement parse.Parser over mmcdole/gofeed: RSS 0.9x/1.0/2.0, Atom 1.0, JSON Feed from one parser, lenient on malformed XML, resolving CDATA/entities and mapping feed TTL, returning core.ParseErr on failure. Raw field mapping only; normalization and dedup-key are separate tickets. Refs: docs/cli-design.md (Parsing and Robustness).

## Design

Package `src/internal/parse`. Implements `parse.Parser` over
`github.com/mmcdole/gofeed`.

```go
type GofeedParser struct{ /* fp *gofeed.Parser */ }

func New() *GofeedParser
func (p *GofeedParser) Parse(ctx context.Context, body []byte, baseURL string) (parse.ParsedFeed, error)
```

- gofeed auto-detects RSS 0.9x/1.0/2.0, Atom 1.0, and JSON Feed from one parser
  (`fp.Parse(bytes.NewReader(body))`).
- Tolerates malformed XML (gofeed is lenient); CDATA and entities are resolved by
  gofeed.
- Maps the feed TTL (`<ttl>` / equivalent) into `ParsedFeed.TTL`.
- Produces `[]core.Item` with raw fields populated; full normalization (dates,
  precedence, base URL, MIME) is the normalization ticket, and the dedup key is
  the dedup-key ticket. This ticket wires the raw mapping and error handling.
- A parse failure returns `core.ParseErr(baseURL, err)`.

TDD plan (real feed fixtures committed under testdata/):

1. (tracer) a valid RSS 2.0 fixture parses to N items with titles and links.
2. an Atom 1.0 fixture parses; entries map to items.
3. a JSON Feed fixture parses (confirms format coverage is free).
4. a malformed-but-recoverable XML fixture still yields items.
5. a totally invalid body returns a `*FeedError` with `CatParse`.

Deep-module note: callers depend on `parse.Parser`; gofeed is hidden and
swappable. Fixtures mirror the corpus seeded by the E9 harness.

## Acceptance Criteria

- `GofeedParser` implements `parse.Parser`; covers RSS 0.9x/1.0/2.0, Atom 1.0,
  JSON Feed; lenient on malformed XML; resolves CDATA/entities; maps TTL.
- Parse failure returns `core.ParseErr` (`CatParse`).
- Behaviors 1-5 covered with real fixtures.
- Supports Req 14 (and the JSON Feed coverage note). `make validate` passes.

## Notes

**2026-06-29T21:28:09Z**

Implemented GofeedParser (internal/parse/gofeed.go) over mmcdole/gofeed v1.3.0 (first ticket to add the dep; now a direct require in src/go.mod). New()/Parse() satisfy parse.Parser (explicit var _ Parser conformance). Raw field mapping only: GUID/Title/Link/Description->Summary/Content->ContentHTML/Categories/PublishedParsed/UpdatedParsed; author from Authors[0].Name (NOT deprecated .Author, which trips staticcheck SA1019); enclosure Length string->int64 via parseLength. Precedence cascades + dates/base-URL/MIME deferred to fee-kx39; dedup key to fee-lzyw. Parse failure -> core.ParseErr (CatParse) scoped to baseURL. KEY GOTCHA: the universal gofeed.Feed DROPS RSS `<ttl>` (only rss.Feed.TTL has it); extractTTL re-parses RSS bodies with &rss.Parser{} to recover `<ttl>` minutes, returns 0 for atom/json. ctx param unused (gofeed.Parse from reader is synchronous, in-memory, fast). 6 behavior tests over real testdata/ fixtures (rss2/atom/feed.json/malformed/invalid) via embed.FS (avoids gosec G304 on variable path - cleaner than //nolint). malformed.xml uses a raw unescaped & that strict encoding/xml rejects but gofeed recovers, verified. make build green, passes -race.
