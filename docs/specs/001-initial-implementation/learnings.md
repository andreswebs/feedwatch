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

## fee-q120: consolidate the exit boundary into Run(args, deps) int

**The package rename `internal/cli` to `internal/command` created a stutter.**
`cli.CommandSchema` was fine; `command.CommandSchema` trips revive's
`exported: ... stutters`. Renamed the type to `command.Schema` (the aggregate
stays `command.SchemaResult`). The type is package-internal, so no external
caller moved; the schema goldens are unaffected because the JSON keys never
mentioned the Go type name.

**The boundary needs a format-aware error renderer, so the reference's
JSON-only `output.EmitError(deps.Err, err)` in `Run` does not port verbatim.**
feedwatch renders `--format text` errors as a symbol-marked line, pinned by
`TestTextErrorNoColorOnNonTTY` and used by the real CLI. `Run` cannot resolve
the format itself without importing the framework (the resolved global flags
live on the parsed `*cli.Command`). The interior `runRoot` therefore returns the
boundary renderer (`*output.Renderer`, a framework-free type) alongside the
error, and `finish` emits through it. This keeps `run.go` framework-free while
preserving format-aware error output.

**`--version` moved off the framework's `VersionPrinter` global.** ADR 0003
forbids mutating framework package globals. Rather than reinstalling
`cliv3.VersionPrinter` from the construction point, the root sets
`HideVersion: true` and defines a plain `--version`/`-v` bool flag; the Before
hook detects it, writes the version envelope, and returns a zero-code
`exitError` so the action never runs and no store setup happens. This also
short-circuits before the store-dir creation that a real command triggers.

**Void framework callbacks surface errors through a captured pointer.**
`CommandNotFound` (unknown command, unsupported completion shell) returns
nothing in urfave v3, so it cannot hand an error to the neutralized boundary.
`runCustom` passes a `*error` that the callbacks set; after `cmd.Run` returns,
the interior prefers that captured error. This replaces the old
render-then-`OsExiter` path so those usage errors flow through the single
boundary like any other.

**The signal override is deterministically unit-testable by pre-filling the
channel and blocking the action on `ctx.Done()`.** `watchSignal` records the
caught signal into a buffered channel *before* cancelling the context; an action
that blocks on `<-ctx.Done()` cannot return until the cancel fires, which cannot
happen until the record is done, so `finish` observes the signal without a race.
`TestRunSignalOverridesExitCode` feeds SIGINT/SIGTERM and asserts 130/143 win
over the action's would-be config error (78). The e2e `signal_test.go` still
drives the real binary unchanged.

**Dropping `Store`/`Fetch`/`Parse` from the exported `Deps` moved the test seam
to unexported same-package fields.** `Deps` is now `{In, Out, Err, Clock,
Version, Signal}` plus unexported `store`/`fetch`/`parse` that only
`package command` tests set; `resolve.go` reads them and builds production
collaborators when nil. Every per-command test helper funnels through one
`drive(t, d, args...)` that runs the real `Run` with temp-file streams (which
still satisfy the `Fd()`/`Stat()` terminal probe, so color stays off on a
non-terminal). The `runResult.exited` bool is gone: `Run` returns the code
directly, and `exited` was exactly `code != 0`.

## fee-nkdl: ADR 0005 NDJSON warning channel with auto-disable producer

**The warning envelope carries `level:"warning"` instead of `ok`, so a consumer
disambiguates it from the error envelope by presence, not by value.**
`output.EmitWarning` mirrors `EmitError`'s best-effort discipline (marshal
failure drops the line rather than escalating), since a warning never changes
the outcome it advises about. `Renderer.Warn` gates on format exactly like
`Result` and `Error`, and text mode uses a new `SymbolWarn` (⚠) with
`ansiYellow`, deliberately not `SymbolFail`: a warning is not a failure, and
color is never the sole carrier of the marker.

**The poll layer stays stream-blind through an injected `WarnFunc` on `Deps`,
not by importing `output`.** `RecordFailure` gained a trailing
`warn WarnFunc` parameter and raises the advisory only on the exact crossing
(`count == threshold`), not `>=`, so a feed that keeps failing past the
threshold does not re-warn. The `command` layer wires `poll.Deps.Warn` to the
renderer's `Warn` method value, whose signature already matches `WarnFunc`. A
nil callback is a no-op, so unit tests that build `Deps` without a warner keep
working; the direct `RecordFailure` lifecycle tests pass `nil` explicitly.

