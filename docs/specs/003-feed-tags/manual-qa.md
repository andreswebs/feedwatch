# Test Plan: feedwatch Feed Tags and Lane Filtering

Manual testing plan for the feed-tags feature, derived from
[plan.md](plan.md). It exercises the two new commands, the `--tag`/`--match`
selection added to the existing commands, the OPML `category` round-trip, and
the schema-2 migration. For the behavioral contract behind each case see
[usage.md](../../usage.md), [library.md](../../library.md), and
[cli-design.md](../../cli-design.md).

This plan is additive to the full-surface plan at
[001-initial-implementation/manual-qa.md](../001-initial-implementation/manual-qa.md),
whose environment conventions and fixture-server routes it reuses rather than
restates. Run that plan for the unfiltered surface; run this one for lanes.

## Executive Summary

- **Under test:** the tags feature of the `feedwatch` command-line tool: the
  `tag` and `tags` commands, `--tag`/`--match` on `list`, `poll`, `check`,
  `items`, `prune`, `rm`, and `export`, `add --tag`, OPML `category`
  interoperability, and the database migration to schema 2.
- **Objective:** confirm that a lane is a correct, stable selection over one
  shared store; that tag canonicalization and the mutually-exclusive write flags
  behave exactly as documented; that every lane misuse is a usage error (exit
  64) rather than a silent narrowing; and that untagged feeds and unfiltered
  invocations are entirely unaffected.
- **Key risks:** a filter applied in Go after a full read, which would silently
  break `--limit`/`--offset` on `items`; a re-add dropping a feed out of its
  lanes; `--tag` and positional refs both being honored, quietly polling the
  wrong set; the OPML `category` attribute being written but never read back;
  an in-place migration losing subscriptions.
- **Test type:** manual, black-box, one-shot CLI invocations, asserting on
  stdout JSON, stderr JSON, and exit codes only.

## Test Scope

**In Scope:**

- The new commands `tag` (read, `--add`, `--remove`, `--set`, `--clear`) and
  `tags`.
- `--tag` and `--match` on `list`, `poll`, `check`, `items`, `prune`, `rm`, and
  `export`, and `--tag` on `add`.
- Tag canonicalization (trim, lowercase, deduplicate, sort) and the tag-name
  rejections.
- The `tags` array on every feed view (`list`, `enable`, `disable`).
- The lane-related usage errors, all exit 64.
- The `rm` result's `removed` array on both the single-ref and lane paths.
- OPML `category` export, import, and round-trip, including untagged feeds.
- The database migration from schema 1 to schema 2 in place, and the too-new
  guard for an older binary.
- `daemon` lane scoping only insofar as it is observable through the library
  examples; the daemon has no CLI surface.

**Out of Scope:**

- Everything covered by the full-surface plan and not touched by tags: fetch,
  parse, dedup, SSRF, concurrency, color gating, and the rest of the output
  contract.
- The PostgreSQL backend, whose tag predicate is deferred with the backend
  itself.
- Bulk tagging by predicate, `list --status`, tag hierarchies, and renaming a
  tag across all feeds, all explicitly out of scope in the plan.
- Internal unit and race coverage, owned by `make test` and `make test-race`.

## Test Strategy

**Test Types:** manual functional, negative testing, idempotence checks,
equivalence-partition checks on the two `--tag` spellings, and a migration
upgrade pass.

**Test Approach:**

- Black box: assert only on stdout JSON, stderr JSON, and the exit code. Read
  state back through `list`, `tag`, `tags`, `items`, and `migrate --status`,
  never by inspecting the database.
- Positive and negative in pairs: each selecting command is exercised with a
  lane that matches, a lane that matches nothing, and its documented misuse.
- Equivalence: `--tag a --tag b` and `--tag a,b` must produce byte-identical
  output, since the spelling carries no semantics.
- Non-interference: every case that filters is paired with the same command run
  unfiltered, to confirm the untagged surface is unchanged.
- Delta assertions: `tag` write cases assert on `added` and `removed`, not only
  on the resulting `tags`, since the delta is what a caller branches on.

## Test Environment

- **OS:** macOS (darwin) and Linux. No platform-specific behavior is expected in
  this feature.
- **Binary:** built with `make build`, which produces
  `bin/feedwatch-<os>-<arch>` for the host platform. Alias it for the session so
  cases read naturally:

  ```sh
  make build
  alias feedwatch="$(pwd)/bin/feedwatch-$(go env GOOS)-$(go env GOARCH)"
  ```

