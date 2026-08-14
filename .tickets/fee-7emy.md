---
id: fee-7emy
status: closed
deps: [fee-2lbg, fee-o5uq, fee-frus]
links: []
created: 2026-08-14T02:49:18Z
type: task
priority: 2
assignee: Andre Silva
parent: fee-zs6b
tags: [cmd, tags]
---
# prune --tag and rm --tag

Lane-scoped prune (narrows an operation, still requires a bound) and bulk rm by lane (a new destructive mode; ref XOR --tag, reports every removed URL). RmResult.removed becomes an array on every path: the feature's one breaking output-contract change, settled, with schema_version deliberately held at 1.

## Design

Add `--tag`/`--match` to `prune` and `rm`. Paired because both are destructive
and both need the same "what does a selector mean here" decision, but note they
differ: `prune` narrows an existing operation, while `rm` gains an entirely new
bulk mode.

Reuse the `tagFilter` helper from the `list` ticket and the store's prune
scoping.

Spec: [docs/specs/003-feed-tags/plan.md](docs/specs/003-feed-tags/plan.md),
section "`prune` and `rm` — lane-scoped".

## `prune --tag`

```go
type PruneRequest struct {
	KeepDays *int     `flag:"keep-days" usage:"tombstone items older than this many days"`
	MaxItems *int     `flag:"max-items" usage:"keep at most this many items per feed, tombstoning the rest"`
	Tags     []string `flag:"tag" usage:"prune only feeds carrying this tag (repeatable); all feeds when omitted"`
	Match    string   `flag:"match" default:"all" usage:"multi-tag semantics: 'all' (default) or 'any'"`
}
```

`--tag` **narrows** a prune; it does not authorize one. A bare
`prune --tag ai` with no `--keep-days` and no `--max-items` stays a usage
error, so `TestPruneRequiresBound` remains valid and must not be weakened. Say
this in the `--tag` usage string.

Validation and resolution both live in `PruneRequest.policy(now)`, which
`Validate` and `App.Prune` share ("state the rules once"). Add the tag
resolution there, populating `core.PrunePolicy.Tags`/`Match`.

`PruneResult` is unchanged: `pruned` already counts tombstoned rows, and
scoping changes the count, not the shape.

## `rm --tag`

This is the only bulk destructive operation the feature adds, so its contract
needs to be explicit.

```go
type RemoveRequest struct {
	Ref   string   `arg:"ref"`
	Tags  []string `flag:"tag" usage:"unsubscribe every feed carrying this tag (repeatable); cannot be combined with a named feed"`
	Match string   `flag:"match" default:"all" usage:"multi-tag semantics: 'all' (default) or 'any'"`
}
```

`Validate` gains two rules, matching the precedent set by `poll`/`check`:

- `Ref` and `Tags` both set is a usage error naming the alternative.
- Neither set is a usage error: an unguarded `rm` with no selector would be
  read by the store as "feed not found" today, but with a tag path present the
  failure mode of a mistyped invocation is unsubscribing nothing or
  everything, so make the requirement explicit rather than emergent.

The result must report what was actually removed, not just a count:

```go
// RmResult is the rm result envelope: the canonical URLs unsubscribed. It is a
// list rather than a single value because --tag removes a lane, and a caller
// that deletes in bulk needs to know exactly what went.
type RmResult struct {
	Head
	Removed []string `json:"removed"`
}
```

**This changes an existing envelope from a string to an array of strings, and
that break is a settled decision — implement it directly.** `RmResult.Removed`
shipped as a string in v0.0.4, so this is the one breaking output-contract
change in the feature; everything else is additive. The alternative that was
considered and rejected is keeping `removed` as the single-ref string and
adding a parallel field for the bulk path, which would leave an agent parsing
two shapes for one command.

`removed` is always an array, including on the single-ref path: `rm REF`
reports `"removed":["https://..."]`, not a bare string. A caller that must
handle both binaries branches on the JSON type once, rather than on which
selector it passed.

**The head's `schema_version` stays 1.** ADR 0005 says the integer is bumped on
breaking shape changes, and this is one, so the deviation is deliberate and
must be recorded rather than left to look like an oversight. The reasoning:
`CHANGELOG.md` already states that while the project is pre-1.0, breaking
changes may land in minor releases, and the changelog is the mechanism agents
are pointed at. Bumping the head would signal a whole-contract generation
change to consumers of all sixteen commands over one field on one command, and
would break every agent pinning `schema_version == 1` for reasons unrelated to
`rm`. Revisit at 1.0, when the head becomes a real compatibility promise.

Consequences this ticket owns:

- Coalesce `Removed` to `[]` in a `MarshalJSON` on `RmResult`, per ADR 0005
  ("collections never serialize as null, enforced by the envelope's own
  MarshalJSON").
- `testdata/schema/rm.stdout` changes structurally, and any behavioral golden
  running `rm` changes shape.
- A `CHANGELOG.md` entry under `### Changed`, in the file's house style: a
  bolded "Breaking:" lede, what moved, what a consuming agent must do
  (`.removed` is now an array; read `.removed[0]` for a single-feed `rm`), and
  the explicit note that `schema_version` stays 1 under the pre-1.0 policy so
  the unchanged head is not mistaken for an unchanged shape. The docs ticket
  owns the prose, but do not leave the break unrecorded if that ticket has not
  landed yet.

`App.Remove` with tags: `ListFeeds(ctx, filter)`, then `RemoveFeed` per feed,
in URL order so the output is deterministic. An empty lane removes nothing and
exits 0 with `"removed":[]` — not an error, matching the "empty lane is not an
error" rule everywhere else.

## TDD plan

Library tests in `prune_test.go` and `rm_test.go` (external, `newTestApp`);
CLI tests in `internal/command/prune_test.go` and `rm_test.go`.

1. **(tracer)** `prune --tag ai --max-items 1` tombstones only in-lane items;
   an out-of-lane feed keeps all of its items.
2. `prune --tag ai --keep-days N` is lane-scoped on the age axis too.
3. `prune --tag ai` alone is still a usage error, exit 64
   (`TestPruneRequiresBound` extended, not replaced).
4. `prune` with no `--tag` is unchanged.
5. `rm --tag ai` unsubscribes every in-lane feed and reports them all; the
   untagged feed survives.
6. `rm --tag ai --match any` versus the default `all` select different sets.
7. `rm REF --tag ai` is a usage error, exit 64, and nothing is removed —
   assert the store still holds every feed.
8. `rm` with neither a ref nor `--tag` is a usage error, exit 64.
9. `rm --tag nosuchlane` removes nothing, exits 0, `"removed":[]`.
10. `rm REF` (no tags) removes exactly that feed and reports
    `"removed":["<url>"]` — the single-ref path now returns a one-element
    array, so assert on the raw stdout bytes to pin the new shape.

Behaviors 7 and 8 must assert on **store state**, not just the exit code: a
destructive command that validates too late is the failure this pair of tests
exists to catch.

## Golden fallout

`testdata/schema/{prune,rm,all}.stdout` and `testdata/help/{prune,rm}.stdout`
change for the new flags, and `schema/rm.stdout` changes **structurally**
because `removed` becomes an array. `testdata/opml/prune.stdout` runs
`prune --max-items 1` with no tag and should be unchanged; verify rather than
assume.

## Gotchas

- The store's max-per-feed prune ranks with a window function; the store ticket
  scoped both its outer and inner statements. Behavior 1 is the test that
  catches a half-scoped implementation, so make its fixture have more items on
  the out-of-lane feed than the cutoff.
- `rm` cascades to items via the `ON DELETE CASCADE` foreign key; a bulk `rm`
  therefore deletes history for every feed in the lane. Note that in the usage
  string.

## Acceptance Criteria

- `PruneRequest` carries `Tags`/`Match`, resolved in `policy(now)`; a bare
  `prune --tag` with no bound is still a usage error.
- Pruning is lane-scoped on both the age and the max-per-feed axes, with
  out-of-lane feeds untouched.
- `RemoveRequest` carries `Tags`/`Match`; a ref and `--tag` together, and
  neither of them, are both usage errors that leave the store untouched.
- `rm --tag` unsubscribes every in-lane feed and reports the removed URLs in
  deterministic order; an empty lane exits 0 having removed nothing.
- `RmResult.Removed` is a `[]string` on every path, including single-ref `rm`,
  coalesced to `[]` by the result's own `MarshalJSON`, with the break recorded
  in `CHANGELOG.md` under `### Changed`.
- `feedwatch.SchemaVersion` is still 1, and the deliberate deviation from ADR
  0005's bump rule is stated in the changelog entry.
- Untagged `prune` and `rm` are unchanged.
- Behaviors 1-10 covered, with 7 and 8 asserting on store state.
- `schema/{prune,rm,all}.stdout` and `help/{prune,rm}.stdout` are regenerated
  and reviewed.
- `make build` passes.

## Notes

**2026-08-14T03:38:06Z**

RmResult envelope break approved by the user (2026-08-14): removed becomes []string on every path, including single-ref rm. schema_version stays 1 - a deliberate deviation from ADR 0005's bump rule under the pre-1.0 policy, to be stated in the CHANGELOG entry rather than left implicit. Revisit at 1.0. Design and acceptance criteria updated; plan.md, the epic (fee-zs6b), and the docs ticket (fee-fxl2) updated to match.

**2026-08-14T20:21:21Z**

Implemented prune --tag/--match and rm --tag/--match.

Library: PruneRequest gains Tags/Match, resolved in policy(now) after the bound check so a bare 'prune --tag' stays a usage error (TestPruneRequiresBound extended, not weakened). RemoveRequest gains Tags/Match plus a shared filter() that rejects ref+tag together and neither-selector; both are validated before resolveStore, so a rejected rm never opens the store. RmResult.Removed is now []string on every path with a MarshalJSON coalescing it to [], and App.Remove removes lane feeds in ListFeeds URL order.

Breaking change recorded in CHANGELOG.md under ### Changed, including the explicit note that schema_version stays 1 (deliberate deviation from ADR 0005, per the pre-1.0 policy; revisit at 1.0). Stale 'removed' examples in docs/usage.md and manual-qa.md were corrected to the array shape so the documented contract is not left contradicting the code; the rest of the tags prose is still fee-fxl2's.

Tests: behaviors 1-10 covered across prune_test.go, rm_test.go, internal/command/prune_test.go and internal/command/rm_test.go. Behaviors 7 and 8 assert on store state (every feed still present), not just exit 64. Goldens regenerated: schema/{prune,rm,all}.stdout, help/{prune,rm,root}.stdout; schema/rm.stdout changed structurally (removed is an array). testdata/opml/prune.stdout verified byte-identical to HEAD, confirming the untagged prune path did not move.

Note for the next person: the working tree carries uncommitted golden updates from earlier tickets in this epic, so 'git diff testdata/' shows more than any one ticket touched. Do not read it against HEAD when reviewing a -update run.

make build passes.
