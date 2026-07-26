---
id: fee-hlu7
status: closed
deps: [fee-q120]
links: []
created: 2026-07-26T11:24:54Z
type: task
priority: 2
assignee: Andre Silva
tags: [adr, testing]
---
# add the ADR 0006 in-process golden-triple harness

Add the ADR 0006 in-process golden-triple harness: scenarios drive `Run(args, deps)` with buffer streams and golden-diff stdout, stderr, and the exit code, with `-update` regeneration. Reduce the exec-based `internal/e2e` suite to what genuinely needs a process (signals). Depends on the `Run` seam ticket, and must land after the envelope and error-shape goldens have settled.

## Design

## Context

`docs/adr/0006-cli-testing.md` makes the in-process golden triple the required
harness: "scenarios invoke the delegate (`Run(args, deps)` per the CLI-structure
ADR) with buffer streams and compare three artifacts per scenario against golden
files: stdout, stderr, and the exit code", with an `-update` flag, normalized
volatile values, and the conformance obligations expressed through it. The
exec-based suite is conditional, kept only for "process-level behavior that
in-process tests cannot reach (signal handling and 130/143 exits, subprocess
lifecycles, child exit-status passthrough)".

feedwatch has the conditional half and not the required half. `internal/e2e` is
exec-based (`e2e_test.go`, `signal_test.go`) and the CLI-level tests drive
`NewRootCommand` through a helper (`internal/cli/root_test.go:28`, pre-rename)
using `*os.File` temporaries and asserting parsed JSON, not golden triples.

Because scenarios touch only the delegate contract, the resulting suite is
framework-blind: it survives a CLI-framework replacement unchanged and is the
safety net for one. That is the payoff, and it is only available once `Run`
exists, which is why this ticket depends on the seam ticket.

## Sequencing note

Land this after the envelope-head and error-envelope tickets have settled their
goldens. The new tree seeds from those shapes; building it earlier means
regenerating it twice.

## Work

### 1. Harness

Add a golden-triple harness in `internal/command` (or a sibling test package)
plus a `testdata` tree. Do not reinvent the normalization and diff logic: lift it
from `internal/e2e/e2e_test.go:110-127` (`harness.run`) and `:190-212`
(`checkGolden`), which already implement the `-update` flag
(`internal/e2e/e2e_test.go:24`), the host placeholder normalization (`:31-33`),
and the write-then-compare discipline.

Shape per scenario: build `Deps` with `bytes.Buffer` for `Out` and `Err`, call
`Run(args, deps)`, then compare three artifacts:

- `<scenario>.stdout`
- `<scenario>.stderr`
- the returned exit code (assert against a scenario-declared expectation, or
  pin it in a third golden file; declaring it in the scenario table is clearer
  and matches the existing `wantExit` parameter at
  `internal/e2e/e2e_test.go:110`)

### 2. Normalization

Volatile values that must be normalized before comparison: the httptest
host:port (already tokenized as a host placeholder in the exec suite), the store
path under a temp dir, `fetched_at` timestamps (the exec goldens use a
`<fetched_at>` token, see `internal/e2e/testdata/lifecycle/items.stdout`), the
VCS commit and Go version in the `--version` envelope (`<commit>`, `<go>` tokens
in `internal/e2e/testdata/version.stdout`). ADR 0006 warns that normalization
discipline is load-bearing and unnormalized volatile values are the harness's
main failure mode.

### 3. Conformance obligations

ADR 0006 requires the harness to carry these, and they are the reason it is
worth more than the tests it replaces:

- Every code in every command's `exit_codes` table
  (`internal/cli/schema_registry.go:81-96`, pre-rename) is exercised by at least
  one scenario, and every observed exit is a member of the declared table. This
  upgrades `exit_conformance_test.go` from a data-only check (does the table
  declare what `ExitCodeFor` can return) into an observed-behavior check (does
  the binary actually produce each declared code). Failure codes needing
  scenarios: 64 (bad flag, unknown command, unknown named feed), 65 (a store
  whose schema version exceeds the binary), 69 (unopenable store), 70
  (unclassified, if reachable), 78 (invalid config), plus results 0, 2, and 3.
  If a declared code has no reachable scenario, say so explicitly in a note
  rather than silently leaving it uncovered.
- Error scenarios cover the error-envelope shape from the error-envelope ticket.
- The warning scenario from the warning ticket covers the warning shape.

### 4. Reduce the exec suite

`internal/e2e` keeps `signal_test.go` and anything else that cannot run
in-process. The scenarios in `e2e_test.go` that only exercise stream contracts
move in-process; delete the exec versions and their goldens rather than keeping
two copies, which would drift. Keep the exec harness itself if the signal tests
depend on it (`internal/e2e/signal_test.go` builds the real binary).

### 5. Migrate the existing CLI tests

The per-command assertions in `internal/cli/*_test.go` (pre-rename) that decode
JSON to check a shape are largely superseded by scenario goldens. Migrating them
is not required by this ticket and would balloon it; what is required is that the
new harness exists, carries the conformance obligations, and that the old tests
still pass. State in a note which old tests are now redundant so a follow-up can
retire them.

## Invariants

- The exec-based signal tests keep passing unchanged: SIGINT to 130, SIGTERM to
  143.
- No golden content changes meaning during this ticket. Content moves from the
  exec tree to the in-process tree; if a byte differs, understand why before
  accepting it (the likely legitimate difference is stream interleaving, since
  buffers do not interleave the way two pipes do).

## TDD plan

1. (tracer) One scenario: `--version` through `Run` with buffers, golden-diffing
   stdout, stderr, and exit 0.
