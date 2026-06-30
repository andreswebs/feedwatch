---
id: fee-gyos
status: closed
deps: [fee-63n9, fee-bqne, fee-vlk9, fee-aqkn, fee-c66o]
links: []
created: 2026-06-29T15:36:07Z
type: epic
priority: 1
assignee: Andre Silva
tags: [store]
---
# E2: Persistence

Epic. State of record behind the Store interface: SQLite backend with required pragmas, embedded versioned migrations with a schema-version guard, the migrate command, and the deferred Postgres backend. Covers requirement 6 and the storage part of 16. Refs: docs/cli-design.md (State and Storage, Schema Lifecycle); docs/plan.bak.md (E2).

## Notes

**2026-06-29T22:35:42Z**

Closed as a dependency gate (same pattern as fee-63n9/fee-8cau), no new code. All four children closed and integrate under green make build: SQLite store (fee-bqne), migrations engine + too-new guard (fee-vlk9), migrate command (fee-aqkn), walking skeleton (fee-c66o). Verified requirement 6 end-to-end on a native build: (1) migrate applies 0001 -> {applied:1,schema_version:1}; (2) fresh-db migrate --status -> {schema_version:1,pending:0,backend:sqlite} (apply-on-status per fee-c66o); (3) postgres:// DSN -> {error:{category:config,...}} exit 1 (deferred backend); (4) SSRF/durability pragmas present in sqlite.go: busy_timeout(5000), journal_mode(WAL), foreign_keys(ON), synchronous(NORMAL) never OFF; (5) ErrSchemaTooNew guard in migrate.go. Closing E2 makes the downstream command lanes (fee-4q22 add, fee-55gy rm, fee-on7r list, fee-ydl6 items, fee-7r0v prune, fee-as1j enable, fee-t8ez disable, fee-jf82/fee-nkks OPML, fee-vzu8 failure lifecycle) ready once their other deps resolve.
