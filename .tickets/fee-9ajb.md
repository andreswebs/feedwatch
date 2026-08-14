---
id: fee-9ajb
status: open
deps: [fee-pfpz, fee-o5uq]
links: []
created: 2026-08-14T02:45:14Z
type: task
priority: 2
assignee: Andre Silva
parent: fee-zs6b
tags: [cmd, tags]
---
# tag command: read and edit a feed's tags

Add the flat tag command and its App.Tag use case: read mode with no flags, plus --add/--remove/--set/--clear with mutual exclusion enforced in Validate, reporting the actual delta in added/removed.

## Design

Add the `tag` command: read and edit one feed's tags. Library use case plus its
CLI projection, per ADR 0007 (the library owns the request/result types; the CLI
is a thin projection derived from struct tags).

Spec: [docs/specs/003-feed-tags/plan.md](docs/specs/003-feed-tags/plan.md),
section "`tag` — manage tags on existing feeds".

## Surface

```sh
feedwatch tag REF                        # read
feedwatch tag REF --add ai --add agents  # idempotent
feedwatch tag REF --remove security
feedwatch tag REF --set ai,agents,research
feedwatch tag REF --clear
```

`REF` is a feed URL or its unique alias, resolved by `store.GetFeed` exactly as
`enable`, `disable`, and `rm` resolve it. A flat command, not a nested group:
the tree has no nesting today and this is not the ticket to introduce it.

## New file: `tag.go` (root package)

Copy the anatomy of `enable.go` verbatim — it is the canonical small use case.

```go
// TagRequest names the subscription whose tags to read or edit, by URL or
// unique alias. With no write flag it reads. Add and Remove compose in one
// invocation; Set and Clear are each exclusive with everything else.
type TagRequest struct {
	Ref    string   `arg:"ref"`
	Add    []string `flag:"add" usage:"tag to add (repeatable); idempotent"`
	Remove []string `flag:"remove" usage:"tag to remove (repeatable); removing an absent tag is a no-op"`
	Set    []string `flag:"set" usage:"replace the feed's tags with exactly these"`
	Clear  bool     `flag:"clear" usage:"remove every tag from the feed"`
}

// TagResult is the tag result envelope: the feed's canonical URL, its tags
// after the operation, and the delta this invocation applied. added and removed
// are always present, empty on a read or a no-op edit, so a caller sees what
// changed rather than what it asked for.
type TagResult struct {
	Head
	URL     string   `json:"url"`
	Tags    []string `json:"tags"`
	Added   []string `json:"added"`
	Removed []string `json:"removed"`
}
```

`Validate` holds all the semantics, in ordinary Go — ADR 0007 explicitly
forbids encoding validation into struct tags:

- `Clear` with any of `Add`/`Remove`/`Set` is a usage error.
- `Set` with any of `Add`/`Remove` is a usage error.
- Every tag in `Add`, `Remove`, and `Set` goes through `core.ValidateTags`.
- An empty `Ref` is left to the store, which reports it as a usage-category
  "feed not found", matching `EnableRequest`.

Use the package's `usageErr` helper (defined in `prune.go`, used package-wide).

`MarshalJSON` must coalesce all three slices to `[]`, per ADR 0005's
"collections never serialize as null, enforced by the envelope's own
MarshalJSON, not by call-site discipline". Use the `type alias TagResult` trick
from `list.go`.

## `App.Tag`

```go
func (a *App) Tag(ctx context.Context, req TagRequest) (TagResult, error)
```

1. `req.Validate()`.
2. `a.resolveStore(ctx)`, then `st.GetFeed(ctx, req.Ref)` — resolves the ref and
   yields the current tags.
3. Compute the new set in Go (the store is a dumb setter):
   - read mode (no write flag): new set is the current set, deltas empty.
   - `Clear`: new set is empty.
   - `Set`: new set is `core.CanonicalTags(req.Set)`.
   - otherwise: current plus `Add` minus `Remove`, canonicalized. Apply `Add`
     before `Remove` so a tag in both is removed, and say so in the doc comment
     rather than leaving it to the reader.
4. Compute `Added`/`Removed` as the set difference between old and new, in
   canonical order — **the actual delta, not the requested one**, so adding an
   existing tag reports `added: []`.
5. Skip the write entirely when the sets are equal, so a read and a no-op edit
   do not bump `updated_at`.
6. `st.SetTags(ctx, feed.URL, newTags)`.
7. Return `TagResult{Head: OKHead(), URL: feed.URL, Tags: newTags, ...}`.

Optional `RenderText(w io.Writer, _ bool) error` for `--format text`: a single
line is enough (`tags: agents, ai`), following `PruneResult.RenderText`'s
one-line style rather than building a table for one row.

