---
id: fee-e4tl
status: open
deps: [fee-v737, fee-7hf3]
links: []
created: 2026-07-26T11:23:25Z
type: task
priority: 2
assignee: Andre Silva
tags: [adr, schema]
---
# grow the schema command: error inventory plus tool-level exit codes

Grow the `schema` command into full ADR 0005 self-description: add the reference core (tool-level `exit_codes` as `[]int` and an `errors` inventory of `{code, exit_code, hint}` projected from `terr.All()`) while keeping feedwatch's richer per-command `exit_codes` map and derived `output_schema` as additive enrichment. Depends on the terr ticket (the registry) and the envelope-head ticket (the head and the reflector fix).

## Design

## Context

`docs/adr/0005-output-contract.md` requires the `schema` command to report, at
tool level, "the command surface, flags, declared exit codes, and the error
inventory (code, exit code, hint)", and that the output be "a projection of the
same declarations that drive behavior: the exit-code registry, the error
registry, and the envelope structs. It is never hand-maintained, so it cannot
drift." `docs/adr/0002-error-handling.md` says the same from the other side:
"error codes are enumerable as data, populated at sentinel construction, so the
documented error surface cannot drift from the real one."

feedwatch's `schema` command already covers commands, args, flags, per-command
`exit_codes` maps, and a per-command derived `output_schema`
(`internal/cli/schema.go`, `internal/cli/schema_registry.go`). What it lacks is
the error inventory, because until the terr ticket lands there are no codes to
enumerate.

## Owner decision folded in

Uniform core plus additive enrichment. feedwatch does not flatten toward the
reference and does not replace the reference core either; it carries both.

- Core, guaranteed and shaped exactly like the reference: `commands`, a
  tool-level `exit_codes` as `[]int` (the union across commands), and `errors` as
  `[{code, exit_code, hint}]` projected from `terr.All()`. A reference-written
  consumer reads this on every fleet tool.
- Enrichment, feedwatch-specific and additive: the per-command `exit_codes` map
  and the derived per-command `output_schema` stay as extra keys. Opt-in extra
  detail, not a replacement.
- The error inventory is tool-level only for now. Per-command error sets are a
  separate future enrichment, once each command declares its error set.
- The reflector remains the source of truth for `output_schema`; the
  hand-written `MarshalJSON` from the envelope-head ticket only fills nulls and
  never renames or omits.
- The cookiecutter template stays the floor. This enrichment is local; no
  template change.

## Target shape

The reference is the `go-cookiecutter` template file
`{{cookiecutter.project_name}}/internal/output/envelopes.go`:

```go
type SchemaEnvelope struct {
    Head
    Tool      string          `json:"tool"`
    Version   string          `json:"version"`
    Commands  []SchemaCommand `json:"commands"`
    ExitCodes []int           `json:"exit_codes"`
    Errors    []SchemaError   `json:"errors"`
}

// SchemaError describes one entry of the error inventory: a stable machine
// code, the exit code it maps to, and its remediation hint.
type SchemaError struct {
    Code     string `json:"code"`
    ExitCode int    `json:"exit_code"`
    Hint     string `json:"hint,omitempty"`
}
```

feedwatch's `SchemaResult` (`internal/cli/schema.go:40`) currently holds
`commands` and `global_flags`. Target composition, core keys first:

```json
{
  "schema_version": 1,
  "ok": true,
  "commands": [ { "command": "poll", "args": [], "flags": [],
                  "exit_codes": {"0": "...", "2": "...", "3": "..."},
                  "output_schema": {} } ],
  "exit_codes": [0, 2, 3, 64, 65, 69, 70, 78],
  "errors": [ {"code": "usage_error", "exit_code": 64, "hint": "..."} ],
  "global_flags": []
}
```

`tool` and `version` are also part of the reference core; add them if they are
cheap (`version.Current()` is already available via `Deps.Version`,
`internal/cli/root.go:30`) and say so in a note if you decide against.

## Work

### 1. Error inventory

Project `terr.All()` into `[]SchemaError`. `terr.All()` returns registration-order
`*terr.E` values, each exposing `Code()`, `ExitCode()`, and `Hint()`, so the
projection is mechanical. It must be a projection, not a literal: the test in
step 5 enforces that.

Decide where the type lives. The reference puts `SchemaError` in the output
package; feedwatch's schema types live in `internal/cli/schema.go`. Either is
fine, but state the choice.

### 2. Tool-level `exit_codes` as `[]int`

Compute the sorted union of every command's declared table
(`internal/cli/schema_registry.go:81-96`, built from `defaultExitCodes()` at
`:46`, `pollExitCodes()` at `:55`, `checkExitCodes()` at `:65`). The tables are
`map[string]string` keyed by the code as a string, so the union means parsing the
keys to `int` and sorting. Do not hand-maintain the union.

### 3. Keep the enrichment

`CommandSchema` (`internal/cli/schema.go:14`) keeps `exit_codes` and
`output_schema`. `SchemaResult` (`:40`) keeps `global_flags`. Both keep the
head added by the envelope-head ticket.

### 4. Update the `schema` command's own self-description

`internal/cli/schema_registry.go:95` currently reads:

```go
"schema": {exitCodes: defaultExitCodes(), output: jsonschema.Scalar("object", "a CommandSchema when narrowed to one command, otherwise {commands,global_flags}")},
```

