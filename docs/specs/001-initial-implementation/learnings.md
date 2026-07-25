# Implementation Learnings

## fee-6ol6: items --fields discoverability

**`jsonschema:"opaque"` suppresses all recursion.** The reflector treats the tag
as a signal that the per-element shape is dynamic and emits a bare
`{"type":"object"}`. Removing it from `ItemsResult.Items` and `PollResult.Items`
is sufficient for the full item shape to appear in `schema items` and
`schema poll`. `ProjectedItemsResult.Items` correctly keeps the tag because its
element shape really is caller-projected.

**`time.Time` is a struct with no exported fields.** Without special-casing it,
the reflector emits `{"type":"object"}` -- correct Go, wrong schema. The fix is
to check `t == timeType` before the struct branch and return
`{"type":"string","format":"date-time"}`. For `*time.Time` (nullable
`published_at`), check `t.Elem() == timeType` in the pointer branch and return
`{"type":["string","null"],"format":"date-time"}`.

**The `schema.Type` field must be `any` (not `string`) to support nullable
types.** Changing it to `any` allows marshaling either a plain string or a JSON
array `["string","null"]` without introducing a separate struct.

**Levenshtein distance 2 misses `published -> published_at` (distance 3).**
The did-you-mean guard was correct as designed; the fix was to append the full
valid field list unconditionally so callers never need a probe round-trip even
when no suggestion fires.

## fee-8klp: poll failures[] message field

**E2e golden files must be updated when the JSON envelope shape changes.**
Adding `message` to `PollFailure` broke two golden files in
`internal/e2e/testdata/`. The fix is straightforward -- update the golden
file content -- but easy to miss if you only run unit tests.

**`Detail()` centralizes the fallback logic that `Error()` also applies.**
Rather than duplicating "prefer Message, fall back to Err.Error()" at every
call site, the `Detail()` method on `*FeedError` owns it once. The `fee-r1kt`
`check` command can reuse it directly for its own `CheckFailure.Message` field.

## fee-r1kt: check command

**A bounded errgroup is sufficient for `check` concurrency.** The ticket notes
that per-host serialization (like poll's `orchestrate`) is nice-to-have for
`check`. Since `check` is a validation pass typically run over imports (mostly
distinct hosts), the bounded errgroup provides adequate politeness without the
complexity of host-keyed worker routing. If per-host fairness becomes important
later, the orchestrate pattern can be extracted as a library.

**Unconditional GET for check: omit ETag/LastModified from FetchRequest.**
Setting `FetchRequest{URL: f.URL}` (no ETag or LastModified) ensures the server
always returns a 200 with a body to parse. A conditional GET that receives 304
would prove the URL is reachable but not that the body is currently parseable.

## fee-8ugz: migrate Go module from src/ to repo root

**Moving go.mod to the VCS root changes VCS stamping on bare `go build`.**
While the module lived at `src/go.mod` (a subdirectory of the git repo), a bare
`go build` reported `debug.ReadBuildInfo().Main.Version` as `(devel)`, so
`version.Current()` fell through to `"dev"`. Once `go.mod` sits at the
repository root (which is also the VCS root), Go stamps `Main.Version` with a
real pseudo-version derived from the git tag (for example
`v0.0.3-0.2026...-<sha>+dirty`). That broke the e2e `version` golden, which
asserts the deterministic dev fallback. Fix: build the e2e binary with
`-buildvcs=false` so `Main.Version` stays `(devel)` and the fallback is
exercised deterministically regardless of git state. The shipped binary is
unaffected because `make` always sets `version.Override` via `-ldflags -X`.

**golangci-lint caches results by absolute path.** After the `git mv`, lint
reported stale gosec/unused findings against nonexistent `/workspace/src/...`
paths. `golangci-lint cache clean` (plus `go clean -cache`) cleared them; the
findings were purely cache artifacts, not real regressions.
