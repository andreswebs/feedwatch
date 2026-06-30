---
id: fee-lzyw
status: closed
deps: [fee-63n9, fee-qnv1]
links: []
created: 2026-06-29T18:45:23Z
type: task
priority: 1
assignee: Andre Silva
parent: fee-2heq
tags: [parse]
---

# Dedup-key derivation

Derive the per-item dedup key (guid then link then title), assigned to core.Item.DedupKey for the store's (feed_url, dedup_key) uniqueness. Pure, deterministic, never empty; no link+published rung. Refs: docs/cli-design.md (Poll Semantics, Item Model and Content).

## Design

Derive the deduplication key for an item, used by the store as part of
`(feed_url, dedup_key)`.

```go
func DedupKey(it core.Item) string
// precedence: GUID/Atom id -> item link -> title.
// Returns a stable string; never empty (falls back to a hash of title+link
// only if all three are empty, documented as a last resort).
```

- Primary: the raw `GUID` (RSS guid or Atom id) when present.
- Fallback 1: the item `Link`.
- Fallback 2: the `Title`.
- The chosen key is assigned to `core.Item.DedupKey` before the item reaches the
  store. The store enforces uniqueness and upsert on `(feed_url, dedup_key)`.

TDD plan (table-driven):

1. (tracer) an item with a GUID uses the GUID as the key.
2. no GUID, has link -> uses the link.
3. no GUID, no link, has title -> uses the title.
4. identical inputs produce identical keys (stable/deterministic).

Deep-module note: a pure function; the policy is documented in
docs/cli-design.md (Poll Semantics). No `link+published` rung (dropped by
design decision) to avoid re-dated items appearing new.

## Acceptance Criteria

- `DedupKey` derives guid -> link -> title; stable and deterministic; never
  empty.
- Matches the dedup identity in docs/cli-design.md (Poll Semantics); no
  `link+published` rung.
- Behaviors 1-4 covered table-driven.
- Supports the dedup part of Req 9. `make validate` passes.

## Notes

**2026-06-29T21:55:01Z**

Implemented parse.DedupKey(core.Item) string in src/internal/parse/dedup.go: precedence GUID -> Link -> Title, with a documented last-resort sha256 hash of title+link so the key is never empty (degenerate items only). Pure and deterministic. Tests in dedup_test.go cover the three-rung precedence (table-driven), never-empty, and determinism (incl. the hashed path). Scope is the pure function ONLY: the parser's mapItem still deliberately defers DedupKey assignment to a later layer (per its doc comment); the consumer poll:dedup-and-consume (fee-q6t3) will call parse.DedupKey and assign core.Item.DedupKey before items reach the store. No link+published rung by design. make build green.
