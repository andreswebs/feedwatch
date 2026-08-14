---
id: fee-c6fa
status: open
deps: []
links: []
created: 2026-08-14T02:42:09Z
type: task
priority: 1
assignee: Andre Silva
parent: fee-zs6b
tags: [store, tags]
---
# sqlite: migration 0002 adds feeds.tags

Add internal/store/sqlite/migrations/0002_feed_tags.sql adding a tags TEXT NOT NULL DEFAULT '[]' column to feeds, and prove an existing v1 store migrates in place with existing feeds landing on '[]'.

## Design

Add the `tags` column to the `feeds` table. Independent of every other ticket:
it touches only SQL and the migration applier's test suite, and no Go code reads
the column yet (T3 does that).

Spec: [docs/specs/003-feed-tags/plan.md](docs/specs/003-feed-tags/plan.md),
section "Schema migration".

## New file: `internal/store/sqlite/migrations/0002_feed_tags.sql`

```sql
ALTER TABLE feeds ADD COLUMN tags TEXT NOT NULL DEFAULT '[]';
```

**Nothing else needs editing to register it.** `migrate.go` embeds
`//go:embed migrations/*.sql`, and `loadMigrations` derives the version from the
`NNNN_` filename prefix with `strings.Cut` plus `strconv.Atoi`, sorts ascending,
and applies each unapplied file in its own transaction. `maxVersion` becomes 2
automatically, which is also what arms the existing `core.ErrSchemaTooNew` guard
(`migrate.go`, the `current > codeMax` branch) for an old binary meeting a
migrated store. There is no Go-side list, no init hook, no manifest.

Storage rationale: a JSON array string mirrors `items.categories` and
`items.enclosures`, which are `TEXT NOT NULL DEFAULT '[]'` in `0001_init.sql`.
SQLite's `ALTER TABLE ... ADD COLUMN` requires a constant non-null default, and
`'[]'` qualifies, so the migration is a single statement with no table rebuild.

## Deliberately not doing

- No index on `tags`. Feed counts are small (tens to low hundreds) and SQLite
  cannot index into a JSON array without an expression index over
  `json_each`, which is not worth it before a measured problem. Note this in
  the ticket notes when closing.
- No `CHECK (json_valid(tags))` constraint: the column is only ever written by
  feedwatch through `encoding/json`, matching how `categories` is handled.
- No backfill. Existing rows land on `'[]'` by the column default.

## TDD plan

Tests belong in `internal/store/sqlite/migrate_internal_test.go` (white-box
`package sqlite`), because proving "an existing v1 store migrates in place"
requires applying only migration 1 first, which needs the unexported
`loadMigrations` and `s.applyMigrations`, plus `s.db` to read the raw column.
Reuse the file's existing helpers `migrateTestNow()` and `openTestStore(t)`
(which opens an **unmigrated** temp-file store; never `:memory:`, because each
pooled connection would get its own empty database).

1. **(tracer)** A fresh store migrated with `Migrate` reports
   `SchemaVersion == 2` and `Pending == 0`.
2. A store migrated to version 1 only (call `applyMigrations` with just the
   first element of `loadMigrations()`), then seeded with a feed row via
   `s.db.ExecContext`, then fully migrated: the pre-existing feed's `tags`
   column reads `'[]'` and no other column changed.
3. `Migrate` is idempotent at version 2: a second call applies 0 and leaves
   `SchemaVersion == 2`.

Existing tests to check rather than assume: `TestPendingReflectsUnappliedMigrations`
and `TestMigrateRefusesNewerSchema` in the same file, and
`TestMigrateIsIdempotent` in `sqlite_test.go`, may hardcode `1` as the expected
version or migration count. Update them to the new count rather than adding a
parallel assertion.

## Gotchas

- `internal/command/testdata/migrate_status.stdout` is a golden file pinning
  the `migrate --status` envelope, which carries the store schema version. It
  will change. Regenerate with `go test ./internal/command -update -count=1`
  and **read the diff** before committing; the only expected change is the
  version number.
- `internal/command/testdata/err/schema_too_new.stdout` exercises the too-new
  guard by stamping a future version; check whether its fixture stamps a
  literal `2` and would now be a valid version rather than a too-new one.

## Acceptance Criteria

- `internal/store/sqlite/migrations/0002_feed_tags.sql` exists and adds
  `tags TEXT NOT NULL DEFAULT '[]'` to `feeds`.
- A fresh store migrates to schema version 2 with 0 pending.
- A store already at version 1 with existing feeds migrates in place; every
  pre-existing feed's `tags` reads `'[]'` and no other column value changes.
- `Migrate` remains idempotent at version 2.
- Behaviors 1-3 covered in `migrate_internal_test.go`; pre-existing migration
  tests that hardcoded version 1 are updated, not duplicated.
- Affected golden files are regenerated and their diffs reviewed.
- `make build` passes.
