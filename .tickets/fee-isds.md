---
id: fee-isds
status: closed
deps:
  [
    fee-63n9,
    fee-0l84,
    fee-jf82,
    fee-nkks,
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

# E7: Discovery and interop

Epic. The discover command (autodiscovery plus bounded probe with parse-validation) and OPML import/export. Covers requirements 8 and 17. Refs: docs/cli-design.md (Feed Discovery, OPML Interoperability).

## Notes

**2026-06-30T00:08:45Z**

Closed as a dependency-gate epic (same pattern as fee-63n9/fee-8cau/fee-gyos): no new code, all three children already closed (discover fee-0l84, OPML import fee-jf82, OPML export fee-nkks). Verified requirements 8 and 17 integrate end-to-end against a native build under a green make build. Live smoke test: (1) discover autodiscovers a parse-validated candidate and writes no store rows; (2) import handles nested outlines, reports a typed-but-urlless entry under failed, and is idempotent on re-import (added then skipped); (3) export emits valid OPML 2.0 with aliases as text/title; (4) the 'export | import -' stdin round-trip re-adds all feeds (added==count, failed empty). Closing E7 makes fee-wwyw (schema command) and fee-8eph (E8) the next work as their other deps resolve.
