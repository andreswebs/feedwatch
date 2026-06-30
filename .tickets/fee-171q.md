---
id: fee-171q
status: closed
deps:
  [
    fee-63n9,
    fee-vzu8,
    fee-4q22,
    fee-55gy,
    fee-on7r,
    fee-as1j,
    fee-t8ez,
    fee-gyos,
    fee-8cau,
    fee-2heq,
  ]
links: []
created: 2026-06-29T15:36:08Z
type: epic
priority: 2
assignee: Andre Silva
tags: [cmd]
---

# E5: Subscriptions and feed lifecycle

Epic. The add, rm, list, enable, and disable commands plus the failure lifecycle (count, exponential backoff, auto-disable, reset on success). Covers requirements 7 and 15. Refs: docs/cli-design.md (Commands, Feed Identity, Failure Handling).

## Notes

**2026-06-29T23:42:37Z**

Closed as a dependency-gate epic (same pattern as fee-63n9/fee-8cau/fee-gyos): all six children closed (add fee-4q22, rm fee-55gy, list fee-on7r, enable fee-as1j, disable fee-t8ez, failure lifecycle fee-vzu8). No new code; verified reqs 7 and 15 integrate end-to-end against the native binary with a local RSS server. Req 7: add --alias -> list (active) -> disable (status disabled) -> enable (active, failures reset) -> rm (removed, list empty), all exit 0 with clean JSON envelopes. Req 15: forced poll against a dead host recorded failures:1 + last_error, emitted a structured network *FeedError on stderr, and exited 2 (all targeted feeds failed). make build green. Closing E5 unblocks fee-jf82 (OPML import) and the E6/E7 epics + schema command (fee-wwyw), all of which list fee-171q as a dep.
