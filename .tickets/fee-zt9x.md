---
id: fee-zt9x
status: open
deps: []
links: []
created: 2026-08-14T02:42:09Z
type: task
priority: 1
assignee: Andre Silva
parent: fee-zs6b
tags: [core, tags]
---
# core: tag type, canonicalization, and filter fields

Add the core value layer for feed tags: TagMatch, CanonicalTags, ValidateTags, ParseTagMatch, a Tags field on core.Feed, and Tags/Match fields on ListFilter, ItemQuery, and PrunePolicy. Pure types and functions only; no store, SQL, or CLI wiring.

## Design

The value layer every other ticket in this epic builds on. **Pure types and
functions plus struct fields only** — no store wiring, no SQL, no CLI. Nothing
in this ticket changes observable behavior; it exists so the store lane (T3, T4)
and the command lane (T6 onward) can proceed against a settled vocabulary.

Spec: [docs/specs/003-feed-tags/plan.md](docs/specs/003-feed-tags/plan.md),
sections "Tag names", "Tag selection syntax", "Where filtering happens".

## New file: `core/tags.go`

```go
// TagMatch selects how a multi-tag filter combines. The zero value matches
// feeds carrying every requested tag, which is the safer default: a lane is
// normally an intersection, not a union.
type TagMatch string

const (
	// MatchAll requires a feed to carry every requested tag.
	MatchAll TagMatch = "all"
	// MatchAny requires a feed to carry at least one requested tag.
	MatchAny TagMatch = "any"
)

// CanonicalTags normalizes a tag set for storage and comparison: each tag is
// trimmed and lowercased, duplicates are dropped, and the result is sorted, so
// two invocations naming the same lane in different spellings or orders produce
// identical stored bytes. It returns an empty, non-nil slice for no tags.
func CanonicalTags(tags []string) []string

// ValidateTags reports the first tag that cannot be stored, as a usage-category
// error. A tag is rejected when it is empty after trimming, or contains a comma
// or any whitespace: commas because a slice flag splits on them, whitespace
// because it makes a tag unquotable in a shell or a cron script.
func ValidateTags(tags []string) error

// ParseTagMatch resolves a --match value. The empty string is MatchAll, so a
// zero-valued filter matches the documented default.
func ParseTagMatch(s string) (TagMatch, error)
```

Notes for the implementer:

- `CanonicalTags` must return `[]string{}` (not nil) for empty input, so a
  caller can marshal it to `[]` without a nil check.
- `ValidateTags` validates the **raw** input, before canonicalization, so the
  message names what the user typed. Use `strings.ContainsFunc(t,
  unicode.IsSpace)` for the whitespace check so tabs and newlines are caught,
  not just the ASCII space.
- Errors are `*core.FeedError{Category: core.CatUsage, Err: core.ErrUsage}`
  built directly (the root package's `usageErr` helper lives in `prune.go` and
  is not importable from `core`). Message style is Go-conventional: lowercase
  lead, no trailing punctuation, and it must quote the offending value with
  `strconv.Quote`, matching `items.go`'s `--time-field` message.
- `ParseTagMatch` accepts `""`, `"all"`, `"any"` and rejects everything else
  with a usage error naming the accepted values.

## Modified: `core/types.go`

Add to `Feed` (after `Alias`, so the field order reads identity-then-metadata):

```go
	Tags         []string      // user-assigned lane labels, canonical order
```

## Modified: `core/query.go`

```go
type ListFilter struct {
	Status FeedStatus // "" matches any status
	Tags   []string   // empty matches every feed
	Match  TagMatch   // "" is MatchAll
}

type ItemQuery struct {
	// ...existing fields...
	Tags  []string
	Match TagMatch
}

type PrunePolicy struct {
	KeepBefore *time.Time
	MaxPerFeed int
	Tags       []string
	Match      TagMatch
}
```

Every existing construction site passes a zero value or `{Status: ...}`, so no
call site changes in this ticket. Confirm with
`grep -rn 'ListFilter{\|ItemQuery{\|PrunePolicy{' --include='*.go' .`.

## What this ticket does NOT do

- No `store.Store` signature change (T3).
- No SQL (T3, T4).
- No `InMemoryStore` change (T5).
- No request struct, no flag, no command (T6 onward).

## TDD plan

New file `core/tags_test.go`, external package `core_test`, matching the
existing `core/query_test.go` and `core/errors_test.go` convention: one
`TestXxx` per behavior with a doc comment naming the contract, plain
`got`/`want` assertions, no testify.

Vertical slices, one test then one implementation each:

1. **(tracer)** `CanonicalTags([]string{"AI", " agents ", "ai"})` returns
   `["agents", "ai"]` — proves trim, lowercase, dedupe, and sort in one pass.
2. `CanonicalTags(nil)` returns a non-nil empty slice.
3. `ValidateTags` accepts a well-formed set and returns nil.
4. `ValidateTags` rejects `""` and `"   "` with a `CatUsage` error whose
   message quotes the offending value. Assert with `errors.As` to
   `*core.FeedError` and `errors.Is(err, core.ErrUsage)`, never on the string.
5. `ValidateTags` rejects a comma-bearing tag and a whitespace-bearing tag
   (table-driven subtests over `"a,b"`, `"machine learning"`, `"a\tb"`).
6. `ParseTagMatch` maps `""` and `"all"` to `MatchAll`, `"any"` to `MatchAny`.
7. `ParseTagMatch("some")` is a `CatUsage` error naming `all` and `any`.

## Gotchas

- `revive`'s `redefines-builtin-id` is enabled: do not name a parameter or
  local `max`, `min`, or `any`. `Match TagMatch` is fine; a local named `any`
  is not.
- Every package needs a doc comment for `revive`; `core` already has one, so a
  new file in it needs no package comment, only the exported-identifier ones.
- Exported identifiers require doc comments starting with the identifier name.

## Acceptance Criteria

- `core.TagMatch`, `core.MatchAll`, `core.MatchAny`, `core.CanonicalTags`,
  `core.ValidateTags`, and `core.ParseTagMatch` exist in `core/tags.go` with
  the signatures in the design section and godoc comments.
- `core.Feed` carries `Tags []string`; `core.ListFilter`, `core.ItemQuery`, and
  `core.PrunePolicy` each carry `Tags []string` and `Match TagMatch`, with the
  zero value preserving today's "match everything" behavior.
- Behaviors 1-7 are covered in `core/tags_test.go`, each asserting through
  `errors.As`/`errors.Is` rather than message strings.
- No file outside `core/` is modified.
- `make build` passes.
