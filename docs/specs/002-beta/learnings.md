# Learnings

Running log of non-obvious problems solved and decisions made during
implementation, newest last. Keyed loosely by ticket.

## fee-e1s2 — poll: failure visibility in the result envelope (Req 1)

- **Pure projection, no new computation.** `poll.Run` already returns
  `(Result, []*core.FeedError, error)`, and `consume` appends exactly one
  `*core.FeedError` per failed outcome, so `len(feedErrs) == result.Failed`. The
  envelope's new `failures` list is just a projection of `feedErrs`
  (`{feed_url, category, status}`); `succeeded` is `result.Polled - result.Failed`.
  Nothing in the `poll` package, store, or fetch layer changed — the work was
  confined to `cli/poll.go`.
- **Empty list, never null.** `failures` is built with
  `make([]PollFailure, 0, len(feedErrs))` so it marshals to `[]` when no feed
  failed. A regression test asserts this on the raw JSON (compacted
  `json.RawMessage` comparison against `[]`), since unmarshalling into a Go slice
  cannot distinguish `[]` from `null`.
- **Status omission is `omitempty` + zero value.** `core.FeedError.Status` is 0
  for non-HTTP categories, and `Status int \`json:"status,omitempty"\``drops the
key for those. No conditional logic needed;`network`/`parse`/`timeout`failures naturally omit`status`.
- **Golden + contract fixtures both move.** The e2e `poll.stdout` goldens
  regenerate with `go test ./internal/e2e -update` (additive: new
  `succeeded`/`failed`/`failures` keys). Separately, the reflection-based
  `TestOutputSchemaContractPreserved` in `cli/schema_test.go` pins poll's
  property/required sets by hand and had to be updated to add the three keys —
  the schema itself regenerates automatically from `PollResult{}` via
  `jsonschema.Reflect`, but that contract guard does not.
- **Text mode untouched.** `PollResult` has no `RenderText`, so `--format text`
  falls to the generic struct dump, which now prints the new counts for free. A
  bespoke renderer was out of scope; the JSON contract is what the spec governs.

## fee-ah78 — items: fetch-time query axis (fetched_at field and --time-field, Req 2)

- **Field exposure was a one-line tag flip, but it rippled into goldens.**
  Changing `Item.FetchedAt` from `json:"-"` to `json:"fetched_at"` makes the
  field appear on every item-bearing envelope (`poll` items and the default
  `items` output), so the e2e `poll`/`items` goldens regenerate. `fetched_at` is
  the wall-clock fetch moment, so it is volatile across runs; the e2e harness
  gained a `reFetchedAt` normalizer (alongside the commit/go ones) that rewrites
  `"fetched_at":"..."` to a stable token before golden comparison.
- **Surfaced a real SQLite/double parity bug.** The in-memory double stamped
  `it.FetchedAt = now` on insert and returned that, but SQLite's `insertItem`
  resolved the fetch time into a local variable without writing it back to the
  returned item. With `fetched_at` hidden this was invisible; exposing it made
  `poll` report `fetched_at:"0001-01-01T00:00:00Z"` (Go zero time) for freshly
  inserted items. Fixed by resolving `FetchedAt` once at the `UpsertItems` entry
  point (a single `now` for the batch, mirroring the double) so the returned
  `newItems` carry the stamped time; `insertItem`/`upsertOne` no longer thread a
  `now` parameter. AC requires `fetched_at` be populated (never null) on every
  stored item, and the poll envelope is the path that exposed the gap. A
  store-level regression test (`TestUpsertItemsResolvesFetchedAt`) pins it.
- **Filter axis vs sort axis are independent.** `--time-field` selects the
  column the `--since`/`--until` window compares (publication coalesce, or
  strictly `fetched_at`); `--order` is unchanged and still picks the sort column.
  In SQLite this is a one-line `axis` switch in `itemFilters`; in the double a
  one-line switch in `matchesItemFilters`. A SQLite-vs-double parity test backs
  both.
- **Discovered stale e1s2 goldens.** The committed e2e `poll` goldens still
  lacked the `succeeded`/`failed`/`failures` keys that `fee-e1s2` added to
  `PollResult`; the suite was only green via the Go test cache. Running
  `go test ./internal/e2e -update` for this ticket also corrected that
  pre-existing drift. Lesson: a ticket that adds envelope fields must regenerate
  e2e goldens, and `make build` can mask the omission through caching.