## New file: `internal/command/tag.go`

Copy `internal/command/enable.go` exactly:

```go
func (d Deps) tagCommand() *cliv3.Command {
	return &cliv3.Command{
		Name:      "tag",
		Usage:     "read or edit the tags on a subscription",
		ArgsUsage: "URL|ALIAS",
		Arguments: argsFor(feedwatch.TagRequest{}),
		Flags:     flagsFor(feedwatch.TagRequest{}),
		Action:    d.tagAction,
	}
}
```

The action is the standard five steps: `d.app(ctx)`, `defer app.Close()`,
`bind(cmd, &req)`, `app.Tag(ctx, req)`, `rendererFrom(ctx).Result(res)`.

## Registration checklist (none of this is automatic)

1. `internal/command/root.go`, `Deps.commands()` — append `d.tagCommand()`.
   **Position in that slice determines help ordering and golden ordering**;
   put it after `d.disableCommand()` so the feed-management verbs group.
2. `internal/command/schema_registry.go` — add
   `"tag": {exitCodes: defaultExitCodes(), output: jsonschema.Reflect(feedwatch.TagResult{})}`.
   A missing entry does not fail; `registryFor` silently falls back to
   `{"type":"object"}`, which is worse than a failure.
3. `internal/command/schema_test.go` — the hand-maintained `want` command-name
   list in `TestSchemaEnumeratesCommands`, and the `objects` map of expected
   properties/required per command.
4. `internal/command/envelope_test.go` — add `TagResult` to `envelopeCases`
   with its collection keys, so the head-order and never-null rules are pinned.
5. Goldens: `testdata/schema/tag.stdout` and `testdata/help/tag.stdout` are
   demanded automatically (the surface tests enumerate commands from the live
   schema envelope), and `testdata/schema/all.stdout` plus
   `testdata/help/root.stdout` change. Regenerate with
   `go test ./internal/command -update -count=1` and read the diff.

## TDD plan

Library tests in `tag_test.go` (external `package feedwatch_test`), using
`newTestApp(t)` from `testapp_test.go`, which returns the `App` and the backing
`InMemoryStore` so a test seeds and verifies through the `store.Store`
interface. CLI tests in `internal/command/tag_test.go` (internal `package
command`), with a `runTag` helper modeled on `runEnable` and a `tagEnvelope`
mirror struct.

1. **(tracer)** `tag REF` on a feed tagged `["ai"]` returns
   `{tags:["ai"], added:[], removed:[]}`, exit 0, and does not write.
2. `--add` on a feed with no tags stores the canonical set and reports it in
   `added`.
3. `--add` of an already-present tag is idempotent: tags unchanged, `added` is
   `[]`.
4. `--remove` drops the tag and reports it in `removed`; removing an absent tag
   is a no-op with `removed: []`.
5. `--set` replaces the whole set, reporting both the additions and the
   removals it caused.
6. `--clear` empties the set and reports every prior tag in `removed`.
7. `--clear --add ai` is a usage error, exit 64, empty stdout. Same for
   `--set a --add b`.
8. An invalid tag (`--add ""`, `--add "a,b"`, `--add "two words"`) is a usage
   error, exit 64.
9. An unknown `REF` is a usage error (from the store's "feed not found").
10. The envelope always carries `added` and `removed` as arrays, never `null`
    — assert on the raw stdout bytes, not on a decoded struct, since decoding
    hides the difference.

## Gotchas

- `[]string` flags accept `--set a,b` and `--set a --set b` identically; do not
  write parsing that assumes one spelling.
- A field with no `flag` or `arg` tag panics at command-tree construction, and
  every test builds the tree, so the failure is immediate and total.
- `usageErr` lives in `prune.go`, not in a helpers file.

## Acceptance Criteria

- `feedwatch.TagRequest`/`TagResult` and `App.Tag` exist, with `Validate`
  holding every rule in ordinary Go and `MarshalJSON` coalescing `tags`,
  `added`, and `removed` to `[]`.
- `added`/`removed` report the actual delta, so an idempotent `--add` reports
  an empty `added` and performs no write.
- The `tag` command is registered in `Deps.commands()`, `schemaRegistry`, the
  `schema_test.go` command-name list and `objects` map, and
  `envelope_test.go`'s `envelopeCases`.
- Mutually exclusive write flags and invalid tag names exit 64 with empty
  stdout.
- Behaviors 1-10 covered across `tag_test.go` and
  `internal/command/tag_test.go`.
- `testdata/schema/tag.stdout`, `testdata/help/tag.stdout`,
  `testdata/schema/all.stdout`, and `testdata/help/root.stdout` are
  regenerated and their diffs reviewed.
- `make build` passes.