That description is now wrong twice over (the envelope gained a head and three
keys). Update it. Consider replacing the hand-written `Scalar` with
`jsonschema.OneOf(jsonschema.Reflect(CommandSchema{}), jsonschema.Reflect(SchemaResult{}))`
so it too becomes a projection rather than prose; that is the ADR's
never-hand-maintained rule applied to the one place still violating it. If the
reflector cannot express it cleanly, keep the `Scalar` with corrected prose and
say why.

### 5. Strengthen the conformance test

`internal/cli/exit_conformance_test.go:16`
(`TestExitCodeTablesCoverExitCodeFor`) currently asserts that every code
`ExitCodeFor` produces is declared as data in every command's table. With a
registry in place, add the reciprocal: every code in `terr.All()` maps to an exit
code that is declared in the relevant table, and the `errors` array the schema
command emits contains every registered code. This is the drift guard the ADRs
are asking for.

### 6. Documentation (folded in)

- `docs/usage.md`: the `schema` section gains the `errors` inventory and the
  tool-level `exit_codes`, and its example output is updated.
- `docs/specs/001-initial-implementation/manual-qa.md`: update the schema step.
- `CHANGELOG.md`: additive entry.

### 7. Goldens

`schema` is not currently in the e2e golden set. If you add a scenario for it,
normalize the version string the way `version.stdout` already is
(`internal/e2e/testdata/version.stdout` uses `<commit>` and `<go>` tokens, see
`internal/e2e/e2e_test.go` normalization).

## Invariants

- `schema --command CMD` still narrows to one command
  (`internal/cli/schema.go:63-69`).
- An unknown command name is still a usage error, exit 64
  (`internal/cli/schema.go:66`, via `unknownCommandErr`).
- Hidden commands and the framework's auto-added `help` command are still
  excluded (`skipCommand`, `internal/cli/schema.go:107`).
- The per-command `exit_codes` map and `output_schema` do not regress.

## TDD plan

1. (tracer) `schema` output contains an `errors` array whose entries carry
   `code`, `exit_code`, and an optional `hint`.
2. The array equals a projection of `terr.All()`, compared element-wise against
   the registry rather than a literal, so a new registration appears with no
   test edit.
3. Registering a new sentinel in a test makes it appear in the schema output
   with no other change.
4. Tool-level `exit_codes` is the sorted union of every command's table, checked
   against the tables rather than a literal.
5. Every registered code's exit code is declared in the relevant command tables
   (the reciprocal conformance assertion).
6. `schema --command poll` still returns one `CommandSchema` with its
   `exit_codes` map and `output_schema` intact, and `schema --command nope`
   still exits 64.

## Acceptance Criteria

- `schema` reports an `errors` array of `{code, exit_code, hint}` that is exactly
  a projection of `terr.All()`, asserted by comparing against the registry rather
  than a literal, so a newly registered code appears with no other edit.
- `schema` reports a tool-level `exit_codes` as `[]int`, the sorted union of
  every command's declared table, computed from the tables and not
  hand-maintained.
- The feedwatch enrichment is intact: every `CommandSchema` still carries its
  per-command `exit_codes` map and derived `output_schema`, and `SchemaResult`
  still carries `global_flags`.
- The schema envelope carries the ADR 0005 head and coalesces its collections to
  `[]`.
- The `schema` command's own registry entry
  (`internal/cli/schema_registry.go:95`) describes the new envelope accurately,
  and a note records whether it became a reflector projection or stayed a
  corrected `Scalar`, with the reason.
- `internal/cli/exit_conformance_test.go` gains the reciprocal assertion: every
  code in `terr.All()` maps to an exit code declared in the relevant command
  tables, and the emitted `errors` array covers every registered code. The
  original assertion still holds.
- `schema --command CMD` still narrows; an unknown name still exits 64; hidden
  and `help` commands are still excluded.
- `internal/e2e` goldens pass (with the version string normalized if a `schema`
  scenario was added); `internal/e2e/signal_test.go` passes.
- `docs/usage.md` (the `schema` section and its example),
  `docs/specs/001-initial-implementation/manual-qa.md`, and `CHANGELOG.md`
  updated and markdownlint clean.
- `make build` passes (it runs `fmt-check`, `vet`, `lint`, `test`, then
  compile).

## Notes

**2026-07-26T11:25:20Z**

Cross-cutting requirements for this ticket (from the approved plan at .local/planning/adr-adoption.md, section 5):

- Quality gate: `make build` must pass. It runs `fmt-check`, `vet`, `lint`, `test`, then compile, and is the full validation. See docs/build.md.
- Follow TDD per the repo `tdd` and `golang` skills: write the failing test first, one behavior at a time, starting from the tracer behavior named in the design.
- Do NOT make git commits and do NOT create branches. The owner manages all git workflow. Staging is allowed only where a workflow requires it.
- Re-verify every `file:line` reference in the design before relying on it; the tree moves as sibling tickets land. Line numbers were verified 2026-07-26 against a clean tree at commit 0b894e9.
- Record non-obvious decisions and discoveries in docs/specs/001-initial-implementation/learnings.md under this ticket heading, and add a `tk add-note` summary before closing.
- Ticket markdown must lint clean: `markdownlint-cli2 --fix ".tickets/*.md"` then `markdownlint-cli2 ".tickets/*.md"` reporting 0 errors.
- Full plan context, including the seven owner decisions (D1-D7) this work implements, is in .local/planning/adr-adoption.md.
