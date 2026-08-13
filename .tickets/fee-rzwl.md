---
id: fee-rzwl
status: open
deps: [fee-f3u8]
links: []
created: 2026-08-13T14:03:47Z
type: task
priority: 1
assignee: Andre Silva
parent: fee-ui25
tags: [lib, app]
---

# App use cases: store-only commands

Fourth step of
[docs/adr/0007-library-and-frontends.md](../docs/adr/0007-library-and-frontends.md).
Moves the seven use cases that need only the store into `App`, along with their
request types, their result envelopes, and their input validation. After this
ticket each of the seven CLI actions is flag decoding, one `App` call, and
rendering.

Scope: `list`, `rm`, `enable`, `disable`, `prune`, `items`, `migrate`. The
network commands are `fee-kj8z`; OPML is `fee-gvuo`.

## Design

### 1. Method set

All in the root `feedwatch` package, one file per use case
(`list.go`, `rm.go`, `enable.go`, `disable.go`, `prune.go`, `items.go`,
`migrate.go`), each carrying its request type, its result type, the result's
`MarshalJSON` and `RenderText` where they exist today, and the method:

```go
func (a *App) List(ctx context.Context, req ListRequest) (ListResult, error)
func (a *App) Remove(ctx context.Context, req RemoveRequest) (RmResult, error)
func (a *App) Enable(ctx context.Context, req EnableRequest) (EnableResult, error)
func (a *App) Disable(ctx context.Context, req DisableRequest) (DisableResult, error)
func (a *App) Prune(ctx context.Context, req PruneRequest) (PruneResult, error)
func (a *App) Items(ctx context.Context, req ItemsRequest) (ItemsResult, error)
func (a *App) Migrate(ctx context.Context) (MigrateApplied, error)
func (a *App) MigrationStatus(ctx context.Context) (MigrateStatus, error)
```

The result types (`ListResult`, `FeedView`, `RmResult`, `EnableResult`,
`DisableResult`, `PruneResult`, `ItemsResult`, `ProjectedItemsResult`,
`MigrateApplied`, `MigrateStatus`) move verbatim from `internal/command`,
including their JSON tags, their collection-coalescing `MarshalJSON`, and their
`RenderText` methods.

**Settled: `RenderText` stays on the library result types.** Do not relitigate
this while implementing, and do not move text rendering into the CLI. The
reasons, recorded so the shape reads as intended rather than accidental:

- The methods are pinned by the text-format goldens, so moving them risks the
  one thing this epic must not change.
- `internal/output.Renderer` dispatches on its `TextRenderer` interface
  structurally, so the library implements it without importing
  `internal/output`, and no dependency runs the wrong way.
- The alternative is a type switch over every result type in every frontend,
  which is exactly the per-frontend duplication the ADR exists to remove.

The godoc on each `RenderText` states what it is: an optional human-text
projection of the same values, which a frontend may ignore. A TUI or server
that wants its own layout reads the struct fields directly.

### 2. Requests, and where validation lives

Request types are plain structs. Semantics live in ordinary Go, per the ADR: no
validation tags. The `flag:` and `usage:` tags that drive CLI flag derivation
are added in `fee-savm`, not here.

```go
type ListRequest struct{}

type RemoveRequest struct{ Ref string }

type EnableRequest struct{ Ref string }

type DisableRequest struct{ Ref string }

// PruneRequest fields are pointers so an explicit zero is distinguishable from
// an absent value: --keep-days 0 prunes everything older than now, while the
// flag being absent applies no age policy. This replaces the framework's
// cmd.IsSet checks in buildPrunePolicy.
type PruneRequest struct {
    KeepDays *int
    MaxItems *int
}

type ItemsRequest struct {
    Feeds     []string
    Since     string
    Until     string
    Limit     int
    Offset    int
    Order     string // "<published|fetched> [asc|desc]", default "published desc"
    TimeField string // "published" (default) or "fetched"
    Contains  string
    Fields    []string
}
```

Each carries `Validate() error`, returning a usage-category `*core.FeedError`
wrapping `core.ErrUsage`, so the CLI still exits 64 and an HTTP frontend can
later map the same category to 400. The following move out of
`internal/command` into the library:

- `buildPrunePolicy` becomes `PruneRequest.Validate` plus an unexported
  `PruneRequest.policy() core.PrunePolicy`. Preserve every message verbatim:
  `prune requires --keep-days and/or --max-items`, `--keep-days must not be
  negative`, `--max-items must not be negative`.
