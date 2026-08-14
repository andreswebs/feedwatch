---
id: fee-7pg3
status: open
deps: [fee-pfpz, fee-o5uq]
links: []
created: 2026-08-14T02:46:34Z
type: task
priority: 2
assignee: Andre Silva
parent: fee-zs6b
tags: [cmd, tags]
---
# tags command: report the lane vocabulary

Add the flagless tags command and its App.Tags use case, reporting every distinct tag with its feed count sorted by tag, counting feeds of any status.

## Design

Add the `tags` command: report the lane vocabulary. This is what makes tags
discoverable to an agent that did not create them — without it, the only way to
learn which lanes exist is to `list` every feed and union the tags client-side.

Spec: [docs/specs/003-feed-tags/plan.md](docs/specs/003-feed-tags/plan.md),
section "`tags` — the lane vocabulary".

## Surface

```sh
feedwatch tags
# {"schema_version":1,"ok":true,"tags":[{"tag":"agents","feeds":12},{"tag":"ai","feeds":31}]}
```

No flags, no arguments. Sorted by tag name. Counts subscriptions of **any**
status, so a lane that has gone entirely disabled is still visible; an agent
that wants the active-only count filters `list --tag X` itself.

## New file: `tags.go` (root package)

```go
// TagsRequest asks for the tag vocabulary. It carries no fields: the command
// reports every tag, and narrowing is a job for list --tag. It exists so a
// frontend projects tags exactly like every other use case.
type TagsRequest struct{}

// Validate reports whether the request is usable. It has no fields, so it
// always succeeds; the method exists so every request type is validated
// uniformly by a frontend.
func (r TagsRequest) Validate() error { return nil }

// TagsResult is the tags result envelope: every distinct tag with the number of
// subscriptions carrying it, sorted by tag.
type TagsResult struct {
	Head
	Tags []core.TagCount `json:"tags"`
}
```

`core.TagCount{Tag string \`json:"tag"\`; Feeds int \`json:"feeds"\`}` is added
by the store ticket; if it landed without JSON tags, add them here.

`MarshalJSON` coalesces `Tags` to `[]` (ADR 0005: collections never serialize
as null, enforced by the envelope's own `MarshalJSON`).

`App.Tags` is a three-liner: validate, `resolveStore`, `st.TagCounts(ctx)`,
wrap in the envelope. All the work is in the store.

`RenderText` is a two-column tabwriter table (`TAG`, `FEEDS`) following
`ListResult.RenderText`.

**Do not** add a `--format`-specific shortcut, a `--json` flag, or a
count-suppressing flag. The empty-request shape is the point.

## New file: `internal/command/tags.go`

```go
func (d Deps) tagsCommand() *cliv3.Command {
	return &cliv3.Command{
		Name:      "tags",
		Usage:     "list every tag in use with the number of feeds carrying it",
		Arguments: argsFor(feedwatch.TagsRequest{}),
		Flags:     flagsFor(feedwatch.TagsRequest{}),
		Action:    d.tagsAction,
	}
}
```

`TagsRequest` is an empty struct, so `flagsFor`/`argsFor` return empty slices —
harmless and consistent with how `list` and `export` are declared. The action
still calls `bind(cmd, &req)`: it is a no-op today, but omitting it is exactly
the trap `list` and `export` fell into, where adding a field later produced a
flag that parsed and was silently discarded.

## Registration checklist

1. `internal/command/root.go`, `Deps.commands()` — append `d.tagsCommand()`
   immediately after `d.tagCommand()`. Slice position drives help and golden
   ordering.
2. `internal/command/schema_registry.go` —
   `"tags": {exitCodes: defaultExitCodes(), output: jsonschema.Reflect(feedwatch.TagsResult{})}`.
3. `internal/command/schema_test.go` — the `want` command-name list in
   `TestSchemaEnumeratesCommands` and the `objects` map.
4. `internal/command/envelope_test.go` — `TagsResult` in `envelopeCases` with
   the `tags` collection key.
5. Goldens: `testdata/schema/tags.stdout`, `testdata/help/tags.stdout`, plus
   regenerated `testdata/schema/all.stdout` and `testdata/help/root.stdout`.
   `go test ./internal/command -update -count=1`, then read the diff.

## TDD plan

Library tests in `tags_test.go` (external `package feedwatch_test`) with
`newTestApp(t)`; CLI tests in `internal/command/tags_test.go` (internal
`package command`) with a `runTags` helper modeled on `runList` and a
`tagsEnvelope` mirror struct.

1. **(tracer)** Three feeds tagged `["ai","agents"]`, `["ai"]`, and none: `tags`
   returns `[{agents,1},{ai,2}]`, sorted by tag, exit 0.
2. A store with no tagged feeds returns `"tags":[]` — assert on the raw stdout
   bytes so a `null` regression is caught.
3. A disabled feed's tags are still counted.
4. `--format text` renders the two-column table with a header.
5. `tags` accepts no positional argument: `feedwatch tags extra` exits 64.

Behavior 5 is worth an explicit test because an empty `Arguments` slice makes
urfave accept and ignore stray positionals in some configurations; confirm what
the tree actually does and pin it.

## Gotchas

- The command name `tags` and the flag `--tag` on other commands are one letter
  apart. Keep the usage strings unambiguous: `tags` lists the vocabulary,
  `--tag` filters by it.
- `core.TagCount` needs JSON tags for the envelope to serialize as
  `{"tag":...,"feeds":...}` rather than `{"Tag":...,"Feeds":...}`.

## Acceptance Criteria

- `feedwatch.TagsRequest`/`TagsResult` and `App.Tags` exist, with `Tags`
  coalescing to `[]` in `MarshalJSON` and a two-column `RenderText`.
- `feedwatch tags` reports every distinct tag with its feed count, sorted by
  tag, counting feeds of any status.
- The command is registered in `Deps.commands()`, `schemaRegistry`, the
  `schema_test.go` command-name list and `objects` map, and
  `envelope_test.go`'s `envelopeCases`; its action calls `bind`.
- Behaviors 1-5 covered across `tags_test.go` and
  `internal/command/tags_test.go`.
- Golden files for `tags`, plus `schema/all.stdout` and `help/root.stdout`, are
  regenerated and their diffs reviewed.
- `make build` passes.