**`--quiet` suppresses logs, never warnings, which the e2e golden pins for
free.** The suite already runs with `--quiet`, so under it a per-feed failure
leaves stderr empty (the failure info log is suppressed) and the only line on
the crossing poll's stderr is the warning object. The `auto_disable` scenario
drives 10 forced polls: nine warm-ups assert only the unchanged exit 2 via
`runJSON`, and the tenth is golden-pinned. `poll --force` re-targets the
still-active feed each round (it selects active feeds, ignoring backoff), and a
further forced poll after the disable targets nothing (exit 0, no warning).

**Final advisory shape:** code `feed_auto_disabled`, message
`feed disabled after N consecutive failures`, hint
`re-enable with: feedwatch enable <feed>`, details
`{feed_url, failures}`. The `<feed>` in the hint is HTML-escaped in the JSON
line by `encoding/json` (`<feed>`), consistent with the error
envelope.

## fee-e4tl: grow the schema command with the error inventory and tool-level exit codes

**Uniform reference core plus additive enrichment.** `SchemaResult` now carries
the ADR 0005 reference core (`tool`, `version`, `commands`, a tool-level
`exit_codes` as `[]int`, and an `errors` inventory of `{code, exit_code, hint}`)
alongside the pre-existing feedwatch enrichment (`global_flags`, the per-command
`exit_codes` map, and the derived per-command `output_schema`). Neither replaces
the other: a reference-written consumer reads the core, and a feedwatch-aware one
also gets the richer detail.

**Both new collections are projections, not literals.** `errorInventory()` maps
`terr.All()` element-wise, and `exitCodeUnion()` reads the same per-command
`ExitCodes` maps the schema already reports (parsing the string keys to int and
sorting). Because the union reads the reported `Schema.ExitCodes` rather than the
registry tables directly, it can never disagree with what each command entry
shows. The tests assert against the registry rather than a hard-coded list, so a
newly registered sentinel (`TestSchemaNewSentinelAppears`) or a new command exit
code appears with no test edit.

**`SchemaError` lives in `internal/command`, not `internal/output`.** The
reference template puts it in the output package, but feedwatch's schema types
(`Schema`, `SchemaResult`) already live in `internal/command/schema.go` and the
projection happens there, so co-locating avoids a cross-package type with no
other consumer.

**The `schema` command's own registry entry stayed a described `Scalar`, not a
reflected `oneOf`.** Both `Schema` and `SchemaResult` carry a `json.RawMessage`
`output_schema` field; `json.RawMessage` is `[]byte`, which the reflector renders
as an array of integers, so `OneOf(Reflect(Schema{}), Reflect(SchemaResult{}))`
would misdescribe `output_schema`. The prose was corrected to name the new
envelope keys instead.

**Reciprocal conformance guard.** `exit_conformance_test.go` gained
`TestRegisteredCodesDeclaredInTables`: every code in `terr.All()` maps to an exit
code declared in the command tables (all registered codes map to 64/65/69/70/78,
which `defaultExitCodes` declares and every command inherits). Combined with the
original `TestExitCodeTablesCoverExitCodeFor`, the tables and the error registry
cannot drift apart in either direction.

**No `schema` e2e golden added.** `schema` is not in the exec golden set, and the
follow-up harness ticket (fee-hlu7) builds the in-process golden triple where a
schema scenario belongs; adding an exec golden here would only be deleted there.
The shape is covered by the `internal/command` unit tests.

## fee-hlu7: ADR 0006 in-process golden-triple harness

**The in-process harness drives real collaborators, not fakes.** Scenarios call
`Run(args, deps)` with `bytes.Buffer` streams and a real store on a temp `--db`
path, a real fetcher, a real parser, and a real `httptest` feed server, exactly
as a user would. Only the clock (`testsupport.FixedClock`) and the version
string are injected, so `published_at`, backoff, and due calculations are
deterministic. This keeps the goldens honest: they pin the same bytes the binary
emits, and every shared golden came out byte-identical to the exec suite it
replaced.

**The exit code is a scenario-table value, not a third golden file.** Each
scenario declares its expected exit as the `wantExit` parameter and the harness
asserts it, which reads clearer than pinning a third artifact per scenario and
matches the exec suite's existing shape.

**Every observed exit is checked against the declared tables at run time.**
`assertDeclared` looks up each observed exit in the union of
`defaultExitCodes`/`pollExitCodes`/`checkExitCodes`, so a scenario that produced
an undeclared code would fail the suite, not just a mismatched golden. Coverage
in the other direction (every declared code has a scenario) is
`TestGoldenExitCodeConformance`.

**Exit 70 (EX_SOFTWARE, internal/unclassified) has no reachable scenario.** It is
produced only by an unclassified Go error or a recovered panic in `main`,
neither of which a real command emits deterministically. Rather than fake it,
the conformance test names it as intentionally uncovered (the explicit note ADR
0006 asks for) and still asserts a table declares it, so the note cannot go
stale.

