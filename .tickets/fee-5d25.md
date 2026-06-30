---
id: fee-5d25
status: closed
deps:
  [
    fee-63n9,
    fee-u0i4,
    fee-q6t3,
    fee-12gs,
    fee-ydl6,
    fee-7r0v,
    fee-gyos,
    fee-8cau,
    fee-2heq,
    fee-171q,
  ]
links: []
created: 2026-06-29T15:36:08Z
type: epic
priority: 2
assignee: Andre Silva
tags: [cmd]
---

# E6: Poll, query and retention

Epic. The poll command (split into fetch-orchestration, dedup-and-consume, output-shaping), plus items and prune. Covers requirements 9, 13, and 16. Refs: docs/cli-design.md (Poll Semantics, Concurrency and Politeness, Querying History, Retention).

## Notes

**2026-06-29T23:45:28Z**

Closed as a dependency gate (same pattern as fee-63n9/fee-8cau/fee-gyos): no new code. All five children closed (fee-u0i4 fetch-orchestration, fee-q6t3 dedup-and-consume, fee-12gs output-shaping+exit, fee-ydl6 items, fee-7r0v prune) and all other deps closed. Verified reqs 9/13/16 end-to-end on a native build (CGO_ENABLED=0 go build ./cmd/feedwatch) against a local python http.server RSS feed: add->poll reports {polled:1,new_items:2} with stable item order; immediate 'poll --force' reports new_items:0 (auto-consume/dedup); 'items --fields title,link' projects+orders published desc; 'prune --max-items 1' returns {pruned:1} keeping the newest item and preserving dedup. All exits 0; make build green. Closing E6 unblocks fee-wwyw (schema command) and E8 (fee-8eph) as their other deps resolve. E7 (fee-isds) is NOT yet closeable: its children fee-0l84 (discover), fee-jf82 (import), fee-nkks (export) are still open.
