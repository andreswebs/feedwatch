---
id: fee-z3bm
status: open
deps: [fee-v737, fee-7hf3]
links: []
created: 2026-07-26T11:21:36Z
type: task
priority: 1
assignee: Andre Silva
tags: [adr, output, errors]
---
# reshape the stderr error envelope and drop the per-feed batch form

Reshape the stderr error envelope to the ADR 0005 contract (`{schema_version, ok:false, error:{code,message,hint,details}}`), move the feed-scoped fields under `error.details`, and remove the per-feed batch `{"errors":[...]}` emission entirely. Two breaking changes. Depends on the terr ticket (for the code set) and the envelope-head ticket (for `SchemaVersion`).

## Design

## Context

`docs/adr/0005-output-contract.md` requires that failures "render on stderr as an
error envelope whose contract shape is JSON: `schema_version`, `ok: false`, and
an `error` object with `code`, `message`, and optional `hint` and `details`,
populated from the typed errors of the error-handling ADR".

feedwatch currently renders `{"error":{category,feed_url,status,message}}` for a
single failure and `{"errors":[...]}` for a batch, via `errorPayload`
(`internal/output/output.go:21`), `payloadFor` (`:32`), `WriteError` (`:46`), and
`WriteErrors` (`:51`). That diverges on two axes at once: it is missing both the
envelope head and the standard error object shape.

## Owner decisions folded in

1. Codes are the finer unique set introduced by the terr ticket (for example
   `http_error`, `feed_unreachable`, `parse_error`, `timeout_error`,
   `usage_error`, `config_error`, `store_unavailable`, `schema_too_new`,
   `internal_error`), not the coarse `category` vocabulary. Read the final list
   from the terr ticket's notes and from `terr.All()`; do not invent codes here.
2. `feed_url` and `status` move under `error.details`, not to top-level siblings
   of `error`. A top-level extension would deviate from the uniform error
   envelope shape, and `details` is the sanctioned home for per-instance
   structure. The values come from `core.FeedError.ErrorDetails()`, added by the
   terr ticket.
3. The batch `{"errors":[...]}` stderr emission is removed outright. Per-feed
   failures are result data: they already appear in the stdout `failures` array
   (`PollFailure` at `internal/cli/poll.go:15`, `CheckFailure` at
   `internal/cli/check.go:16`), and the poll and check exit codes 2 and 3 are
   driven by the aggregate result, not by a returned error. stderr carries only
   whole-invocation error envelopes. This also resolves by construction the
   question of what `ok` should be for a partially-failed run (exit 3), which is
   not a whole-invocation failure at all: there is no batch envelope left to
   mislabel.

## Target shape

The reference is the `go-cookiecutter` template file
`{{cookiecutter.project_name}}/internal/output/output.go`:

```go
type errorEnvelope struct {
    SchemaVersion int         `json:"schema_version"`
    OK            bool        `json:"ok"`
    Error         errorDetail `json:"error"`
}

type errorDetail struct {
    Code    string `json:"code"`
    Message string `json:"message"`
    Hint    string `json:"hint,omitempty"`
    Details any    `json:"details,omitempty"`
}

func EmitError(w io.Writer, err error) {
    env := errorEnvelope{
        SchemaVersion: SchemaVersion,
        Error:         errorDetail{Code: "internal_error", Message: err.Error()},
    }

    var coded terr.Coded
    if errors.As(err, &coded) {
        env.Error.Code = coded.Code()
        env.Error.Hint = coded.Hint()
    }
    var detailed terr.Detailed
    if errors.As(err, &detailed) {
        env.Error.Details = detailed.ErrorDetails()
    }

    data, merr := json.Marshal(env)
    if merr != nil {
        // Unmarshalable details: degrade to the envelope without them,
        // which cannot fail to marshal.
        env.Error.Details = nil
        data, _ = json.Marshal(env)
    }
    _, _ = w.Write(append(data, '\n'))
}
```

Three properties of that function are contract, not incidental:

- `ok` is always false, so it is a literal, not a parameter.
- An unclassified error renders as `internal_error` so a missing classification
  stays visible instead of being silently mislabeled.
- The write is best-effort. A failure to report an error on stderr is
  unrecoverable, so it is never escalated over the error it describes. Note that
  this changes `WriteError`'s current signature: it returns `error` today
  (`internal/output/output.go:46`) and the reference returns nothing. Follow the
  reference; every current caller already discards the result
  (`internal/cli/root.go:151`, `:169`, `:201`).

Example output for a feed-scoped HTTP failure that reaches the boundary:

```json
{"schema_version":1,"ok":false,"error":{"code":"http_error","message":"server returned HTTP 404","details":{"feed_url":"http://feedserver/feed.xml","status":404}}}
```

## Work

### 1. `internal/output/output.go:21-57`

Replace `errorPayload`, `payloadFor`, and `WriteError` with the reference
`errorEnvelope`, `errorDetail`, and `EmitError`. Delete `WriteErrors` (`:51`)
entirely.

Note that `EmitError` takes a plain `error`, not a `*core.FeedError`. That is the
point: the boundary stops coercing everything into a `FeedError` and instead
resolves `terr.Coded` and `terr.Detailed` through the chain.

### 2. `internal/output/renderer.go`

- `Error` (`:71`) takes a plain `error` and delegates to `EmitError` in JSON
  mode.
- `Errors` (`:80`) is deleted.
- `textError` (`:95`) keeps rendering one symbol-marked line. It currently takes
  `*core.FeedError` and calls `e.Error()`; retarget it at a plain `error` and
  call `err.Error()`. The fail symbol must stay present regardless of color, so
  stripping color never removes meaning.

### 3. Call sites

