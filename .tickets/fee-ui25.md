---
id: fee-ui25
status: closed
deps: [fee-3p3r]
links: []
created: 2026-08-13T14:03:47Z
type: epic
priority: 1
assignee: Andre Silva
tags: [lib, arch]
---

# Library and thin frontends (ADR 0007)

Implements [docs/adr/0007-library-and-frontends.md](../docs/adr/0007-library-and-frontends.md):
turn feedwatch from one program with one frontend into an importable library
with thin frontends, so a TUI, an HTTP server, an MCP server, and a third-party
program can all consume the same use cases and the same result types.

Today the layer that assembles the domain into a use case does not exist as a
named thing: it is dissolved into the CLI actions, and the result envelope types
are declared in the CLI package. Everything lives under `internal/`, so nothing
is importable. This epic extracts the `App` application service, promotes the
minimum surface needed to consume it, and leaves the CLI as one frontend among
several.

## Public surface when the epic closes

| Package            | Contents                                                     |
| ------------------ | ------------------------------------------------------------ |
| `feedwatch`        | `App`, its constructor and options, request and result types |
| `feedwatch/core`   | domain types, the error category taxonomy, `FeedError`       |
| `feedwatch/store`  | the `Store` interface                                        |
| `feedwatch/daemon` | the poll scheduler                                           |

Everything else stays internal, including the SQLite adapter, the fetcher, the
parser, the poll orchestrator, discovery, OPML, the output renderer, and the CLI
itself.

## Children, in dependency order

1. `fee-d32a` promote `core` to the public API surface
2. `fee-lq28` promote the `Store` interface to a public `store` package
3. `fee-f3u8` `feedwatch.Config`, `App` skeleton, and the options constructor
4. `fee-rzwl` `App` use cases: store-only commands
5. `fee-kj8z` `App` use cases: network commands
6. `fee-gvuo` `App` use cases: OPML import and export
7. `fee-savm` derive CLI flags from request-struct tags by reflection
8. `fee-vbid` `feedwatch/daemon`: the embeddable poll scheduler
9. `fee-3p3r` public API documentation and runnable examples

`fee-vbid` depends only on `fee-kj8z` and can run in parallel with `fee-gvuo`
and `fee-savm`.

## The invariant every child shares

The CLI's observable contract does not change. Every
`internal/command/testdata/**` golden must compare byte-identical with no
`-update` run, at every step, and `feedwatch schema` output must stay stable.
The golden suite drives the framework-free `Run(args, deps)` boundary of ADR
0003, so it is blind to this refactor by construction. If a golden moves, the
extraction was not behavior-preserving.

## Deliberately deferred

The framework-agnostic command registry, from which every frontend's command
surface would be generated. The ADR defers it until a second frontend exists,
because its requirements cannot be known before there is a second projection to
generalize from. Until then the registry is the `App` method set and each
frontend wires its own commands.

## Acceptance Criteria

- All nine children are closed.
- The four public packages exist with the contents above; no other package is
  importable from outside the module.
- `internal/command` contains no domain logic: every action is flag decoding,
  one `App` call, and rendering.
- Every `internal/command/testdata/**` golden compares byte-identical with no
  `-update` run.
- `make build` and `make test-race` pass.

## Notes

**2026-08-13T15:53:34Z**

Epic closed. Verified all five acceptance criteria against the working tree:

1. All nine children closed.
2. Public surface is exactly the four documented packages: 'go list' shows feedwatch, feedwatch/core, feedwatch/store, feedwatch/daemon as the only non-internal importable packages (cmd/feedwatch and cmd/qafixtures are package main). Surface spot-checked with 'go doc -short': App + New + Option (WithStore/WithFetcher/WithParser/WithClock/WithWarner) + Config + the request/result types at the root, Store alone in store, Scheduler in daemon.
3. internal/command holds no domain logic. Its only internal imports are output, terr, and jsonschema; resolve.go, storeopen.go, and storepath.go are gone, and every action is now app() + bind() + one App call + render. The ban is enforced as a test by TestTheCLIHoldsNoDomainCollaborators in the module-root imports_test.go.
4. No tracked internal/command/testdata golden is modified and no -update run was needed; the two untracked testdata dirs (help/, schema/) are new golden sets added by children, not moved ones.
5. make build and make test-race both pass.

One gap closed as epic work: ADR 0007 calls the request-surface table test mandatory because it must walk EVERY request type in the library, but TestRequestSurfaceMapping walked a hand-maintained list of 12. That is satisfied in letter, not in effect: declaring a request type is what admits an unmapped field type, so the failure must land when the type is added, not when a frontend first wires it and panics at command-tree construction. Added TestRequestSurfaceCoverage (internal/command/reflectflags_test.go), which parses the library's own source with go/ast for exported *Request types and requires each to appear in the shared requestSurfaceCases table. The table was extracted into that function so the mapping test and the coverage test share one source of truth.

For the next person: the coverage guard was verified sensitive by deleting the prune row and confirming it failed naming feedwatch.PruneRequest, then restoring it. It uses glob + parser.ParseFile, not parser.ParseDir, which staticcheck rejects as deprecated (SA1019). It also fails when the walk finds no types, so it cannot pass vacuously. Adding a use case now means adding a row to requestSurfaceCases; the guard tells you so by name.

The deliberately deferred item stays deferred: no framework-agnostic command registry. Per the ADR it waits for a second frontend, since its requirements cannot be known before there is a second projection to generalize from.
