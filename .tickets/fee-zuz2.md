---
id: fee-zuz2
status: closed
deps: [fee-63n9, fee-chr5]
links: []
created: 2026-06-29T19:33:04Z
type: task
priority: 1
assignee: Andre Silva
parent: fee-299l
tags: [test]
---

# Test harness (store double, http mock, fixtures, fake clock)

Build the shared test harness: an in-memory Store double, programmable Fetcher/Parser doubles, a fixed Clock, httptest helpers with conditional-GET assertions, and a fixture corpus of valid/malformed/encoded feeds. Refs: docs/cli-design.md (Architecture); docs/plan.bak.md (E9).

## Design

The shared test harness used across packages. Package `src/internal/testsupport`
(test-only helpers) plus a fixture corpus.

- `InMemoryStore`: a `store.Store` double backed by maps for fast unit tests of
  commands that do not need SQL semantics (the SQLite store has its own tests).
- `FakeFetcher`/`FakeParser`: programmable `fetch.Fetcher`/`parse.Parser` doubles
  returning canned `core.FetchResult`/`parse.ParsedFeed` keyed by URL.
- `FixedClock(t)`: a `core.Clock` returning a constant time for deterministic
  backoff/due/date tests.
- `httptest` helpers: spin a server with registered endpoints, validator
  assertions (assert `If-None-Match` carried; return 304), and hit counters
  (mirrors the newsboat conditional-GET harness pattern).
- A fixture corpus under `testdata/`: valid RSS/Atom/JSON Feed, plus malformed
  and oddly-encoded documents (bad dates, broken XML, ISO-8859-1, UTF-16 BOM).

TDD plan (the harness is itself exercised by a couple of self-tests):

1. (tracer) `InMemoryStore` satisfies `store.Store` (compile-time) and
   round-trips a feed + items.
2. `FakeFetcher` returns the canned result for a registered URL and an error for
   others.
3. `FixedClock` returns the same instant on repeated calls.
4. the httptest helper asserts a conditional-GET request and returns 304.

Deep-module note: this consolidates the doubles seeded by the interface keystone;
it has no production dependents (test-only).

## Acceptance Criteria

- Provides `InMemoryStore`, `FakeFetcher`, `FakeParser`, `FixedClock`, httptest
  helpers, and a fixture corpus (valid/malformed/encoded feeds).
- Behaviors 1-4 (self-tests) pass; doubles satisfy their interfaces.
- Supports Req 20 (determinism, fixtures). `make validate` passes.

## Notes

**2026-06-29T21:42:50Z**

Implemented src/internal/testsupport: InMemoryStore (store.Store double mirroring SQLite semantics — alias resolution, GetFeed not-found as CatUsage, conditional-GET validator skip, dedup-and-upsert with tombstones, DueFeeds status+schedule, QueryItems since/until/contains/order/limit-offset/--fields projection, prune by age and max-per-feed preserving dedup fingerprints), FakeFetcher (canned core.FetchResult keyed by URL, network error otherwise, records requests), FakeParser (canned parse.ParsedFeed keyed by baseURL, parse error otherwise), FixedClock(t) core.Clock, and FeedServer httptest helper (conditional-GET 304 on matching If-None-Match/If-Modified-Since, per-path hit counters, LastRequest header capture). Fixture corpus under testdata/: valid_rss/valid_atom/valid_jsonfeed, malformed.xml (raw & breaks strict XML, gofeed recovers), bad_dates.xml, iso8859-1.xml (real 0xE9 latin1 bytes via iconv), utf16-bom.xml (FF FE BOM + UTF-16LE). Fixture(t,name) accessor via embed.FS. All four self-test behaviors covered plus store-behavior tests; 19 tests, race-clean. Notes for next: doubles take sync.Mutex so they are concurrency-safe for poll-pool tests; FakeParser/FakeFetcher key on the same URL so they compose (fetch URL == parse baseURL); contains-filter is case-insensitive to mirror SQLite LIKE; encoded fixtures are raw bytes for the charset decoder (fee-fp2h), not yet UTF-8-decoded by parse.
