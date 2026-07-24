---
id: fee-rgmp
status: open
deps: [fee-8ugz]
links: [fee-8ugz]
created: 2026-07-22T20:05:19Z
type: task
priority: 2
assignee: Andre Silva
---
# Migrate exit codes to the fleet taxonomy (ADR 0001)

## Context

`docs/adr/0001-exit-code-taxonomy.md` (already in this repo) adopts a
family-wide taxonomy: 0 success; 1 recoverable result; 2-63 optional
per-tool result sub-codes (registry-documented, result classes only);
64 EX_USAGE; 65 EX_DATAERR; 70 EX_SOFTWARE; 74 EX_IOERR; 78 EX_CONFIG;
other sysexits on exact fit (69 EX_UNAVAILABLE is used below); 130/143
signals.

Feedwatch is in the unusual position that its result-side codes and
signal handling already conform, while its failure side does not:
today every whole-invocation failure (usage, config, store, internal)
exits 1, which the taxonomy reserves for recoverable results. Breaking
change (v0.x); include release notes.

What stays exactly as it is:

- Poll/check outcome codes: 0 all ok, 2 all polled feeds failed,
  3 partial failure (`internal/poll/run.go:68-77` `Result.ExitCode`).
  Under the ADR these are per-tool result sub-codes in the 2-63 range;
  they remain, and the registry documents them as result classes.
- Feed-scoped `FeedError` categories (network, http, parse, timeout)
  mapping to exit 0 with outcomes carried in the envelope
  (`internal/core/errors.go` `ExitCodeFor`).
- Signal exits: SIGINT 130, SIGTERM 143 (`cmd/feedwatch/main.go`);
  feedwatch is the fleet's reference implementation for the ADR's
  signal rule.

## Old to new mapping (decided; implement exactly this)

| Old | Condition | New |
| --- | --------- | --- |
| 0 | success; feed-scoped errors | 0 (unchanged) |
| 2 | poll/check: all polled feeds failed | 2 (unchanged, result sub-code) |
| 3 | poll/check: partial failure | 3 (unchanged, result sub-code) |
| 1 | `ErrUsage`; `FeedError` CatUsage; framework usage errors | 64 |
| 1 | `ErrConfig`; `FeedError` CatConfig | 78 |
| 1 | `ErrStoreUnavailable`; `FeedError` CatStore | 69 |
| 1 | `ErrSchemaTooNew` (stored data newer than the binary can read) | 65 |
| 1 | `FeedError` CatInternal; unclassified fallback | 70 |
| 130/143 | signals | unchanged |

## Production sites to change (verified 2026-07-22)

- `internal/core/errors.go` `ExitCodeFor` (lines 118-146): replace the
  single `return 1` bucket with the per-class mapping above. The
  sentinels (`ErrUsage`, `ErrConfig`, `ErrStoreUnavailable`,
  `ErrSchemaTooNew` at lines 107-116) and `FeedError.Category` are
  already distinguishable via `errors.Is`/`errors.As`, so this is a
  local change to one function: usage 64, config 78, store 69,
  schema-too-new 65, internal and the final fallback 70. Update the
  function's doc comment (lines 118-122).
- `internal/cli/root.go`: the usage-error funnels (`onUsageError`
  around line 173, `commandNotFound` around line 146,
  `completionShellNotFound` around line 159) produce usage-category
  `FeedError`s that flow through `ExitCodeFor`, so they follow
  automatically; verify with tests rather than changing them.
- `internal/poll/run.go` `Result.ExitCode` (lines 68-77): unchanged;
  extend its doc comment to cite the ADR's result-sub-code rule.
- `internal/cli/exit.go` (`exitError`) and the `exitErrHandler`
  boundary in `root.go:187-201`: mechanism unchanged.
- `cmd/feedwatch/main.go` signal wiring: unchanged.

## Registry updates (`internal/cli/schema_registry.go`)

The schema command already declares exit codes as data:

- `defaultExitCodes()` (line 31): shared table for non-poll commands;
  today it describes 0/1. Change to describe 0, 64, 65, 69, 70, 74*,
  78 with one-line meanings per the ADR. (*74 only if an I/O class
  distinct from store-unavailable actually exists; feedwatch routes
  store I/O through CatStore/69 and has no separate local-file I/O
  failure class today. Do not declare codes the tool cannot produce.)
- `pollExitCodes()` (line 41) and `checkExitCodes()` (line 50): keep
  0/2/3 and add the failure classes; state explicitly that 2 and 3 are
  result sub-codes (command completed), not failures.
- Conformance (ADR requirement): the e2e suite already asserts real
  exit codes per scenario (`internal/e2e/e2e_test.go:118,142`
  `wantExit`). Add one test that cross-checks: every code asserted by
  an e2e scenario for command X is present in X's declared table (or
  at minimum a unit test asserting `ExitCodeFor` outputs are all keys
  of the relevant tables).

## Tests to update

- `internal/e2e/e2e_test.go` and scenario files: every `wantExit`
  argument for failure scenarios changes per the mapping (usage 1 to
  64, config 1 to 78, store 1 to 69). Outcome scenarios (`all_failed`
  wants 2, `partial` wants 3) are unchanged. Golden stdout/stderr
  files only change where they embed schema output (exit-code tables);
  regenerate with the suite's `-update` flag and review.
- `internal/e2e/signal_test.go`: unchanged (130/143); run it to
  confirm.
- Unit tests on `ExitCodeFor` in `internal/core`: update expected
  values; add cases for each class.
- Grep `src -rn "wantExit\|ExitCodeFor" --include="*_test.go"` for
  stragglers.

## Docs to update

- `README.md` line 88: the exit-code summary (`0` success, `1` usage
  and other whole-invocation failures, plus the 2/3 outcome codes)
  changes to the new classes.
- `docs/cli-design.md`: the "Exit Codes" section (line 117 onward) and
  the boundary narrative (lines 217-240, "maps to exit 1" at line 72
  and around line 237).
- `docs/usage.md`: command reference documents exit codes per command
  (referenced from README line 99); update every failure-code mention.
- `AGENTS.md` and `docs/specs/*`: grep for literal exit-code mentions;
  update living docs, and for historical specs add a pointer to ADR
  0001 rather than rewriting history.

## Acceptance

- `ExitCodeFor` implements the class mapping; no whole-invocation
  failure exits 1; outcome codes 2/3 and signal codes 130/143 are
  byte-for-byte unchanged in behavior.
- Schema registry tables describe the new failure classes and label
  2/3 as result sub-codes; `feedwatch schema` output reflects this;
  the registry/behavior cross-check test passes.
- e2e suite green with updated `wantExit` values; signal tests
  unchanged and green.
- `README.md`, `docs/cli-design.md`, and `docs/usage.md` describe only
  the new scheme.
- `make build` passes from the repo root.
- Release notes drafted (breaking change: whole-invocation failures
  reclassified from 1 to 64/65/69/70/78; poll outcome codes and signal
  codes unchanged).
- Leave committing to the repo owner; do not create git commits.

## Notes

**2026-07-24T20:37:32Z**

Depends on and linked to fee-8ugz (Migrate Go module from src/ to repo root). After that migration lands, the Go module lives at the REPO ROOT, not under src/. Every src/ path in this ticket must be re-checked and de-prefixed before use: production sites become internal/core/errors.go, internal/cli/root.go, internal/poll/run.go, internal/cli/exit.go, internal/cli/schema_registry.go; tests become internal/e2e/e2e_test.go, internal/e2e/signal_test.go, internal/core; and the straggler grep 'src -rn "wantExit|ExitCodeFor"' becomes a root-relative grep (e.g. grep -rn 'wantExit|ExitCodeFor' --include='*_test.go' .). The cited line numbers should still hold since no .go content changes in the migration, only the src/ path prefix is dropped. make build runs from the repo root either way.
