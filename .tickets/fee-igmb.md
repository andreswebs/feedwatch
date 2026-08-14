---
id: fee-igmb
status: open
deps: [fee-pfpz, fee-o5uq, fee-frus]
links: []
created: 2026-08-14T02:51:32Z
type: task
priority: 2
assignee: Andre Silva
parent: fee-zs6b
tags: [cmd, tags]
---
# OPML tags round-trip

Write feed tags as the OPML category attribute on export (omitempty so untagged feeds are unchanged), add --tag/--match to export (including the missing bind call), and read category back on import. One ticket because a half-implemented round-trip loses data silently.

## Design

Make tags survive an OPML round-trip: `export` writes them as the standard
`category` attribute, `export --tag` filters, and `import` reads them back.

Export and import are one ticket deliberately. A half-implemented round-trip
loses data silently: `export` would emit an attribute nothing reads, and the
existing `TestExportRoundTripsWithImport` would still pass while the lane
assignment evaporated.

Spec: [docs/specs/003-feed-tags/plan.md](docs/specs/003-feed-tags/plan.md),
sections "`export` — OPML per lane" and "`import` — OPML tags round-trip".

## Encoding

OPML 2.0 defines `category` as a comma-separated list of slash-delimited
paths. Tags cannot contain commas (rejected at write time by
`core.ValidateTags`), so a plain comma join is unambiguous and needs no
escaping. Slash-delimited hierarchy is **not** interpreted: a tag containing a
slash is one tag.

```xml
<outline type="rss" text="example" title="example"
         xmlUrl="https://example.com/feed" category="agents,ai"/>
```

## 1. `internal/opml/opml.go` — write side

- Add `Category string \`xml:"category,attr,omitempty"\`` to `exportOutline`.
  **`,omitempty` is required**: without it, every untagged feed emits
  `category=""`, which changes`internal/command/testdata/opml/export.stdout`
  for feeds that have nothing to do with this feature.
- Attribute order in `encoding/xml` output follows struct field order, so
  append `Category` **after** `XMLURL` to keep the existing attributes in their
  current positions and the golden diff minimal.
- Add `Tags []string` to `opml.Feed` and join it in `Write`.

## 2. `internal/opml/opml.go` — parse side

- Add `Category string \`xml:"category,attr"\`` to `outline`.
- Split on commas in `walk`, populating `Feed.Tags`. Trim each element and drop
  empties, so `category="ai, agents,"` yields `["ai","agents"]` before
  canonicalization.
- **Do not** inherit tags from enclosing folder outlines. `walk` threads no
  parent context today, and adding an ancestry parameter to make folders imply
  tags is a real feature with its own semantics (does a nested folder append or
  replace?). Out of scope; note it in the ticket when closing.

## 3. `export --tag`

```go
type ExportRequest struct {
	Tags  []string `flag:"tag" usage:"export only feeds carrying this tag (repeatable); all feeds when omitted"`
	Match string   `flag:"match" default:"all" usage:"multi-tag semantics: 'all' (default) or 'any'"`
}
```

**The `bind` trap applies here.** `internal/command/export.go`'s action passes
`feedwatch.ExportRequest{}` literally and never calls `bind` — `export` and
`list` are the only two commands that do this, because their request structs
were empty. Add the standard `var req; bind(cmd, &req)` or the flags will
appear in `--help` and `schema`, parse cleanly, and be silently discarded.

`App.Export` passes the filter to `ListFeeds` and sets `opml.Feed.Tags` from
each feed.

`ExportRequest` already has a `Validate` method; route it through the shared
`tagFilter` helper.

## 4. `import` tag mapping

`App.Import` runs three phases: sequential classification into
`[]importCandidate`, optional concurrent validation, then sequential
subscribe. Carry tags through:

- add a `tags []string` field to the unexported `importCandidate` struct;
- populate it from `feed.Tags` in the classification loop, canonicalized and
  validated — an outline carrying an invalid tag should have that tag dropped
  rather than failing the whole outline, since OPML comes from foreign tools;
- pass it as `core.Feed{URL:, Alias:, Tags:}` to `st.AddFeed` in the subscribe
  loop.

Because the store's upsert sets tags only on create, importing an OPML that
names an already-subscribed feed will **not** overwrite its tags. That is
consistent with `add`'s "omitted preserves" rule and with import already
reporting such feeds as `skipped`.

Consider whether `import` should also accept a `--tag` flag applying to every
imported feed (a natural "import this OPML into lane X"). It is **not** in
scope for this ticket; if it seems obviously right while implementing, file a
follow-up rather than widening the diff.

## TDD plan

Package tests in `internal/opml/opml_test.go` (fixtures are inline Go string
literals; there is no `testdata/` directory in that package). Library tests in
`export_test.go` and `import_test.go` (which parse output back with
`opml.Parse` rather than string-matching). CLI tests in
`internal/command/export_test.go` and `import_test.go`.

1. **(tracer)** `opml.Write` emits `category="agents,ai"` for a tagged feed and
   **no** `category` attribute at all for an untagged one.
2. `opml.Parse` reads `category="ai, agents"` into `Tags: ["ai","agents"]`,
   trimming and dropping empties.
3. `App.Export` sets each outline's tags from the feed.
4. `export --tag ai` exports only in-lane feeds; the untagged feed is absent.
5. `App.Import` assigns tags from `category`, canonicalized.
6. An outline with an invalid tag (`category="ai,two words"`) imports with the
   valid tags only, and the feed is still subscribed.
7. **Full round-trip**: tag two feeds differently, `export`, `import` into a
   fresh store, and assert both feeds' tags match the originals exactly. Extend
   the existing `TestExportRoundTripsWithImport` rather than writing a parallel
   one.
8. Importing an OPML naming an already-subscribed feed leaves its existing tags
   alone.
9. `export --match bogus` is a usage error, exit 64.

## Golden fallout

`testdata/schema/{export,all}.stdout` and `testdata/help/export.stdout` (new
flags). `testdata/opml/export.stdout` should be **unchanged** because its
scenario feed has no tags and `Category` is `omitempty` — verify this
explicitly; if it changed, the `,omitempty` was omitted.

## Gotchas

- `internal/opml` is internal and must stay internal (ADR 0007 fixes the public
  surface at four packages). Do not export a tag type from it.
- Go's `encoding/xml` emits `<outline></outline>`, never self-closing. Do not
  fight it; the golden already reflects this.
- `export`'s payload is the document itself, not a JSON envelope — it is the
  one sanctioned exception to the stdout rule. Do not add a `Head`.

## Acceptance Criteria

- `opml.Feed` carries `Tags`; `Write` emits a comma-joined `category`
  attribute with `,omitempty`, and `Parse` reads it back, trimming and dropping
  empty elements.
- `ExportRequest` carries `Tags`/`Match`, and
  `internal/command/export.go`'s action calls `bind`.
- `export --tag` filters the exported set; `App.Import` assigns tags from
  `category`, dropping individually invalid tags without failing the outline.
- A full export/import round-trip preserves each feed's tags exactly, proven by
  extending `TestExportRoundTripsWithImport`.
- Importing an already-subscribed feed does not overwrite its tags.
- Folder-outline tag inheritance is not implemented, and that is noted on
  close.
- Behaviors 1-9 covered across the opml, library, and command test files.
- `testdata/opml/export.stdout` is confirmed unchanged;
  `schema/{export,all}.stdout` and `help/export.stdout` are regenerated.
- `make build` passes.