- **Schema needed no contract-test edit.** Item JSON is `jsonschema:"opaque"`,
  so `fetched_at` does not enter the reflected schema; `--time-field` appears
  automatically from flag introspection. Only `usage.md` prose was updated.

## fee-7dsa — Validated import (--no-validate to skip, Req 4)

- **Reused add's validator verbatim.** `import` and `add` share the `cli`
  package, so import calls `validateParsesAsFeed(ctx, fetcher, parser, url)`
  directly; the production fetcher from `buildFetcher` already carries
  `WithRetry`, so the transient-retry AC came for free with no extra wiring.
- **Three-phase importFeeds isolates concurrency from determinism.** Phase 1
  classifies each outline entry sequentially (dedup against existing subs and an
  OPML-internal `urls` reservation, plus the absolute-http(s) syntax check),
  producing order-preserving candidates. Phase 2 validates candidates
  concurrently with an `errgroup` limited to `cfg.Concurrency`, writing into
  position-indexed slots; each `g.Go` returns `nil` even on a validation failure
  so one bad feed never cancels its siblings. Phase 3 subscribes the survivors
  sequentially, so alias assignment stays deterministic. Reserving the URL in
  phase 1 (not phase 3) preserves the prior behavior that an OPML-internal
  duplicate counts as `skipped`.
- **Alias assignment stays in phase 3, not phase 1.** A feed that fails
  validation must not consume an alias a later valid sibling could use, so the
  `aliases` reservation happens only when a candidate is actually added.
- **`validateCandidates` returns nil under --no-validate.** Callers treat a nil
  error slice as "all valid" without allocating, and importAction skips
  resolving the fetcher/parser entirely, so --no-validate performs no fetch
  (asserted by a recording `FakeFetcher` with zero requests).
- **Schema carries no per-flag description.** `FlagSchema` exposes only
  name/type/default, so the reachability caveat could not live in the
  machine-readable schema. It lives in the `--no-validate` flag `Usage` (shown by
  `--help`) and in usage.md instead; a machine-readable `validated` envelope
  boolean was left out of scope per the ticket, keeping the result shape stable.
- **e2e fan-out.** Existing classification-focused cli/import tests and the
  export round-trip moved to `--no-validate` (they assert OPML parsing/dedup, not
  reachability). `TestImportExportPrune` keeps the default validating path
  against a real local feed server for genuine e2e coverage; the signal test's
  import must use `--no-validate` because its slow feed is deliberately
  unreachable until cancelled and both feeds must be subscribed for the poll to
  have an in-flight target.

## fee-n4p6 — items: field selection ergonomics (feed_url no-op, did-you-mean, Req 5)

- **`feed_url` skipped before the membership check, not added to the valid set.**
  `feed_url` stays absent from `core.ValidItemFields` (it is the always-on
  identity field, not a selectable projection field), so `buildItemQuery` short-
  circuits it with an explicit `if f == "feed_url" { continue }` ahead of the
  `ValidItemFields` lookup. Passing it through in `q.Fields` is harmless:
  `core.ProjectItem` always seeds `feed_url` and has no `case "feed_url"`, and the
  SQLite `projectedColumns` selects it via `alwaysColumns` regardless, so no store
  or core change was needed.
- **Candidate set derived from `ValidItemFields`, not hardcoded.** Added
  `core.ItemFieldNames()` (sorted keys plus `feed_url`) so suggestions pick up new
  fields automatically (e.g. `fetched_at` from Req 2) and never leak map-iteration
  nondeterminism. `nearestField` ranges over it in sorted order and keeps the
  first strictly-closest match, so ties are broken deterministically by the sorted
  candidate order.
- **Suggestion thresholds guard against absurd matches.** A candidate is offered
  only when its Levenshtein distance is `<= 2` _and_ strictly less than
  `len(name)`. The second bound is what makes `--fields ""` (empty) and very short
  garbage names yield no suggestion: distance-to-`id` would otherwise be 2 for an
  empty string.
- **Test assertions decode the stderr error object.** The structured error on
  stderr JSON-escapes the embedded quotes (`did you mean \"title\"?`), so a raw
  substring match on the stderr bytes fails. The `did-you-mean` test unmarshals
  `{"error":{"message":...}}` and asserts on the decoded message instead.

