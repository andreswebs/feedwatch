---
id: fee-r1kt
status: closed
deps: []
links: [fee-8klp]
created: 2026-07-04T12:21:07Z
type: feature
priority: 3
assignee: Andre Silva
tags: [cmd]
---
# check command: validate subscriptions without storing items

## Context

First-customer feedback (v0.0.1): `import --no-validate` is great for bulk
adds, but there is no lightweight way to test reachability afterwards without
pulling content. Today the only option is `poll --force`, which fetches,
stores items, updates validators and schedules, and drives the failure
lifecycle. The customer wants a `feedwatch check` for post-import cleanup and
cron health checks that must not mutate anything.

## Design

New command in `src/internal/cli/check.go` (+ `check_test.go`), registered in
the `commands()` list in `src/internal/cli/root.go` (after `poll`) and in
`schemaRegistry` (`src/internal/cli/schema_registry.go`).

### Contract

```text
feedwatch check [FEED...]
```

- No args: check every active feed (`ListFeeds` with
  `core.ListFilter{Status: core.FeedActive}`), like `poll --force` targeting.
- Named refs: resolved exactly like poll's named path (URL or unique alias via
  `st.GetFeed`); an unknown ref is a usage error (exit 1). Disabled feeds can
  be checked when named explicitly.
- Each feed is fetched with a plain unconditional GET (no stored ETag or
  Last-Modified validators; a 304 would prove nothing about parseability) and
  its body parsed with the shared `parse.Parser`.
- Zero store writes: no items, no validators, no schedule changes, no
  success/failure lifecycle. Read-only like `discover`.

Envelope (declare `CheckResult` and `CheckFailure` in `check.go`;
`failures` always present, `[]` when empty, mirroring `PollResult`):

```go
type CheckFailure struct {
    FeedURL  string        `json:"feed_url"`
    Category core.Category `json:"category"`
    Status   int           `json:"status,omitempty"`
    Message  string        `json:"message"`
}

type CheckResult struct {
    Checked  int            `json:"checked"`
    OK       int            `json:"ok"`
    Failed   int            `json:"failed"`
    Failures []CheckFailure `json:"failures"`
}
```

`Message` carries the underlying detail (`fe.Message`, falling back to
`fe.Err.Error()`), consistent with fee-8klp's poll failures change; align the
helper if both land.

Exit codes mirror poll (register with a `checkExitCodes()` beside
`pollExitCodes()`):

- 0: all checked feeds passed (or nothing to check)
- 1: usage or configuration error (unknown ref, unreachable store)
- 2: all checked feeds failed
- 3: partial, some failed

Reuse poll's derivation shape (`Result.ExitCode` in
`src/internal/poll/run.go`) as a method on `CheckResult`; return the code via
the existing `exitError` type in `src/internal/cli/exit.go` the way
`pollAction` does.

### Concurrency

Fetch and parse concurrently with `errgroup` bounded by `cfg.Concurrency`,
following `validateCandidates` in `src/internal/cli/import.go` (results into a
position-indexed slice, one failure never cancelling siblings). Politeness:
per-host serialization like poll's `orchestrate` is nice-to-have; for a
validation pass over mostly-distinct hosts the bounded errgroup is
sufficient. Note the decision in the ticket notes when implementing.

### Failure classification

Fetch and parse errors already arrive as `*core.FeedError` with `Category`
(`network`, `http`, `timeout`, `parse`) and `Status` for HTTP failures; use
`errors.As` like poll's `asFeedError` (`src/internal/poll/orchestrate.go`),
falling back to `core.NetworkErr`. Do not re-wrap as usage errors the way
`validateParsesAsFeed` (`src/internal/cli/add.go`) does: check reports
per-feed outcomes, it does not abort the invocation.

### Per-feed stderr detail

Mirror poll: emit the collected `[]*core.FeedError` via `r.Errors(feedErrs)`
after the envelope, so humans get readable per-feed lines under
`--format text` while agents read the envelope.

### Docs

- `docs/usage.md`: new `check` section (contract, envelope, exit codes, "does
  not store items or update schedules", cron health-check example).
- `docs/specs/001-initial-implementation/manual-qa.md`: add a check scenario
  (import with `--no-validate`, then `check` flags the dead feeds).

## TDD plan

CLI-level tests with `httptest` and the injected `InMemoryStore`
(`src/internal/testsupport`), following `import_test.go`/`poll_test.go`
patterns:

1. Two good feeds, one 404, one HTML page (parse failure): envelope reports
   `checked=4 ok=2 failed=2` with categorized failures (`http` with
   `status:404`, `parse`), exit 3.
2. All feeds good: exit 0, `failures: []`.
3. All feeds bad: exit 2.
4. Named ref by alias resolves; unknown ref is a usage error, exit 1.
5. Store mutation guard: after a check over feeds with items-bearing bodies,
   the store contains no items, and feed rows (validators, last-fetch,
   failure counters, schedule) are unchanged.
6. `schema check` returns the registered contract (exit codes and reflected
   `CheckResult` schema).

## Acceptance criteria

- `feedwatch check [FEED...]` validates reachability and parseability with
  zero store writes and poll-style exit codes.
- Envelope shape as specified; `failures` always present.
- Registered in `root.go` and `schemaRegistry`; covered by tests above.
- `docs/usage.md` and manual QA plan updated.
- `make build` passes.

## Notes

**2026-07-04T15:53:42Z**

Implemented feedwatch check command in src/internal/cli/check.go. Uses errgroup bounded by cfg.Concurrency for concurrent fetch+parse with position-indexed result slots. Unconditional GET (no ETag/LastModified in FetchRequest) so 304s cannot mask real parse failures. Zero store writes: no UpsertItems, RecordSuccess, or RecordFailure calls. checkFeedError extracts *core.FeedError via errors.As, falling back to core.NetworkErr. CheckResult.ExitCode() mirrors poll.Result.ExitCode(). Registered in root.go commands() after pollCommand and in schemaRegistry with checkExitCodes(). 8 CLI-level tests cover all behaviors. Docs: added check section to docs/usage.md and TC-CHECK-001 through TC-CHECK-004 to manual-qa.md.
