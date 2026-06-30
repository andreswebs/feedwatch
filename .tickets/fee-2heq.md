---
id: fee-2heq
status: closed
deps: [fee-63n9, fee-e487, fee-kx39, fee-lzyw]
links: []
created: 2026-06-29T15:36:08Z
type: epic
priority: 1
assignee: Andre Silva
tags: [parse]
---

# E4: Parsing and item model

Epic. gofeed-backed Parser, format coverage and malformed handling, item normalization (dates to UTC, null-date rule, content/summary/author precedence, base URL, MIME), and dedup-key derivation. Covers requirements 12 and 14 and the dedup part of 9. Refs: docs/cli-design.md (Parsing and Robustness, Item Model and Content, Poll Semantics).

## Notes

**2026-06-29T22:20:19Z**

Epic complete: all three children closed and their deliverables landed in src/internal/parse (gofeed.go format coverage, normalize.go item normalization, dedup.go dedup-key derivation), each with passing tests. Closing the epic.
