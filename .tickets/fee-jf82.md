---
id: fee-jf82
status: closed
deps: [fee-gyos, fee-171q]
links: []
created: 2026-06-29T19:33:03Z
type: task
priority: 2
assignee: Andre Silva
parent: fee-isds
tags: [cmd]
---

# OPML import command

Implement the import command: parse OPML from a file or stdin, walk outlines recursively, fall back xmlUrl to url, assign free aliases, skip duplicates, and report per-entry results without failing the whole import. Refs: docs/cli-design.md (OPML Interoperability); docs/research/urfave-cli.reference.md.

## Design

The `import` command: add subscriptions from an OPML outline.

```go
&cli.Command{ Name:"import", ArgsUsage:"FILE|-",
  Arguments: []cli.Argument{ &cli.StringArg{Name:"file"} }, Action: importAction }

type ImportResult struct {
  Added   int          `json:"added"`
  Skipped int          `json:"skipped"`
  Failed  []ImportFail `json:"failed"`
}
type ImportFail struct { XMLURL string `json:"xmlUrl"`; Reason string `json:"reason"` }
```

- Read OPML from the file argument or stdin when the arg is `-`.
- Walk outlines recursively (any nesting depth). For each feed outline, read
  `xmlUrl`, falling back to `url` when absent. Use `text`/`title` as the alias
  when free.
- Reuse the `add` upsert path (validation optional/configurable; default add as
  given without re-fetching, to keep import offline and fast). Skip and report
  duplicates. Never fail the whole import on one bad entry; collect failures.
- Report per-entry results as JSON.

TDD plan (OPML fixtures):

1. (tracer) a flat OPML with two feeds imports both; `added:2`.
2. nested folders at depth >1 are traversed; all feeds found.
3. `xmlUrl` missing -> falls back to `url`.
4. a duplicate (already subscribed) is skipped and counted.
5. one malformed entry is reported in `failed` while the rest import.
6. `import -` reads from stdin.

Deep-module note: OPML parsing in `src/internal/opml`; the command wires it to
the store via the add upsert.

## Acceptance Criteria

- `import` reads a file or stdin, walks outlines recursively, falls back
  `xmlUrl`->`url`, assigns free aliases, skips duplicates, and reports per-entry
  results without failing the whole import.
- Behaviors 1-6 covered with OPML fixtures.
- Supports Req 17. `make validate` passes.

## Notes

**2026-06-30T00:01:14Z**

Implemented OPML import. New internal/opml package: Parse(io.Reader) walks outlines recursively and returns []Feed (XMLURL resolved xmlUrl->url, Title resolved text->title) plus []Invalid (feed-typed outlines lacking a URL). Folders (no URL, no feed type) are traversed but not emitted. New import command (internal/cli/import.go): reads file arg or stdin when arg is '-'; dedups against existing subscriptions by URL and assigns the outline title as alias only when free (pre-scanned via ListFeeds, tracked across the run); store-level add failures collected into Failed without aborting; malformed parse entries become Failed entries too. Always exits 0 even with failures; missing/unreadable file or invalid XML is a CatUsage hard failure (exit 1). Added In *os.File to cli.Deps (wired to os.Stdin in main) as the stdin test seam. Envelope: {added,skipped,failed:[{xmlUrl,reason}]}. All 6 ticket behaviors covered (opml_test.go + import_test.go); make build green; live smoke-tested stdin+file+missing paths. Export (fee-nkks) can now round-trip against this.
