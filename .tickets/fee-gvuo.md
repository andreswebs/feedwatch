---
id: fee-gvuo
status: open
deps: [fee-kj8z]
links: []
created: 2026-08-13T14:03:47Z
type: task
priority: 1
assignee: Andre Silva
parent: fee-ui25
tags: [lib, app]
---

# App use cases: OPML import and export

Sixth step of
[docs/adr/0007-library-and-frontends.md](../docs/adr/0007-library-and-frontends.md).
Moves the last two use cases into `App` and retires the CLI's collaborator
resolver. After this ticket every CLI action is flag decoding, one `App` call,
and rendering, and `internal/command` holds no domain logic.

The interesting question here is where the filesystem boundary sits, since
`import` reads a file or stdin and `export` writes a file or stdout.

## Design

### 1. The library never touches the filesystem

Reading the OPML source and writing the OPML document are frontend concerns: a
CLI reads a path or stdin, an HTTP server reads a request body and writes a
response body, a TUI may do neither. So the use cases exchange bytes, and the
frontend owns the I/O:

```go
func (a *App) Import(ctx context.Context, req ImportRequest) (ImportResult, error)
func (a *App) Export(ctx context.Context, req ExportRequest) (ExportResult, error)

type ImportRequest struct {
    OPML     []byte // the OPML document itself
    Validate bool   // fetch and parse each feed before subscribing
}

type ExportRequest struct{}

// ExportResult carries the OPML 2.0 document. It is not a JSON envelope: the
// document is the result payload, and a frontend writes it verbatim.
type ExportResult struct {
    OPML string
}
```

Note the polarity of `ImportRequest.Validate`: the CLI flag is the negative
`--no-validate`, and the CLI inverts it (`Validate: !cmd.Bool("no-validate")`),
exactly as `importAction` does today. Validation stays the default.

`ExportResult` deliberately has no `Head`: export's output is the OPML document,
not a JSON envelope, which is why `schemaRegistry` describes it as
`jsonschema.Scalar("string", ...)`. That entry is unchanged.

### 2. Import

`App.Import` absorbs `importAction`'s body plus `importFeeds`,
`importCandidate`, `importOpts`, and `validateCandidates`. It parses the
document with `opml.Parse`, and keeps the three-phase structure, which exists to
make concurrency safe without making results order-dependent:

1. Sequential classification against existing subscriptions and URL syntax
   (dedup by URL, reserve OPML-internal duplicates, reject non-http(s) entries).
2. Concurrent validation of the surviving candidates at `Config.Concurrency`,
   where one failure never cancels another.
3. Sequential subscription, so alias assignment stays order-stable.

An unparseable document stays a usage-category error with the current message,
`import source is not a valid OPML document`. A failure to list existing
subscriptions stays a hard error, since dedup and alias decisions depend on it.
Per-entry failures never abort the import.

`validateParsesAsFeed` and `isAbsoluteHTTPURL` are already in the root package
from `fee-kj8z`; reuse them rather than reintroducing copies.

### 3. Export

`App.Export` reads every subscription, maps each to an `opml.Feed` with
`exportTitle`'s rule (alias when set, else URL), and renders the document with
`opml.Write` into a buffer. The CLI then writes `res.OPML` to the `-o` file or
to stdout, keeping `exportDest` and its usage-category "cannot create OPML file"
error in the frontend.

### 4. CLI rewiring and resolver removal

`importAction` keeps `importSource` (stdin when the argument is `-`, otherwise
`os.Open`), reads it to a `[]byte`, and passes it in. The current
usage-category errors for a missing argument and an unopenable file stay in the
CLI verbatim.

With these two actions moved:

- Delete [internal/command/resolve.go](../internal/command/resolve.go) and
  [internal/command/storeopen.go](../internal/command/storeopen.go); their
  logic now lives in the App.
- Delete the unexported `store`, `fetch`, and `parse` fields from
  `command.Deps`. Tests that injected through them now inject through
  `WithStore`, `WithFetcher`, and `WithParser`. `Deps` returns to the lean
  contract ADR 0003 describes: `In`, `Out`, `Err`, `Clock`, `Version`, `Signal`,
  plus the options the tests supply.

  The mechanism: give `Deps` one unexported field holding extra
  `feedwatch.Option` values (`opts []feedwatch.Option`), which the same-package
  tests set and `Deps.app` appends. That keeps a single test seam instead of
  three typed ones, and it is expressed in public library types.

- Audit `internal/command` for leftovers: after this ticket it must not import
  `internal/store/sqlite`, `internal/fetch`, `internal/parse`, `internal/poll`,
  `internal/discover`, or `internal/opml`.

## TDD notes

Two vertical slices, `export` first (no network, smaller surface), then
`import`.

For each: write the library test red against an injected in-memory store, move
the logic green, then rewire the action and delete the dead code. Do the
resolver deletion as its own commit after both use cases are green, so a
breakage is attributable.

Behaviors worth a test each:

- `Export` on an empty store produces a valid OPML document with no outlines.
- `Export` labels an outline with the alias when set and the URL when not.
- An `Export` document round-trips through `Import` with no additions and no
  failures (every feed is already subscribed, so all are skipped).
- `Import` of a document that is not OPML returns a usage-category error.
- `Import` skips an already-subscribed URL, counting it in `skipped`, not
  `failed`.
- `Import` skips a duplicate that appears twice inside the same document.
- `Import` records a non-http(s) `xmlUrl` in `failed` with the current reason,
  and keeps importing the remaining entries.
- `Import` assigns the outline text as alias only when that alias is free.
- `Import` with `Validate: true` records a feed that does not parse in `failed`
  and does not subscribe it; with `Validate: false` the same feed is added.
- `ImportResult.Failed` marshals as `[]`, never null, when nothing failed.
- An entry that `opml.Parse` reports as invalid appears in `failed` with an
  empty `xmlUrl`.

Then the CLI-level checks, which the existing suite already covers and which
must keep passing untouched: reading from `-` uses `Deps.In`, a missing
argument is exit 64, an unopenable file is exit 64, and `-o` writes the document
to the named file.

Every `internal/command/testdata/**` golden, `testdata/opml/**` in particular,
must compare byte-identical with no `-update` run.

## Acceptance Criteria

- `Import` and `Export` exist on `*App` with the signatures given.
- `ImportResult`, `ImportFail`, and `ExportResult` live in the root package;
  `ImportResult` and `ImportFail` no longer exist in `internal/command`.
- The library performs no filesystem I/O for either use case: `os.Open`,
  `os.Create`, and stdin handling remain in `internal/command`.
- `internal/command/resolve.go` and `internal/command/storeopen.go` no longer
  exist.
- `command.Deps` has no `store`, `fetch`, or `parse` fields; test injection goes
  through `feedwatch.Option` values.
- `internal/command` imports none of `internal/store/sqlite`, `internal/fetch`,
  `internal/parse`, `internal/poll`, `internal/discover`, `internal/opml`.
- Every `internal/command/testdata/**` golden compares byte-identical with no
  `-update` run.
- `make build` passes.

## Files

```text
import.go, export.go                    (new, root package)
import_test.go, export_test.go          (new, root package)
internal/command/import.go, export.go   (reduced to I/O + call + render)
internal/command/run.go                 (Deps loses the three port fields, gains opts)
internal/command/resolve.go             (deleted)
internal/command/storeopen.go           (deleted)
internal/command/*_test.go              (injection through feedwatch.Option)
```