- **Store:** a throwaway DB per case, so cases are independent and a fresh
  machine is reproducible:

  ```sh
  export FEEDWATCH_DB="$(mktemp -d)/fw.db"     # or --db per invocation
  ```

  The MIGRATE module deliberately reuses one store across invocations; every
  other case starts from a fresh path.
- **Fixture server:** started from the repository, listening on loopback:

  ```sh
  make qa-server                         # listens on 127.0.0.1:8099
  ```

  Set `FIX=http://127.0.0.1:8099` for the commands below. The routes and the
  bundled feed fixtures are documented in the full-surface plan and are not
  restated here.
- **OPML fixtures:** two outlines ship with the fixture server:
  `${FIX}/feeds/subs.opml`, which carries no `category` attribute, and
  `${FIX}/feeds/subs-tagged.opml`, added for this feature. The tagged variant
  covers four cases in one document: a feed with a plain `category`, a feed
  whose `category` needs canonicalizing (`"AI, Security"`, mixed case and a
  space after the comma), a feed with an empty `category`, and a feed with no
  `category` attribute at all.
- **A pre-feature binary (MIGRATE module only):** TC-MIGRATE-002 needs a binary
  built before this feature, to prove the too-new guard. Build one from the last
  release tag that predates schema 2 into a separate path, and refer to it as
  `feedwatch_old`.
- **JSON tooling:** `jq` available. "stdout is valid JSON" throughout means
  `feedwatch ... | jq -e .` exits 0.

Capture conventions, as in the full-surface plan: redirect stderr with
`2>err.json` and read the exit code with `; echo $?`.

### Baseline subscriptions

Most cases below assume this baseline, which gives one feed per lane shape:
a feed in one lane, a feed in two lanes, and an untagged feed.

```sh
feedwatch add "${FIX}/feeds/rss20.xml"     --alias qarss  --tag ai --tag agents
feedwatch add "${FIX}/feeds/atom.xml"      --alias qaatom --tag ai --tag infra
feedwatch add "${FIX}/feeds/jsonfeed.json" --alias qajson
```

## Entry Criteria

- [ ] `make build` passes (full quality gate green).
- [ ] `feedwatch --version` returns JSON and exit 0.
- [ ] `feedwatch migrate --status` reports `"store_schema_version":2`.
- [ ] Fixture HTTP server reachable on loopback, serving `subs-tagged.opml`.
- [ ] `jq` and a writable temp directory available.

## Exit Criteria

- [ ] All P0 and P1 cases executed.
- [ ] 100% pass on P0 cases; 95%+ overall pass rate.
- [ ] No open P0/P1 defect in tag canonicalization, lane selection, the exit-64
      misuse set, or the migration.
- [ ] Every unfiltered invocation verified to behave exactly as it did before
      the feature, for an untagged store.

## Risk Assessment

| Risk                                                       | Probability | Impact | Mitigation                                                           |
| ---------------------------------------------------------- | ----------- | ------ | -------------------------------------------------------------------- |
| Lane filter applied after paging, corrupting `items` output | L           | H      | Paging-under-filter case (TC-LANE-SELECT-008).                       |
| A re-add silently clears a feed's tags                      | M           | H      | Omitted-preserves and given-replaces cases (TC-TAG-009/010).         |
| `--tag` and positional refs both honored, polling the wrong set | L      | H      | Dedicated exit-64 cases on `poll`, `check`, and `rm`.                |
| `category` written on export but never read on import       | M           | M      | Full round-trip case (TC-OPML-003) asserting equality.               |
| Migration loses or rewrites existing subscriptions          | L           | H      | In-place upgrade case (TC-MIGRATE-001) diffing `list` before/after.  |
| Two tag spellings diverging in semantics                    | M           | M      | Byte-equality case across both spellings (TC-LANE-SELECT-006).       |
| Disabled feeds silently vanishing from the vocabulary       | L           | M      | Disabled-feed counting case (TC-TAGS-003).                           |

## Test Deliverables

- This plan, the test cases below, an execution report (pass/fail per case), and
  bug reports for any failures (`BUG-NNN`, severity and priority assigned).

---

## Test Cases

Conventions: each case is a one-shot invocation. Unless stated, **Preconditions**
includes a fresh `FEEDWATCH_DB` path, the fixture server running, and the
baseline subscriptions above.

