---
id: fee-3p3r
status: open
deps: [fee-savm, fee-vbid]
links: []
created: 2026-08-13T14:03:47Z
type: task
priority: 2
assignee: Andre Silva
parent: fee-ui25
tags: [lib, docs]
---

# Public API documentation and runnable examples

Final step of
[docs/adr/0007-library-and-frontends.md](../docs/adr/0007-library-and-frontends.md).
The library is only usable if its surface is documented as a surface rather
than as an implementation detail of the CLI. This ticket writes the godoc, the
runnable examples, and the user-facing documentation, and states the stability
commitment the four public packages now carry.

Depends on `fee-savm` and `fee-vbid`, so the documented surface is the final
one.

## Design

### 1. Godoc for the four public packages

Every exported identifier needs a doc comment beginning with its own name. Beyond
mechanical coverage, each package doc must answer "why does this package exist
and what does it promise":

- **`feedwatch`** (`doc.go`): the library is the substance and the CLI is one
  frontend; the four public packages and what each is for; the port model
  (store, fetcher, parser, clock, warner) and which are extension points; the
  lifecycle rule that `New` performs no I/O, the store opens lazily, and `Close`
  releases only what the App opened; the result-envelope contract, with
  `SchemaVersion` as its version; and the error model, where every failure
  carries a `core.Category` from which a frontend derives its own encoding.
- **`core`**: the domain types and the error taxonomy; that `*core.FeedError`
  exposes `Code`, `ExitCode`, and `Hint` so an embedder never needs the internal
  `terr` package.
- **`store`**: already extended in `fee-lq28` with the backend contract; verify
  it still matches the shipped adapter's behavior after all the moves.
- **`daemon`**: written in `fee-vbid`; verify it states the wake-cadence
  semantics.

### 2. Runnable examples

In `package feedwatch_test`, so they compile against the same import path an
embedder uses:

- `ExampleNew`: build an `App` from `Defaults()` with an explicit `Config.Store`
  in a temp directory, then `Close`.
- `ExampleApp_Add` and `ExampleApp_Poll`: the subscribe-then-poll arc, the
  smallest useful program.
- `ExampleApp_Items`: query history with a time window and a field projection,
  including `ItemsRequest.Envelope`.
- `ExampleWithStore`: a custom `store.Store`, the documented extension point.
  It only needs to compile, so a stub implementation in the example file is
  enough to show the shape.
- `ExampleApp_Poll_errors`: classifying a failure by `core.Category` rather than
  by string matching, which is the pattern a frontend needs.
- One `daemon` example in `package daemon_test`: embed a scheduler, range over
  `Events()`, and stop by cancelling the context.

Examples whose behavior depends on the network or the wall clock carry **no**
`// Output:` comment, so the toolchain compiles them without running them. At
least one example must be genuinely runnable with a deterministic `// Output:`
line, so the example suite is not entirely compile-only; `Defaults()` field
values or an `Items` query against an empty temp store are both deterministic.

### 3. User documentation

- **`docs/library.md`** (new): how to embed feedwatch. The four packages and
  the stability statement; constructing an `App` and the lifecycle; a table of
  use cases mapping method to request and result type; the error model with a
  category-to-handling table; implementing a custom store; embedding the
  daemon. Task-oriented, not a godoc transcript.
- **`README.md`**: a short "Use as a library" section with the smallest working
  snippet and a link onward to `docs/library.md`. The CLI stays the headline.
- **`AGENTS.md`**: add a row to the reference table pointing at
  `docs/library.md`, and update the one-line description of the project if it
  still implies CLI-only.
- **`docs/adr/0007-library-and-frontends.md`**: no rewrite. If implementation
  diverged from the ADR anywhere, record the divergence in the learnings file
  rather than silently editing the decision.
- **`CHANGELOG.md`**: one entry covering the whole epic, written from the
  consumer's point of view: feedwatch is now importable; the CLI contract is
  unchanged; the four public packages; the pre-1.0 stability caveat.
- **`docs/specs/001-initial-implementation/learnings.md`**: append the
  non-obvious findings from the epic under a heading for it. Candidates already
  visible from the plan: why `core` and `store` cannot live in the root package
  (import cycles through the adapters), why `ParsedFeed` had to move for the
  public `Parser` port to be assignable, and why `New` must not open the store
  eagerly (`discover` would start creating database files).

### 4. Stability statement

State it once, in `doc.go`, and reference it from `docs/library.md` and the
README rather than restating it:

- The four public packages are the supported surface; anything under
  `internal/` is not, and may change without notice.
- The project is pre-1.0: the Go API may change with a minor version bump, and
  such changes are called out in the changelog.
- The JSON output contract is versioned independently by `SchemaVersion`, per
  ADR 0005, and a breaking envelope change bumps it.

### 5. Explicitly out of scope

Publishing the test doubles as a `feedwatchtest` package. The ADR lists it as a
later nice-to-have, contingent on someone actually implementing a backend. Note
it in `docs/library.md` as "not yet published; implementors write their own
doubles against the documented contract" so the omission reads as a decision.

## TDD notes

Documentation has no red-green loop, but it does have executable parts, and
those come first:

1. Write each example as a compiling test before writing the prose that
   references it. An example that does not compile is the documentation bug
   this ordering prevents.
2. `go vet ./...` (part of `make build`) checks example naming, so a typo'd
   `ExampleApp_Poll` surfaces in the gate rather than silently never running.
3. `go doc github.com/andreswebs/feedwatch` and `go doc -all` for each of the
   four packages, read end to end. Every exported identifier must have a
   comment starting with its name; any that does not is a defect to fix in this
   ticket.
4. Markdown, per the project rule: fix first, then validate.

   ```sh
   markdownlint-cli2 --fix 'docs/**/*.md' '*.md'
   markdownlint-cli2 'docs/**/*.md' '*.md'
   ```

   The second command must report `0 error(s)`.
5. Every snippet in `docs/library.md` and the README must be copied from a
   compiling example, not hand-written. Where a snippet is abridged, keep the
   full version in the example file it came from.

## Acceptance Criteria

- Every exported identifier in `feedwatch`, `core`, `store`, and `daemon` has a
  doc comment beginning with its name.
- Package docs for all four explain purpose and promises, not just contents.
- The examples listed in section 2 exist and compile; at least one has a
  deterministic `// Output:` comment and passes.
- `docs/library.md` exists and covers the packages, lifecycle, use-case table,
  error model, custom store, and daemon embedding.
- `README.md` has a "Use as a library" section linking to it.
- `AGENTS.md`'s reference table includes `docs/library.md`.
- `CHANGELOG.md` has an entry for the epic.
- The learnings file has an appended section for this epic including the three
  findings named above.
- The stability statement appears in `doc.go` and is referenced, not restated,
  elsewhere.
- No document references a local filesystem path outside the repository.
- `markdownlint-cli2` reports `0 error(s)` for all changed Markdown.
- `make build` passes.

## Files

```text
doc.go                                             (stability statement, package doc)
core/doc.go, store/doc.go, daemon/doc.go           (package docs reviewed and extended)
example_test.go                                    (new, package feedwatch_test)
daemon/example_test.go                             (new, package daemon_test)
docs/library.md                                    (new)
README.md                                          (library section)
AGENTS.md                                          (reference table row)
CHANGELOG.md                                       (epic entry)
docs/specs/001-initial-implementation/learnings.md (appended)
```
