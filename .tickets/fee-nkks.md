---
id: fee-nkks
status: closed
deps: [fee-gyos]
links: []
created: 2026-06-29T19:33:03Z
type: task
priority: 2
assignee: Andre Silva
parent: fee-isds
tags: [cmd]
---

# OPML export command

Implement the export command: serialize current subscriptions as valid OPML 2.0 (outlines with xmlUrl and alias as text/title) to a file or stdout, round-tripping with import. Refs: docs/cli-design.md (OPML Interoperability); docs/research/urfave-cli.reference.md.

## Design

The `export` command: write current subscriptions as OPML 2.0.

```go
&cli.Command{ Name:"export", Action: exportAction,
  Flags: []cli.Flag{ &cli.StringFlag{Name:"o", TakesFile:true} } }
```

- `store.ListFeeds` -> emit a valid OPML 2.0 document: each feed an `<outline>`
  with `type="rss"`, `xmlUrl`, and `text`/`title` from the alias or URL.
- Write to the `-o` file when given, else stdout.
- If per-feed tags are ever added, round-trip them via the OPML `category`
  attribute (not in scope now; documented for forward-compat).

TDD plan (temp db seeded + parse the emitted OPML back):

1. (tracer) export of two feeds yields OPML with two outlines carrying `xmlUrl`.
2. aliases populate `text`/`title`.
3. `-o FILE` writes to the file; without it, to stdout.
4. the emitted OPML re-imports cleanly (round-trip with the import command).

Deep-module note: OPML serialization in `src/internal/opml`; export is a thin
read-only command.

## Acceptance Criteria

- `export` writes valid OPML 2.0 (outlines with `xmlUrl`, alias as text/title) to
  `-o FILE` or stdout; round-trips with import.
- Behaviors 1-4 covered.
- Supports Req 17. `make validate` passes.

## Notes

**2026-06-30T00:06:24Z**

Implemented OPML 2.0 export. Serialization (opml.Write) lives in internal/opml (deep-module split, mirrors opml.Parse); the export command (internal/cli/export.go) is a thin read-only wrapper. Key decisions: (1) OPML is the result PAYLOAD, written raw to the -o file or to r.Out (stdout), NOT wrapped in the JSON envelope -- matches the doc's 'feedwatch export | curl' usage and is required for the import round-trip. (2) opml.Write uses encoding/xml marshaling (not hand-built strings) so attribute escaping of &/</" in titles and URLs is correct; verified by TestWriteEscapesSpecialChars. (3) Outline text/title = alias when set, else the URL (OPML requires a text attr); on round-trip an aliasless feed's URL-as-title becomes the new alias, an accepted minor quirk. (4) encoding/xml emits `<outline></outline>` not self-closing `<outline/>`, valid OPML and parses fine. exportStore mirrors listStore (store-only deps, no fetcher/parser); exportDest opens the -o file (unwritable -> CatUsage exit 1). 4 cli behaviors + 3 opml.Write tests (round-trip, escaping, empty). make build green; live round-trip smoke verified (export -o then import into a fresh db preserves URL+alias). This was the last open child of E7 (fee-isds), which is now closeable.
