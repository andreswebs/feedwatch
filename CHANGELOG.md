# Changelog

All notable changes to feedwatch are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
While the project is pre-1.0 (v0.x), breaking changes may land in minor
releases.

## [Unreleased]

### Changed

- **Breaking: the `rm` result's `removed` field is now an array of strings
  instead of a single string.** `rm` gained a bulk mode (`rm --tag ai`
  unsubscribes every feed in a lane), so one command can now remove many feeds
  and a caller that deletes in bulk needs to know exactly which went. The field
  is an array on every path: a single-feed `rm REF` reports
  `{"removed":["https://..."]}`, never a bare string, and an empty lane reports
  `{"removed":[]}`. Agents reading `.removed` as a string must read
  `.removed[0]` for a single-feed `rm`, or iterate the array; branching once on
  the JSON type is enough to support both binaries.

  The envelope head's `schema_version` deliberately stays `1`.
  [ADR 0005](docs/adr/0005-output-contract.md) bumps the integer on a breaking
  shape change, and this is one, but bumping the head would signal a
  whole-contract generation change to consumers of every command over one field
  on one command, breaking agents that pin `schema_version == 1` for reasons
  unrelated to `rm`. While the project is pre-1.0 this changelog is the
  mechanism that records the break; an unchanged head must not be read as an
  unchanged shape. Revisit at 1.0, when the head becomes a real compatibility
  promise.

- **Breaking: `store.Store.DueFeeds` now takes a `core.ListFilter`**, so a
  tagged lane can be polled on its own schedule rather than only under
  `--force`. Only the filter's `Tags` and `Match` are honored; `Status` is
  ignored, because a due feed is active by definition. Embedders implementing
  `store.Store` themselves must update the signature. The same change adds two
  methods to the interface: `SetTags`, which replaces a feed's tag set, and
  `TagCounts`, which reports each tag with the number of subscriptions carrying
  it. `ListFeeds` honors the same tag filter, per
  [ADR 0007](docs/adr/0007-library-and-frontends.md).

- **Breaking: whole-invocation failures now use the BSD `sysexits.h` exit
  codes instead of exit 1**, adopting the taxonomy in
  [ADR 0001](docs/adr/0001-exit-code-taxonomy.md). Exit 1 and the 2-63 range are
  now reserved for result classes; failures live in the 64-78 range. The
  mapping is:

  | Failure                                             | Old | New |
  | --------------------------------------------------- | --- | --- |
  | Usage error (bad arguments, flags, unknown command) | 1   | 64  |
  | Stored schema newer than the binary supports        | 1   | 65  |
  | Store unavailable (could not open or reach)         | 1   | 69  |
  | Configuration error                                 | 1   | 78  |
  | Internal or unclassified failure                    | 1   | 70  |

  Scripts and agents that branched on exit 1 for these failures must be updated.
  A `set -e` script that treated exit 1 as failure will no longer stop on these
  errors unless it also tests for codes 64 and above.

  Unchanged: full success still exits 0; the poll and check result sub-codes
  (2 when every targeted feed failed, 3 on partial failure) and the signal exits
  (130 for `SIGINT`, 143 for `SIGTERM`) keep their meaning and values exactly.
  The `feedwatch schema` output now declares the new failure classes as data.

- **Breaking: the `migrate` result field carrying the store schema version is
  renamed from `schema_version` to `store_schema_version`**, and the `check`
  result field carrying the count of feeds that passed is renamed from `ok` to
  `passed`. Both renames free the `schema_version` and `ok` keys for the new
  envelope head (see Added). Agents reading `.schema_version` from a `migrate`
  result now read `.store_schema_version`; agents reading `.ok` from a `check`
  result now read `.passed`.