- `buildItemQuery`, `parseItemOrder`, `parseTimeRef`, and
  `parseRelativeDuration` from [internal/command/items.go](../internal/command/items.go)
  become an unexported `ItemsRequest.query(now time.Time) (core.ItemQuery,
  error)`. `Validate` is then `_, err := r.query(time.Unix(0, 0)); return err`,
  so parsing rules are stated once and validation cannot drift from resolution.
  `App.Items` calls `r.query(a.clock())`.
- `unknownFieldMessage` and the did-you-mean suggester in
  [internal/command/suggest.go](../internal/command/suggest.go) move with the
  field validation, together with `suggest_test.go`.

Every user-visible message must survive the move unchanged; several are pinned
by goldens under `internal/command/testdata/err/`.

### 3. Items projection

**Settled: `App.Items` always returns the full `ItemsResult`, and a shared
helper selects the rendered shape.** Implement exactly this; do not change
`App.Items` to return an interface, and do not merge the two result types.

```go
// Project narrows the result to the requested fields, mirroring the JSON
// projection of the items use case.
func (r ItemsResult) Project(fields []string) ProjectedItemsResult

// Envelope selects the shape the request asked for: the projected envelope
// when Fields is set, otherwise the full one.
func (r ItemsRequest) Envelope(res ItemsResult) any
```

The CLI action becomes `return rr.Result(req.Envelope(res))`.

Why this shape and not the two alternatives that will suggest themselves:

- Returning an interface implemented by both result types would push a type
  assertion onto every embedder for what is a rendering choice, and would give
  the library's most-used method the least useful signature.
- Merging the two types into one that projects inside `MarshalJSON` would
  change the `items` entry in `schemaRegistry` and therefore the emitted
  `output_schema`, which this epic must not do.

Keeping both types leaves `schemaRegistry`'s `items` entry and its emitted
`output_schema` untouched.

The one cost is that a frontend must call `Envelope` rather than rendering the
result directly. Contain it by making `Envelope` the only place the choice is
expressed: no frontend inspects `req.Fields` itself, and `docs/library.md`
(`fee-3p3r`) shows `Envelope` in the `items` example so an embedder meets it
immediately.

`ProjectedItemsResult.fields` is currently unexported and set at construction;
keep it unexported and set it in `Project`.

### 4. Migration semantics

Preserve the current behavior exactly, which `fee-f3u8` accounted for:

- Every use case except these two applies pending migrations once per `App` on
  first store use.
- `App.Migrate` bypasses that guard so its `applied` count is truthful, then
  reports the resulting `store_schema_version`.
- `App.MigrationStatus` applies pending migrations first (today's
  `migrateAction` does this on the `--status` path too) and then reports
  version, pending, and backend.

`MigrateStatus.Backend` comes from the scheme classifier that moved into the
root package in `fee-f3u8`; expose it as an unexported helper, since the value
is already reported in the envelope.

### 5. CLI rewiring

Each action loses its `resolver` usage and its shaping code. The pattern:

```go
func (d Deps) listAction(ctx context.Context, _ *cliv3.Command) error {
    app, err := d.app(ctx)
    if err != nil {
        return err
    }
    defer app.Close()

    res, err := app.List(ctx, feedwatch.ListRequest{})
    if err != nil {
        return err
    }
    return rendererFrom(ctx).Result(res)
}
```

Add one unexported `Deps.app(ctx)` helper in `internal/command` that builds the
`*App` from `configFrom(ctx)` plus the still-present unexported `Deps.store`,
`Deps.fetch`, `Deps.parse` test seams (mapped onto `WithStore`, `WithFetcher`,
`WithParser`) and `WithClock`. Those unexported fields are removed in
`fee-gvuo`, once every action uses the App and the tests can inject through the
public options instead.

The CLI keeps:

- flag decoding into the request,
- the `omitted_no_date` info log in `itemsAction`, now driven by
  `res.OmittedNoDate`,
- `--status` selecting `MigrationStatus` over `Migrate`,
- rendering and the exit boundary.

`internal/command/resolve.go` stays for now; the network and OPML actions still
use it.

## TDD notes

Vertical slices, one use case at a time. For each of the seven, in order
(`list`, `rm`, `enable`, `disable`, `prune`, `items`, `migrate`):

1. **RED**: write the library test first, in `package feedwatch_test`, driving
   the method against `testsupport.NewInMemoryStore` injected with `WithStore`
   and a fixed clock via `WithClock`. Assert observable behavior, not
   internals: the returned envelope's values, and the store state afterwards
   read back through the public `Store` interface.