### Module: Tag Management (TAG)

#### TC-TAG-001: Read mode reports the stored tags with an empty delta (P0)

- **Steps:** `feedwatch tag qarss`.
- **Expected:** exit 0; stdout is valid JSON carrying `url` (the canonical URL,
  not the alias), `"tags":["agents","ai"]` sorted, and both `"added":[]` and
  `"removed":[]`. No write occurs: a second read returns identical `tags`.

#### TC-TAG-002: `--add` adds and reports only the delta (P0)

- **Steps:** `feedwatch tag qarss --add research`.
- **Expected:** exit 0; `tags` is `["agents","ai","research"]`,
  `"added":["research"]`, `"removed":[]`. The delta names only what changed, not
  what was requested.

#### TC-TAG-003: `--add` is idempotent (P0)

- **Steps:** run `feedwatch tag qarss --add ai` twice.
- **Expected:** both exit 0; `tags` is unchanged and contains one `ai`, and both
  runs report `"added":[]` because nothing changed.

#### TC-TAG-004: `--remove` removes, and removing an absent tag is a no-op (P0)

- **Steps:**
  1. `feedwatch tag qarss --remove agents`
  2. `feedwatch tag qarss --remove ghost`
- **Expected:** the first exits 0 with `"removed":["agents"]`; the second exits
  **0** (not an error) with `"removed":[]` and unchanged `tags`.

#### TC-TAG-005: `--add` and `--remove` compose in one invocation (P1)

- **Steps:** `feedwatch tag qarss --add research --remove agents`.
- **Expected:** exit 0; `"added":["research"]`, `"removed":["agents"]`, and
  `tags` reflects both.

#### TC-TAG-006: `--set` replaces the whole set (P0)

- **Steps:** `feedwatch tag qarss --set ai,agents,research`.
- **Expected:** exit 0; `tags` is exactly `["agents","ai","research"]` sorted,
  and the delta names only the tags that were not already present.

#### TC-TAG-007: `--clear` removes every tag (P0)

- **Steps:** `feedwatch tag qarss --clear`, then `feedwatch tag qarss`.
- **Expected:** the clear exits 0 with `"tags":[]` and `removed` listing every
  tag the feed had; the read confirms `"tags":[]`, serialized as an empty array
  and never as `null`.

#### TC-TAG-008: Canonicalization on write (P0)

- **Steps:**
  1. `feedwatch tag qarss --set AI`
  2. `feedwatch tag qarss --add Agents --add agents`
  3. `feedwatch list --tag ai`
- **Expected:** stored tags are lowercased, deduplicated, and sorted, so step 2
  stores one `agents`, and step 3 finds the feed by the lowercase spelling of a
  tag written in mixed case. `AI` and `ai` name one lane.

#### TC-TAG-009: A re-add without `--tag` preserves the lanes (P0)

- **Steps:** `feedwatch add "${FIX}/feeds/rss20.xml" --alias qarss` (no `--tag`),
  then `feedwatch tag qarss`.
- **Expected:** the add exits 0 with `"created":false`; the feed's tags are
  unchanged. A routine re-add never drops a feed out of its lanes.

#### TC-TAG-010: A re-add with `--tag` replaces the lanes (P0)

- **Steps:** `feedwatch add "${FIX}/feeds/rss20.xml" --tag research`, then
  `feedwatch tag qarss`.
- **Expected:** exit 0; `tags` is exactly `["research"]`. A given `--tag` is
  declarative and replaces the whole set.

#### TC-TAG-011: Mutually exclusive write flags are usage errors (P0)

- **Steps:** each of the following, capturing stderr and the exit code:
  1. `feedwatch tag qarss --add x --set y`
  2. `feedwatch tag qarss --clear --add x`
  3. `feedwatch tag qarss --clear --set y`
- **Expected:** every one exits **64**; stdout is empty; stderr carries one
  error envelope with `"code":"usage_error"` and a message naming the conflict.
  No tag change is applied: a following `feedwatch tag qarss` shows the prior
  set.

#### TC-TAG-012: Invalid tag names are usage errors (P0)

- **Steps:** each of the following:
  1. `feedwatch tag qarss --add ""`
  2. `feedwatch tag qarss --add " machine learning "`
  3. `feedwatch list --tag ""`
- **Expected:** every one exits **64** with `"code":"usage_error"` and a message
  naming the offending value. Whitespace is rejected before trimming, so a
  padded but otherwise valid tag is still rejected rather than silently
  accepted.

