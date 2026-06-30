---
id: fee-qhx0
status: closed
deps: []
links: []
created: 2026-06-30T02:43:04Z
type: task
priority: 2
assignee: Andre Silva
tags: [schema, cli]
---

# Derive command output schemas from result structs

Replace the hand-authored output JSON Schema strings in
`internal/cli/schema_registry.go` with schemas reflected at init time from each
command's Go result struct, so the result type is the single source of truth and
the `output_schema` half of the `schema` contract can no longer drift from what
commands actually return.

Today `schemaRegistry` hand-maintains an `output` (`json.RawMessage`) per
command (raw JSON Schema strings, plus the `feedViewSchema` const and the
`feedObjectSchema`/`feedCollectionSchema` string builders). The flag and
argument halves of the contract are already introspected from the live `urfave`
tree in `schema.go`; only the exit-code table and the output schema are
hand-maintained. `TestSchemaDriftGuard` guards the flag half, but nothing guards
the output half: adding a field to `AddResult` lets its schema silently go stale.
Exit codes stay hand-maintained (they are not derivable from any type); this
ticket changes only the output schema.

## Design

Introduce a small, pure `internal/jsonschema` package that reflects a Go value
into a draft-07 JSON Schema, and rewire the registry to call it. Four mechanisms
cover all 13 commands:

1. Struct walk into `{type, properties, required}` (the 9 straightforward
   commands).
2. `oneOf` for a command with two result shapes (`migrate`).
3. Scalar plus description for a non-object output (`export`, and the
   self-describing `schema`).
4. An opacity marker that halts recursion (`poll`, `items`).

### 1. New package `src/internal/jsonschema/jsonschema.go`

Pure, no dependencies beyond stdlib (`encoding/json`, `reflect`, `strings`).
Public surface:

```go
package jsonschema

// Reflect returns the draft-07 JSON Schema for the struct type of zero.
func Reflect(zero any) json.RawMessage

// OneOf wraps alternative schemas as {"oneOf": [...]}.
func OneOf(alts ...json.RawMessage) json.RawMessage

// Scalar returns a primitive-typed schema with an optional description,
// e.g. Scalar("string", "OPML 2.0 document ...").
func Scalar(typ, description string) json.RawMessage
```

Internal representation marshaled to JSON:

```go
type schema struct {
    Type        string                     `json:"type,omitempty"`
    Properties  map[string]json.RawMessage `json:"properties,omitempty"`
    Required    []string                   `json:"required,omitempty"`
    Items       json.RawMessage            `json:"items,omitempty"`
    Description string                     `json:"description,omitempty"`
    OneOf       []json.RawMessage          `json:"oneOf,omitempty"`
}
```

Reflection rules in `reflectType(t reflect.Type) json.RawMessage`:

- Pointer: deref to the element type.
- Struct: `{"type":"object","properties":{...},"required":[...]}`. Iterate
  exported fields in declaration order (`t.NumField()` / `t.Field(i)`); skip
  `json:"-"`; field name from the `json` tag (before any comma) or the Go field
  name; a field is required unless its `json` tag carries `,omitempty`.
- Slice or array: `{"type":"array","items": <element schema>}`.
- Map: `{"type":"object"}` (freeform).
- `string` to `{"type":"string"}`; `bool` to `{"type":"boolean"}`; all `int`
  and `uint` kinds to `{"type":"integer"}`; float kinds to `{"type":"number"}`.

Note: the integer mapping is `integer`, not `number`. feedwatch's authored
schemas use `"type":"integer"` for counts, so integer and unsigned kinds must
map to `"integer"` and only float kinds to `"number"`.

Field tag `jsonschema`:

- `jsonschema:"opaque"` on a field stops recursion. The field's value type is
  rendered as a bare `{"type":"object"}` (struct or map) or
  `{"type":"array","items":{"type":"object"}}` (slice), regardless of the
  underlying element type.

`encoding/json` sorts map keys, so `properties` emit in alphabetical order
deterministically; `required` preserves struct declaration order.

### 2. New test `src/internal/jsonschema/jsonschema_test.go`

Table-driven coverage of every rule, asserting on the parsed schema rather than
raw bytes:

- A flat struct yields `properties` plus `required` from non-`omitempty` fields.
- An `omitempty` field is present in `properties`, absent from `required`.
- A `json:"-"` field is absent entirely.
- A nested struct and a slice-of-struct recurse into a nested object and an
  array-of-object.
- `integer` vs `number` vs `string` vs `boolean` kind mapping.
- `jsonschema:"opaque"` on a slice-of-struct yields
  `{"type":"array","items":{"type":"object"}}` with no expansion.
- `OneOf(a, b)` yields `{"oneOf":[a,b]}`.
- `Scalar("string", "desc")` yields `{"type":"string","description":"desc"}`.

### 3. Rewire `src/internal/cli/schema_registry.go`

Replace the hand-authored `output` strings with derived calls; keep
`exitCodes`, `registryFor`, the permissive fallback, and
`defaultExitCodes`/`pollExitCodes`. Delete the `feedViewSchema` const and the
`feedObjectSchema`/`feedCollectionSchema` builders (`FeedView` is reached by
recursion).