**Schema-too-new (65) is set up by stamping the store directly.** After a real
`migrate` brings the store to the current version, the test opens the sqlite
file with a second `sql.Open("sqlite", path)` connection and inserts
`MAX(version)+1` into `schema_migrations`, mimicking a db written by a newer
binary. The preceding `Run` has already closed the store, so the two connections
do not contend.

**The one legitimate byte difference from the exec goldens is the version
value.** In-process, `--version` reports the injected `Deps.Version` ("1.2.3");
the exec suite builds with `-buildvcs=false`, so its binary reports "dev". This
is a difference in the version injection path, not a contract change; every
other shared golden is byte-identical, and there is no stream-interleaving
difference because both harnesses keep stdout and stderr as separate sinks.

**Exec suite reduced to its conditional half.** `internal/e2e` now carries only
`signal_test.go` (SIGINT to 130, SIGTERM to 143, which need a real process to
receive a signal) plus the shared `TestMain`/`binPath`/`exitCodeOf`/`rssFeed` it
depends on. Every stream-contract scenario moved in-process and its exec goldens
were deleted (the whole `internal/e2e/testdata` tree) rather than kept as a
drifting second copy. The `fee-udsl` first-poll regression moved in-process too,
as a JSON-decoding scenario (`TestGoldenFirstPollReportsAllNewItems`), since it
never needed a process.

**Per-command tests now partly redundant (for a follow-up to retire).** The
JSON-shape assertions in `add_test.go`, `list_test.go`, `items_test.go`,
`poll_test.go`, `migrate_test.go`, `discover_test.go`, `export_test.go`,
`import_test.go`, and `prune_test.go` that decode stdout to check the envelope
shape are largely superseded by the golden scenarios, which pin the exact bytes.
The error-code assertions scattered across `root_test.go` and the per-command
usage tests are likewise covered by the `err/*` golden scenarios plus
`assertDeclared`. Retiring them is out of scope here (the acceptance only
requires the old tests keep passing, which they do).

## fee-by04: final documentation pass for the ADR adoption

This ticket carried no code; its value is recording the six adoption decisions
that were resolved by owner decision rather than derivable from the code, so a
future reader understands why the adopted shape looks as it does. The
per-ticket implementation discoveries live under the sibling headings above;
these are the design rationale behind them.

**`terr` layers onto `core.FeedError` rather than replacing it.** `FeedError`
survives for its per-instance structure (`FeedURL`, `Status`, `Category`) and
gains `terr.Coded`/`terr.Detailed` by delegating to a per-category class
sentinel (ADR 0002's "per-instance structure delegates to its class sentinel"
pattern). Replacing it outright would have churned the ~40 `errors.Is(err,
core.Err...)` call sites for no contract gain. The sentinels stay in
`internal/core` for the same reason (see fee-v737).

**The stderr per-feed batch `{"errors":[...]}` form was dropped, not migrated.**
Per-feed failures are result data: they already appear in the stdout `failures`
array, and the poll and check exit codes (2 and 3) are driven by the aggregate
result, not by a returned error. Keeping a second stderr copy would only invite
drift and mislabel a partial run as `ok:false`. stderr now carries only
whole-invocation error envelopes, warnings, and logs (see fee-z3bm).

**The migrate store version moved to `store_schema_version`.** The envelope head
owns the `schema_version` key for every command, so the migrate payload's own
store-version field could not keep that name without colliding into two
`schema_version` keys in one object. Renaming the payload field, not the head
key, keeps the head uniform across the fleet (see fee-7hf3, which also caught
the parallel `check` `ok`-to-`passed` rename).

**The schema command is uniform core plus additive enrichment.** The `commands`
list, the tool-level `exit_codes` union, and the `errors` inventory of
`{code, exit_code, hint}` are the reference core every fleet tool shares, so a
reference-written consumer works unchanged against feedwatch; the per-command
`exit_codes` map and derived `output_schema` stay as additive feedwatch detail.
The floor is uniform, the ceiling is per-tool (see fee-e4tl).

**Exactly one warning producer was wired.** Only the auto-disable-after-threshold
advisory (`feed_auto_disabled`) is a state change a caller cannot otherwise
observe from a single invocation's output, so it is the one warning raised. The
three other candidates the plan considered were deliberately left unwired: the
permanent-redirect rename is already in the `renamed` result array, and the
lossy-charset fallback and the guessed-versus-autodiscovered candidate are
observable from their own result fields. A warning earns its place only when the
result stream does not already carry the signal (see fee-nkdl).

**Warnings are not suppressed by `--quiet`.** A warning is contract output, not a
log; `--quiet` raises the log floor only. The e2e suite runs with `--quiet` and
still sees the `feed_auto_disabled` line, which pins the distinction for free
(see fee-nkdl).
