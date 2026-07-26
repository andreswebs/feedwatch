---
id: fee-7hf3
status: closed
deps: []
links: []
created: 2026-07-26T11:17:21Z
type: task
priority: 1
assignee: Andre Silva
tags: [adr, output]
---
# adopt the ADR 0005 result envelope head and null-coalescing marshaling

Add the ADR 0005 result envelope head (`schema_version`, `ok`) to every JSON result on stdout, enforce non-null collections through each envelope's own `MarshalJSON`, teach `internal/jsonschema` to inline embedded structs, keep the head out of `--format text`, and rename the colliding migrate `schema_version` payload field to `store_schema_version`. Runs in parallel with the terr ticket.

## Design

## Context

`docs/adr/0005-output-contract.md` requires that "every result envelope opens
with the same head: `schema_version` (an integer, bumped on breaking shape
changes) and `ok` (a boolean), followed by the command-specific payload", and
that "collections never serialize as `null`: absent lists are `[]`, absent maps
are `{}`, enforced by the envelope's own `MarshalJSON`, not by call-site
discipline".

Today `internal/output/output.go:13` (`WriteJSON`) encodes the result value
directly and every result struct is a bare payload. Some envelopes already
hand-build empty slices at the call site (`internal/cli/check.go:117`,
`internal/cli/poll.go:132`, `:142`), which is exactly the call-site discipline
the ADR replaces.

## Target shape

The reference is the `go-cookiecutter` template, files
`{{cookiecutter.project_name}}/internal/output/output.go` and
`.../internal/output/envelopes.go`:

```go
// SchemaVersion is the version of the output contract, bumped on breaking
// shape changes to any envelope.
const SchemaVersion = 1

// Head opens every result envelope: embed it as the first field of each
// command's envelope struct.
type Head struct {
    SchemaVersion int  `json:"schema_version"`
    OK            bool `json:"ok"`
}

func OKHead() Head { return Head{SchemaVersion: SchemaVersion, OK: true} }
```

and the null-coalescing pattern, one method per envelope that owns a
collection:

```go
func (e SchemaEnvelope) MarshalJSON() ([]byte, error) {
    type alias SchemaEnvelope
    a := alias(e)
    if a.Commands == nil {
        a.Commands = []SchemaCommand{}
    }
    return json.Marshal(a)
}
```

The `type alias` indirection is load-bearing: it drops the method set so
`json.Marshal` does not recurse infinitely.

## Work

### 1. `internal/output/output.go`

Add `SchemaVersion`, `Head`, `OKHead`. Keep `WriteJSON` or rename it to
`EmitJSON` to match the reference; if renamed, update
`internal/output/renderer.go:61` and `internal/cli/version.go:42`.
`Renderer.Result` (`internal/output/renderer.go:59`) keeps its signature: the
head arrives with the value it is handed.

### 2. Embed the head in every result envelope

Add `output.Head` as the first field and set `output.OKHead()` at construction.
Envelopes, all in `internal/cli`:

| Envelope                                    | Declared at      | Constructed at             |
| ------------------------------------------- | ---------------- | -------------------------- |
| `MigrateStatus`                             | `migrate.go:11`  | `migrate.go:70`            |
| `MigrateApplied`                            | `migrate.go:19`  | `migrate.go:81`            |
| `PollResult`                                | `poll.go:31`     | `poll.go:145` (in `shapePollResult`, used at `:102` and `:107`) |
| `CheckResult`                               | `check.go:26`    | `check.go:115`             |
| `AddResult`                                 | `add.go:19`      | `add.go:92`                |
| `ListResult`                                | `list.go:15`     | `list.go:73`               |
| `RmResult`                                  | `rm.go:11`       | `rm.go:54`                 |
| `EnableResult`                              | `enable.go:13`   | `enable.go:69`             |
| `DisableResult`                             | `disable.go:13`  | `disable.go:61`            |
| `ItemsResult`                               | `items.go:21`    | `items.go:101`             |
| `ProjectedItemsResult`                      | `items.go:31`    | `items.go:94`              |
| `PruneResult`                               | `prune.go:15`    | `prune.go:59`              |
| `DiscoverResult`                            | `discover.go:18` | `discover.go:60`           |
| `ImportResult`                              | `import.go:21`   | `import.go:106`            |
| `SchemaResult`                              | `schema.go:40`   | `schema.go:71`             |
| `CommandSchema`                             | `schema.go:14`   | `schema.go:113` (rendered directly at `schema.go:68`) |
| the anonymous `--version` struct            | `version.go:28`  | `version.go:42`            |

