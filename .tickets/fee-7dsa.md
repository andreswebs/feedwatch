---
id: fee-7dsa
status: open
deps: []
links: []
created: 2026-06-30T22:54:52Z
type: feature
priority: 2
assignee: Andre Silva
parent: fee-bqyy
tags: [beta, cli, opml]
---
# Validated import (--no-validate to skip)

Implements Req 4 of the 002-beta spec
([requirements.md](../docs/specs/002-beta/requirements.md), section 4). Amends
baseline Req 17 (OPML Interoperability).

**Breaking change.** Import validates feeds by default. An import of an outline
with unreachable or non-feed URLs will now report a smaller `added` count and
corresponding per-entry failures, where it previously reported every entry as
added. The prior fast bulk-add is available with `--no-validate`. See
[Appendix A](../docs/specs/002-beta/requirements.md) of the spec.

Today `import` only checks that an `xmlUrl` is syntactically an absolute
http(s) URL; it never fetches. So `added` counts subscriptions created, not
feeds proven usable, and an OPML full of dead or non-feed URLs still reports
them as added (observed in a real session). This change makes a reported
`added` mean the feeds actually resolve and parse, applying the same validation
`add` already performs.

## Design

- By default, `import` fetches and parses each candidate feed before
  subscribing (the same check `add` runs).
- Validation runs concurrently under the configured concurrency limit and uses
  the same transient-failure retry policy as other fetches (it is already built
  into the production fetcher).
- A feed that fails validation is not subscribed and is recorded in the
  per-entry `failed` list with a reason; one failing entry never aborts the
  import.
- `added` counts only successfully validated and subscribed feeds.
- `--no-validate` restores the fast bulk-add: subscribe every syntactically
  valid feed without fetching. The docs state that a successful import then does
  not imply reachability.

```sh
feedwatch import subs.opml
# {"added":40,"skipped":3,"failed":[{"xmlUrl":"https://dead/feed","reason":"could not fetch ..."}]}
feedwatch import --no-validate subs.opml   # fast bulk-add, no reachability check
```

## Acceptance Criteria

- By default, import validates each feed by fetching and parsing it before
  subscribing, applying the same validation as `add`.
- Validation runs concurrently, honoring the configured concurrency limit.
- Validation applies the same transient-failure retry policy as other fetches.
- A feed that fails validation is not subscribed and is recorded among the
  per-entry failures with a reason.
- `added` is the number of feeds successfully validated and subscribed.
- Individual validation failures never abort the whole import.
- `--no-validate` subscribes each syntactically valid feed without fetching.
- Import results describe that a successful import does not imply reachability
  when validation was skipped.

## Implementation Plan

`import` and `add` are both in the `cli` package, so import can reuse `add`'s
exact validation helper, `validateParsesAsFeed(ctx, fetcher, parser, url)`
(`src/internal/cli/add.go`), which fetches the URL and confirms the body parses
as a feed. The production fetcher from `buildFetcher`
(`src/internal/cli/resolve.go`) already carries `WithRetry(cfg.RetryAttempts,
...)`, so the transient retry policy comes for free. Concurrency is confined to
the network step; subscription bookkeeping (dedup and alias assignment) stays
sequential and deterministic.

1. Add the flag. In `src/internal/cli/import.go`, `importCommand` gains:

   ```go
   &cliv3.BoolFlag{Name: "no-validate", Usage: "subscribe without fetching each feed (fast bulk-add; no reachability check)"}
   ```

2. Wire collaborators. In `importAction`, when validating (the default), resolve
   the fetcher and parser from the existing `resolver`:

   ```go
   validate := !cmd.Bool("no-validate")
   var fetcher fetch.Fetcher
   var parser parse.Parser
   if validate {
       if fetcher, err = rs.Fetcher(); err != nil {
           return err
       }
       parser = rs.Parser()
   }
   ```

   Pass `validate`, `fetcher`, `parser`, and `cfg.Concurrency` into
   `importFeeds`.

3. Restructure `importFeeds` into three phases so concurrency is isolated and
   the alias/dedup decisions stay deterministic:

   - Phase 1 (sequential, as today): walk `feeds`, classify each against the
     existing subscriptions and syntax. Already-subscribed URLs increment
     `Skipped`; non-absolute URLs go straight to `Failed`. The remainder are
     `candidates` (preserving outline order).
   - Phase 2 (concurrent, only when `validate`): validate the `candidates`
     concurrently with an `errgroup` whose limit is `cfg.Concurrency`, writing
     pass/fail into position-indexed slots so order is stable and no candidate
     cancels another. Each worker calls `validateParsesAsFeed`; a non-nil result
     is the candidate's failure reason. Crucially, the `g.Go` funcs return
     `nil` even on a validation failure (capturing it in the slot), so one bad
     feed never cancels the group:

     ```go
     g, gctx := errgroup.WithContext(ctx)
     g.SetLimit(concurrency)
     results := make([]error, len(candidates))
     for i, c := range candidates {
         i, c := i, c
         g.Go(func() error {
             results[i] = validateParsesAsFeed(gctx, fetcher, parser, c.XMLURL)
             return nil
         })
     }
     _ = g.Wait()
     ```

   - Phase 3 (sequential): for each candidate in order, if validation was
     skipped or passed, run the existing alias-assignment + `AddFeed` and
     increment `Added`; if it failed, append `ImportFail{XMLURL, Reason}` where
     the reason is the validation error's message. Because phase 3 is
     sequential, the `aliases`/`urls` bookkeeping stays exactly as deterministic
     as today.

4. `--no-validate` simply skips phase 2; phase 3 subscribes every candidate. The
   result shape (`ImportResult{added, skipped, failed}`) is unchanged.

5. Reachability disclosure. The spec asks that results "describe" the
   no-validate caveat; satisfy it in [usage.md](../docs/usage.md) and the
   `import` schema description (the design doc is already updated). Adding a
   machine-readable `validated` boolean to the envelope is intentionally out of
   scope: the AC says "describe", and the smaller change keeps the contract
   stable. Note this trade-off in the learnings entry.

6. Per-host politeness: poll groups same-host fetches with a delay; import
   validation does not require that (the AC asks only for the concurrency limit
   and retry policy). Keep the simple bounded `errgroup` rather than reaching
   into poll's host-grouping, to avoid coupling `import` to the poll
   orchestrator.

## Verification

- Tests (`src/internal/cli`, injecting a `testsupport` fetcher/parser): a
  default import of an outline mixing a good feed, an unreachable URL, and an
  HTML (non-feed) URL adds only the good feed, records the other two in `failed`
  with reasons, and does not abort; `--no-validate` adds all syntactically valid
  entries and performs no fetch (assert with a fetcher that records calls or
  fails if called); already-subscribed URLs still count as `skipped`;
  non-absolute URLs still land in `failed` before any fetch.
- A test that multiple candidates validate concurrently up to the limit (e.g. a
  fetcher that blocks until N concurrent calls are observed), and that a single
  validation failure does not cancel siblings.
- `make build` green; learnings entry under the Req 4 heading in
  [learnings.md](../docs/specs/001-initial-implementation/learnings.md).

## References

- Spec: [requirements.md](../docs/specs/002-beta/requirements.md) section 4 and
  Appendix A.
- Design: [cli-design.md](../docs/cli-design.md), "OPML Interoperability".
- Field notes: [usage-learnings.md](../docs/specs/002-beta/usage-learnings.md),
  "Bulk subscribe via OPML import".
- Reuses: `validateParsesAsFeed` from `add` (`src/internal/cli/add.go`).
