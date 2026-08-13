---
id: fee-savm
status: closed
deps: [fee-gvuo]
links: []
created: 2026-08-13T14:03:47Z
type: task
priority: 2
assignee: Andre Silva
parent: fee-ui25
tags: [lib, cli]
---

# Derive CLI flags from request-struct tags by reflection

Seventh step of
[docs/adr/0007-library-and-frontends.md](../docs/adr/0007-library-and-frontends.md).
The request types are already the single source of truth for what a use case
accepts. This ticket makes the CLI's flag surface a *projection* of them
instead of a parallel declaration, so a new field cannot exist without a flag
and a renamed field cannot leave a stale flag behind.

The ADR is precise about the boundary: **reflection describes shape, ordinary Go
describes semantics.** Tags carry names, types, usage text, and defaults.
Validation stays in the `Validate() error` methods written in `fee-rzwl`,
`fee-kj8z`, and `fee-gvuo`. Do not add a validation tag language; that is the
failure mode this design exists to avoid.

## Design

### 1. Tags on the request types

Add to the library request types, using the *exact* usage strings currently
declared in `internal/command`, since they are pinned by the `schema` command's
output and by help text:

```go
type ItemsRequest struct {
    Feeds     []string `flag:"feed" usage:"feed url or alias to query (repeatable); all feeds when omitted"`
    Since     string   `flag:"since" usage:"lower time bound: RFC3339 or relative such as 24h or 7d"`
    Until     string   `flag:"until" usage:"upper time bound: RFC3339 or relative such as 24h or 7d"`
    Limit     int      `flag:"limit" usage:"maximum items to return; 0 returns all"`
    Offset    int      `flag:"offset" usage:"items to skip before returning results"`
    Order     string   `flag:"order" default:"published desc" usage:"sort: 'published|fetched asc|desc'"`
    TimeField string   `flag:"time-field" default:"published" usage:"axis for --since/--until: 'published' or 'fetched'"`
    Contains  string   `flag:"contains" usage:"substring matched over title and content"`
    Fields    []string `flag:"fields" usage:"project to a subset of item fields (...); full item when omitted"`
}
```

Four tag keys, all shape:

| Tag       | Meaning                                                              |
| --------- | -------------------------------------------------------------------- |
| `flag`    | flag name; `-` excludes the field from the CLI surface entirely       |
| `arg`     | the field is a positional argument with this name, not a flag         |
| `alias`   | comma-separated flag aliases                                          |
| `default` | compiled-in default, as the string the flag's parser accepts          |
| `usage`   | usage text                                                            |

`arg` is read only by the CLI projector; an HTTP projector will ignore it and
treat the field as a path or query parameter. That is the ADR's rule in
practice: shared shape in the tags, frontend-specific interpretation in the
frontend.

Coverage of the current surface:

- `arg:"url"` on `AddRequest.URL` and `DiscoverRequest.URL`; `arg:"ref"` on
  `RemoveRequest.Ref`, `EnableRequest.Ref`, `DisableRequest.Ref`;
  `arg:"file"` is *not* used, because `ImportRequest.OPML` is bytes, not a path
  (see below).
- `PollRequest.Feeds` and `CheckRequest.Feeds` are variadic positionals:
  `arg:"feed" variadic:"true"`, matching today's `ArgsUsage: "[FEED...]"`.
- `PollRequest.Force` carries `flag:"force" alias:"all"`.
- `AddRequest.Alias` and `AddRequest.Interval` keep their current usage text;
  `Interval` is a `time.Duration`.
- `PruneRequest.KeepDays` and `MaxItems` are `*int`, so the projector must map
  a pointer kind to a plain flag whose absence leaves the pointer nil. This is
  the mechanism that preserves "`--keep-days 0` differs from an absent flag".
- `ImportRequest.OPML` carries `flag:"-"`: the CLI reads the document from a
  file or stdin, and that path argument belongs to the CLI, not to the request.
  `ImportRequest.Validate` maps to the negative CLI flag and therefore also
  carries `flag:"-"`; the CLI keeps declaring `--no-validate` by hand and
  inverting it. Handling negation in the tag language would be the first step
  down the mini-language road.
