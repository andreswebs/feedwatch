---
id: fee-vlk9
status: closed
deps: [fee-bqne, fee-63n9]
links: []
created: 2026-06-29T15:36:08Z
type: task
priority: 1
assignee: Andre Silva
parent: fee-gyos
tags: [store]
---

# Embedded migrations engine and schema-version guard

Embed versioned SQL migrations and apply pending ones idempotently in a
transaction; expose schema version and pending count; refuse a database newer
than the binary; abort and roll back (never swallow) on a failed migration. Refs:
docs/cli-design.md (Schema Lifecycle).

## Design

Package `src/internal/store/sqlite`. Migrations embedded via `embed.FS` in
`migrations/` (`0001_init.sql` holds the feeds/items DDL, including the items
`tombstoned` column, plus indexes; later files `NNNN_*.sql`). A
`schema_migrations(version INTEGER PRIMARY KEY, applied_at TEXT)` table records
applied versions.

```go
func (s *Store) Migrate(ctx context.Context) (applied int, err error)
// - dbMax = max(version) from schema_migrations (0 if table absent)
// - codeMax = highest embedded version
// - if dbMax > codeMax -> return fmt.Errorf("...: %w", core.ErrSchemaTooNew)
// - for each pending version ascending: BEGIN; exec file; INSERT version,
//   applied_at; COMMIT. On any error: ROLLBACK and return the wrapped error
//   (never continue, never swallow).

func (s *Store) SchemaVersion(ctx context.Context) (int, error) // dbMax
func (s *Store) pending(ctx context.Context) (int, error)       // codeMax - dbMax
```

TDD plan:

1. (tracer) `Migrate` on a fresh db applies 0001 (`applied >= 1`);
   `SchemaVersion == codeMax`.
2. `Migrate` is idempotent: a second call applies 0; `pending == 0`.
3. A db stamped with `codeMax+1` -> `Migrate` returns an error and
   `errors.Is(core.ErrSchemaTooNew)`.
4. A deliberately broken migration (injected via a test `embed.FS`) -> `Migrate`
   returns the error and `schema_migrations` is unchanged (transaction rolled
   back).

Deep-module note: `Migrate` is the only entry point; `embed.FS` and transactions
are hidden behind it.

## Acceptance Criteria

- Embedded migrations applied in order, idempotently, each inside a transaction.
- `SchemaVersion` and `pending` exposed for `migrate --status`.
- A newer-than-binary db is refused (`errors.Is(core.ErrSchemaTooNew)`).
- A failed migration aborts and rolls back (behavior 4); never logged-and-skipped.
- Supports Req 6. `make validate` passes.

## Notes

**2026-06-29T21:15:37Z**

Built on the migration applier fee-bqne seeded in migrate.go (Migrate/applyMigration/SchemaVersion + migrations/0001_init.sql). Remaining scope delivered:

1. ErrSchemaTooNew guard: Migrate now computes codeMax = highest embedded version and returns fmt.Errorf("...: %w", core.ErrSchemaTooNew) when the stored version exceeds it, instead of silently applying zero migrations.
2. Pending(ctx) (int, error): counts embedded migrations with version > current; exposed on the store.Store interface (and the sqlite impl) for 'migrate --status'. fakeStore conformance double updated.
3. Refactor seam: Migrate delegates to unexported applyMigrations(ctx, []migration) so the rollback test injects a custom slice (valid 0001 + broken 0002) and asserts SchemaVersion stays at 1 after the broken step aborts. No fs.FS injection needed.

Tests are white-box (package sqlite) in migrate_internal_test.go because they need s.db access (stamping a future version) and the unexported applyMigrations seam; the external sqlite_test.go still covers the idempotent happy path. make validate / make build pass (vet, lint 0 issues, tests, compile).

Note for fee-aqkn (migrate command): SchemaVersion + Pending are both on the store.Store interface, so the command reads them polymorphically without a type assertion; backend string comes from the cli layer.