```go
var schemaRegistry = map[string]cmdMeta{
    "migrate":  {exitCodes: defaultExitCodes(), output: jsonschema.OneOf(jsonschema.Reflect(MigrateApplied{}), jsonschema.Reflect(MigrateStatus{}))},
    "poll":     {exitCodes: pollExitCodes(), output: jsonschema.Reflect(PollResult{})},
    "add":      {exitCodes: defaultExitCodes(), output: jsonschema.Reflect(AddResult{})},
    "list":     {exitCodes: defaultExitCodes(), output: jsonschema.Reflect(ListResult{})},
    "rm":       {exitCodes: defaultExitCodes(), output: jsonschema.Reflect(RmResult{})},
    "enable":   {exitCodes: defaultExitCodes(), output: jsonschema.Reflect(EnableResult{})},
    "disable":  {exitCodes: defaultExitCodes(), output: jsonschema.Reflect(DisableResult{})},
    "items":    {exitCodes: defaultExitCodes(), output: jsonschema.Reflect(ItemsResult{})},
    "prune":    {exitCodes: defaultExitCodes(), output: jsonschema.Reflect(PruneResult{})},
    "discover": {exitCodes: defaultExitCodes(), output: jsonschema.Reflect(DiscoverResult{})},
    "import":   {exitCodes: defaultExitCodes(), output: jsonschema.Reflect(ImportResult{})},
    "export":   {exitCodes: defaultExitCodes(), output: jsonschema.Scalar("string", "OPML 2.0 XML document written to the output file or stdout; not a JSON envelope")},
    "schema":   {exitCodes: defaultExitCodes(), output: jsonschema.Scalar("object", "a CommandSchema when narrowed to one command, otherwise {commands,global_flags}")},
}
```

The `cmdMeta` struct and `registryFor` are unchanged: `output` is still
`json.RawMessage`, now produced by the reflector rather than a literal. Function
calls in a package-level `var` map literal run at init, so the schemas are built
once at startup.

### 4. Opacity tags on the item-carrying results

`poll` and `items` keep their `items` array opaque (a bare object element).
`items` in particular MUST stay opaque: the `items` command's `--fields` flag
projects to a caller-chosen subset, so the per-item shape is dynamic and no
fixed schema is correct. Add the tag:

```go
// src/internal/cli/poll.go, in PollResult
Items []core.Item `json:"items" jsonschema:"opaque"`

// src/internal/cli/items.go, in ItemsResult
Items []core.Item `json:"items" jsonschema:"opaque"`
```

### 5. Tests in `src/internal/cli`

- The existing `schema_test.go` tests must still pass unchanged (poll output is
  non-empty valid JSON, bare schema lists all commands, flag types, args,
  unknown command, flag drift guard).
- Add `TestOutputSchemaContractPreserved`: for each command, assert the derived
  schema's `required` set and `properties` key set match the previously authored
  contract (encode the expected sets in the test). This documents the contract
  and proves the migration did not change it, modulo the intended delta below.
  It is the output-half twin of `TestSchemaDriftGuard`.

### Known intended delta

Deriving `ImportResult` marks the `failed` element's `xmlUrl` and `reason` as
`required` (both are non-`omitempty` and always emitted). The previously
authored `import` schema omitted `required` on that element, so the derived
schema is strictly more accurate. This is the one place the emitted schema
changes meaning; it tightens to match real output. Every other command's derived
schema is semantically identical to the authored one. Property key order also
becomes alphabetical, which JSON Schema treats as insignificant for
`properties`.

## Acceptance Criteria

- New `src/internal/jsonschema` package with `Reflect`/`OneOf`/`Scalar` and
  table-driven tests; no non-stdlib dependency added.
- `schema_registry.go` derives every command's `output` via the reflector;
  `feedViewSchema`, `feedObjectSchema`, and `feedCollectionSchema` are deleted;
  the exit-code tables and `registryFor` are unchanged.
- `PollResult.Items` and `ItemsResult.Items` carry `jsonschema:"opaque"`; their
  `output_schema` still renders `items` as
  `{"type":"array","items":{"type":"object"}}`.
- `feedwatch schema migrate` still emits a `oneOf` of the two shapes;
  `feedwatch schema export` still emits the string schema with its description.
- `TestOutputSchemaContractPreserved` asserts derived `properties`/`required`
  match the authored contract for all commands, with the documented
  `import.failed` tightening.
- A field added to any result struct appears in its `output_schema` with no
  registry edit (the output-half drift guard).
- `make build` passes (fmt-check, vet, lint, test).

## Files

```text
src/internal/jsonschema/jsonschema.go       (new)
src/internal/jsonschema/jsonschema_test.go  (new)
src/internal/jsonschema/doc.go              (new)
src/internal/cli/schema_registry.go         (rewire; delete 3 builders + const)
src/internal/cli/poll.go                    (opaque tag on PollResult.Items)
src/internal/cli/items.go                   (opaque tag on ItemsResult.Items)
src/internal/cli/schema_test.go             (add TestOutputSchemaContractPreserved)
```

## Notes

**2026-06-30T02:53:28Z**

Added internal/jsonschema (Reflect/OneOf/Scalar, draft-07, stdlib-only) and rewired internal/cli/schema_registry.go to derive every command's output_schema from its Go result struct. Deleted feedViewSchema const + feedObjectSchema/feedCollectionSchema builders (FeedView now reached by recursion). PollResult.Items and ItemsResult.Items carry jsonschema:"opaque" so items stays an array-of-bare-object. Integer kinds map to "integer" (not "number"). Two non-object outputs: migrate is OneOf(applied,status); export/schema are Scalar. One intended semantic delta: import.failed[] now marks xmlUrl/reason required (strictly more accurate). New TestOutputSchemaContractPreserved pins properties/required per command (white-box via registryFor) as the output-half twin of TestSchemaDriftGuard. make build green. Note: cmd/qafixtures/* showed transient fmt/test flakiness during the run from concurrent external edits; resolved on its own, unrelated to this change.
