---
id: fee-d32a
status: open
deps: []
links: []
created: 2026-08-13T14:03:47Z
type: task
priority: 1
assignee: Andre Silva
parent: fee-ui25
tags: [lib, core]
---

# Promote core to the public API surface

First step of [docs/adr/0007-library-and-frontends.md](../docs/adr/0007-library-and-frontends.md).
The ADR makes feedwatch an importable library whose public surface is four
packages: `feedwatch` (the `App`), `feedwatch/core`, `feedwatch/store`, and
`feedwatch/daemon`. `core` must move first because every other public signature
is written in terms of its types.

This ticket is a pure relocation: move `internal/core` to `core`, and pull two
domain types that currently live in soon-to-be-internal packages down into it.
No behavior changes, no envelope changes, no golden changes.

## Why these two extra types move

- `parse.ParsedFeed` moves to `core.ParsedFeed`. The ADR's constructor exposes
  `feedwatch.WithParser`, so the `Parser` port interface becomes public in a
  later ticket. Go's structural typing lets an internal `parse.Parser` and a
  public `feedwatch.Parser` be interchangeable **only if their method sets are
  identical**, and a method returning `parse.ParsedFeed` is not the same method
  as one returning `core.ParsedFeed`. Moving the type is what makes the two
  interfaces assignable without `internal/parse` importing the root package
  (which would be an import cycle).
- `discover.Candidate`, with `discover.SourceAutodiscovery` and
  `discover.SourceProbe`, moves to `core.Candidate` / `core.SourceAutodiscovery`
  / `core.SourceProbe`. `DiscoverResult.Candidates` is a public result field in
  a later ticket, and `internal/discover` stays internal, so an external
  consumer could not name the element type.

`internal/terr` stays internal. `core.FeedError` already exposes `Code()`,
`ExitCode()`, and `Hint()` as methods, so an embedder classifies errors through
`errors.As` on `*core.FeedError` (or its own structurally-identical interface)
and never needs to name `terr.Coded`. A public package importing an internal
one is legal inside the module; only external importers are blocked.

## Design

### 1. Move the package directory

```sh
git mv internal/core core
```

The package clause stays `package core`; only the import path changes, from
`github.com/andreswebs/feedwatch/internal/core` to
`github.com/andreswebs/feedwatch/core`. Rewrite every importer:

```sh
grep -rl 'internal/core' --include='*.go' . \
  | xargs sed -i 's#feedwatch/internal/core#feedwatch/core#g'
gofmt -w .
```

Then fix the import grouping by hand or with `goimports`: the path is still
third-party-shaped, so the existing group placement is unchanged.

### 2. Move `ParsedFeed` into core

Delete the type from `internal/parse/parse.go` and add it to `core/types.go`
(or a new `core/parse.go`), keeping the doc comment:

```go
// ParsedFeed is the normalized result of parsing a feed body: the items mapped
// onto core types, the feed's title, and its declared TTL, when present.
type ParsedFeed struct {
    Title string        // feed title, empty when absent
    TTL   time.Duration // declared poll interval; 0 when absent
    Items []Item
}
```

`internal/parse.Parser` keeps its declaration but now returns `core.ParsedFeed`:

```go
type Parser interface {
    Parse(ctx context.Context, body []byte, baseURL string) (core.ParsedFeed, error)
}
```

Affected files (all `parse.ParsedFeed` references become `core.ParsedFeed`):

```text
internal/parse/parse.go
internal/parse/gofeed.go
internal/parse/parse_test.go
internal/poll/orchestrate.go
internal/poll/orchestrate_test.go
internal/poll/consume_test.go
internal/poll/run_test.go
internal/poll/run_grace_test.go
internal/poll/run_interrupt_test.go
internal/testsupport/parser.go
internal/testsupport/parser_test.go
internal/command/add_test.go
internal/command/check_test.go
internal/command/poll_test.go
internal/command/import_test.go
```

### 3. Move `Candidate` and its source constants into core

From `internal/discover/discover.go` into `core` (a new `core/discover.go` is
the tidiest home), preserving the JSON tags exactly, since they are the
`discover` command's stdout contract:

```go
// Source labels how a candidate feed was found.
const (
    SourceAutodiscovery = "autodiscovery"
    SourceProbe         = "probe"
)

// Candidate is one feed found for a page, validated by parsing. Source tells the
// agent whether the feed was declared by the page (autodiscovery) or guessed
// from a common path (probe).
type Candidate struct {
    Title  string `json:"title,omitempty"`
    URL    string `json:"url"`
    Type   string `json:"type,omitempty"`
    Source string `json:"source"`
}
```

`discover.Discover` now returns `[]core.Candidate`. Update
`internal/discover/discover.go`, `internal/discover/discover_test.go`, and
`internal/command/discover.go`.

### 4. Update the package doc

`core/doc.go` keeps its existing text and gains a sentence stating that this is
a public package of the library surface per ADR 0007, and that it holds the
domain types every public signature is written in terms of.

## TDD notes

This is a refactor, not a feature: the correct discipline is **keep the suite
green at every step**, not write new failing tests first. The existing suite is
the safety net, and it moves with the code.

- Do the four steps above as four separate commits, running `go build ./... &&
  go test ./...` after each. Never leave the tree red between steps.
- `core`'s own tests (`clock_test.go`, `errors_test.go`, `types_test.go`,
  `query_test.go`) move with the package unchanged. If any of them is in
  `package core` (internal test), it stays that way.
- The golden suite in `internal/command` is the behavioral pin: every
  `testdata/**` golden must compare byte-identical with no `-update` run. If a
  golden changes, the move was not behavior-preserving; fix the code, not the
  golden.
- One genuinely new test is worth adding, since it pins the invariant this
  ticket exists to create. In `core/doc_test.go` (package `core_test`), assert
  that `ParsedFeed` and `Candidate` are reachable from outside the package by
  simply constructing them; a compile failure there is the signal that a later
  ticket regressed the surface.

## Acceptance Criteria

- `internal/core` no longer exists; `core` exists at the repository root with
  the same package name and contents, plus `ParsedFeed`, `Candidate`,
  `SourceAutodiscovery`, and `SourceProbe`.
- No `.go` file imports `github.com/andreswebs/feedwatch/internal/core`.
- `internal/parse` no longer declares `ParsedFeed`; `parse.Parser.Parse`
  returns `core.ParsedFeed`.
- `internal/discover` no longer declares `Candidate` or the source constants;
  `discover.Discover` returns `[]core.Candidate`.
- The `discover` command's JSON output is unchanged (the golden for
  `discover` compares byte-identical without `-update`).
- Every `internal/command/testdata/**` golden compares byte-identical with no
  `-update` run.
- `core` imports only the standard library and `internal/terr`; it imports no
  other feedwatch package.
- `make build` passes (`fmt-check`, `vet`, `lint`, `test`, then compile).

## Files

```text
core/**                          (moved from internal/core)
core/discover.go                 (new: Candidate + source constants)
core/doc.go                      (updated: public-surface note)
core/doc_test.go                 (new: external-reachability compile pin)
internal/parse/parse.go          (ParsedFeed removed; Parser signature updated)
internal/parse/gofeed.go
internal/discover/discover.go    (Candidate removed; returns []core.Candidate)
internal/command/discover.go
internal/poll/orchestrate.go
internal/testsupport/parser.go
... plus the mechanical import rewrite across every file listed by
    `grep -rl 'internal/core' --include='*.go' .`
```
