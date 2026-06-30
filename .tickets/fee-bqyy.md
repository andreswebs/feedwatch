---
id: fee-bqyy
status: open
deps: [fee-e1s2, fee-ah78, fee-aag4, fee-7dsa, fee-n4p6, fee-9otc]
links: []
created: 2026-06-30T22:54:34Z
type: epic
priority: 1
assignee: Andre Silva
tags: [beta]
---
# E10: Beta refinements

Epic for the 002-beta requirement set: six refinements to the feedwatch
command-line contract found while operating the tool against a large
subscription set. Each closes a gap between what an invocation reported and what
a calling agent could rely on. The full specification is in
[requirements.md](../docs/specs/002-beta/requirements.md); the field notes that
motivated it are in
[usage-learnings.md](../docs/specs/002-beta/usage-learnings.md); and the target
design state has already been folded into
[cli-design.md](../docs/cli-design.md).

## Scope

Each requirement is one child ticket:

- `fee-e1s2` Req 1: poll failure visibility in the result envelope.
- `fee-ah78` Req 2: fetch-time query axis (`fetched_at` field and
  `--time-field`).
- `fee-aag4` Req 3: honest handling of missing publication dates (breaking).
- `fee-7dsa` Req 4: validated import (`--no-validate` to skip) (breaking).
- `fee-n4p6` Req 5: field selection ergonomics (`feed_url` no-op, did-you-mean).
- `fee-9otc` Req 6: permanent-redirect rename visibility.

## Breaking changes

Two children change baseline behavior and are flagged in their own bodies and in
[Appendix A](../docs/specs/002-beta/requirements.md) of the spec:

- Req 3 (`fee-aag4`) stops coalescing a null publication time to the fetch time
  on the publication axis. Publication-axis `--since`/`--until` queries no longer
  return dateless items; they remain reachable through the fetch-time axis added
  by Req 2.
- Req 4 (`fee-7dsa`) makes `import` validate feeds by default, so an import of
  unreachable or non-feed URLs reports a smaller `added` count and per-entry
  failures. The prior fast bulk-add is available with `--no-validate`.

## Sequencing

Two short dependency chains reflect real implementation ordering on shared code
(see each child for detail):

- Req 2 (`fee-ah78`) lands the fetch-time axis first; Req 3 (`fee-aag4`) then
  removes coalescing on the publication axis, and Req 5 (`fee-n4p6`) extends the
  same `--fields` validation path. Both depend on Req 2.
- Req 1 (`fee-e1s2`) reshapes the poll result envelope first; Req 6 (`fee-9otc`)
  then adds the `renamed` list to it. Req 6 depends on Req 1.
- Req 4 (`fee-7dsa`) is independent.

## Definition of done

- All six children closed with `make build` green (the full quality gate:
  `fmt-check`, `vet`, `lint`, `test`, compile). See
  [build.md](../docs/build.md).
- `feedwatch schema` and [usage.md](../docs/usage.md) reflect every contract
  change.
- A learnings entry per child under
  [learnings.md](../docs/specs/001-initial-implementation/learnings.md).