## fee-aag4 — items: honest handling of missing publication dates (Req 3)

- **SQL three-valued logic does the exclusion for free.** Dropping the
  `COALESCE(published_at, fetched_at)` in `itemFilters` and filtering on
  `published_at` directly means a null row makes `published_at >= ?` /
  `<= ?` evaluate to NULL (not true), so dateless items fall out of a
  publication-axis window with no explicit `IS NOT NULL` clause. The fetch axis
  keeps filtering on the always-present `fetched_at`, so it is untouched.
- **SQLite NULL ordering already matches the contract.** SQLite sorts NULL below
  any value, so `published_at DESC` puts dateless items last and `ASC` puts them
  first, exactly as Req 3 demands; no `NULLS LAST/FIRST` was needed. Noted in the
  `itemOrder` comment that the deferred Postgres backend defaults the opposite
  way and will need explicit NULLS ordering behind the `Store` seam.
- **The omitted count is computed where the filter lives, not via a second Store
  method.** `QueryItems` now returns `core.ItemQueryResult{Items, OmittedNoDate}`.
  Factoring `nonDateFilters` out of `itemFilters` lets the row query and a
  `COUNT(*) ... AND published_at IS NULL` count query share the exact same
  feed/contains/tombstone predicates, so they can never drift. The count is taken
  only on the publication axis with an active `--since`/`--until`.
- **In-memory double split into match/count phases.** `matchesItemFilters` became
  `matchesNonDateFilters` + `matchesDateFilter`; `QueryItems` increments
  `OmittedNoDate` for a null-published item inside a publication window before the
  date check. Gotcha: `matchesDateFilter` must early-return true when no
  `--since`/`--until` is set, otherwise a no-filter query wrongly drops every
  dateless item (caught by the round-trip and full-item tests). `coalesce` is kept
  only for `PruneItems`, whose age semantics are out of scope for this ticket.
- **Breaking-change test updates.** `TestItemsSinceUntilWindow` and the SQLite
  null-ordering test asserted the old coalesce behavior and were rewritten to the
  honest contract. `TestOutputSchemaContractPreserved` gains `omitted_no_date` as
  a property (not required, via `omitempty`), which the reflected schema picks up
  automatically.

## fee-9otc — poll: permanent-redirect rename visibility (Req 6)

- **Report what the store did, not what poll intended.** `fee-n6j6` already
  renamed a feed on a 301/308, but the store declines the rename when the
  redirect target is already subscribed (no merge). Deriving the `renamed` entry
  from the intended `finalURL` would falsely claim a rename that never happened.
  The fix threads the _actual_ landing URL back out: `Store.RecordSuccess` now
  returns `(renamedTo string, err error)`, `""` when the URL was unchanged
  (including a declined rename). `consumeSuccess` builds the `core.FeedRename`
  only when `renamedTo != ""`.
- **Signature change rippled through every `RecordSuccess` implementer and
  caller.** The SQLite store, the in-memory double, the `store_test` fakeStore
  mock, the `run_interrupt_test` wrapper, `poll.RecordSuccess` (the lifecycle
  helper), and `cli/enable.go` all needed updating. The compiler drove this;
  non-rename call sites just take `_, err :=`.
- **Envelope list initialized empty, never null.** `PollResult.Renamed` is filled
  with `make([]core.FeedRename, 0, len(...))` + append so it marshals to `[]` when
  empty, matching the existing `failures` contract. Asserted distinctly from
  `null` via `pollEnvelopeHasField(..., "renamed", "[]")`.
- **The info log line rides the default level.** `--log-level` defaults to
  `info`, and a clean poll emits no logs, so the one-line
  `"renamed feeds after permanent redirect"` (with `count`) shows up on stderr at
  the default level; the cli test decodes it with the shared `decodeLogLine`.
- **Schema and goldens regenerate, contract test does not.** `renamed` appears in
  the reflected `poll` schema automatically, but
  `TestOutputSchemaContractPreserved` pins the expected property/required sets by
  hand and had to gain `renamed`. The e2e poll goldens were regenerated with
  `go test ./internal/e2e -update` (they gain `"renamed":[]`).
