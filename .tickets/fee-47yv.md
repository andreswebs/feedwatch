---
id: fee-47yv
status: closed
deps: [fee-pfpz, fee-o5uq]
links: []
created: 2026-08-14T02:46:34Z
type: task
priority: 2
assignee: Andre Silva
parent: fee-zs6b
tags: [cmd, tags]
---
# FeedView.tags and add --tag

Add a non-omitempty tags array to FeedView (visible in list, enable, disable) with its own MarshalJSON coalescing, a TAGS column in the list text table, and a repeatable --tag on add with omitted-preserves / given-replaces semantics. One ticket because both regenerate the same golden and schema files.

## Design

Two changes that share one blast radius: make tags visible wherever a feed is
reported, and let `add` set them at creation time. They are one ticket because
both regenerate the same golden and schema files, and splitting them would mean
reviewing that diff twice.

Spec: [docs/specs/003-feed-tags/plan.md](docs/specs/003-feed-tags/plan.md),
sections "`add` — tag at creation time" and "`list` — filter by tag" (the
`FeedView` paragraph).

## Part 1: `FeedView.Tags`

`FeedView` in `list.go` is the single agent-facing feed projection, built by
`feedView(f core.Feed) FeedView` and reused by `ListResult.Feeds`,
`EnableResult.Feed`, and `DisableResult.Feed`. Add:

```go
	Tags []string `json:"tags"`
```

**Not** `omitempty`: tags are part of the contract, so an untagged feed reports
`"tags":[]` rather than omitting the key, and an agent never has to distinguish
"no tags" from "this binary predates tags".

That means the nil case must be coalesced. `FeedView` is nested inside three
different envelopes, so give `FeedView` its own `MarshalJSON` rather than
patching each parent:

```go
// MarshalJSON coalesces tags so it always serializes as [] rather than null.
func (v FeedView) MarshalJSON() ([]byte, error) {
	type alias FeedView
	a := alias(v)
	if a.Tags == nil {
		a.Tags = []string{}
	}
	return json.Marshal(a)
}
```

Also add a `TAGS` column to `ListResult.RenderText`'s tabwriter table, joined
with `", "`, using `dashIfEmpty` so an untagged feed shows `-` and the table
stays rectangular.

## Part 2: `add --tag`

```go
type AddRequest struct {
	URL      string        `arg:"url"`
	Alias    string        `flag:"alias" usage:"short, unique name to reference the feed"`
	Interval time.Duration `flag:"interval" usage:"minimum poll interval; 0 uses the configured default"`
	Tags     []string      `flag:"tag" usage:"tag to assign (repeatable); omitted preserves existing tags on a re-add"`
}
```

`AddRequest.Validate` gains a `core.ValidateTags(r.Tags)` call after the
existing URL check.

`AddResult` gains `Tags []string \`json:"tags,omitempty"\`` reporting the
feed's tags after the upsert. Here `omitempty` **is** right: `AddResult` is not
a `FeedView` and its `alias`/`interval` fields already use `omitempty`, so the
struct stays internally consistent.

`App.Add` semantics — "omitted preserves, given replaces":

- `st.AddFeed` receives `core.Feed{..., Tags: req.Tags}`. The store sets tags
  on **create** and ignores them on re-add (its upsert omits `tags` from
  `DO UPDATE SET`), so a re-add with no `--tag` preserves the stored set with
  no extra logic.
- When `len(req.Tags) > 0`, call `st.SetTags(ctx, feed.URL, req.Tags)` after
  the upsert, then re-read with `st.GetFeed` for the reported tags. That single
  branch makes "given replaces" true on both the create and the re-add path,
  and it is why the store's upsert deliberately does not touch tags.

There is no way to clear tags through `add`; that is `tag --clear`. Say so in
the `--tag` usage string, which is the only place an agent will look.

## Golden and schema fallout — exhaustive

Schema goldens change because `FeedView` is reflected into three commands:

- `internal/command/testdata/schema/list.stdout`
- `internal/command/testdata/schema/enable.stdout`
- `internal/command/testdata/schema/disable.stdout`
- `internal/command/testdata/schema/add.stdout` (the new `tags` property)
- `internal/command/testdata/schema/all.stdout`
- `internal/command/testdata/help/add.stdout` (the new `--tag` flag)
- `internal/command/testdata/help/root.stdout` is unaffected by this ticket.

Behavioral goldens: `internal/command/testdata/lifecycle/list.stdout` is the
only behavioral golden containing a `FeedView`, and it **will** change to carry
`"tags":[]` because the field is non-omitempty. The `add` goldens
(`lifecycle/add.stdout`, `all_failed/add.stdout`, `partial/add_*.stdout`,
`auto_disable/add.stdout`) are `AddResult` with `omitempty` tags and no
`--tag` in the scenario, so they stay unchanged — verify that rather than
assuming it.