#### TC-TAG-013: A comma is consumed by the flag parser, not stored (P1)

- **Steps:** `feedwatch tag qarss --add a,b`, then `feedwatch tag qarss`.
- **Expected:** exit 0 with **two** tags added, `a` and `b`: the `[]string` flag
  splits on the comma before validation sees it, which is exactly why a comma
  inside a tag name is illegal. No stored tag ever contains a comma, so the
  OPML `category` encoding stays unambiguous.

#### TC-TAG-014: An unknown feed reference is a usage error (P1)

- **Steps:** `feedwatch tag nosuchfeed 2>err.json; echo $?`.
- **Expected:** exit **64**; stdout empty; stderr's error envelope carries
  `"code":"usage_error"` and `details.feed_url`.

### Module: Tag Vocabulary (TAGS)

#### TC-TAGS-001: `tags` reports every tag with its feed count (P0)

- **Steps:** `feedwatch tags`.
- **Expected:** exit 0; stdout is valid JSON with a `tags` array of
  `{tag, feeds}` objects sorted by tag name. Against the baseline: `agents` 1,
  `ai` 2, `infra` 1. The untagged feed contributes to no entry.

#### TC-TAGS-002: An empty vocabulary is an empty array (P1)

- **Preconditions:** a fresh store with one untagged subscription.
- **Steps:** `feedwatch tags`.
- **Expected:** exit 0 with `"tags":[]`, never `null` and never an error.

#### TC-TAGS-003: Disabled feeds still count (P1)

- **Steps:** `feedwatch disable qaatom`, then `feedwatch tags`.
- **Expected:** exit 0; the counts are unchanged from TC-TAGS-001. The
  vocabulary is a property of the subscriptions, not of what `poll` would
  select.

#### TC-TAGS-004: `tags` takes no flags (P2)

- **Steps:** `feedwatch tags --tag ai 2>err.json; echo $?`.
- **Expected:** exit **64** with a usage error; `feedwatch tags --help` lists no
  command-specific options beyond `--help`.

### Module: Lane Selection (LANE-SELECT)

#### TC-LANE-SELECT-001: `--match all` is the default and intersects (P0)

- **Steps:** `feedwatch list --tag ai --tag infra`.
- **Expected:** exit 0; only `qaatom` is listed, the one feed carrying both
  tags. Running the same command with an explicit `--match all` produces
  identical output.

#### TC-LANE-SELECT-002: `--match any` unions (P0)

- **Steps:** `feedwatch list --tag agents --tag infra --match any`.
- **Expected:** exit 0; both `qarss` and `qaatom` are listed.

#### TC-LANE-SELECT-003: Every feed view carries `tags` (P0)

- **Steps:** `feedwatch list`, `feedwatch disable qajson`,
  `feedwatch enable qajson`.
- **Expected:** exit 0 for each; every feed object carries a `tags` array. The
  untagged feed reports `"tags":[]`, never `null` and never an absent key.

#### TC-LANE-SELECT-004: A lane matching nothing is an empty result, not an error (P0)

- **Steps:** `feedwatch list --tag nosuchlane; echo $?`.
- **Expected:** exit **0** with `"feeds":[]`. Repeat for `items --tag`,
  `check --tag`, `poll --tag`, `prune --tag ... --keep-days 1`, `rm --tag`, and
  `export --tag`: each completes with an empty result and exit 0.

#### TC-LANE-SELECT-005: Untagged feeds are excluded from any lane, included unfiltered (P0)

- **Steps:** compare `feedwatch list` with
  `feedwatch list --tag ai --tag agents --tag infra --match any`.
- **Expected:** the unfiltered listing includes `qajson`; the filtered one does
  not, no matter which tags are named.

#### TC-LANE-SELECT-006: The two `--tag` spellings are identical (P0)

- **Steps:**

  ```sh
  feedwatch list --tag ai --tag infra > a.json
  feedwatch list --tag ai,infra       > b.json
  diff a.json b.json
  ```

- **Expected:** byte-identical output. The spelling carries no AND/OR meaning;
  `--match` is the only carrier. Repeat with `--match any` on both.

#### TC-LANE-SELECT-007: `--match` rejects any other value (P0)

