---
id: fee-t8ez
status: closed
deps: [fee-gyos]
links: []
created: 2026-06-29T19:28:37Z
type: task
priority: 2
assignee: Andre Silva
parent: fee-171q
tags: [cmd]
---

# disable command

Implement the disable command: resolve a feed and set it disabled so poll skips it; unknown ref is a usage error; re-enabled via enable. Refs: docs/cli-design.md (Commands, Failure Handling); docs/research/urfave-cli.reference.md.

## Design

The `disable` command: manually disable a feed so poll skips it.

```go
&cli.Command{ Name:"disable", ArgsUsage:"URL|ALIAS",
  Arguments: []cli.Argument{ &cli.StringArg{Name:"ref"} }, Action: disableAction }
```

Action resolves the ref and `store.SetStatus(url, FeedDisabled)`. Reports the new
state. Distinct from auto-disable (which the failure lifecycle triggers); a
manually disabled feed is also re-enabled with `enable`.

TDD plan (cmd.Run + temp db):

1. (tracer) `disable` sets status disabled; the feed is excluded from `DueFeeds`.
2. `disable` of an unknown ref -> usage error, exit 1.
3. `disable` then `enable` round-trips status.

Deep-module note: thin command over `store.SetStatus`.

## Acceptance Criteria

- `disable` sets a feed to disabled (skipped by poll); unknown ref -> exit 1;
  round-trips with `enable`.
- Behaviors 1-3 covered.
- Supports Req 15. `make validate` passes.

## Notes

**2026-06-29T23:29:47Z**

Implemented the disable command (internal/cli/disable.go + disable_test.go), registered in root.go commands() after enable. Mirrors enable's structure (DisableResult{Feed: FeedView}, disableStore test-seam helper, openStoreMigrated for production) but is intentionally simpler: it only resolves the ref and calls store.SetStatus(url, core.FeedDisabled), then re-reads and reports. Deliberately does NOT touch the failure lifecycle (count/last_error are left as-is) — manual disable is distinct from auto-disable; only enable resets the lifecycle. 3 behaviors covered against the InMemoryStore double via the runDisable harness: (1) active feed -> disabled + excluded from DueFeeds, (2) unknown ref -> CatUsage exit 1 (GetFeed miss propagates as the usage *FeedError), (3) disable then enable round-trips status to active. make build green. Closing this (the last open child of E5 fee-171q) makes the E5 epic closeable.