`CommandSchema` is rendered as a top-level result by `schema --command CMD`
(`schema.go:68`) and is also nested inside `SchemaResult.Commands`, so a head on
it appears nested too. Either accept that (it is self-describing and harmless)
or wrap the narrowed case in its own single-command envelope; state the choice
in a note.

`FeedView` (`list.go:21`), `PollFailure` (`poll.go:15`), `CheckFailure`
(`check.go:16`), and `ImportFail` (`import.go:29`) are nested payload structs,
not envelopes: no head.

`export` is the one deliberate exception (`internal/cli/export.go`,
`exportAction`): its payload is an OPML document, not a JSON envelope. Leave it
alone.

Promote the anonymous `--version` struct (`version.go:28`) to a named envelope
with a head: `--version` is a JSON result on stdout like any other.

### 3. Null-coalescing `MarshalJSON`

Add one per envelope that owns a collection: `PollResult` (`items`, `failures`,
`renamed`), `CheckResult` (`failures`), `ItemsResult` (`items`),
`ProjectedItemsResult` (`items`), `ListResult` (`feeds`), `DiscoverResult`
(`candidates`), `ImportResult` (`failed`), `SchemaResult` (`commands`,
`global_flags`), `CommandSchema` (`args`, `flags`).

Once the marshaler guarantees it, the call-site `make(...)` calls that exist
only to avoid `null` can go: `internal/cli/check.go:117`,
`internal/cli/poll.go:132`, `:142`. Removing them is the point of the ADR rule.

### 4. `internal/jsonschema`: inline anonymous embedded structs

`reflectStruct` (`internal/jsonschema/jsonschema.go:78`) iterates fields and
takes the property name from the json tag, falling back to the Go field name
(`jsonField`, `:104`). An anonymous embedded `output.Head` has no json tag, so
it would emit a nested property named `Head` instead of flattening
`schema_version` and `ok` into the parent object, corrupting every
`output_schema` in `internal/cli/schema_registry.go:81-96`.

Fix: when a field is anonymous, has no json tag, and is a struct, recurse and
merge its properties and required entries into the parent. The reflector stays
the single source of truth for `output_schema`; the hand-written `MarshalJSON`
only fills nulls and never renames or omits.

### 5. Resolve the `schema_version` key collision

`MigrateStatus.SchemaVersion` (`internal/cli/migrate.go:12`) and
`MigrateApplied.SchemaVersion` (`internal/cli/migrate.go:21`) already occupy the
`schema_version` json key, meaning the store schema version. Two fields with the
same key in one object is a silent contract break.

Owner decision: rename the payload field to `store_schema_version`. The head's
`schema_version` is the fleet contract name and cannot be renamed, and nesting
the migrate payload would break envelope-depth uniformity.

Resulting envelopes:

```json
{"schema_version":1,"ok":true,"store_schema_version":1,"pending":0,"backend":"sqlite"}
{"schema_version":1,"ok":true,"applied":1,"store_schema_version":1}
```

Breaking change: `CHANGELOG.md` entry under `## [Unreleased]`.

### 6. Keep the head out of `--format text`

The five `RenderText` implementations (`list.go:79`, `discover.go:80`,
`prune.go:94`, `items.go:237`, `items.go:258`) hand-build their output and are
unaffected. The generic fallback `renderText`
(`internal/output/renderer.go:108`) walks every exported field by json tag, so
an embedded `Head` would leak `schema_version:` and `ok:` lines into text output
for the eleven envelopes with no `RenderText` (`AddResult`, `RmResult`,
`EnableResult`, `DisableResult`, `MigrateStatus`, `MigrateApplied`,
`CheckResult`, `PollResult`, `ImportResult`, `SchemaResult`, `CommandSchema`).

Make the fallback skip the embedded head (skip anonymous embedded struct fields,
or skip `output.Head` by type) and pin it with a test. `--format text` output
must be byte-identical to before for every command.

### 7. Documentation (folded in, not deferred)

- `docs/usage.md`: every per-command JSON example gains the head; the migrate
  examples at `:423-425` also get the field rename.
- `docs/specs/001-initial-implementation/manual-qa.md`: update the asserted
  shapes.
