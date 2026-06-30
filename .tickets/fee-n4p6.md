---
id: fee-n4p6
status: open
deps: [fee-ah78]
links: []
created: 2026-06-30T22:54:52Z
type: feature
priority: 2
assignee: Andre Silva
parent: fee-bqyy
tags: [beta, cli, items]
---
# Field selection ergonomics (feed_url no-op, did-you-mean)

Implements Req 5 of the 002-beta spec
([requirements.md](../docs/specs/002-beta/requirements.md), section 5). Amends
baseline Req 13 (Querying History). Purely additive.

`feed_url` is the always-on identity field returned in every item, but naming it
in `--fields` is currently a hard usage error (`fee-j4w1` made `feed_url` the
documented always-on field and made unknown names exit 1). An agent that lists
`title,link,feed_url` for clarity gets exit 1 with empty stdout, which reads as
"no results" when stderr is discarded. Separately, a simple typo like
`--fields tilte` fails with no hint. This change accepts `feed_url` as a no-op
and adds a did-you-mean suggestion to the unknown-field error, so projection
tolerates natural usage while still catching real mistakes. It depends on Req 2
(`fee-ah78`) because both edit the same `--fields` validation path and the field
name set (`fetched_at` is added there).

## Design

- `--fields feed_url` (alone or in a list) is accepted and treated as a no-op:
  `feed_url` is emitted regardless, so naming it changes nothing.
- An unrecognized field name is still a usage error (exit 1) that returns no
  partial results.
- When the unknown name closely resembles a valid field, the error includes a
  did-you-mean suggestion.

```sh
feedwatch items --fields title,link,feed_url   # accepted; feed_url is a no-op
feedwatch items --fields tilte
# stderr: {"error":{"category":"usage","message":"--fields: unknown field \"tilte\"; did you mean \"title\"?"}}
# exit 1
```

## Acceptance Criteria

- `feed_url` is accepted within a `--fields` projection and treated as a no-op,
  not an error.
- An unrecognized field name is rejected as a usage error and returns no partial
  results.
- When the rejected name closely resembles a valid field name, the error message
  includes a suggestion of the intended field.

## Implementation Plan

The validation lives in `buildItemQuery` (`src/internal/cli/items.go`), which
loops over `cmd.StringSlice("fields")` and rejects any name not in
`core.ValidItemFields`. `feed_url` is intentionally absent from that set (it is
the always-on identity field), so it currently triggers the unknown-field
error.

1. Accept `feed_url` as a no-op. In the `buildItemQuery` validation loop, skip
   the identity field before the membership check:

   ```go
   for _, f := range fields {
       if f == "feed_url" { // always-on identity field: naming it is a no-op
           continue
       }
       if !core.ValidItemFields[f] {
           return core.ItemQuery{}, usageErr(unknownFieldMessage(f))
       }
   }
   ```

   `feed_url` may stay in `q.Fields`: `core.ProjectItem` always seeds
   `feed_url` and has no `case "feed_url"`, and the SQLite `projectedColumns`
   selects it via `alwaysColumns` regardless, so passing it through is harmless.
   (The `testsupport` `project` is likewise always-retaining `feed_url`.) No
   change needed in `core` or the stores.

2. Build the did-you-mean message. Add a small helper in the `cli` package
   (e.g. `src/internal/cli/items.go` or a new `suggest.go`):

   ```go
   func unknownFieldMessage(name string) string {
       msg := "--fields: unknown field " + strconv.Quote(name)
       if s, ok := nearestField(name); ok {
           msg += "; did you mean " + strconv.Quote(s) + "?"
       }
       return msg
   }
   ```

   `nearestField` computes the Levenshtein distance from `name` to each
   candidate and returns the closest when it is within a small threshold (e.g.
   distance <= 2, and strictly less than `len(name)` to avoid absurd matches).
   Iterate candidates in a deterministic (sorted) order so the suggestion is
   stable, breaking ties by the shortest distance then lexical order.

3. Single source of truth for candidates. The candidate set is the valid
   projectable fields plus the `feed_url` identity field. Derive it from
   `core.ValidItemFields` rather than hardcoding, so it automatically includes
   `fetched_at` (added by Req 2). Add a small exported helper in
   `src/internal/core/query.go` to avoid map-iteration nondeterminism leaking
   into the message:

   ```go
   // ItemFieldNames returns the projectable field names plus the always-on
   // feed_url identity field, sorted, for suggestions and help text.
   func ItemFieldNames() []string { /* keys of ValidItemFields + "feed_url", sorted */ }
   ```

   `nearestField` ranges over `core.ItemFieldNames()`.

4. Keep the no-partial-results guarantee: rejection happens in `buildItemQuery`
   before any store query, so an unknown field never returns rows. This is
   already the structure; do not move validation past the query.

## Verification

- Tests (`src/internal/cli`): `--fields feed_url` and `--fields title,feed_url`
  succeed and return the expected keys (with `feed_url` present); `--fields
  tilte` exits 1 with a usage error whose message contains
  `did you mean "title"?`; `--fields zzzzzz` (no close match) exits 1 with the
  unknown-field message and no suggestion; a partially valid list like
  `title,bogus` still exits 1 and returns no items.
- Unit-test `nearestField`/Levenshtein for stable, deterministic suggestions
  (including tie-breaking).
- `make build` green; learnings entry under the Req 5 heading in
  [learnings.md](../docs/specs/001-initial-implementation/learnings.md).

## References

- Spec: [requirements.md](../docs/specs/002-beta/requirements.md) section 5.
- Design: [cli-design.md](../docs/cli-design.md), "Querying History".
- Field notes: [usage-learnings.md](../docs/specs/002-beta/usage-learnings.md),
  "Query stored history" (the `feed_url` gotcha).
- Builds on: `fee-j4w1` (`feed_url` as always-on identity; unknown field exits
  1). Depends on: `fee-ah78` (Req 2) for the shared `--fields` path.
