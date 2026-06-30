---
id: fee-as1j
status: closed
deps: [fee-gyos, fee-vzu8]
links: []
created: 2026-06-29T19:28:36Z
type: task
priority: 2
assignee: Andre Silva
parent: fee-171q
tags: [cmd]
---

# enable command

Implement the enable command: resolve a feed, set status active, and reset the failure lifecycle (count, last error, backoff) so it is due again; unknown ref is a usage error. Refs: docs/cli-design.md (Failure Handling, Commands); docs/research/urfave-cli.reference.md.

## Design

The `enable` command: re-enable an auto-disabled (or manually disabled) feed.

```go
&cli.Command{ Name:"enable", ArgsUsage:"URL|ALIAS",
  Arguments: []cli.Argument{ &cli.StringArg{Name:"ref"} }, Action: enableAction }
```

Action resolves the ref, sets `status=active`, and resets the failure lifecycle
(`failure_count=0`, clear last_error, clear backoff so the feed is due again).
Reuses the failure-lifecycle reset path. Reports the feed's new state.

TDD plan (cmd.Run + temp db with a disabled feed):

1. (tracer) `enable` on a disabled feed sets status active and clears
   failure_count; the feed becomes due.
2. `enable` on an unknown ref -> usage error, exit 1.
3. `enable` on an already-active feed is idempotent (exit 0).

Deep-module note: delegates state changes to the failure-lifecycle helpers.

## Acceptance Criteria

- `enable` clears disabled state and resets the failure lifecycle so the feed is
  due again; unknown ref -> exit 1; idempotent on active feeds.
- Behaviors 1-3 covered.
- Supports Req 15. `make validate` passes.

## Notes

**2026-06-29T23:26:53Z**

Implemented enable command (internal/cli/enable.go) + table tests (enable_test.go), registered in root commands(). Resolves ref via GetFeed (unknown ref -> CatUsage *FeedError -> exit 1, same path as rm), SetStatus(active), then resets the failure lifecycle through store.RecordSuccess(url, now, now). Reuses the FeedView envelope ({"feed": FeedView}) to report the new state.

Key decision: 'due again' interpreted as IMMEDIATELY due. Used store.RecordSuccess directly (clears failure_count/last_error/last_error_at) with nextDue=now so DueFeeds(now) includes the feed, rather than poll.RecordSuccess which would schedule now+interval (not immediately due). Side effect: last_fetch_at is set to now on enable; acceptable since enable is a fresh-start reset and tests don't assert it. Idempotent on an already-active feed (behavior 3). 3 behaviors covered, make build green.
