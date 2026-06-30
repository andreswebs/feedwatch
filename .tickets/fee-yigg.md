---
id: fee-yigg
status: closed
deps: [fee-h4bz]
links: []
created: 2026-06-30T03:42:48Z
type: bug
priority: 2
tags: [store]
---

# store: default XDG directory not auto-created on fresh machine

The default XDG store directory is not created on first run, so a fresh-machine invocation using the default `--db` path fails. Found during manual QA (TC-CONFIG-003); see `docs/qa.result.bak.md` BUG-002.

## Design

Expected (REQ 5, `docs/usage.md`): with `FEEDWATCH_DB` unset, the store is created at `$XDG_STATE_HOME/feedwatch/feedwatch.db`, falling back to `~/.local/state/feedwatch/feedwatch.db`.

Observed: the path is computed correctly but the `feedwatch/` subdirectory is not created, so SQLite cannot open the file:

```text
{"error":{"category":"store","message":"open sqlite \".../feedwatch/feedwatch.db\": unable to open database file (14)\nstore unavailable"}}
```

Pre-creating the `feedwatch/` subdirectory makes the same command succeed (`{"applied":1,"schema_version":1}`).

Repro:

```sh
env -u FEEDWATCH_DB XDG_STATE_HOME="$(mktemp -d)" feedwatch migrate   # exit 1, store error
```

Likely fix: `mkdir -p` the parent directory of the resolved store path before opening SQLite (for the tool-owned default location). This also affects the spirit of TC-NFR-004 (works today only when `--db` points at an existing directory).

## Acceptance Criteria

- `feedwatch migrate` (and any command) on a fresh machine creates `$XDG_STATE_HOME/feedwatch/` (or `~/.local/state/feedwatch/`) and succeeds with no manual setup.
- The `--db <path>` behavior for a missing intermediate directory is unchanged (still a `store` error per TC-STORE-002).

## Implementation Plan

Create the parent directory only for the tool-owned default location, leaving an explicit `--db`/`FEEDWATCH_DB` path strict (a missing intermediate directory must still fail per TC-STORE-002). The default-vs-explicit decision lives in `resolveStorePath`; the directory creation and its error surface where the store is opened, so the side-effect stays out of the pure path resolver and out of `sqlite.Open`.

1. Change `resolveStorePath` in `src/internal/cli/storepath.go` to report whether it returned the tool-owned default:

   ```go
   func resolveStorePath(flagVal string) (path string, isDefault bool) {
       if flagVal != "" {
           return flagVal, false
       }
       // XDG / home / relative branches each return (path, true)
   }
   ```

2. Add a helper in the same file that creates the parent directory and maps a failure to a store-category error:

   ```go
   func ensureStoreDir(path string) error {
       if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
           return &core.FeedError{
               Category: core.CatStore,
               Message:  err.Error(),
               Err:      core.ErrStoreUnavailable,
           }
       }
       return nil
   }
   ```

3. Thread `isDefault` from `buildConfig` (whose only caller is the `before` hook) to `before` in `src/internal/cli/root.go`. After `cfg.Validate()`, when `isDefault` and `backendName(cfg.Store) == "sqlite"`, call `ensureStoreDir(cfg.Store)` and return its error:

   ```go
   cfg, isDefault := buildConfig(d.Cfg, cmd)
   if err := cfg.Validate(); err != nil {
       return ctx, err
   }
   if isDefault && backendName(cfg.Store) == "sqlite" {
       if err := ensureStoreDir(cfg.Store); err != nil {
           return ctx, err
       }
   }
   ```

Verification:

- A test running with `FEEDWATCH_DB` unset and `XDG_STATE_HOME` pointing at a fresh temp dir: `migrate` creates `feedwatch/` and succeeds.
- A test asserting an explicit `--db` with a missing intermediate directory still fails with a `store` error (TC-STORE-002 unchanged).
- `make build` green; learnings entry.

## Notes

**2026-06-30T03:42:48Z**

Source: manual QA report docs/qa.result.bak.md BUG-002 (TC-CONFIG-003). Severity Medium, Priority P1. Cross-refs TC-NFR-004.

**2026-06-30T13:42:23Z**

Fixed: default XDG store dir now auto-created. resolveStorePath returns (path, isDefault); before() calls new ensureStoreDir(cfg.Store) only when isDefault && backend==sqlite, so the fresh-machine default path is MkdirAll'd (0o700) while an explicit --db/FEEDWATCH_DB stays strict (TC-STORE-002 unchanged). MkdirAll failure maps to CatStore/ErrStoreUnavailable. Added TestDefaultStoreDirAutoCreated; updated TestResolveStorePath for the new signature. Verified ticket repro (env -u FEEDWATCH_DB XDG_STATE_HOME=tmp feedwatch migrate -> exit 0, dir created) and explicit-missing-dir still exits 1. make build green.