- **Steps:** `feedwatch list --match sometimes 2>err.json; echo $?`.
- **Expected:** exit **64**; stderr's error envelope names the valid values
  `all` and `any`. Repeat on `poll`, `check`, `items`, `prune`, `rm`, and
  `export`: each rejects identically, since the check lives on the request type.

#### TC-LANE-SELECT-008: Paging is applied after the lane filter on `items` (P0)

- **Preconditions:** poll the baseline so every feed has stored items.
- **Steps:**

  ```sh
  feedwatch items --tag ai --limit 2 --offset 0 --order published desc
  feedwatch items --tag ai --limit 2 --offset 2 --order published desc
  ```

- **Expected:** exit 0; each page holds at most 2 items, every item comes from a
  feed in the lane, the two pages do not overlap, and their union matches
  `feedwatch items --tag ai --limit 0`. A filter applied after paging would
  return short or empty pages instead.

#### TC-LANE-SELECT-009: `--tag` narrows `rm`, and `removed` is always an array (P0)

- **Steps:**
  1. `feedwatch rm qajson`
  2. `feedwatch rm --tag infra`
  3. `feedwatch rm --tag nosuchlane`
- **Expected:** all exit 0. The single-ref `rm` reports
  `{"removed":["<canonical url>"]}`, an array of one and never a bare string;
  the lane `rm` reports every URL it unsubscribed; the empty lane reports
  `"removed":[]`. `feedwatch list` afterwards confirms the removals, and
  `feedwatch items` confirms the removed feeds' items are gone.

#### TC-LANE-SELECT-010: `--tag` with positional refs is a usage error (P0)

- **Steps:** each of the following, capturing stderr and the exit code:
  1. `feedwatch poll --tag ai qarss`
  2. `feedwatch check --tag ai qarss`
  3. `feedwatch rm --tag ai qarss`
- **Expected:** every one exits **64**; stdout empty; stderr names the conflict.
  Neither side is silently ignored: naming feeds and naming a lane are two
  different selections. Confirm no work was done (no items stored, nothing
  unsubscribed).

#### TC-LANE-SELECT-011: `rm` with neither a ref nor `--tag` is a usage error (P1)

- **Steps:** `feedwatch rm 2>err.json; echo $?`.
- **Expected:** exit **64** with a usage error naming both accepted forms.

#### TC-LANE-SELECT-012: `--tag` narrows a prune but does not authorize one (P0)

- **Steps:**
  1. `feedwatch prune --tag ai; echo $?`
  2. `feedwatch prune --tag ai --keep-days 30; echo $?`
- **Expected:** the first exits **64**, since a prune still requires
  `--keep-days` and/or `--max-items`. The second exits 0 and reports a `pruned`
  count; `feedwatch items` confirms only the lane's history was trimmed and the
  untagged feed's items survive.

#### TC-LANE-SELECT-013: `export --tag` emits one lane (P1)

- **Steps:** `feedwatch export --tag infra`.
- **Expected:** exit 0; the OPML body carries exactly the lane's feeds, and each
  `<outline>` carries the feed's full tag set in `category`, not only the tag
  that was filtered on.

#### TC-LANE-SELECT-014: Unfiltered invocations are unchanged (P0)

- **Preconditions:** a store whose feeds are all untagged.
- **Steps:** run `list`, `poll`, `check`, `items`, `prune --keep-days 1`, and
  `export` with no `--tag`.
- **Expected:** every result matches the pre-feature behavior, with the single
  additive difference that feed views carry `"tags":[]`.

### Module: Lane Scheduling (LANE-SCHED)

#### TC-LANE-SCHED-001: `poll --tag` honors the schedule (P0)

- **Steps:**
  1. `feedwatch poll --tag ai` on a fresh store
  2. `feedwatch poll --tag ai` immediately again
- **Expected:** the first exits 0, polls the lane's feeds, and reports its new
  items. The second exits 0 with `"polled":0` and `"new_items":0`: a lane runs
  on its own cadence rather than only under `--force`.

#### TC-LANE-SCHED-002: `skipped` counts against the lane, not the whole store (P0)

- **Steps:** with the baseline polled, run `feedwatch poll --tag ai`.
- **Expected:** exit 0; `skipped` equals the number of not-due feeds **in the
  lane** (2 for the baseline), not the number in the store. The untagged feed is
  neither polled nor counted as skipped.

#### TC-LANE-SCHED-003: `--force --tag` polls every active feed in the lane (P0)