- **Breaking: the stderr error object is reshaped to the ADR 0005 envelope and
  the per-feed batch form is removed**, adopting
  [ADR 0005](docs/adr/0005-output-contract.md). A whole-invocation failure now
  renders on stderr as `{"schema_version", "ok": false, "error": {"code",
  "message", "hint"?, "details"?}}` instead of the old
  `{"error": {"category", "feed_url", "status", "message"}}`. `code` is the
  stable machine code from the error registry (for example `usage_error`,
  `http_error`, `feed_unreachable`); an unclassified error renders
  `internal_error`. A feed-scoped failure's `feed_url` and `status` now live
  under `error.details` rather than as siblings of `error`. The per-feed batch
  form `{"errors": [...]}` that a `poll` or `check` wrote to stderr on partial
  or total failure is removed entirely; that detail is not lost, because it
  already appears in the stdout `failures` array (unchanged, still keyed by
  `feed_url`, `category`, `status`, and `message`). Agents that parsed the
  stderr `error.category` now read `error.code`, and agents that scraped the
  stderr `errors` batch read the stdout `failures` array instead.

### Added

- **Feeds can be tagged, and every selecting command can be narrowed to a
  tagged lane.** A lane is a set of feeds sharing a tag, so an agent can poll,
  query, prune, export, or unsubscribe one interest group without splitting
  state across databases; keeping one store is what preserves global
  deduplication, since an item carried by two feeds in two databases would be
  reported as new twice. Two new commands: `tag REF` reads a subscription's tags
  and edits them with `--add`, `--remove`, `--set`, or `--clear`, reporting the
  resulting set plus an `added`/`removed` delta so a caller sees what actually
  changed; `tags` lists the whole vocabulary as `{tag, feeds}` counts, sorted by
  tag and counting feeds of any status. Seven existing commands gain a
  repeatable `--tag` and a `--match` (`all`, the default, or `any`): `list`,
  `poll`, `check`, `items`, `prune`, `rm`, and `export`. `add` gains `--tag` to
  tag at subscribe time, following "omitted preserves, given replaces" on the
  idempotent re-add path, so a routine re-add never silently drops a feed out of
  its lanes. Tags are canonicalized on every write (trimmed, lowercased,
  deduplicated, stored sorted), so `--tag AI` and `--tag ai` name the same lane;
  an empty tag, or one containing a comma or whitespace, is a usage error
  (exit 64), as is `--match` with any other value, `--tag` combined with
  positional feed refs, and conflicting `tag` write flags. No new exit codes:
  tags are a filter, not a new operational mode. Every `FeedView` (`list`,
  `enable`, `disable`, `rm`) gains an additive `tags` array, always present and
  `[]` when empty. Tags round-trip through OPML: `export` writes them
  comma-separated in the `category` attribute, per the OPML 2.0 convention, and
  `import` reads that attribute back, ignoring an empty or unparseable value
  rather than failing the outline. `daemon.Scheduler` gains `WithTags` and
  `WithMatch`, which populate the `PollRequest` it already issues, so one
  long-running process can watch a single lane with no new poll path. See
  [docs/usage.md](docs/usage.md) for the command reference and the per-lane cron
  pattern.

  **The envelope head's `schema_version` stays `1`**, and an unchanged head must
  not be read as an unchanged shape. Every envelope change in this feature is
  additive except `rm`'s `removed` (see Changed), and
  [ADR 0005](docs/adr/0005-output-contract.md) bumps the head integer on a
  breaking shape change. Holding it at 1 is a deliberate, settled deviation, not
  an oversight: the pre-1.0 policy stated at the top of this file already covers
  breaking changes in minor releases, and bumping the head would signal a
  whole-contract generation change to consumers of all sixteen commands over one
  field on one command. Revisit at 1.0. Note also that the **database** schema
  version, reported as `store_schema_version` by `migrate`, does move (1 to 2);
  the two numbers are independent (see Migration).
