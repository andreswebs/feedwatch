---
id: fee-j4w1
status: closed
deps: [fee-d974]
links: []
created: 2026-06-30T03:43:07Z
type: bug
priority: 2
tags: [cli]
---

# items: --fields ignores subset and accepts invalid field names

`items --fields` does not narrow output to the requested subset, and an invalid field name is silently accepted instead of rejected. Found during manual QA (TC-ITEMS-007); see `docs/qa.result.bak.md` BUG-005.

## Design

Expected (REQ 13, `docs/usage.md`): `--fields` projects to exactly the requested subset; an invalid field name is rejected with a usage error (exit 1).

Observed two issues:

1. A fixed base set is always emitted regardless of request: `feed_url`, `title`, `link`, `published_at`. Requested valid fields are added on top. For example `--fields summary` returns `feed_url, link, published_at, title, summary` instead of just `summary`; `--fields title` returns `feed_url, link, published_at, title`.
2. An invalid field name is silently accepted: `items --fields bogusfield` exits `0` with no error (expected exit `1`, usage category). Partially-valid lists like `title,bogus` also succeed silently.

```sh
feedwatch --db "$DB" items --fields summary --limit 1 | jq ".items[0]|keys"   # extra base fields present
feedwatch --db "$DB" items --fields bogusfield; echo $?                        # 0, expected 1
```

## Acceptance Criteria

- `items --fields <list>` returns only the requested fields (plus any always-on identity field that is documented as such).
- An unknown field name is rejected with a usage error and exit `1`.
- Omitting `--fields` still returns the full normalized item.

## Implementation Plan

Decision (confirmed): `feed_url` is the documented always-on identity field. `--fields <list>` returns exactly `feed_url` plus the requested fields, an unknown field name is a usage error (exit `1`), and omitting `--fields` returns the full normalized item unchanged (preserving the documented `null` `published_at`).

1. Canonical field set in `core` (single source of truth). In `src/internal/core/query.go`, add the canonical valid field names (the 13 documented in `docs/usage.md`) and a membership check:

   ```go
   // ValidItemFields are the field names accepted by items --fields.
   var ValidItemFields = map[string]bool{
       "id": true, "title": true, "link": true, "summary": true,
       "content_html": true, "content_text": true, "content_mime_type": true,
       "base_url": true, "author": true, "categories": true, "enclosures": true,
       "published_at": true, "updated_at": true,
   }
   ```

   The sqlite name-to-column mapping (`fieldColumns`) stays in the store but is keyed by these same names.

2. Validate in the CLI. In `buildItemQuery` (`src/internal/cli/items.go`), after reading `cmd.StringSlice("fields")`, reject the first unknown name with a usage error:

   ```go
   for _, f := range fields {
       if !core.ValidItemFields[f] {
           return core.ItemQuery{}, usageErr("--fields: unknown field " + strconv.Quote(f))
       }
   }
   ```

3. Project the output to exactly `feed_url` + requested fields. Add a projector in `core` that reads a `core.Item` into a map keyed by the requested field names, always including `feed_url`:

   ```go
   func ProjectItem(it Item, fields []string) map[string]any
   ```

   In `itemsAction`, when `--fields` is non-empty, emit the projected `[]map[string]any`; when empty, emit the full items exactly as today. This makes the JSON output reflect the projection without adding `omitempty` to `core.Item`, which would otherwise suppress the documented `null` `published_at` in the full-item path and churn the golden files. `ItemsResult.Items` is already `jsonschema:"opaque"`, so the dynamic per-item shape is already declared.

   Implementation note: the projected path needs an envelope carrying `[]map[string]any` (widen `Items` to `any`, or add a sibling result type). Under `--format text` the projected output renders the same requested field set; the full (unprojected) text output is unchanged.

4. No store change is required for correctness: the stores already populate `feed_url` plus the requested columns, and the CLI projects the returned struct into the output map, so the existing sqlite/testsupport column selection becomes a pure read optimization.

Verification:

- Tests: `--fields summary` returns exactly `feed_url` + `summary`; `--fields title` returns `feed_url` + `title`; `--fields bogusfield` and `--fields title,bogus` exit `1` with a `usage` error; omitting `--fields` returns the full item (with `published_at` present or `null`).
- Update `docs/usage.md` and REQ 13 in `docs/specs/001-initial-implementation/requirements.md` to document `feed_url` as the always-on identity field.
- `make build` green (watch the golden-file suite); learnings entry.

## Notes

**2026-06-30T03:43:07Z**

Source: manual QA report docs/qa.result.bak.md BUG-005 (TC-ITEMS-007). Severity Medium, Priority P1.

**2026-06-30T13:52:46Z**

Fixed --fields projection and validation. Added core.ValidItemFields (single source of truth) and core.ProjectItem(it, fields) -> map[string]any keyed by requested field names, always seeding feed_url. buildItemQuery now rejects the first unknown field name as a CatUsage error (exit 1, empty stdout); title,bogus fails too. itemsAction emits a ProjectedItemsResult ([]map[string]any) when --fields is set, else the full ItemsResult ([]core.Item) unchanged (preserving null published_at). Kept two concrete envelope types rather than widening Items to any, because jsonschema.opaqueSchema renders an interface as {type:object} and would have broken the schema contract test (it renders a slice as array-of-object). Text mode renders feed_url + requested columns. Docs: usage.md and REQ 13 now document feed_url as always-on identity. New cli tests: exact-subset keys, unknown-field rejection (alone + partial), full-item null published_at. make build green.