- `ListRequest` and `ExportRequest` are empty; the projector must return empty
  flag and argument slices for them without special-casing.

Global flags (`--db`, `--format`, `--concurrency`, and the rest) are **not**
derived: they map to `feedwatch.Config`, not to a request, and stay declared in
[internal/command/flags.go](../internal/command/flags.go).

### 2. The projector

New file `internal/command/reflectflags.go`:

```go
// flagsFor derives the flag set a request type accepts from its struct tags.
func flagsFor(req any) []cliv3.Flag

// argsFor derives the positional arguments a request type accepts.
func argsFor(req any) []cliv3.Argument

// bind fills a request from the parsed command, honoring set-vs-unset for
// pointer fields.
func bind(cmd *cliv3.Command, req any) error
```

The whole of the reflection risk is one mapping table from Go kind to flag
kind:

| Go type         | Flag                | Bind                                        |
| --------------- | ------------------- | ------------------------------------------- |
| `string`        | `StringFlag`        | `cmd.String(name)`                          |
| `bool`          | `BoolFlag`          | `cmd.Bool(name)`                            |
| `int`           | `IntFlag`           | `cmd.Int(name)`                             |
| `*int`          | `IntFlag`           | set only when `cmd.IsSet(name)`             |
| `time.Duration` | `DurationFlag`      | `cmd.Duration(name)`                        |
| `[]string`      | `StringSliceFlag`   | `cmd.StringSlice(name)`                     |

A field whose type is absent from the table is a programming error that cannot
be caught at compile time, so `flagsFor` must `panic` with a message naming the
struct, field, and type rather than skipping the field silently. A panic at
command-tree construction is reached by every test in the suite, which turns
the failure into an immediate, unmissable one.

### 3. Wiring

Each command definition drops its hand-written `Flags` and `Arguments` slices:

```go
func (d Deps) itemsCommand() *cliv3.Command {
    return &cliv3.Command{
        Name:      "items",
        Usage:     "query stored item history with filters, ordering, and pagination",
        Flags:     flagsFor(feedwatch.ItemsRequest{}),
        Arguments: argsFor(feedwatch.ItemsRequest{}),
        Action:    d.itemsAction,
    }
}
```

and each action starts with `bind`:

```go
var req feedwatch.ItemsRequest
if err := bind(cmd, &req); err != nil {
    return err
}
```

`Name` and `Usage` on the command itself stay hand-written: they are the
command's own metadata, not the request's shape.

### 4. What must not change

The `schema` command introspects the live command tree, so a derived flag must
be indistinguishable from the hand-declared one it replaces: same primary name,
same aliases, same type name, same default, same usage. `feedwatch schema` and
`feedwatch schema <command>` output must be byte-identical before and after.

## TDD notes

Build the projector bottom-up with a table-driven test before wiring any
command, then migrate commands one at a time.

1. **RED**: `flagsFor` on a local test struct with one `string` field yields
   one `*cliv3.StringFlag` with that name and usage. **GREEN**: implement the
   string case only.
2. Add one type per cycle: `bool`, `int`, `time.Duration`, `[]string`, `*int`.
   Each is one red test then one table entry.
3. `flag:"-"` excludes a field. `alias` produces aliases. `default` sets the
   flag's `Value`.
4. `argsFor` maps `arg:"url"` to a `*cliv3.StringArg` and `variadic:"true"` to
   a `*cliv3.StringArgs`.
5. An unmapped type panics with a message naming struct, field, and type
   (`func() { flagsFor(badRequest{}) }` asserted with a recover helper).
6. `bind` round-trips: build a command from a request type, run it with
   argv, assert the bound struct equals the expected value. Cover the
   `*int` set-vs-unset asymmetry explicitly, since it is the one case where the
   Go type and the flag type disagree.

Then the two guards that give this ticket its value:

- **Mapping coverage table**: a test listing every wired request type, calling
  `flagsFor` and `argsFor` on each, asserting no panic and that the derived
  flag count equals the expected count per command. It is the executable
  version of "every field maps to a known kind". Note in a comment that the
  list must be extended when a use case is added; an unwired request type
  cannot affect the CLI, so the list being CLI-side is intentional.
- **Surface equivalence**: before starting, capture the current
  `feedwatch schema` output as a fixture; after wiring, assert the new output
  equals it. The existing `TestSchemaDriftGuard` stays green alongside it.

Migrate commands one at a time, running the full suite between each, so a
divergence is attributable to one command.

## Acceptance Criteria

- Every library request type carries `flag`, `arg`, `alias`, `default`, and
  `usage` tags as needed; no validation tag exists.
- `internal/command/reflectflags.go` provides `flagsFor`, `argsFor`, and
  `bind`, with a documented type mapping table.
- An unmapped field type panics at command-tree construction with a message
  naming the struct, field, and type.
- All fourteen command definitions derive `Flags` and `Arguments` from their
  request type, except the hand-kept `--no-validate` on `import` and
  `--status` on `migrate` (neither is a request field), and `--o` on `export`
  (a frontend output destination, not a request field).
- Global flags remain hand-declared in `flags.go`.
- `feedwatch schema` and `feedwatch schema <command>` output is byte-identical
  to the pre-change output, pinned by a fixture-based test.
- `--keep-days 0` remains distinguishable from an absent `--keep-days`.
- Every `internal/command/testdata/**` golden compares byte-identical with no
  `-update` run.
- `make build` passes.

## Files

```text
internal/command/reflectflags.go        (new)
internal/command/reflectflags_test.go   (new)
internal/command/schema_test.go         (surface-equivalence fixture)
internal/command/*.go                   (command definitions derive flags; actions call bind)
items.go, poll.go, check.go, add.go, discover.go, rm.go, enable.go,
    disable.go, prune.go, list.go, import.go, export.go   (root package: tags on request types)
```

## Notes

**2026-08-13T15:26:49Z**

Done. internal/command/reflectflags.go provides flagsFor/argsFor/bind over a single flagKinds table mapping reflect.Type -> {newFlag, bind}, so construction and binding cannot drift. Supported types: string, bool, int, *int, time.Duration, []string. Tags: flag ("-" excludes), arg, alias, default, usage, variadic. All twelve request types carry tags; no validation tag exists. Two panics at command-tree construction: an unmapped field type, and a field carrying neither flag nor arg (a new field cannot silently have no CLI surface).

Hand-kept by design: --no-validate and the file argument on import (ImportRequest.OPML is bytes, not a path; negation in a tag would start a mini-language), -o on export (an output destination, not a request field), --status on migrate (no request type at all), and global flags in flags.go (they map to Config). --fields keeps a CLI-side usage override via withUsage, because its text enumerates core.ItemFieldNames() and a struct tag is a compile-time constant.

DEVIATION worth reading: schema output is byte-identical for every flag on every command, but NOT for the args array of poll and check. Both accepted trailing feed refs through urfave's implicit positional tail while declaring no Arguments, so 'schema poll' reported args:[] even though ArgsUsage said [FEED...] and docs/cli-design.md line 179 documented args:[{name:feed,variadic:true}]. Declaring the variadic argument (as this ticket's design mandates) fixes that drift; the design doc was right and the implementation was wrong. Those two fixtures were regenerated deliberately, docs/usage.md line 488 was corrected to match, and nothing else changed. All pre-existing internal/command/testdata/** goldens compared byte-identical with no -update run.

Note for the next person: a variadic StringArgs needs Max: -1 or the framework refuses to parse it, and declaring one moves values from cmd.Args().Slice() to cmd.StringArgs(name).

New fixtures: testdata/schema/*.stdout pins the machine-readable input surface, testdata/help/*.stdout pins the usage strings (FlagSchema carries no usage, so schema alone would not catch a mangled usage tag). The bare-schema golden normalizes the errors array to a token: TestSchemaNewSentinelAppears registers a test-only sentinel into the process-global terr registry and cannot unregister it, so that array is not stable across a package run.

make build and make test-race pass.