Non-golden tests to update: `internal/command/list_test.go`'s `listEnvelope`
mirror struct, and `internal/command/schema_test.go`'s `feedViewProps` /
`feedViewReq` expectations.

Regenerate with `go test ./internal/command -update -count=1` and read every
diff hunk; the goldens are a reviewed contract under ADR 0006, not a snapshot
to rubber-stamp.

## TDD plan

Library tests in `add_test.go` and `list_test.go` (external `package
feedwatch_test`, `newTestApp`/`newNetworkApp` helpers); CLI tests in
`internal/command/add_test.go` and `list_test.go`.

1. **(tracer)** `add URL --tag AI --tag agents` on a new feed stores
   `["agents","ai"]` and reports them in `AddResult.Tags`.
2. `list` reports `"tags":[]` for an untagged feed — assert on the raw stdout
   bytes so a `null` regression is caught.
3. `list` reports a tagged feed's tags in canonical order.
4. Re-adding an existing tagged feed with `--alias` and **no** `--tag`
   preserves the stored tags (`created:false`, tags unchanged).
5. Re-adding with `--tag research` **replaces** the set with `["research"]`.
6. `add URL --tag "a,b"` and `add URL --tag ""` are usage errors, exit 64, and
   nothing is subscribed.
7. `enable` and `disable` envelopes carry the feed's tags (one test, since both
   share `feedView`).
8. `--format text` `list` renders a `TAGS` column, with `-` for an untagged
   feed.

## Gotchas

- Behavior 6 must assert that **no subscription was created**, not just the
  exit code: tag validation belongs in `Validate`, which runs before any fetch
  or store call, and a test that only checks the exit code would pass even if
  validation ran after the upsert.
- Adding a method to `FeedView` makes it non-comparable in exactly no ways that
  matter, but check that no test compares `FeedView` values with `==`; use
  field-wise or `reflect.DeepEqual` assertions if any do.
- `add` fetches and parses the URL before subscribing, so its CLI tests need
  `newNetworkApp`/`netOpts` with a registered `FakeFetcher`/`FakeParser`, not
  just a store.

## Acceptance Criteria

- `FeedView` carries `Tags []string` with a non-omitempty `tags` key and its
  own `MarshalJSON` coalescing nil to `[]`, so `list`, `enable`, and `disable`
  all report tags.
- `ListResult.RenderText` has a `TAGS` column rendering `-` when empty.
- `AddRequest` accepts a repeatable `--tag`, validated in `Validate` before any
  network or store call; `AddResult` reports the resulting tags.
- Omitting `--tag` on a re-add preserves stored tags; supplying it replaces the
  set.
- Behaviors 1-8 covered across the library and command test files.
- The schema goldens for `list`, `enable`, `disable`, `add`, and `all`, the
  help golden for `add`, and `lifecycle/list.stdout` are regenerated and
  reviewed; the `add` behavioral goldens are confirmed unchanged.
- `listEnvelope` and the `feedViewProps`/`feedViewReq` expectations in
  `schema_test.go` are updated.
- `make build` passes.

## Notes

**2026-08-14T19:38:15Z**

Implemented both halves. FeedView gained a non-omitempty Tags []string with its own MarshalJSON coalescing nil to [], so list, enable, and disable all report tags; ListResult.RenderText gained a TAGS column (comma-joined, dash when empty). AddRequest gained a repeatable --tag validated in Validate via core.ValidateTags before any fetch or store call; AddResult reports the resulting tags with omitempty. Omitted-preserves/given-replaces works through the store's upsert (which ignores tags on re-add) plus a single SetTags+GetFeed branch when tags are given.

One deviation from the ticket: behavior 6's CLI half. 'add URL --tag "a,b"' cannot exit 64 because urfave splits []string flags on commas before Validate runs, and the feed-tags plan documents --tag a,b as identical to --tag a --tag b. The comma rule in core.ValidateTags guards library callers instead. The CLI test now pins that equivalence (TestAddTagCommaSpellingIsRepeatedSpelling) and the empty-tag rejection; the library test keeps the comma rejection. Rejecting it at the CLI would require breaking the documented spelling rule.

Also fixed two goldens that were stale before this ticket (migrate_status.stdout and err/schema_too_new.stderr still carried store schema version 1 after the tags migration bumped it to 2). They passed the opening make build only from the go test cache.

Predicted gotcha hit: enable_test.go compared FeedView with !=, which a slice field makes illegal; switched to reflect.DeepEqual. Regenerated goldens for schema list/enable/disable/add/all, help/add, and lifecycle/list; confirmed the add behavioral goldens are unchanged. make build passes.
