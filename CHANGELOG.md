# Changelog

All notable changes to feedwatch are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
While the project is pre-1.0 (v0.x), breaking changes may land in minor
releases.

## [Unreleased]

### Changed

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

- **Every JSON result on stdout now opens with an envelope head**:
  `schema_version` (the output-contract version, an integer bumped on breaking
  shape changes) and `ok` (a boolean), followed by the command-specific payload,
  adopting [ADR 0005](docs/adr/0005-output-contract.md). Collections in the
  payload never serialize as `null`; an absent list is always `[]`. `--format
  text` output is unchanged and does not carry the head, and `export` still
  emits a bare OPML document rather than a JSON envelope.
