---
id: fee-nkdl
status: open
deps: [fee-7hf3]
links: []
created: 2026-07-26T11:21:36Z
type: task
priority: 2
assignee: Andre Silva
tags: [adr, output]
---
# add the NDJSON warning channel with the auto-disable producer

Add the ADR 0005 NDJSON warning channel: `output.EmitWarning` plus a `Renderer.Warn` gate, and wire exactly one producer, the auto-disable-after-threshold advisory in `internal/poll/lifecycle.go`, raised through an injected callback so the poll layer stays stream-blind. Depends on the envelope-head ticket for `SchemaVersion`.

## Design

## Context

`docs/adr/0005-output-contract.md` requires that "advisories that do not change
the exit code render on stderr as NDJSON objects marked as warnings, one per
line, distinct from log records. Libraries raise them through an injected
callback and never write to the stream themselves."

feedwatch has no warning emitter anywhere under `internal/`.

## Owner decisions folded in

- Plumb the emitter and a `Renderer.Warn` method so the format gate lives in one
  place beside `Result` and `Error`.
- Wire exactly one producer: the auto-disable-after-threshold advisory at
  `internal/poll/lifecycle.go:50`. It is the one state change a caller cannot
  otherwise observe from a single invocation's output: `RecordFailure` flips the
  feed to `core.FeedDisabled` and nothing on either stream says so.
- Do not wire the other candidates. The permanent-redirect feed rename
  (`internal/poll/consume.go:97`) is already reported in the `renamed` result
  array and an info log (`internal/cli/poll.go:111-114`); the lossy charset
  fallback (`internal/fetch/http.go`, precedence documented at `:183`) and a
  guessed rather than autodiscovered candidate
  (`internal/discover/discover.go:23`) are lower value and carry no
  caller-facing single-invocation surprise. If you find yourself wanting one of
  them, that is a new ticket.
- `--quiet` does not suppress warnings. Warnings are contract output, not logs;
  `--quiet` raises the log floor only (`internal/cli/logger.go`, `config.Quiet`).
  The e2e suite runs with `--quiet` (`internal/e2e/e2e_test.go:107`), so the new
  scenario will show a warning line on stderr. That golden change is expected.

## Target shape

The reference is the `go-cookiecutter` template file
`{{cookiecutter.project_name}}/internal/output/output.go`:

```go
// warningEnvelope is a non-fatal, machine-readable advisory written to
// stderr. It carries level "warning" instead of an ok field, so a consumer
// can tell it apart from the error envelope unambiguously, and it never
// changes the exit code.
type warningEnvelope struct {
    SchemaVersion int    `json:"schema_version"`
    Level         string `json:"level"`
    Code          string `json:"code"`
    Message       string `json:"message"`
    Hint          string `json:"hint,omitempty"`
    Details       any    `json:"details,omitempty"`
}

func EmitWarning(w io.Writer, code, message, hint string, details any) {
    env := warningEnvelope{
        SchemaVersion: SchemaVersion,
        Level:         "warning",
        Code:          code,
        Message:       message,
        Hint:          hint,
        Details:       details,
    }
    data, err := json.Marshal(env)
    if err != nil {
        return
    }
    _, _ = w.Write(append(data, '\n'))
}
```

`level:"warning"` instead of `ok` is deliberate: a consumer can tell a warning
line from an error envelope unambiguously. Successive calls append one
newline-terminated object each, so they form a valid NDJSON stream.

Expected line for the auto-disable advisory:

```json
{"schema_version":1,"level":"warning","code":"feed_auto_disabled","message":"feed disabled after 5 consecutive failures","hint":"re-enable with: feedwatch enable <feed>","details":{"feed_url":"http://feedserver/feed.xml","failures":5}}
```

Settle the final code string and hint in the ticket record; `feed_auto_disabled`
is the proposal.

## Work

### 1. `internal/output/output.go`

Add `warningEnvelope` and `EmitWarning` per the reference.

### 2. `internal/output/renderer.go`

Add `Warn` alongside `Result` (`:59`) and `Error` (`:71`), gating on format the
same way: JSON emits the NDJSON line; `--format text` emits a human line on
stderr. For text, mirror `textError` (`:95`): a always-present marker plus
optional color, so color is never the sole carrier of meaning. Do not reuse
`SymbolFail` (`:18`), which means failure; a warning is not a failure.

