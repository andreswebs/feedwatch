---
id: fee-d38c
status: closed
deps: [fee-8eph]
links: []
created: 2026-06-29T19:33:04Z
type: task
priority: 3
assignee: Andre Silva
parent: fee-299l
tags: [test]
---

# Golden-file end-to-end suite

Build a golden-file e2e suite that runs the real binary across command sequences (add/poll/items/list/migrate/version/discover/import/export/prune), diffing JSON stdout/stderr and asserting exit codes, with timestamp normalization and an -update flag. Refs: docs/cli-design.md (Input and Output Contract, Exit Codes); docs/plan.bak.md (E9).

## Design

A golden-file end-to-end suite that runs the real built binary and diffs its JSON
output, exercising the full agent-first contract.

- Build the binary (or `go run` the cmd) against a temp `--db`; drive command
  sequences: `add` (with a local httptest feed), `poll`, `poll` again (empty),
  `items`, `list`, `migrate --status`, `--version`, `discover`, `import`/
  `export`, `prune`.
- Capture stdout and stderr separately; compare against golden files. Because the
  output is JSON, goldens are JSON (normalize volatile fields like timestamps via
  the injected clock / a normalizer).
- Assert exit codes per scenario (0/1/2/3; 130/143 are covered by a signal test
  if feasible).
- `-update` flag to regenerate goldens.

TDD plan (the suite is the test):

1. (tracer) `add` then `poll` against a local feed emits the expected items JSON
   and exit 0; a second `poll` emits empty and exit 0.
2. an all-failed `poll` (unreachable feed) emits per-feed errors on stderr and
   exits 2; stdout still a valid envelope.
3. a partial `poll` exits 3.
4. `--version` and `migrate --status` match their goldens.

Deep-module note: black-box over the binary; the strongest guard that the JSON
contract and exit codes hold end-to-end.

## Acceptance Criteria

- Golden-file e2e drives the real binary across command sequences, diffing JSON
  stdout/stderr and asserting exit codes (0/1/2/3), with timestamp normalization
  and an `-update` flag.
- Behaviors 1-4 covered.
- Supports Req 1, 2, 3, 20. `make validate` passes.

## Notes

**2026-06-30T00:26:57Z**

Golden-file e2e suite landed in src/internal/e2e (black-box over the real built binary). TestMain go-builds the binary once to a temp dir; harness runs it with --quiet (suppresses slog info lines so stderr is only the structured error envelope) and a per-test temp --db, asserts exit codes, and golden-diffs normalized stdout/stderr. -update regenerates goldens. Normalizer rewrites only the volatile bits: the httptest server base URL -> <http://feedserver>, and --version's commit/go fields; fixture timestamps/guids/links are fixed so they stay verbatim (fetched_at/seen are json:"-" so never leak). Covers behaviors 1-4: lifecycle (add/poll new_items:2 / poll-again empty / items published-desc / list), all_failed (exit 2, valid empty stdout envelope + http-404 error on stderr), partial (exit 3), version + migrate --status; plus discover, OPML import/export, prune for breadth. 34 goldens, 7 tests, race-clean. make build green, 0 lint.
