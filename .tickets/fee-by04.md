---
id: fee-by04
status: open
deps: [fee-7hf3, fee-z3bm, fee-nkdl, fee-e4tl]
links: []
created: 2026-07-26T11:24:54Z
type: chore
priority: 3
assignee: Andre Silva
tags: [adr, docs]
---
# final documentation pass for the ADR adoption

Final documentation pass for the ADR adoption: rewrite the `docs/usage.md` stream-contract section for the new envelopes, audit the EARS requirements for the removed shapes, verify the three breaking `CHANGELOG.md` entries read as one coherent set, and append the adoption decisions to the learnings doc. Per-command examples are handled by the shape tickets; this covers what they do not.

## Design

## Context

The envelope-head, error-envelope, warning, and schema tickets each fold their
own per-command documentation updates in, so the docs never lag the code. Three
things are deliberately left to a single final pass, because they describe the
contract as a whole rather than one command:

1. The stream-contract section of `docs/usage.md` (`:39-65`).
2. The EARS requirements doc, which may name shapes that no longer exist.
3. The coherence of the accumulated `CHANGELOG.md` entries, plus the learnings
   record the repo convention asks for.

## Work

### 1. `docs/usage.md:39-65` (the stream-contract section)

Currently this section says stderr carries "structured JSON log lines and
structured error objects", that a failure emits "a single JSON error object to
stderr and nothing to stdout", and it documents the `category` vocabulary
(`:48-53`) and shows a worked example (`:56-65`) whose `err.json` is the removed
batch form:

```text
# err.json: {"errors":[{"feed_url":"...","category":"http","status":404,
#                      {"feed_url":"...","category":"network",
```

Rewrite it to describe the contract as adopted:

- stdout carries exactly one newline-terminated result envelope per invocation,
  opening with `schema_version` and `ok`, and never a log, warning, or banner.
- stderr carries three distinguishable things: the error envelope
  (`ok:false` plus an `error` object of `code`, `message`, optional `hint`,
  optional `details`), NDJSON warning lines (`level:"warning"`), and slog
  diagnostic records. Say how a consumer tells them apart, since that is the
  question an agent actually has: `ok` present means error envelope, `level`
  present means warning, neither means log.
- Per-feed failures live in the stdout `failures` array, not on stderr. State
  this explicitly, because it is the visible removal.
- Collections are never `null`.
- Update the worked example so `err.json` shows the new shape (or shows that it
  is empty for a partial poll, which is now the truth).

Cross-check the exit-code table (`:87-91`) and the poll and check sections
(`:204-207`, `:243`, `:258`) against what the shape tickets already changed, so
the whole document is consistent rather than half-migrated.

### 2. `docs/specs/001-initial-implementation/requirements.md`

Audit for EARS requirements that name the old error object, the `category`
vocabulary as the machine code, the stderr batch form, or the migrate
`schema_version` payload field. Update the ones that describe removed behavior.
Requirements are the spec of record: if a requirement is now wrong, it is a
correction, not an edit for style. Note which requirement IDs changed.

### 3. `CHANGELOG.md`

Three breaking changes land across the adoption:

- the stderr error object reshaped to `{code, message, hint, details}` inside a
  headed envelope,
- the stderr per-feed batch `{"errors":[...]}` form removed (per-feed detail is
  not lost; it remains in the stdout `failures` array),
- the migrate envelope's `schema_version` payload field renamed to
  `store_schema_version`,

plus additive entries for the envelope head, NDJSON warnings, and the schema
error inventory. Each ticket writes its own entry as it lands. This pass verifies
they read as one coherent set under `## [Unreleased]`: no duplication, no
contradiction, consistent ordering, and an agent-facing migration note that says
what a consumer must change (branch on `error.code` rather than
`error.category`; read per-feed failures from stdout rather than stderr; read
`store_schema_version`).

### 4. `docs/specs/001-initial-implementation/learnings.md`

Append the adoption decisions under the relevant ticket headings, per the repo
convention in `CLAUDE.md`. The decisions worth recording, because they were
non-obvious and were resolved by owner decision rather than derivable from the
code:

- layering `terr` onto `core.FeedError` via per-category class sentinels rather
  than replacing it, and why (per-instance structure plus ~40 `errors.Is` call
  sites),
- dropping the stderr batch form because per-feed failures are result data and
  the aggregate drives the exit code,
- `store_schema_version`, because the head owns `schema_version`,
- schema core plus additive enrichment, so a reference-written consumer works
  across the fleet while feedwatch keeps its per-command detail,
- the one wired warning producer and why the other three were not,
- warnings not suppressed by `--quiet`, because they are contract output.

### 5. Lint

```sh
markdownlint-cli2 --config .markdownlint.yaml --fix docs/usage.md CHANGELOG.md docs/specs/001-initial-implementation/*.md
markdownlint-cli2 --config .markdownlint.yaml docs/usage.md CHANGELOG.md docs/specs/001-initial-implementation/*.md
```

The second command must report `0 error(s)`.

## Invariants

- No document shows a headless result envelope, the
  `{category, feed_url, status, message}` error object, the stderr batch form, or
  the migrate `schema_version` payload field.
- Every documented example matches real output. Where an example is long, copy it
  from an actual run or from an `internal/e2e` golden rather than hand-writing
  it; a hand-written example is how documentation drifts.
- No local filesystem paths in any document; examples use environment variables
  for placeholders.

## Acceptance Criteria

- `docs/usage.md:39-65` describes the adopted contract: one headed result
  envelope per invocation on stdout; error envelope, NDJSON warnings, and log
  records on stderr with a stated rule for telling them apart (`ok` means error,
  `level` means warning, neither means log); per-feed failures in the stdout
  `failures` array; collections never `null`. The worked example matches real
  output.
- The rest of `docs/usage.md` is consistent with it: the exit-code table and the
  poll, check, migrate, and schema sections show no removed shape.
- `docs/specs/001-initial-implementation/requirements.md` is audited and
  corrected where an EARS requirement described removed behavior; the changed
  requirement IDs are listed in a note.
- `CHANGELOG.md` under `## [Unreleased]` carries the three breaking entries and
  the additive ones as one coherent set, with an agent-facing migration note
  (branch on `error.code` not `error.category`; per-feed failures from stdout;
  `store_schema_version`).
- `docs/specs/001-initial-implementation/learnings.md` records the six adoption
  decisions with their rationale.
- No document shows a headless envelope, the old error object, the stderr batch
  form, or the migrate `schema_version` payload field; no document references a
  local filesystem path.
- `markdownlint-cli2 --config .markdownlint.yaml` reports `0 error(s)` for every
  touched file.
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