- `CHANGELOG.md`: one entry for the head (additive) and one for the
  `store_schema_version` rename (breaking).
- Lint with `markdownlint-cli2 --config .markdownlint.yaml --fix <files>`.

### 8. Goldens

Regenerate `internal/e2e` goldens with `-update` and review the diff: every JSON
stdout file gains exactly the two leading keys, `migrate_status.stdout` also
gains the field rename, and no stderr file and no exit code changes. ADR 0006
makes that diff the evidence, so read it before accepting it.

## TDD plan

1. (tracer) `output.OKHead()` marshals as `{"schema_version":1,"ok":true}`.
2. A table over every envelope type: marshal the zero value and assert the first
   two keys are `schema_version` and `ok` (decode into
   `map[string]json.RawMessage` plus a prefix check on the raw bytes).
3. The same table asserts every declared collection marshals as `[]`, never
   `null`, from the zero value.
4. `jsonschema.Reflect` on an envelope with an embedded head yields top-level
   `schema_version` and `ok` properties, both required, and no `Head` property.
5. `renderText` on a headless-but-embedded envelope emits no `schema_version` or
   `ok` line.
6. The migrate envelopes marshal with exactly one `schema_version` key and a
   `store_schema_version` payload field.

## Acceptance Criteria

- `output.SchemaVersion`, `output.Head`, and `output.OKHead()` exist, and every
  JSON result on stdout opens with `{"schema_version":1,"ok":true,...}`,
  verified by a test that iterates the envelope types rather than by per-command
  assertions alone.
- No collection ever serializes as `null`: a table test constructs each envelope
  zero-valued and asserts `[]` for every declared list, and the call-site
  `make(...)` calls that existed only to avoid `null`
  (`internal/cli/check.go:117`, `internal/cli/poll.go:132`, `:142`) are gone.
- `schema --command CMD` reports an `output_schema` whose properties include
  `schema_version` and `ok` at the top level, both required, with no `Head`
  property, for every command.
- The migrate envelopes carry `store_schema_version` and exactly one
  `schema_version` key; `docs/usage.md:423-425` and the manual QA plan match.
- `--format text` output is byte-identical to before for every command, pinned
  by a test that the generic fallback omits the head.
- `export` still emits a bare OPML document with no envelope.
- `internal/e2e` goldens regenerated with `-update` and reviewed: only the two
  new leading keys per JSON stdout file plus the migrate rename; no stderr file
  changed and no exit code changed. `internal/e2e/signal_test.go` and
  `internal/cli/exit_conformance_test.go` pass.
- `docs/usage.md`, `docs/specs/001-initial-implementation/manual-qa.md`, and
  `CHANGELOG.md` (head entry plus breaking rename entry) updated and
  markdownlint clean.
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

**2026-07-26T11:55:22Z**

Adopted the ADR 0005 result envelope head across all stdout envelopes.

Done:

- output.SchemaVersion, output.Head, output.OKHead() added; every JSON result now opens with {"schema_version":1,"ok":true,...}.
- Head embedded in all 17 result envelopes (incl. promoted VersionResult); null-coalescing MarshalJSON added to every collection-owning envelope (Poll, Check, List, Items, ProjectedItems, Discover, Import, Schema, CommandSchema). Removed the call-site make()/nil-guards that existed only to avoid null (check, poll x2, items).
- jsonschema.structSchema now inlines anonymous embedded structs, so output_schema shows schema_version+ok at top level (required) with no Head property; migrate output_schema (OneOf) inherits it.
- Generic renderText skips anonymous embedded fields, so --format text is byte-identical to before (pinned by TestRendererTextOmitsEmbeddedHead).
- Key-collision renames (both breaking, in CHANGELOG): migrate schema_version -> store_schema_version; AND check ok(int) -> passed (NOT in original ticket, but the head's ok boolean would have silently shadowed it via encoding/json field-depth rule).

Tests: new envelope table (head-leads + collections-never-null over all types), jsonschema inlining, renderText head-skip, output.OKHead tracer. Updated TestOutputSchemaContractPreserved, migrate_test, check_test. e2e goldens regenerated with -update: only the two leading keys per .stdout + migrate rename; no .stderr or exit-code change.

Docs: usage.md (head note + all per-command examples + migrate/check renames), manual-qa.md shapes, CHANGELOG (Added: head; Changed: two breaking renames). export left as bare OPML (deliberate exception). make build green.
