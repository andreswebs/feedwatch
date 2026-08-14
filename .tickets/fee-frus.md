---
id: fee-frus
status: closed
deps: [fee-pfpz, fee-o5uq, fee-47yv]
links: []
created: 2026-08-14T02:48:06Z
type: task
priority: 2
assignee: Andre Silva
parent: fee-zs6b
tags: [cmd, tags]
---
# list --tag and the --match pattern

Add Tags/Match to ListRequest, fix the action to call bind (list is one of two commands that never did), and establish the shared tagFilter helper and --match validation that poll, check, items, prune, and rm all reuse.

## Design

Add `--tag` and `--match` to `list`, and in doing so **establish the pattern
every other filter command copies** (T10 poll/check, T11 items, T12 prune/rm).
Get the shared helper and the validation message right here; the later tickets
should be able to reuse rather than reinvent.

Spec: [docs/specs/003-feed-tags/plan.md](docs/specs/003-feed-tags/plan.md),
sections "Tag selection syntax" and "`list` — filter by tag".

## 1. `ListRequest` stops being empty

Today:

```go
// ListRequest selects the subscriptions to report. It carries no filters today:
// list reports every subscription, and narrowing it is a query concern the items
// use case already covers.
type ListRequest struct{}
```

That doc comment is now wrong and must be rewritten, not merely extended. A
lane is a property of the **subscription**, not of the item history, so it
belongs on `list`; `items` covers narrowing over history, which is a different
axis. The replacement:

```go
// ListRequest selects the subscriptions to report. An empty Tags reports every
// subscription; naming tags narrows to a lane, combined per Match.
type ListRequest struct {
	Tags  []string `flag:"tag" usage:"tag to filter by (repeatable); all feeds when omitted"`
	Match string   `flag:"match" default:"all" usage:"multi-tag semantics: 'all' (default) or 'any'"`
}
```

Do **not** add `--status` in this ticket. It is a reasonable future addition
and explicitly out of scope for the feature.

## 2. The shared request-side helper

Every filter command needs the same three steps: validate the tag names,
resolve `--match`, and build a `core.ListFilter`. Put it once in the root
package (a new `tagfilter.go`, or beside `usageErr` — but one place):

```go
// tagFilter validates a request's tag selection and resolves it into a store
// filter. It is shared by every use case that accepts --tag/--match so the
// rules are stated once and cannot drift between commands.
func tagFilter(tags []string, match string) (core.ListFilter, error) {
	if err := core.ValidateTags(tags); err != nil {
		return core.ListFilter{}, err
	}
	m, err := core.ParseTagMatch(match)
	if err != nil {
		return core.ListFilter{}, err
	}
	return core.ListFilter{Tags: tags, Match: m}, nil
}
```

`ListRequest.Validate` calls it and discards the filter, exactly as
`ItemsRequest.Validate` calls `r.query(time.Unix(0,0))` and discards the
result: "the parsing rules are stated once and validation cannot drift from
resolution".

## 3. **The `bind` trap** — read this before writing code

`internal/command/list.go`'s action does **not** call `bind`. It passes a
literal:

```go
	res, err := app.List(ctx, feedwatch.ListRequest{})
```

`list` and `export` are the only two commands that do this, because their
request structs were empty. Adding fields without adding `bind` produces flags
that appear in `--help` and in `schema`, parse without error, and are silently
discarded — and the surface goldens would happily accept it. Change the action
to the standard shape:

```go
	var req feedwatch.ListRequest
	if err := bind(cmd, &req); err != nil {
		return err
	}
	res, err := app.List(ctx, req)
```

Write behavior 1 as a test that fails for exactly this reason before fixing it.

## 4. `App.List`

```go
	filter, err := req.filter()      // shared helper, also used by Validate
	if err != nil {
		return ListResult{}, err
	}
	feeds, err := st.ListFeeds(ctx, filter)
```

`core.ListFilter{}` with no tags matches every feed, so the untagged path is
byte-identical to today's behavior.

## TDD plan

Library tests in `list_test.go` (external `package feedwatch_test`,
`newTestApp`); CLI tests in `internal/command/list_test.go` (internal, `runList`
helper, `listEnvelope` mirror struct). Fixture: three feeds — `["ai","agents"]`,
`["ai"]`, untagged.

1. **(tracer)** `list --tag ai` returns the two tagged feeds and omits the
   untagged one, exit 0. Written first, it fails on the missing `bind` before
   it fails on anything else.
2. `list` with no `--tag` returns all three, unchanged from today.
3. `list --tag ai --tag agents` (default match) returns only the feed carrying
   both.
