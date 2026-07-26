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

## fee-v737: terr coded errors layered onto core.FeedError

**Giving `FeedError` an `ExitCode() int` method silently made it satisfy
`urfave/cli/v3`'s `ExitCoder` interface (`error` plus `ExitCode() int`).** The
exit boundary in `internal/cli/root.go` distinguished the poll/check result
sub-code carrier from hard failures with
`errors.As(err, &coder)` against `cliv3.ExitCoder`. Once `FeedError` carried
`ExitCode()` (required by ADR 0002's `terr.Coded`), every hard `FeedError`
matched that interface, so the boundary treated it as an already-reported
outcome and skipped rendering the JSON error object to stderr entirely (exit code
still set, stderr empty). Fix: match the concrete unexported `exitError` type
(`errors.As(err, &ee)`), which is the only intentional result sub-code carrier,
instead of the now-too-broad `cli.ExitCoder` interface. The `terr.Coded` and
`cli.ExitCoder` interfaces overlap by construction; disambiguate by concrete
type at the boundary.

**Feed-scoped `FeedError` now exits 70 at the boundary, not 0.** The old
`core.ExitCodeFor` returned 0 for feed-scoped categories (http/network/parse/
timeout) because feed outcomes drive the aggregate poll/check codes 2 and 3, not
a returned error. The new `output.ExitCodeFor` resolves `terr.Coded` via
`errors.As` and returns the class sentinel's exit code, which is 70 for every
feed-scoped category. This is intentional (ADR 0002 invariant): a feed-scoped
`FeedError` reaching the whole-invocation boundary is a bug path and must
classify loudly as the internal-error class (70) rather than silently exit 0.
The `internal/cli/exit_conformance_test.go` inputs (whole-invocation classes
only) are unchanged: 64/65/69/78/70.

**Where the sentinels live.** The registered `terr.E` sentinels stay in
`internal/core` (not `internal/terr`) so the ~40 `errors.Is(err, core.ErrUsage)`
call sites keep compiling and `core` gains only a `terr` import (`terr` imports
nothing but `fmt`/`sync`, so no cycle). `ExitCodeFor` moved to `internal/output`
per the ticket, resolving `terr.Coded` at the boundary.

## fee-7hf3: ADR 0005 result envelope head and null-coalescing marshaling

**Two payload-field renames were forced by the head, not just the one the
ticket named.** The head occupies the `schema_version` and `ok` JSON keys. The
ticket flagged the `migrate` collision (its store version also used
`schema_version`, renamed to `store_schema_version`). A second, unflagged
collision existed: `CheckResult` carried the passing-feed count as `ok` (an
integer). Embedding the boolean head would have let the shallower `ok:int` win
under encoding/json's field-depth rule, silently dropping the head's `ok:true`.
Renamed that field to `passed`. Both renames are breaking and are recorded in
the CHANGELOG; the `check` usage prose and manual-QA shapes were updated too.

**The `type alias` trick in each envelope's `MarshalJSON` drops the whole method
set, so the embedded head still marshals normally.** `json.Marshal(alias(e))`
inlines `output.Head` (schema_version, ok) because Head has no marshaler of its
own; only the outer envelope's `MarshalJSON` is shed, which is exactly what
prevents infinite recursion.

**`jsonschema` had to learn to inline an anonymous embedded struct.** The
reflector took a field's property name from the json tag, falling back to the Go
field name, so an embedded `output.Head` (no tag) would have emitted a nested
`Head` property. `structSchema` now detects an anonymous field with no json tag
and merges the embedded struct's properties and required entries into the
parent, matching how encoding/json flattens the field. This keeps the reflected
`output_schema` the single source of truth: every reflected envelope now shows
`schema_version` and `ok` at the top level, both required.

**The generic text fallback skips anonymous embedded fields.** `renderText`
walks exported struct fields; without a guard it would have printed a
`Head: {1 true}` line under `--format text`. Skipping anonymous fields keeps
text output byte-identical to the pre-head shape (the e2e goldens are all
`.stdout`; no `.stderr` or text golden changed).

**ADR 0006 evidence.** Regenerating the e2e goldens with `-update` produced
exactly the reviewed diff: every JSON `.stdout` gained the two leading keys,
`migrate_status.stdout` also gained the `store_schema_version` rename, and no
`.stderr` file and no exit code changed.

## fee-z3bm: ADR 0005 stderr error envelope, batch form removed

**The reference `EmitError(w, err)` message assumed a bare `Error()` string.**
The go-cookiecutter reference sets `Message: err.Error()`, which is correct
there because its coded errors render a bare message. feedwatch's
`core.FeedError.Error()` prepends a `<category> <url> (status):` head for text
output, so using it verbatim would have leaked that prefix into the envelope
message (the ticket's target shape shows a bare `"server returned HTTP 404"`).
`EmitError` now resolves the message through an optional `detailer` interface
(`Detail() string`), which `FeedError` already implements, falling back to
`err.Error()` for everything else. The structured `code` and `details` already
carry the classification the prefix duplicated.

**`details` must be omitted, not rendered as `{}`, for a whole-invocation
error.** `FeedError.ErrorDetails()` returned a non-nil `feedErrorDetails{}` even
when the error had no feed scope (empty URL, zero status), so every usage or
config error leaked `"details":{}`. `ErrorDetails()` now returns nil in that
case, so `EmitError`'s `errors.As(err, &terr.Detailed)` still matches but the
`omitempty` on the envelope's `details any` field drops it. The acceptance
criterion is "details present only when populated"; an empty object is not that.

**Per-feed failures are stdout-only result data now.** Removing the stderr batch
`{"errors":[...]}` had no information cost: the poll and check `failures` arrays
on stdout already carry `{feed_url, category, status, message}`, and exit codes 2
and 3 are driven by the aggregate result, not by a returned error. The
`all_failed` and `partial` e2e `poll.stderr` goldens regenerated to empty; the
matching `poll.stdout` and exits (2 and 3) were unchanged.

**Context cancellation stays non-internal without coercing to FeedError.** The
old `feedErrorFor` coerced every boundary error into a `*core.FeedError`; its one
load-bearing behavior was mapping `context.Canceled`/`DeadlineExceeded` to the
timeout category so a graceful interrupt never rendered as `internal_error`. The
replacement `boundaryError` passes any `terr.Coded` error through untouched (so
`EmitError` classifies it) and only wraps an uncoded residual cancellation as a
timeout-category error. Pinned by `TestBoundaryErrorContextCancellationIsNotInternal`.