- **Steps:** immediately after TC-LANE-SCHED-001, run
  `feedwatch poll --force --tag ai`.
- **Expected:** exit 0; `polled` equals the lane's active feed count and
  `skipped` is 0. `new_items` is 0 on a second forced run, since dedup is
  unaffected by the lane.

#### TC-LANE-SCHED-004: Disabled feeds are skipped by a lane poll (P1)

- **Steps:** `feedwatch disable qaatom`, then `feedwatch poll --force --tag ai`.
- **Expected:** exit 0; the disabled feed is not polled, though it still appears
  in `feedwatch list --tag ai` and still counts in `feedwatch tags`. The status
  filter applies before the tag filter.

#### TC-LANE-SCHED-005: A lane poll is deduplicated against the shared store (P0)

- **Preconditions:** one feed carrying two tags, `ai` and `infra`.
- **Steps:** `feedwatch poll --force --tag ai`, then
  `feedwatch poll --force --tag infra`.
- **Expected:** the first reports the feed's items as new; the second exits 0
  with `"new_items":0`. Lanes overlap without re-emitting items, which is the
  property that one shared store buys.

#### TC-LANE-SCHED-006: `check --tag` mirrors `poll --tag` selection (P1)

- **Steps:** `feedwatch check --tag ai`.
- **Expected:** exit 0; `checked` equals the lane's active feed count.
  `check` writes no state, so repeating it gives identical counts. A lane where
  every feed fails exits 2, and a mixed lane exits 3, matching `poll`.

### Module: OPML Interoperability (OPML)

#### TC-OPML-001: `export` writes tags as `category` (P0)

- **Steps:** `feedwatch export`.
- **Expected:** exit 0; each tagged feed's `<outline>` carries
  `category="<tags comma-separated>"` in stored (lowercased, sorted) order. The
  document is valid OPML 2.0.

#### TC-OPML-002: An untagged feed emits no `category` (P0)

- **Steps:** `feedwatch export | grep jsonfeed`.
- **Expected:** the untagged feed's `<outline>` carries **no** `category`
  attribute at all, rather than an empty one.

#### TC-OPML-003: Export and import round-trip tags (P0)

- **Steps:**

  ```sh
  feedwatch export -o round.opml
  SECOND="$(mktemp -d)/fw.db"
  feedwatch --db "${SECOND}" import --no-validate round.opml
  feedwatch --db "${SECOND}" list
  feedwatch --db "${SECOND}" export -o round2.opml
  diff round.opml round2.opml
  ```

- **Expected:** every feed's tags in the second store equal the original's, and
  `round2.opml` matches `round.opml` in its `xmlUrl` and `category` attributes.
  Nothing an export writes is unread on import.

#### TC-OPML-004: `import` canonicalizes `category` entries (P0)

- **Steps:** `feedwatch import --no-validate` on
  `${FIX}/feeds/subs-tagged.opml`, then `feedwatch list`.
- **Expected:** exit 0. The feed whose `category` is `"AI, Security"` lands on
  `["ai","security"]`: entries are split on the comma, trimmed, lowercased, and
  sorted by the same rules as `--tag`.

#### TC-OPML-005: An empty or missing `category` is ignored, not an error (P0)

- **Steps:** the same import as TC-OPML-004.
- **Expected:** exit 0 with all four outlines added and `"failed":[]`. The
  outline with `category=""` and the outline with no `category` both land on
  `"tags":[]`; neither fails its entry nor aborts the outline.

#### TC-OPML-006: An untagged outline imports as before (P1)

- **Steps:** `feedwatch import --no-validate` on `${FIX}/feeds/subs.opml`.
- **Expected:** exit 0; identical results to the pre-feature behavior, with
  every feed on `"tags":[]`. Nested folders are still walked recursively, and
  the folder name is not adopted as a tag.

#### TC-OPML-007: `import` still reports per-entry results (P2)

- **Steps:** `feedwatch import` (validating) on an outline mixing reachable and
  unreachable feeds with `category` attributes.
- **Expected:** exit 0; `added`, `skipped`, and `failed` behave exactly as
  documented. A feed that fails validation is not subscribed, so it acquires no
  tags either.

### Module: Migration (MIGRATE)

#### TC-MIGRATE-001: A pre-feature store migrates in place (P0)

- **Preconditions:** a store created by a pre-feature binary, holding several
  subscriptions and polled item history. Capture `feedwatch_old list` and
  `feedwatch_old items --limit 0` output first.
