---
id: fee-7r0v
status: closed
deps: [fee-gyos]
links: []
created: 2026-06-29T19:28:37Z
type: task
priority: 2
assignee: Andre Silva
parent: fee-5d25
tags: [cmd]
---

# prune command

Implement the prune command: trim stored history by --keep-days and/or --max-items via the store's tombstoning (preserving the dedup fingerprint), reporting the count; pruning is explicit, never automatic. Refs: docs/cli-design.md (Retention); docs/research/urfave-cli.reference.md.

## Design

The `prune` command: trim stored item history while preserving dedup.

```go
&cli.Command{ Name:"prune", Action: pruneAction, Flags: []cli.Flag{
  &cli.IntFlag{Name:"keep-days"}, &cli.IntFlag{Name:"max-items"},
}}
```

Action builds a `core.PrunePolicy` (`KeepBefore = now - keep-days`,
`MaxPerFeed = max-items`) and calls `store.PruneItems`, which tombstones the
matching rows (clears content, retains the `(feed_url, dedup_key)` fingerprint).
Reports the number tombstoned. No automatic pruning; cadence is external.

```go
type PruneResult struct { Pruned int `json:"pruned"` }
```

TDD plan (cmd.Run + temp db seeded with items + fixed clock):

1. (tracer) `prune --keep-days 90` tombstones items older than 90 days; output
   `pruned:N`.
2. `prune --max-items 10` keeps the newest 10 per feed, tombstones the rest.
3. after prune, `items` no longer returns the pruned rows, and a re-poll of a
   pruned key yields no new items (dedup preserved).
4. no flags -> nothing pruned (or a usage error requiring one bound).

Deep-module note: thin command over `store.PruneItems`; the tombstone mechanics
live in the store.

## Acceptance Criteria

- `prune` trims by `--keep-days` and/or `--max-items` via tombstoning (dedup
  fingerprint preserved); reports the count; never automatic.
- Behaviors 1-4 covered.
- Supports Req 16. `make validate` passes.

## Notes

**2026-06-29T23:39:07Z**

Implemented prune command (internal/cli/prune.go + prune_test.go), registered in root commands().

- Thin command over store.PruneItems: builds core.PrunePolicy from --keep-days and --max-items and reports {pruned:N}. Tombstone mechanics live in the store; the dedup fingerprint is preserved (verified end-to-end: a re-upsert of a pruned key yields no new items).
- buildPrunePolicy uses cmd.IsSet() to distinguish a flag set to 0 from an absent flag: --keep-days 0 legitimately means 'prune everything older than now', so 0 cannot be treated as 'unset'. KeepBefore/MaxPerFeed are each applied only when their flag is set.
- Behavior 4 decided as a USAGE ERROR (exit 1, empty stdout), not a silent no-op: a bare 'prune' with no bound is almost certainly a mistake, and failing fast is more agent-friendly than a no-op. Negative --keep-days/--max-items are also usage errors.
- Reused the per-command store-accessor seam (pruneStore mirrors disableStore/itemsStore): injected Deps.Store in tests, openStoreMigrated in production.
- Behaviors 1-4 covered. make build green. This was the last open child of E6 (fee-5d25).