- `internal/cli/poll.go:116-120`: delete the `r.Errors(feedErrs)` block. The
  `feedErrs` slice is still needed by `shapePollResult` (`:102`, `:107`), so keep
  the variable and only drop the emission.
- `internal/cli/check.go:138-142`: delete the `r.Errors(collectedErrs)` block.
  `collectedErrs` exists only for that emission (built at `:119`, `:129`), so
  remove it too, leaving `result.Failures` as the sole record.
- `internal/cli/root.go:151`, `:169`, `:201`: pass the error straight to the
  renderer; drop the `feedErrorFor` coercion (`:216`) if nothing else needs it.
  `feedErrorFor`'s classification switch has one behavior worth preserving: it
  maps `context.Canceled` and `context.DeadlineExceeded` to the timeout category
  so a graceful interrupt never surfaces as an internal error
  (`internal/cli/root.go:229-233`). Preserve that intent in whatever replaces
  it, and pin it with a test.

### 4. Tests to update or delete

- `internal/output/output_test.go:41` (`TestWriteErrorShape`), `:76`
  (`TestWriteErrorOmitsZeroFields`): rewrite against the new envelope.
- `internal/output/output_test.go:94` (`TestWriteErrorsShape`): delete with the
  batch form.
- `internal/output/renderer_test.go:101` (`TestRendererTextErrorsEach`): delete.
- `internal/output/renderer_test.go:68` (`TestRendererTextErrorSymbolAndColor`):
  keep passing.
- `internal/cli/root_test.go:81` (`errEnvelope`): update the decode target to
  the new shape; every test decoding stderr through it follows.

### 5. Out of scope, state it in the ticket record

The stdout `failures` arrays keep their current field names:
`PollFailure{feed_url, category, status, message}`
(`internal/cli/poll.go:15-20`) and `CheckFailure` (`internal/cli/check.go:16-21`)
are unchanged. They are result payloads, not error envelopes, and the finer code
set applies to the error envelope only. Do not opportunistically rename them; a
future ticket can if the owner wants the `category` enum retired from result
data too.

### 6. Documentation (folded in)

- `docs/usage.md:48-53`: the documented `category` vocabulary is no longer the
  machine code; describe the new `code` set and where `feed_url` and `status`
  now live.
- `docs/usage.md:204-207`, `:243`, `:258`: per-command text describing the
  stderr batch form and the error object.
- `docs/usage.md:39-65` is the stream-contract section and belongs to the final
  documentation ticket, not this one.
- `CHANGELOG.md`: one breaking entry with two parts, the error object reshape and
  the removal of the stderr batch form, stating explicitly that the per-feed
  detail is not lost because it remains in the stdout `failures` array.

### 7. Goldens

Regenerate with `-update` and review:
`internal/e2e/testdata/all_failed/poll.stderr` and `partial/poll.stderr` lose
their batch object and become empty. The corresponding `poll.stdout` files and
their `failures` arrays must be unchanged, and the exits must stay 2 and 3.

## TDD plan

1. (tracer) `EmitError` on a registered sentinel writes exactly
   `{"schema_version":1,"ok":false,"error":{"code":"usage_error","message":...,"hint":...}}`
   with a single trailing newline.
2. `hint` and `details` are omitted when empty.
3. A `*core.FeedError` renders its `feed_url` and `status` under
   `error.details`.
4. An unclassified `errors.New` error renders `"code":"internal_error"`, and
   `output.ExitCodeFor` on it returns 70.
5. Details that cannot marshal degrade to the envelope without them rather than
   producing no output.
6. A cancelled context at the boundary does not render as `internal_error`
   (the preserved `feedErrorFor` behavior).
7. Text mode still writes one symbol-marked line per failure, with the symbol
   present when color is off.
8. `poll` on an all-failed run writes nothing to stderr beyond logs, keeps its
   stdout `failures` array, and exits 2.

## Acceptance Criteria

- A single whole-invocation failure emits exactly one newline-terminated stderr
  line of the form
  `{"schema_version":1,"ok":false,"error":{"code":...,"message":...}}`, with
  `hint` and `details` present only when populated.
- A feed-scoped failure reaching the boundary renders `feed_url` and `status`
  under `error.details`, sourced from `core.FeedError.ErrorDetails()`.
- An unclassified error renders `"code":"internal_error"` and exits 70.
- Unmarshalable details degrade to the envelope without them; `EmitError` never
  escalates a stderr write failure over the error it describes.
- `output.WriteErrors` and `Renderer.Errors` no longer exist; the call sites at
  `internal/cli/poll.go:116-120` and `internal/cli/check.go:138-142` are gone;
  a grep for `"errors":[` across `internal/e2e/testdata` finds nothing.
- The stdout `failures` arrays are untouched: `PollFailure` and `CheckFailure`
  keep their `{feed_url, category, status, message}` field names.
- A cancelled or deadline-exceeded context at the boundary still does not
  surface as an internal error, pinned by a test.
- Text mode still emits one symbol-marked line per failure, with the symbol
  present when color is off
  (`internal/output/renderer_test.go:68` and `internal/cli/root_test.go:240`
  pass).
- `internal/e2e/testdata/all_failed/poll.stderr` and `partial/poll.stderr`
  regenerated (now free of the batch object) and reviewed; the matching
  `poll.stdout` files and their `failures` arrays are unchanged and the exits
  stay 2 and 3. `internal/e2e/signal_test.go` and
  `internal/cli/exit_conformance_test.go` pass.
- `docs/usage.md` (`:48-53`, `:204-207`, `:243`, `:258`) and `CHANGELOG.md`
  (one breaking entry covering both the reshape and the batch removal) updated
  and markdownlint clean.
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