- **Steps:**

  ```sh
  feedwatch migrate --status      # before
  feedwatch migrate
  feedwatch migrate --status      # after
  feedwatch list
  feedwatch items --limit 0
  ```

- **Expected:** the before status reports `"store_schema_version":1` with
  `"pending":1`; `migrate` exits 0 reporting the applied count and
  `"store_schema_version":2`; the after status reports `"pending":0`. Every
  subscription and every stored item survives, and each feed now carries
  `"tags":[]`. No retagging is required and nothing is silently dropped.

#### TC-MIGRATE-002: An older binary refuses a migrated store (P0)

- **Preconditions:** the store from TC-MIGRATE-001, already at schema 2.
- **Steps:** `feedwatch_old list 2>err.json; echo $?`.
- **Expected:** exit **65** (`EX_DATAERR`); stdout empty; stderr carries one
  error envelope with `"code":"schema_too_new"`. The older binary refuses to
  operate rather than writing against a schema it does not understand. The store
  is unmodified: the current binary still reads it cleanly afterwards.

#### TC-MIGRATE-003: Migration is idempotent (P0)

- **Steps:** run `feedwatch migrate` twice in a row on the same store.
- **Expected:** both exit 0; the second reports `"applied":0` and the same
  `"store_schema_version":2`. `migrate --status` reports `"pending":0`.

#### TC-MIGRATE-004: Any command applies pending migrations (P1)

- **Preconditions:** a schema-1 store, with `migrate` never run explicitly.
- **Steps:** `feedwatch list`, then `feedwatch migrate --status`.
- **Expected:** the `list` exits 0 and reports `"tags":[]` per feed; the status
  reports `"store_schema_version":2` and `"pending":0`. A fresh machine needs no
  manual upgrade step.

#### TC-MIGRATE-005: The output contract version does not move (P0)

- **Steps:** run `feedwatch list`, `feedwatch tag qarss`, `feedwatch tags`, and
  `feedwatch migrate --status` against a migrated store.
- **Expected:** every envelope head carries `"schema_version":1`. The database
  schema version, reported separately as `store_schema_version`, is 2. The two
  numbers are independent, and an unchanged head must not be read as an
  unchanged shape: `rm`'s `removed` did change (TC-LANE-SELECT-009).

---

## Traceability Matrix

| Design area (see [plan.md](plan.md))       | Test Cases                                      |
| ------------------------------------------- | ----------------------------------------------- |
| Schema migration, two version numbers       | TC-MIGRATE-001..005                             |
| Tag names and canonicalization              | TC-TAG-008, TC-TAG-012, TC-TAG-013, TC-OPML-004 |
| Tag selection syntax (`--tag`, `--match`)   | TC-LANE-SELECT-001..002, 006..007               |
| Where filtering happens (store-side)        | TC-LANE-SELECT-008                              |
| `add` tag at creation time                  | TC-TAG-009..010                                 |
| `tag` command                               | TC-TAG-001..007, TC-TAG-011, TC-TAG-014         |
| `tags` command                              | TC-TAGS-001..004                                |
| `list` filter and the `tags` feed view      | TC-LANE-SELECT-001..006, TC-LANE-SELECT-014     |
| `poll` lane selection and scheduling        | TC-LANE-SCHED-001..005, TC-LANE-SELECT-010      |
| `check` lane selection                      | TC-LANE-SCHED-006, TC-LANE-SELECT-010           |
| `items` lane selection                      | TC-LANE-SELECT-004, TC-LANE-SELECT-008          |
| `prune` lane selection                      | TC-LANE-SELECT-012                              |
| `rm` lane selection and the `removed` array | TC-LANE-SELECT-009..011                         |
| `export` per lane                           | TC-LANE-SELECT-013, TC-OPML-001..002            |
| `import` tag round-trip                     | TC-OPML-003..007                                |
| Untagged feeds                              | TC-LANE-SELECT-005, TC-LANE-SELECT-014          |
| Exit codes (no new codes)                   | TC-TAG-011..012, TC-TAGS-004, TC-LANE-SELECT-007, 010..012, TC-MIGRATE-002 |

## Execution Report Template

Record one row per executed case.

| Test Case   | Result (Pass/Fail/Blocked) | Exit Code | Defect ID | Notes |
| ----------- | -------------------------- | --------- | --------- | ----- |
| TC-TAG-001  |                            |           |           |       |
