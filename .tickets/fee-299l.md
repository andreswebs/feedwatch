---
id: fee-299l
status: closed
deps: [fee-63n9, fee-zuz2, fee-lfyq, fee-d38c, fee-v2e5]
links: []
created: 2026-06-29T15:36:08Z
type: epic
priority: 1
assignee: Andre Silva
tags: [test]
---

# E9: Quality and docs

Epic. Shared test harness (in-memory Store double, httptest server, fixture corpus, fake clock), golden-file e2e suite, CI gate, and user documentation. Covers requirement 20 and the scheduling recipe of 18. Refs: docs/cli-design.md (Scheduling); docs/plan.bak.md (E9, tk conventions).

## Notes

**2026-06-30T00:34:40Z**

Closed as a dependency-gate/roll-up epic (same pattern as fee-63n9, fee-8cau, fee-gyos): no new code, only verification. All four children closed and their deliverables present and integrating: test harness (src/internal/testsupport/), golden-file e2e suite (src/internal/e2e/), CI workflow + src/.golangci.yml, and user docs (README.md, docs/usage.md). Verified the quality gate green on the current tree: make validate (vet, golangci-lint 0 issues, all tests ok) and make build both pass.

Known loose end, intentionally left untouched: .github/workflows/ci.yml is still ci.yml.disabled. This was a deliberate, documented deferral by the fee-lfyq implementer (CI activation + broken-change-fails-CI verification 'should be picked up in a follow-up'). Out of scope for this roll-up epic, and activating a GitHub Actions workflow on the user's repo is left to the user. The workflow file is complete and ready (setup-go 1.26, module cache, runs make validate + make build on push/PR); a future ticket need only rename it to ci.yml and verify a broken commit fails CI.
