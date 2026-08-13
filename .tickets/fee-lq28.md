---
id: fee-lq28
status: closed
deps: [fee-d32a]
links: []
created: 2026-08-13T14:03:47Z
type: task
priority: 1
assignee: Andre Silva
parent: fee-ui25
tags: [lib, store]
---

# Promote the Store interface to a public store package

Second step of
[docs/adr/0007-library-and-frontends.md](../docs/adr/0007-library-and-frontends.md).
The ADR names the store the supported extension point: an embedder must be able
to hand `App` a Postgres, in-memory, or otherwise custom backend. That requires
the `Store` interface to be public while the shipped SQLite adapter stays
internal, so feedwatch commits to the contract without committing to the
implementation.

Depends on `fee-d32a`, since `Store`'s entire signature is written in `core`
types.

## Design

### 1. Move the interface

```sh
git mv internal/store/store.go store/store.go
git mv internal/store/doc.go store/doc.go
git mv internal/store/store_test.go store/store_test.go
```

The package clause stays `package store`; the import path changes from
`github.com/andreswebs/feedwatch/internal/store` to
`github.com/andreswebs/feedwatch/store`. The interface itself is unchanged: the
same fourteen methods over `core` types.

`internal/store/sqlite` stays where it is and now imports the public `store`
package. This is the same legal direction as `core` importing `internal/terr`:
an internal package may import a public one, and a public one may import an
internal one inside the same module. Only external importers are blocked from
`internal/**`.

### 2. Why the interface cannot live in the root package

Worth recording in `store/doc.go`, because it is the question a reviewer will
ask. The root `feedwatch` package imports `internal/store/sqlite` to build the
default store from config. If `Store` were declared in the root package, then
`internal/store/sqlite` (which must reference the interface it satisfies, and
whose `Open` is consumed by the root) would import the root package, and the
root package imports it: an import cycle. A separate `store` package is a
constraint of the dependency graph, not a stylistic choice. The same argument
applies to `core`.

### 3. Update importers

```sh
grep -rl 'internal/store"' --include='*.go' . \
  | xargs sed -i 's#feedwatch/internal/store"#feedwatch/store"#g'
```

Take care not to rewrite `internal/store/sqlite` import paths, which keep their
`internal` prefix. The affected files are:

```text
internal/store/sqlite/sqlite.go          (satisfies store.Store)
internal/store/sqlite/sqlite_test.go
internal/command/storeopen.go
internal/command/resolve.go
internal/command/import.go
internal/testsupport/store.go            (InMemoryStore double)
internal/testsupport/failing_store.go
internal/poll/*.go                       (wherever poll.Deps.Store is typed)
```

### 4. Keep the compliance assertions

Wherever an implementation asserts it satisfies the interface, the assertion
now names the public package. If no such assertion exists today, add one to
each implementation, since the interface is now a compatibility commitment and
a silent drift would only surface at a call site:

```go
var _ store.Store = (*Store)(nil)          // internal/store/sqlite
var _ store.Store = (*InMemoryStore)(nil)  // internal/testsupport
var _ store.Store = (*FailingStore)(nil)   // internal/testsupport
```

### 5. Document the extension point

`store/doc.go` gains the contract an alternative backend must honor, since this
is now the text a third-party implementor reads. Keep it to what the interface
already promises and what the SQLite adapter already does:

- Feed references (`ref`) resolve either an exact URL or a unique alias.
- A `GetFeed` miss returns a usage-category `*core.FeedError`, which is what
  `feedIsNew` in the `add` path relies on to distinguish "not subscribed" from
  a real store failure.
- `UpsertItems` returns only items not previously seen, and preserves dedup
  state across `PruneItems` (tombstones).
- Implementations are safe for concurrent use across distinct feeds.
- Migration methods (`SchemaVersion`, `Pending`, `Migrate`) may be no-ops
  returning zero for a backend with no schema of its own.

## TDD notes

Still a relocation, so the discipline is again "keep the suite green at every
step" rather than red-first. The one place where a genuinely new test is
warranted is the extension point itself, and it is worth writing red-first:

1. **RED**: add `store/store_test.go` (package `store_test`) with a minimal
   third-party-shaped implementation declared in the test file itself, a struct
   embedding nothing and implementing all fourteen methods, asserted with
   `var _ store.Store = (*fakeBackend)(nil)`. Written before the move, it fails
   to compile because the package is not importable from outside.
2. **GREEN**: perform the move; the assertion compiles.

That test is not ceremony: it is the executable statement of "an external
package can implement this interface", which is exactly the promise this ticket
makes, and it will fail loudly if a later ticket moves an argument type back
into `internal/`.

Everything else is covered by the existing suite plus the `internal/command`
goldens, which must compare byte-identical with no `-update` run.

## Acceptance Criteria

- `store` exists at the repository root and holds the `Store` interface and its
  doc; `internal/store/store.go` and `internal/store/doc.go` no longer exist.
- `internal/store/sqlite` still exists and is still internal, and imports
  `github.com/andreswebs/feedwatch/store`.
- No `.go` file imports `github.com/andreswebs/feedwatch/internal/store"`
  (the interface package); imports of `internal/store/sqlite` are unchanged.
- `store/store_test.go` contains an external (package `store_test`)
  implementation of the interface and a compile-time compliance assertion.
- Compile-time compliance assertions exist for the SQLite store, the
  `testsupport` in-memory store, and the failing store double.
- `store/doc.go` documents the behavioral contract an alternative backend must
  honor, including the usage-category `GetFeed` miss and the dedup-preserving
  prune.
- Every `internal/command/testdata/**` golden compares byte-identical with no
  `-update` run.
- `make build` passes.

## Files

```text
store/store.go             (moved from internal/store/store.go)
store/doc.go               (moved; extended with the backend contract)
store/store_test.go        (moved; plus the external-implementation pin)
internal/store/sqlite/*.go (import path update; compliance assertion)
internal/testsupport/store.go
internal/testsupport/failing_store.go
internal/command/storeopen.go
internal/command/resolve.go
internal/command/import.go
internal/poll/*.go
```

## Notes

**2026-08-13T14:30:51Z**

Moved the Store interface out of internal/. store/store.go, store/doc.go, and store/store_test.go now live at the repository root as package store; internal/store/ holds only sqlite/. All 24 importing files were rewritten from feedwatch/internal/store to feedwatch/store (the internal/store/sqlite paths are unchanged, since the sed pattern anchored on the trailing quote). TDD: the external pin went in first and failed with 'no non-test Go files in /workspace/store', then the move made it compile. store/doc.go gained the backend contract a third-party implementor reads: ref resolves URL or unique alias, a GetFeed miss must be a usage-category *core.FeedError (feedIsNew in the add path depends on that category), UpsertItems returns only never-before-seen dedup keys, PruneItems preserves dedup tombstones, concurrent-safe across distinct feeds, migration methods may be no-ops. It also records why the interface cannot live in the root package (root imports internal/store/sqlite, which must name the interface it satisfies: a cycle). Added the missing compile-time assertion for FailingUpsertStore in internal/testsupport/failing_store_test.go; it embeds store.Store so it conformed structurally and would have silently absorbed any method added later. sqlite.Store and InMemoryStore already had theirs. docs/cli-design.md's architecture block now lists store/ as public. Every internal/command/testdata golden compared byte-identical with no -update run; make build green. Next: fee-f3u8 (feedwatch.Config, App skeleton, options constructor) is now unblocked and can type App's store port as store.Store.
