# Changelog

All notable changes to feedwatch are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
While the project is pre-1.0 (v0.x), breaking changes may land in minor
releases.

## [Unreleased]

### Changed

- **Breaking: whole-invocation failures now use the BSD `sysexits.h` exit
  codes instead of exit 1**, adopting the family-wide taxonomy in
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
