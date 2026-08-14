---
id: fee-fxl2
status: closed
deps: [fee-9ajb, fee-7pg3, fee-47yv, fee-frus, fee-5wk4, fee-0fcw, fee-7emy, fee-igmb, fee-93m0]
links: []
created: 2026-08-14T02:51:32Z
type: task
priority: 2
assignee: Andre Silva
parent: fee-zs6b
tags: [docs, tags]
---
# docs, changelog, and manual QA for feed tags

Documentation sweep once the surface is final: usage.md (two new commands, seven modified), README, library.md (including the breaking store.Store contract), CHANGELOG, a new manual-qa.md, learnings, and a tagged OPML QA fixture.

## Design

The documentation sweep, sequenced last because docs written against a moving
surface are worse than no docs. Every command's shape is settled by the time
this starts.

Spec: [docs/specs/003-feed-tags/plan.md](docs/specs/003-feed-tags/plan.md).

## 1. `docs/usage.md`

Structure to respect: `## Synopsis` (with a command table), `## Output
contract`, `## Exit codes`, `## Global flags`, `## Environment variables`,
`## Commands` (one `###` per command), `## Scheduling`.

- **Synopsis command table**: two new rows for `tag` and `tags`, positioned to
  match the order of the `###` sections below and the order in
  `Deps.commands()`.
- **Two new `###` sections** following the established template: prose
  describing behavior, an `Options:` bullet list of `` - `--flag <arg>` -
  description. ``, then a ```sh fence showing the invocation with the JSON
  result as a `#` comment carrying a real `schema_version`/`ok` head. Copy the
  shape of the existing `add` section, which is the canonical
  `Options:`-bearing example.
- **Seven modified sections** (`add`, `list`, `poll`, `check`, `items`,
  `prune`, `import`/`export`): new `--tag`/`--match` bullets, and updated
  example JSON lines. The JSON in those fences is hand-written, so every
  example showing a feed must gain `"tags":[...]`, matching the new
  non-omitempty `FeedView` field. Run the real binary and paste, rather than
  editing by eye.
- **Exit codes**: no new codes, but add the new exit-64 conditions to the
  prose if the section enumerates causes.
- **A new "Lanes" subsection** under `## Scheduling`, or a short block in the
  synopsis, showing the per-lane cron pattern from the plan: one script per
  lane against one database, so global dedup survives.
- `README.md` also carries a command list; check and update it.

## 2. `docs/library.md`

- Add `Tag`, `Tags` rows to the `## Use cases` table
  (`| Use case | Method | Request | Result |`).
- The stated invariant "every request type carries a `Validate() error`
  method" must hold for `TagRequest` and `TagsRequest`.