### 3. Injected callback on the poll layer

`internal/poll/orchestrate.go:19` (`poll.Deps`) gains a warning callback field,
for example:

```go
// Warn, when non-nil, receives non-fatal advisories. The poll layer never
// writes to a stream itself.
Warn func(code, message, hint string, details any)
```

`internal/poll/lifecycle.go:39-52` (`RecordFailure`) raises it on the
auto-disable branch (`:50`, `threshold > 0 && count >= threshold`). Note
`RecordFailure` currently takes loose parameters rather than `Deps`
(`internal/poll/consume.go:120` is the caller), so the callback has to be
threaded through explicitly or `RecordFailure` restructured. Either is fine;
what matters is that `internal/poll` imports no stream and no output package,
and that a nil callback is a no-op so existing unit tests keep compiling.

`internal/cli/poll.go` wires the callback to the renderer's `Warn` when building
`poll.Deps` (`:84-94`).

### 4. e2e scenario

Add a scenario that drives a feed past the failure threshold and asserts the
warning line on stderr, the unchanged exit code, and the feed disabled in the
store. The existing `all_failed` scenario is the closest starting point
(`internal/e2e/testdata/all_failed/`); the threshold comes from
`config.FailureThreshold`, so the scenario needs enough polls to reach it or a
lowered threshold if a flag or env var exposes it.

### 5. Documentation (folded in)

- `docs/usage.md`: the warning shape in the stream section's per-command text,
  and the auto-disable advisory in the poll section.
- `docs/specs/001-initial-implementation/manual-qa.md`: a step covering the
  advisory.
- `CHANGELOG.md`: additive entry.

## Invariants

- A warning never changes an exit code.
- A warning never appears on stdout.
- A warning is not a log record and must not duplicate envelope content
  (`docs/adr/0004-logging.md`). If the auto-disable also warrants a log line,
  pick one; the warning is the machine contract.
- `internal/poll` stays stream-blind.

## TDD plan

1. (tracer) `EmitWarning` writes exactly one newline-terminated object carrying
   `level:"warning"` and the passed code, message, hint, and details.
2. `hint` and `details` are omitted when empty.
3. Two successive calls produce two lines, each independently decodable (a
   valid NDJSON stream).
4. `Renderer.Warn` in text mode writes a marked human line, with the marker
   present when color is off, and writes nothing to stdout.
5. `poll.RecordFailure` invokes the callback exactly once, on the poll that
   reaches the threshold, and not on earlier failures.
6. A nil callback is a no-op.
7. e2e: a feed crossing the threshold produces the warning line on stderr under
   `--quiet`, the exit code is unchanged, and the feed is disabled afterwards.

## Acceptance Criteria

- `output.EmitWarning(w, code, message, hint, details)` produces exactly the
  reference bytes: one newline-terminated JSON object carrying
  `"level":"warning"`, with `hint` and `details` omitted when empty, and
  successive calls forming a valid NDJSON stream (a test decodes two lines).
- `Renderer.Warn` gates on format like `Result` and `Error`, uses a marker that
  is not the failure symbol, keeps the marker when color is off, and writes
  nothing to stdout.
- `internal/poll` raises the advisory only through an injected callback on
  `poll.Deps`; it imports no stream and no output package, and a nil callback is
  a no-op.
- The advisory fires exactly once, on the poll that crosses
  `FailureThreshold` in `internal/poll/lifecycle.go`, and not on earlier
  failures.
- The other three candidate producers (redirect rename, lossy charset fallback,
  guessed discover candidate) are not wired.
- An e2e scenario drives a feed past the threshold and asserts: the warning line
  on stderr under `--quiet`, an unchanged exit code, and the feed disabled in
  the store.
- `--quiet` does not suppress warnings, pinned by a test.
- No warning appears on stdout and no warning changes an exit code;
  `internal/cli/exit_conformance_test.go` and `internal/e2e/signal_test.go`
  pass, and all other e2e goldens are unchanged apart from the new scenario.
- The final warning code and hint are recorded in a note on this ticket.
- `docs/usage.md`, `docs/specs/001-initial-implementation/manual-qa.md`, and
  `CHANGELOG.md` updated and markdownlint clean.
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