4. `list --tag ai,agents --match any` returns both tagged feeds — and asserts
   that the comma spelling is equivalent to the repeated spelling by running
   the same query both ways and comparing results.
5. `list --tag ai --match bogus` is a usage error, exit 64, with a message
   naming `all` and `any`.
6. `list --tag ""` and `list --tag "a b"` are usage errors, exit 64.
7. `list --tag nosuchlane` returns `"feeds":[]`, exit 0 — an empty lane is not
   an error.
8. The untagged `list` envelope is byte-identical to the pre-change golden
   (guards against an accidental shape change).

Behavior 4 is the one that pins the plan's central syntax decision: since
`--tag a --tag b` and `--tag a,b` parse identically, `--match` must be the only
thing that changes the answer.

## Golden fallout

`testdata/schema/list.stdout` (new flags), `testdata/help/list.stdout` (new
flags), `testdata/schema/all.stdout`. `testdata/lifecycle/list.stdout` is a
plain `list` invocation and should be unchanged by this ticket (T8 already
changed it for the `tags` field). Regenerate with
`go test ./internal/command -update -count=1` and read the diff.

## Gotchas

- The `default:"all"` struct tag makes `cmd.String("match")` return `"all"`
  when the flag is absent, so `ParseTagMatch` will rarely see `""` from the
  CLI — but it will from a library embedder constructing `ListRequest{}`
  directly. Both must work; that is why `""` maps to `MatchAll`.
- Keep the flag named `--match`, not `--tag-match`: it is per-command and there
  is nothing else it could match.
- `revive` forbids a local named `any`; the match value is a `core.TagMatch`.

## Acceptance Criteria

- `ListRequest` carries `Tags []string` and `Match string` (default `all`),
  and its doc comment no longer claims list carries no filters.
- `internal/command/list.go`'s action calls `bind`; a test proves the flags
  actually reach the request.
- A shared `tagFilter` helper in the root package validates tag names, resolves
  `--match`, and returns a `core.ListFilter`; `Validate` and the use case both
  go through it.
- `MatchAll` is the default; `--tag a --tag b` and `--tag a,b` are equivalent,
  proven by a test that runs both spellings.
- An invalid `--match` or an invalid tag name exits 64; an empty lane exits 0
  with `"feeds":[]`.
- `list` with no `--tag` is unchanged.
- Behaviors 1-8 covered across `list_test.go` and
  `internal/command/list_test.go`.
- `schema/list.stdout`, `help/list.stdout`, and `schema/all.stdout` are
  regenerated and reviewed.
- `make build` passes.

## Notes

**2026-08-14T19:46:46Z**

Added Tags []string (flag "tag") and Match string (flag "match", default "all") to ListRequest; rewrote its doc comment. New root-package tagfilter.go holds the shared tagFilter(tags, match) (core.ListFilter, error) helper that validates tag names via core.ValidateTags, resolves --match via core.ParseTagMatch, and builds the filter; T10-T12 (poll/check, items, prune/rm) should reuse it rather than reinvent. ListRequest.filter() wraps it; Validate discards its result and App.List keeps it, mirroring ItemsRequest/query.

Fixed the bind trap: internal/command/list.go's action passed a literal ListRequest{} and now calls bind(cmd, &req). The CLI tag test was written first and failed with 'flag provided but not defined: -tag', exactly as the ticket predicted. export is now the only command left with a literal-request action -- it needs the same fix the moment ExportRequest grows a field.

Behaviors 1-8 covered: list_test.go gained TestListFiltersByTag (table: no tags, one tag, two tags default-all, --match any, explicit all, empty lane) and TestListRejectsInvalidTagSelection (asserts both Validate and App.List report the same usage error). internal/command/list_test.go gained TestListTagFlagsReachTheRequest, TestListTagSpellingsAreEquivalent (--tag a,b vs --tag a --tag b compared on raw stdout, run under both match values), and TestListRejectsInvalidTagSelection (exit 64, empty stdout, usage_error envelope). Shared seedLaneFeeds/listedURLs helpers in each package.

reflectflags_test.go's TestRequestSurfaceMapping list row went 0 -> 2 flags; that table failed before any golden did, but note it only proves flags are declared, not that they reach the request -- the bind gap lived in exactly that blind spot.

Goldens regenerated: schema/list.stdout (new flags), help/list.stdout (new flags plus the reworded usage line, which now mentions tags), schema/all.stdout. lifecycle/list.stdout was untouched by this ticket as expected.

Note: --match is validated even when no --tag is given, so 'list --match bogus' exits 64 rather than silently ignoring the typo. Learnings appended under 'fee-frus' in docs/specs/learnings.md. make build passes.
