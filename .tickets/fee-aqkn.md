---
id: fee-aqkn
status: closed
deps: [fee-vlk9, fee-7ons, fee-po72, fee-63n9]
links: []
created: 2026-06-29T15:36:08Z
type: task
priority: 2
assignee: Andre Silva
parent: fee-gyos
tags: [store]
---

# migrate command

Add the migrate subcommand: migrate applies pending migrations; migrate --status
reports schema version, pending count, and backend as JSON. Refs:
docs/cli-design.md (Schema Lifecycle, Commands); docs/research/urfave-cli.reference.md.

## Design

Package `src/internal/cli` (command registration) using the store.

```go
&cli.Command{
    Name:  "migrate",
    Flags: []cli.Flag{ &cli.BoolFlag{Name: "status"} },
    Action: migrateAction, // func(ctx, *cli.Command) error
}
```

(urfave ref: cli.Command, BoolFlag, Action signature.) The action opens the store
from the resolved `--db` (scheme decides backend). With `--status` it reports
`{schema_version, pending, backend}` from `SchemaVersion` + `pending`; otherwise
it runs `Migrate` and reports `{applied, schema_version}`. Render via
`output.Renderer`. Store/usage failures map to `core` errors -> exit 1 through
the boundary.

```go
type MigrateStatus struct {
    SchemaVersion int    `json:"schema_version"`
    Pending       int    `json:"pending"`
    Backend       string `json:"backend"`
}
type MigrateApplied struct {
    Applied       int `json:"applied"`
    SchemaVersion int `json:"schema_version"`
}
```

TDD plan (via `cmd.Run` with a temp db + buffers):

1. (tracer) `migrate --status` on a fresh db -> JSON MigrateStatus, backend
   `sqlite`, exit 0.
2. `migrate` (no flag) applies pending; reports `applied >= 1`; a following
   `--status` shows `pending == 0`.
3. backend reflects the DSN scheme (sqlite here).

Deep-module note: the command is thin; all logic lives in the store.

## Acceptance Criteria

- `migrate` and `migrate --status` implemented as a urfave subcommand emitting
  JSON.
- Uses `store.Migrate` / `SchemaVersion` / `pending`; exit codes via the
  boundary.
- Behaviors 1-3 covered. Supports Req 6 and 19. `make validate` passes.

## Notes

**2026-06-29T22:27:21Z**

Implemented migrate subcommand (internal/cli/migrate.go) plus a store opener (internal/cli/storeopen.go). 'migrate' applies pending migrations -> {applied, schema_version}; 'migrate --status' reports {schema_version, pending, backend} without applying. Thin command: all logic in store.Migrate/SchemaVersion/Pending. openStore dispatches on the --db URL scheme via backendName (postgres://|postgresql:// -> postgres, else sqlite); postgres is deferred and returns a CatConfig error -> exit 1. Registered through new Deps.commands() in root.go. Tests in migrate_test.go drive cmd.Run with a temp db (behaviors 1-3): fresh status (v0, pending>=1, sqlite), apply-then-clean-status, and a table-driven backendName unit test. make build green; smoke-tested the binary end-to-end including the postgres deferral. Unblocks fee-c66o (walking skeleton).
