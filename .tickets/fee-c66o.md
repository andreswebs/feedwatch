---
id: fee-c66o
status: closed
deps: [fee-7ons, fee-aqkn]
links: []
created: 2026-06-29T15:36:08Z
type: task
priority: 2
assignee: Andre Silva
parent: fee-gyos
tags: [cli]
---

# Walking skeleton: version + migrate --status end-to-end

Prove the whole contract end-to-end on the two simplest paths, version and
migrate --status, through the real CLI, store, and JSON envelope. This is the
capstone of the persistence lane (E2): it depends on the real migrate command,
so it runs once that lane lands, not before it. Refs: docs/cli-design.md
(Execution Model, Schema Lifecycle, CLI Framework).

## Design

End-to-end integration. Wire `main` + CLI skeleton + SQLite store + migrations

- migrate command so the two simplest paths work end-to-end across the assembled
  stack:

```text
feedwatch --version             -> JSON {"version","commit","go"} on stdout, exit 0
feedwatch migrate --status      -> opens/creates the SQLite store at --db, ensures
                                   schema, prints {"schema_version":N,"pending":0,
                                   "backend":"sqlite"}
feedwatch --format text --version -> human line, color gated
```

The version string is injected at build time via `-ldflags` (the Makefile
already sets `VERSION`); `main` passes it into `Deps`.

TDD plan (end-to-end via `cmd.Run` with a temp db path + buffers):

1. (tracer) `--version` prints valid JSON containing a version field; exit 0.
2. `migrate --status` on a fresh temp db reports `schema_version >= 1`,
   `pending == 0`, backend `sqlite`; exit 0.
3. `migrate --status` is idempotent across two runs (same version, no error).
4. an unwritable `--db` path maps to `core.ErrStoreUnavailable` -> exit 1 + JSON
   error.

This is the first true integration and proves the CLI + store + migrations +
envelope contract. Deep-module note: `main` holds only signal ctx + Run +
`HandleExitCoder`.

## Acceptance Criteria

- `feedwatch --version` emits JSON (behavior 1), exit 0.
- `feedwatch migrate --status` works on fresh and existing dbs (behaviors 2-3).
- store-unavailable path -> exit 1 + JSON error (behavior 4).
- Exercises CLI + store + migrations + envelope end-to-end.
- Supports Req 1 (one-shot), 6. `make validate` passes.

## Notes

**2026-06-29T22:32:35Z**

Walking skeleton: proved version + migrate --status end-to-end through the real CLI+store+migrations+envelope.

Key reconciliation: behavior 2 (fresh db migrate --status -> schema_version>=1, pending==0) directly contradicted fee-aqkn's deliberate '--status never applies' decision and its TestMigrateStatusFreshDB (which asserted version==0, pending>=1). The ticket's own design block ('opens/creates the store, ensures schema, prints pending:0') plus cli-design.md Schema Lifecycle ('on any command... applies pending migrations idempotently') are authoritative, so I made the migrate --status branch call st.Migrate(ctx) (ensure) before reading SchemaVersion/Pending, and updated TestMigrateStatusFreshDB to the new ensure-then-report semantics.

Change was surgical (migrate.go --status branch only); bare 'migrate' is unchanged and still reports a real applied count, so TestMigrateAppliesThenStatusClean stays valid. The broader 'auto-migrate on EVERY command' (poll/add/etc.) is NOT yet wired -- those commands don't exist; when they land they should ensure schema on store open (likely in openStore), not duplicate the migrate command. Behaviors 1-4 covered: TestVersionJSON + new TestVersionTextFormat (1), TestMigrateStatusFreshDB (2), TestMigrateStatusIdempotent (3), TestMigrateUnwritableDBExits1 (4, CatStore/exit1). Live smoke test confirmed all four. make build green. Closing fee-c66o unblocks E2 (fee-gyos).