- **feedwatch is now importable as a Go library**, adopting
  [ADR 0007](docs/adr/0007-library-and-frontends.md). The substance moved out of
  the CLI actions into an application service, `App`, with one method per use
  case over request and result types it owns; the CLI became a thin frontend that
  assembles a request, calls one method, and renders the result. Four packages are
  public: `feedwatch` (`App`, `New`, the options, `Config`, and the request and
  result types), `feedwatch/core` (domain types and the error taxonomy),
  `feedwatch/store` (the `Store` interface an alternative backend implements), and
  `feedwatch/daemon` (an embeddable poll scheduler that calls `App.Poll` on a wake
  cadence). Everything else, including the SQLite, HTTP, and parser adapters, the
  poll orchestrator, and the CLI itself, stays under `internal/`.

  **The CLI contract is unchanged.** Commands, flags, stdout and stderr shapes,
  and exit codes are exactly as before; the same golden tests assert them at the
  same boundary. An embedder gets the CLI's behavior without shelling out: the
  result types marshal to byte-for-byte the JSON the binary prints, and failures
  carry the same `core.Category` from which the CLI derives its exit codes.

  The Go API is pre-1.0 and may change in a minor release, with any such change
  noted here; the JSON output contract remains versioned independently by
  `schema_version`. See [docs/library.md](docs/library.md) for the use-case table,
  the error model, implementing a custom store, and embedding the daemon.
- **`schema` now emits the full tool-level self-description**, adopting
  [ADR 0005](docs/adr/0005-output-contract.md). Bare `feedwatch schema` gains
  `tool`, `version`, a tool-level `exit_codes` array (the sorted union of every
  command's declared codes), and an `errors` inventory of `{code, exit_code,
  hint}` projected from the error registry, so the documented error surface
  cannot drift from the real one. The existing per-command `exit_codes` map,
  derived `output_schema`, and `global_flags` are retained as additive detail.
- **A non-fatal NDJSON warning channel on stderr**, adopting
  [ADR 0005](docs/adr/0005-output-contract.md). Advisories that do not change the
  exit code are written to stderr as one JSON object per line, marked
  `"level": "warning"` (instead of an `ok` field) so a consumer can tell them
  apart from the error envelope. Warnings are contract output, not logs, so
  `--quiet` does not suppress them. The first advisory is `feed_auto_disabled`,
  raised by the `poll` that crosses the consecutive-failure threshold and
  auto-disables a feed, carrying the feed URL and failure count in `details`.
- **Every JSON result on stdout now opens with an envelope head**:
  `schema_version` (the output-contract version, an integer bumped on breaking
  shape changes) and `ok` (a boolean), followed by the command-specific payload,
  adopting [ADR 0005](docs/adr/0005-output-contract.md). Collections in the
  payload never serialize as `null`; an absent list is always `[]`. `--format
  text` output is unchanged and does not carry the head, and `export` still
  emits a bare OPML document rather than a JSON envelope.

### Migration

For an agent or script consuming the JSON contract, the breaking changes above
require these updates:

- Branch on whole-invocation failures by exit code 64-78 (per
  [ADR 0001](docs/adr/0001-exit-code-taxonomy.md)), not exit 1.
- Read a whole-invocation error's machine code from `error.code`, not from the
  old top-level `error.category`; a feed-scoped error's `feed_url` and `status`
  are now under `error.details`.
- Read per-feed poll and check failures from the stdout result envelope's
  `failures` array, not from the removed stderr `{"errors": [...]}` batch object;
  each entry there still carries `feed_url`, `category`, `status`, and `message`.
- Read the store schema version from a `migrate` result's `store_schema_version`,
  not `schema_version` (now the envelope head), and read a `check` result's
  passing-feed count from `passed`, not `ok`.
- Read `rm`'s `removed` as an array on every path, taking `.removed[0]` where a
  single feed is expected.

**Database schema 2.** The tags feature adds a `tags` column to `feeds`,
defaulting to `'[]'`. `feedwatch migrate` applies it automatically and
idempotently, as does any other command on first store use, so there is no
manual upgrade step. Existing feeds land on an empty tag set and behave exactly
as before under every unfiltered invocation; no retagging is required, and
tagging is purely additive when an agent chooses to start. Item deduplication is
untouched: tags live on `feeds`, while the dedup key stays `(feed_url,
dedup_key)`.

There is no schema downgrade path, by design. An older binary run against a
migrated store finds a stored version higher than its highest embedded
migration, refuses to operate, and exits 65 (`EX_DATAERR`) rather than risk
corrupting data written by a future version. Roll the binary forward, not the
database back.
