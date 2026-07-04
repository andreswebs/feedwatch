---
id: fee-6ol6
status: open
deps: []
links: []
created: 2026-07-04T12:21:07Z
type: feature
priority: 2
assignee: Andre Silva
tags: [cli, schema]
---
# items: make --fields names discoverable (error list, help, schema)

## Context

First-customer feedback (v0.0.1): `feedwatch items --fields published` fails
with `{"error":{"category":"usage","message":"--fields: unknown field
\"published\""}}`. The actual field is `published_at`, discoverable only by
running an unprojected query and inspecting JSON keys. `feedwatch schema`
documents flags but not item fields, so an agent wastes a probe round-trip.

Why the existing did-you-mean did not fire: `unknownFieldMessage`
(`src/internal/cli/suggest.go`) suggests the nearest field only within
Levenshtein distance 2 (`suggestMaxDistance`), and
`published -> published_at` is distance 3. Why `schema items` does not list
fields: `ItemsResult.Items` is tagged `jsonschema:"opaque"`
(`src/internal/cli/items.go`), so the reflector
(`src/internal/jsonschema/jsonschema.go`) emits a bare
`{"type":"array","items":{"type":"object"}}` instead of descending into
`core.Item`.

## Design

Three independent improvements; implement all.

### 1. Error message lists the valid fields

In `unknownFieldMessage` (`src/internal/cli/suggest.go`), always append the
full valid list after the optional did-you-mean:

```go
msg := "--fields: unknown field " + strconv.Quote(name)
if s, ok := nearestField(name); ok {
    msg += "; did you mean " + strconv.Quote(s) + "?"
}
msg += "; valid fields: " + strings.Join(core.ItemFieldNames(), ", ")
```

`core.ItemFieldNames()` (`src/internal/core/query.go`) already returns the
sorted projectable names plus `feed_url`.

### 2. Help text enumerates the fields

In `itemsCommand` (`src/internal/cli/items.go`), build the `--fields` flag
usage from the same source of truth so it cannot drift:

```go
&cliv3.StringSliceFlag{
    Name: "fields",
    Usage: "project to a subset of item fields (" +
        strings.Join(core.ItemFieldNames(), ", ") + "); full item when omitted",
}
```

This also flows into `feedwatch schema items` flag descriptions if flag usage
is ever added to the schema output; today it at least fixes `--help`.

### 3. `schema items` and `schema poll` document the item shape

Remove the `jsonschema:"opaque"` tag from `ItemsResult.Items`
(`src/internal/cli/items.go`) and `PollResult.Items`
(`src/internal/cli/poll.go`) so `jsonschema.Reflect` descends into
`core.Item`. Keep `opaque` on `ProjectedItemsResult.Items`: its per-element
shape is caller-projected and dynamic.

This requires teaching the reflector about time values, otherwise `time.Time`
(a struct with no exported fields) reflects as a bare object. In
`src/internal/jsonschema/jsonschema.go`:

- Add a `Format string` field with `json:"format,omitempty"` to the internal
  `schema` struct.
- In `reflectType`, before the kind switch, special-case
  `reflect.TypeOf(time.Time{})`:

    ```go
    if t == timeType { // var timeType = reflect.TypeOf(time.Time{})
        return marshal(schema{Type: "string", Format: "date-time"})
    }
    ```

    Pointers already unwrap via the existing `reflect.Pointer` case, so
    `*time.Time` (nullable `published_at`) also renders as a date-time
    string. Nullability: `published_at` is `*time.Time` without `omitempty`,
    so it is emitted as `null` when unset. Render pointer-to-time as
    `{"type":["string","null"],"format":"date-time"}` by changing the
    `schema.Type` field to `any` (marshals as a string or an array) and
    returning the two-type form from the pointer branch when the element is
    `timeType`. If that complicates the reflector disproportionately, the
    fallback is acceptable: plain `{"type":"string","format":"date-time"}`
    for both, with nullability noted in `docs/usage.md`.

`core.Item` (`src/internal/core/types.go`) already carries agent-facing JSON
tags (`id`, `title`, `link`, `summary`, `content_html`, `content_text`,
`content_mime_type`, `base_url`, `author`, `categories`, `enclosures`,
`published_at`, `updated_at`, `fetched_at`; `DedupKey` and `Seen` are
`json:"-"` and stay hidden), so the reflected schema matches the real output.

## TDD plan

1. `src/internal/cli/suggest_test.go`: message for `published` contains
   `valid fields:` and `published_at`; message for a near-typo still contains
   the did-you-mean.
2. `src/internal/cli/items_test.go`: the usage error returned for an unknown
   field carries the full list (assert one known name and the `valid fields:`
   prefix).
3. `src/internal/jsonschema/jsonschema_test.go`: a struct with `time.Time`
   and `*time.Time` fields reflects to `"type":"string"` with
   `"format":"date-time"` (and the null-tolerant type for the pointer, if
   implemented).
4. `src/internal/cli/schema_test.go`: `schema items` output schema's
   `items.items.properties` includes `published_at` and `title`; `schema poll`
   likewise for its `items` array. `schema` for projected output
   (`ProjectedItemsResult`) remains opaque.

## Acceptance criteria

- Unknown `--fields` errors list every valid field name.
- `feedwatch items --help` enumerates the field names.
- `feedwatch schema items` and `feedwatch schema poll` document the item
  object's fields and types, with time fields as `date-time` strings.
- `docs/usage.md` items section documents the field list and nullability of
  `published_at`/`updated_at`.
- `make build` passes.