- **`## Implementing a custom store`**: document the `store.Store` changes —
  the new `SetTags` and `TagCounts` methods and the changed `DueFeeds`
  signature — including their behavioral contracts (canonical tag storage,
  filter semantics, `DueFeeds` ignoring the filter's `Status`). This section is
  the supported extension point, so an embedder implementing a backend needs
  the contract stated here, not just the signature.
- If a row is added to `## Where the examples live`, the example must actually
  exist in `example_test.go` — that table is the enforced "no snippet without
  an example behind it" rule.

## 3. `CHANGELOG.md`

Keep a Changelog format; the file currently has a single `## [Unreleased]`
section with `### Changed`, `### Added`, `### Migration` subsections in that
order. House style: each bullet opens with a bolded lede sentence, links the
governing ADR inline, and explains agent-facing impact.

- `### Added`: the tags feature — the two new commands, `--tag`/`--match` on
  seven commands, the `tags` field on `FeedView`, OPML `category` round-trip,
  daemon lane scoping.
- `### Changed`: **the breaking `store.Store` change** (`DueFeeds` signature,
  two new methods), which the file's own policy binds — "The Go API is pre-1.0
  and may change in a minor release, with any such change noted here." And
  **the breaking `rm` envelope change**: `removed` becomes an array of URLs on
  every path, including single-ref `rm`, because `rm --tag` removes a lane.
  `.removed` consumers read `.removed[0]` for a single feed.
- `### Migration`: `feedwatch migrate` applies schema 0002 automatically on any
  command; existing feeds land on `[]`; no retagging is required; an older
  binary against a migrated store exits 65.

Note explicitly that the **output contract version stays 1**, and why. Every
envelope change in this feature is additive **except** `rm`'s `removed`, and
ADR 0005 says the head integer is bumped on breaking shape changes. Not
bumping it is a deliberate, settled deviation: the pre-1.0 policy already
stated at the top of the changelog covers breaking changes in minor releases,
and bumping the head would signal a whole-contract generation change to
consumers of all sixteen commands over one field on one command. An agent
reading the changelog must not have to infer any of this, and specifically must
not read "head unchanged" as "shapes unchanged". Revisit at 1.0.

## 4. `docs/specs/003-feed-tags/manual-qa.md`

New file, following the structure of
`docs/specs/001-initial-implementation/manual-qa.md`: Executive Summary, Test
Scope, Test Strategy, Test Environment, Entry/Exit Criteria, Risk Assessment,
Test Deliverables, then `## Test Cases` with `### Module: <Name>` groups of
`#### TC-<AREA>-NNN: <title> (P0|P1|P2)` cases using **Preconditions:**,
**Steps:**, **Expected:** bullets, then a Traceability Matrix and an Execution
Report Template.

Modules to cover, at minimum:

- **TAG**: read, add, remove, set, clear, idempotency, delta reporting, the
  mutually-exclusive-flag errors, invalid tag names.
- **TAGS**: vocabulary and counts, including disabled feeds.
- **LANE-SELECT**: `--match all` versus `any` on each of list, poll, check,
  items, prune, rm; the comma versus repeated spelling equivalence; `--tag`
  plus positional refs exiting 64.
- **LANE-SCHED**: `poll --tag` respecting the schedule versus
  `poll --force --tag`; the `skipped` count against the lane.
- **OPML**: export `category`, import round-trip, an untagged export emitting
  no `category`.
- **MIGRATE**: an existing pre-feature store migrating in place; an old binary
  exiting 65.

Environment conventions to restate or reference: `make build`; a throwaway DB
per case via `--db "$(mktemp -d)/fw.db"`; `make qa-server` on
`127.0.0.1:8099`; capture with `2>err.json; echo $?`; "stdout is valid JSON"
means `| jq -e .` exits 0. The fixture server ships an OPML outline at
`cmd/qafixtures/feeds/subs.opml`; a **tagged variant** is needed for the OPML
module and should be added in this ticket.

## 5. Learnings

Append the non-obvious decisions from this epic to `docs/specs/learnings.md`
under a heading for the epic ticket. Candidates worth recording: the two
distinct "schema version" numbers and why only one moved; the first SQL-side
JSON usage in the codebase; why `AddFeed` omits `tags` from its `DO UPDATE
SET`; the `bind` trap on `list` and `export`; and why `--match` rather than the
`--tag` spelling carries AND/OR.

**Note on a stale reference**: `AGENTS.md` and `docs/build.md` point at
`docs/specs/001-initial-implementation/learnings.md`, which does not exist; the
real file is `docs/specs/learnings.md`. Fixing those two links is a one-line
change and belongs in this ticket.

## Verification

Docs are not covered by tests, so the check is manual and must actually be
performed:

1. Every ```sh example in the touched sections is executed against a binary
   from `make build`, and its output pasted rather than hand-written.
2. `markdownlint-cli2 --fix 'docs/**/*.md' 'README.md' 'CHANGELOG.md'` then
   `markdownlint-cli2 'docs/**/*.md' 'README.md' 'CHANGELOG.md'` reports
   `0 error(s)`. Use the project's `.markdownlint.yaml`, not the global one.
3. No document references a local filesystem path or names another project.
4. No em dashes, no emoji.

## Acceptance Criteria

- `docs/usage.md` documents `tag` and `tags` as new `###` sections and adds
  `--tag`/`--match` to the seven modified commands, with every example JSON
  line regenerated from a real binary (including the new `tags` key on feed
  envelopes), plus a per-lane cron pattern.
- `README.md`'s command list is updated.
- `docs/library.md` lists the two new use cases and documents the `store.Store`
  changes and their behavioral contracts under the custom-store section.
- `CHANGELOG.md` records the feature under `### Added`, the breaking
  `store.Store` change under `### Changed`, and the schema-0002 upgrade under
  `### Migration`, stating explicitly that the output contract version stays 1.
- `docs/specs/003-feed-tags/manual-qa.md` exists, follows the 001 plan's
  structure, and covers the TAG, TAGS, LANE-SELECT, LANE-SCHED, OPML, and
  MIGRATE modules with a traceability matrix.
- A tagged OPML fixture is added under `cmd/qafixtures/feeds/`.
- `docs/specs/learnings.md` carries the epic's non-obvious decisions, and the
  stale `learnings.md` links in `AGENTS.md` and `docs/build.md` are corrected.
- Every touched Markdown file passes `markdownlint-cli2` with the project
  config, reporting `0 error(s)`.
- No document references a local filesystem path, another project, an em dash,
  or an emoji.

## Notes

**2026-08-14T20:44:34Z**

Documentation sweep for the feed-tags epic, all deliverables landed.

docs/usage.md: added a '### Lanes' subsection to the synopsis (tag selection syntax, canonicalization, untagged semantics); added 'tag' and 'tags' as new ### sections plus the two missing synopsis rows; also added the 'check' row, which had been missing from that table since check shipped. Added --tag/--match option bullets to add, rm, list, poll, check, items, prune, and export, and a tags round-trip paragraph to import/export. Extended the exit-codes prose with the six lane-related exit-64 conditions. Added a 'One cron entry per lane' subsection under Scheduling with the per-lane script and crontab pattern, plus why one shared DB is required (global dedup).

Every JSON and OPML example in the touched sections was regenerated by running the make-build binary against the qa-server fixtures in one coherent session (add qarss -> tag read/add/remove/set -> add qaatom -> tags -> list -> poll -> check -> items -> prune -> export -> rm) and pasted verbatim. That replay caught two pre-existing staleness bugs: the unfiltered poll fences were missing 'renamed', and migrate showed store_schema_version 1 (now 2).

docs/library.md: Tag/Tags rows in the use-case table; a new '### Selecting a lane' subsection covering the per-request Tags/Match fields and the core.CanonicalTags / ValidateTags / ParseTagMatch helpers; a new '### Tag support in the store contract' subsection under 'Implementing a custom store' documenting DueFeeds' changed signature and the new SetTags/TagCounts, with five behavioral contracts (canonical storage, filter semantics, DueFeeds ignoring Status, TagCounts counting any status, AddFeed omitting tags from DO UPDATE SET); daemon WithTags/WithMatch snippet; ExampleScheduler_lane added to the examples table (it exists in daemon/example_test.go).

CHANGELOG.md: the tags feature under ### Added, with an explicit paragraph on why the output-contract head stays 1 and that an unchanged head is not an unchanged shape. The rm and store.Store breaking entries were already present under ### Changed from earlier tickets. ### Migration gained the schema-2 paragraph (auto-applied, existing feeds land on [], no retagging, no downgrade path, old binary exits 65) and a bullet on reading .removed[0].

README.md: a lanes block in Quickstart listing the seven commands that accept --tag; also fixed the poll example, which was missing the envelope head.

docs/specs/003-feed-tags/manual-qa.md: new, following the 001 structure. 6 modules, 46 cases: TAG (14), TAGS (4), LANE-SELECT (14), LANE-SCHED (6), OPML (7), MIGRATE (5), plus a traceability matrix keyed to plan.md design areas and an execution report template. It is explicitly additive to the 001 plan and reuses its environment conventions rather than restating them.

cmd/qafixtures/feeds/subs-tagged.opml: new fixture, covering four category shapes in one outline (plain, needs-canonicalizing 'AI, Security', empty, absent). Verified the round-trip end to end: it imports to ai/security canonicalized, untagged feeds land on [], and export re-emits no category for them.

Stale links fixed: AGENTS.md (x2) and docs/build.md pointed at docs/specs/001-initial-implementation/learnings.md, which does not exist; the real file is docs/specs/learnings.md. CLAUDE.md is a symlink to AGENTS.md so both names are fixed. Also fixed the Makefile qa-server comment, which pointed at a non-existent docs/manual-qa.md.

docs/specs/learnings.md: a '## fee-zs6b: feed tags and lane filtering (epic)' section with the six cross-ticket decisions (the two schema versions and why only one moved, the first SQL-side JSON usage and what it buys, why AddFeed omits tags from DO UPDATE SET, the bind trap on list and export, why --match rather than the --tag spelling carries AND/OR, and the daemon needing no new poll path), plus a short section for this ticket.

Verification: make build green; markdownlint-cli2 with the project .markdownlint.yaml reports 0 issues across docs/**/*.md, README.md, CHANGELOG.md, and AGENTS.md; no em dashes, emoji, or local filesystem paths in anything touched.

Note for the next person: markdownlint --fix also touched docs/specs/001-initial-implementation/manual-qa.md, but that file's diff (TC-SUB-006 expecting removed as an array) came from an earlier ticket in this epic and was already in the working tree.