2. `-update` writes all three artifacts and a second run compares clean.
3. A scenario with a normalized volatile value (a temp store path) is stable
   across two runs in different temp dirs.
4. An error scenario golden-diffs the error envelope on stderr with empty
   stdout and the declared exit code.
5. The conformance test over the scenario table: every declared exit code has a
   scenario, and every scenario's observed exit is declared.

## Acceptance Criteria

- An in-process golden-triple harness exists: scenarios call
  `Run(args, deps)` with buffer streams and compare stdout, stderr, and the exit
  code against goldens, with an `-update` flag that regenerates them.
- Normalization covers every volatile value in scope (httptest host:port, temp
  store paths, `fetched_at`, VCS commit, Go version), and a scenario is stable
  across two runs in different temp directories.
- Every code in every command's declared `exit_codes` table is exercised by at
  least one scenario, and every observed exit is a member of the declared table.
  Any declared code with no reachable scenario is named in a note with the
  reason, rather than silently uncovered.
- Error scenarios cover the error-envelope shape and, if the warning ticket has
  landed, the warning line shape.
- The exec-based `internal/e2e` suite retains only what needs a process:
  `signal_test.go` passes unchanged (SIGINT to 130, SIGTERM to 143), and any
  stream-contract scenarios that moved in-process are deleted from the exec tree
  rather than duplicated.
- Golden content did not change meaning: any byte difference between the old
  exec golden and the new in-process golden is explained in a note (stream
  interleaving being the one expected source).
- A note lists the per-command tests now redundant under the harness, for a
  follow-up to retire.
- `internal/cli/exit_conformance_test.go` (or its post-rename equivalent) passes
  and is now backed by observed exits, not just declared data.
- `make build` passes (it runs `fmt-check`, `vet`, `lint`, `test`, then
  compile).

## Notes

**2026-07-26T11:25:20Z**

Cross-cutting requirements for this ticket (from the approved plan at .local/planning/adr-adoption.md, section 5):

- Quality gate: `make build` must pass. It runs `fmt-check`, `vet`, `lint`, `test`, then compile, and is the full validation. See docs/build.md.
- Follow TDD per the repo `tdd` and `golang` skills: write the failing test first, one behavior at a time, starting from the tracer behavior named in the design.
- Do NOT make git commits and do NOT create branches. The owner manages all git workflow. Staging is allowed only where a workflow requires it.
- Re-verify every `file:line` reference in the design before relying on it; the tree moves as sibling tickets land. Line numbers were verified 2026-07-26 against a clean tree at commit 0b894e9.
- Record non-obvious decisions and discoveries in docs/specs/001-initial-implementation/learnings.md under this ticket heading, and add a `tk add-note` summary before closing.
- Ticket markdown must lint clean: `markdownlint-cli2 --fix ".tickets/*.md"` then `markdownlint-cli2 ".tickets/*.md"` reporting 0 errors.
- Full plan context, including the seven owner decisions (D1-D7) this work implements, is in .local/planning/adr-adoption.md.

**2026-07-26T17:12:15Z**

Added the ADR 0006 in-process golden-triple harness in internal/command
(golden_test.go + golden_scenarios_test.go, testdata/ tree). Scenarios call
Run(args, deps) with bytes.Buffer streams and diff three artifacts per scenario:
stdout golden, stderr golden, and the exit code (declared as the wantExit table
value, the clearer form the design sanctioned). -update regenerates goldens.

Normalization: feed-server host:port (<feedserver>), temp store path (<db>, used
by the store-unavailable scenario), fetched_at (<fetched_at>), and the --version
commit/go tokens. A fixed clock (FixedClock(pollFixedTime)) makes published_at
and backoff deterministic. Stability across temp dirs is proven by the
store-unavailable golden being identical from the -update run to a clean run in a
fresh t.TempDir().

Conformance: assertDeclared checks every observed exit against the union of the
command exit-code tables at run time; TestGoldenExitCodeConformance asserts every
declared code has a scenario. Codes covered by scenarios: 0, 2, 3, 64
(err/usage_bad_flag), 65 (err/schema_too_new, set up by stamping MAX(version)+1
into schema_migrations via a second sqlite connection), 69 (err/store_unavailable),
78 (err/config_concurrency). Exit 70 (internal/unclassified) is intentionally
uncovered with an explicit note: it is only reachable via an unclassified Go
error or a recovered panic in main, neither deterministic from a real command.
The data-only exit_conformance_test.go still passes and is now backed by these
observed exits.

Exec suite reduced to its conditional half: internal/e2e keeps only signal_test.go
(130/143) plus TestMain/binPath/exitCodeOf/rssFeed it depends on. All stream-
contract scenarios moved in-process and their exec goldens (the whole
internal/e2e/testdata tree) were deleted rather than duplicated. The fee-udsl
first-poll regression moved in-process as TestGoldenFirstPollReportsAllNewItems
(JSON decode, never needed a process). doc.go updated.

Golden meaning unchanged: every shared golden is byte-identical to the deleted
exec golden except version.stdout, where in-process reports the injected
Deps.Version ("1.2.3") vs the exec build's "dev" (-buildvcs=false). That is an
injection-path value difference, not a contract change; there is no stream-
interleaving difference since both harnesses keep stdout/stderr as separate sinks.

Redundant-now (for a follow-up to retire, not done here): the JSON-shape decode
assertions in add/list/items/poll/migrate/discover/export/import/prune _test.go
and the error-code assertions in root_test.go and per-command usage tests are
largely superseded by the golden scenarios + assertDeclared. Acceptance only
required the old tests keep passing; they do. make build passes.
