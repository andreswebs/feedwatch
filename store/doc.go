// Package store defines the Store interface over core domain types: feed and
// item persistence, the dedup upsert, history queries, and retention. Concrete
// implementations live in their own packages so each can be replaced by a test
// double.
//
// store is a public package of the library surface described in ADR 0007, and
// the supported extension point: an embedder hands App a Postgres, in-memory,
// or otherwise custom backend by implementing this interface. The shipped
// SQLite adapter stays internal, so feedwatch commits to the contract without
// committing to the implementation.
//
// The interface cannot live in the root feedwatch package. The root package
// imports the SQLite adapter to build the default store from configuration, and
// the adapter must name the interface it satisfies; declaring Store at the root
// would make those two packages import each other. The separate package is a
// constraint of the dependency graph, not a stylistic choice. The same argument
// applies to core.
//
// # Backend contract
//
// An alternative backend honors the behavior the interface promises and the
// SQLite adapter provides:
//
//   - A feed reference (ref) resolves either an exact feed URL or a unique
//     alias.
//   - A GetFeed miss returns a usage-category *core.FeedError. The add path
//     relies on that category to tell "not subscribed" apart from a real store
//     failure, so a miss reported as a bare error or a store-category error
//     turns a fresh subscription into a command failure.
//   - UpsertItems is atomic per item and returns only the items whose dedup key
//     was never recorded for that feed before, so a second poll of unchanged
//     content returns nothing.
//   - PruneItems deletes item rows but preserves each pruned item's dedup
//     state, so a pruned item a feed still advertises is never re-emitted as
//     new.
//   - Implementations are safe for concurrent use across distinct feeds, which
//     never share rows.
//   - The migration methods (SchemaVersion, Pending, Migrate) may be no-ops
//     returning zero for a backend with no schema of its own.
//
// The test doubles feedwatch uses internally are not published, so an implementor
// writes their own against the contract above.
//
// For the stability commitment this package carries, see the feedwatch package
// documentation.
package store