2. **GREEN**: move the logic from the CLI action into the method.
3. Only then rewire the CLI action and delete the dead helper from
   `internal/command`.

Behaviors worth a test each, chosen because they are the ones with logic rather
than plumbing:

- `List` on an empty store returns an empty, non-nil `Feeds` slice that
  marshals as `[]`.
- `List` maps a feed with a zero interval to an omitted `interval` field.
- `Remove` on an unknown ref returns a usage-category error, and does not
  report success (the store's `RemoveFeed` is a no-op on a missing feed, which
  is why the use case resolves first).
- `Remove` reports the canonical URL when given an alias.
- `Enable` resets the failure lifecycle: after enabling a feed with failures
  and a backed-off schedule, the feed reads back active, with zero failures, no
  last error, and immediately due.
- `Enable` is idempotent on an already-active feed.
- `Disable` leaves the failure count untouched (the contrast with auto-disable).
- `PruneRequest.Validate` rejects an empty request, and a negative value in
  either field, with the exact current messages.
- `PruneRequest` with `KeepDays` pointing at `0` produces a policy with a
  cutoff of now, distinguishable from an absent policy.
- `ItemsRequest.Validate` rejects an unknown field with the did-you-mean
  message, a malformed `--order`, an unknown `--time-field`, and an unparseable
  `--since`, each with its current wording.
- `ItemsRequest.query` resolves `7d` and `24h` relative to the injected clock,
  and accepts RFC3339 verbatim.
- `feed_url` in `Fields` is accepted as a no-op rather than rejected.
- `Items` sets `OmittedNoDate` when a publication-axis window excludes items
  with a null publication time.
- `Envelope` returns `ProjectedItemsResult` when `Fields` is set and
  `ItemsResult` otherwise, and the projected JSON has `feed_url` first.
- `Migrate` on a fresh store reports a non-zero `applied`; a second call
  reports zero and the same version.
- `MigrationStatus` reports zero pending after migrating.

Existing `internal/command` action tests keep passing unchanged wherever they
assert stdout, stderr, and exit codes: that is the point of the ADR 0003
boundary. Where a test reaches into a moved helper (`buildItemQuery`,
`parseItemOrder`, `buildPrunePolicy`, `unknownFieldMessage`), move the test to
the library alongside the code rather than deleting it.

Every `internal/command/testdata/**` golden must compare byte-identical with no
`-update` run. That is the real acceptance test for this ticket.

## Acceptance Criteria

- The eight methods above exist on `*App` with the signatures given.
- `ListResult`, `FeedView`, `RmResult`, `EnableResult`, `DisableResult`,
  `PruneResult`, `ItemsResult`, `ProjectedItemsResult`, `MigrateApplied`, and
  `MigrateStatus` live in the root package and no longer in `internal/command`.
- `ItemsRequest`, `PruneRequest`, `RemoveRequest`, `EnableRequest`,
  `DisableRequest`, and `ListRequest` exist with `Validate() error` returning
  usage-category errors.
- No validation is expressed in struct tags.
- The result types keep their `RenderText` methods, each documented as an
  optional human-text projection; the library imports no part of
  `internal/output`.
- `App.Items` returns the concrete `ItemsResult`, never an interface, and
  `ItemsRequest.Envelope` is the only place the projected-versus-full choice is
  made; no CLI action inspects `req.Fields` itself.
- `buildItemQuery`, `parseItemOrder`, `parseTimeRef`, `parseRelativeDuration`,
  `buildPrunePolicy`, and `unknownFieldMessage` no longer exist in
  `internal/command`.
- The seven CLI actions contain no store calls and no envelope shaping.
- `schemaRegistry` still emits identical `output_schema` for all seven
  commands, including the `migrate` `oneOf`.
- Every `internal/command/testdata/**` golden compares byte-identical with no
  `-update` run.
- `make build` passes.

## Files

```text
list.go, rm.go, enable.go, disable.go, prune.go, items.go, migrate.go  (new, root package)
suggest.go                                                             (moved from internal/command)
list_test.go, rm_test.go, ... , migrate_test.go, suggest_test.go       (new/moved, root package)
internal/command/list.go, rm.go, enable.go, disable.go, prune.go,
    items.go, migrate.go, suggest.go                                   (reduced to flags + call + render)
internal/command/schema_registry.go                                    (imports the library result types)
internal/command/app.go                                                (new: Deps.app helper)
```
