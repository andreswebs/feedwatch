---
id: fee-63n9
status: closed
deps: [fee-mls1, fee-qnv1, fee-4m17, fee-chr5, fee-cmkb, fee-po72, fee-l45i, fee-7ons]
links: []
created: 2026-06-29T15:27:44Z
type: epic
priority: 0
assignee: Andre Silva
tags: [cli]
---

# E1: Foundation and CLI contract

Epic. Establishes the urfave/cli v3 command tree, global/inherited flags, output envelope, exit-code boundary, structured logging, color gating, core error taxonomy, and the signal-aware context. Covers requirements 1, 2, 3, 5, 19 and part of 4. See docs/plan.bak.md (E1) and docs/cli-design.md.

## Notes

**2026-06-29T20:54:52Z**

Closed as the foundation gate. All substantive children are done and integrate cleanly under a green 'make build': error taxonomy (fee-4m17), interfaces+Clock (fee-chr5), config+defaults (fee-cmkb), output envelope/renderers/color gating (fee-po72), slog setup (fee-l45i), and the urfave/cli v3 root with global flags + exit-code boundary (fee-7ons). Signal-aware context (SIGINT/SIGTERM via signal.NotifyContext) is wired in cmd/feedwatch/main.go, with HandleExitCoder owning the exit. Smoke-tested the live binary: 'feedwatch --version' -> {"version","commit","go"} JSON exit 0; '--format text --version' -> human line; 'feedwatch bogus' -> {"error":{"category":"usage",...}} on stderr exit 1. Covers reqs 1 (one-shot + signal ctx), 2 (output contract), 3 (exit codes), 5 (config precedence), and the --help/--version part of 4. The remaining open child fee-c66o (walking skeleton: version + migrate --status end-to-end) is the deferred capstone and is intentionally blocked by the migrate/store lanes this epic gates (fee-bqne -> fee-vlk9 -> fee-aqkn); it is proven after those land, not before. Closing this unblocks the P1 lanes: fee-bqne (SQLite store), fee-b91x (HTTP client), fee-e487 (parser), fee-lzyw (dedup-key), fee-zuz2 (test harness), fee-lfyq (CI).
