---
id: fee-v737
status: open
deps: []
links: []
created: 2026-07-26T11:17:21Z
type: task
priority: 1
assignee: Andre Silva
tags: [adr, errors]
---
# adopt terr: coded errors layered onto core.FeedError

Add `internal/terr` (the coded-error package of ADR 0002) and layer it onto the existing `core.FeedError` plus sentinels without regressing the ADR 0001 exit mapping. Also moves `ExitCodeFor` from `core` to `output` and clears the deferred "family-wide" wording. First of eight tickets adopting the six-ADR contract; runs in parallel with the envelope-head ticket.

## Design

## Context

feedwatch has no coded-error package. Classification uses `*core.FeedError`
(`internal/core/errors.go:36`) with a `Category` enum plus four sentinels
(`internal/core/errors.go:108-117`), and `core.ExitCodeFor`
(`internal/core/errors.go:127`) maps them to exit codes. That mapping already
conforms to `docs/adr/0001-exit-code-taxonomy.md`; what is missing is the
`docs/adr/0002-error-handling.md` requirement that every failure reaching a
consumer carry a stable `snake_case` machine code, an exit code, and an optional
hint, discovered at the boundary through one interface, and that the code set be
enumerable as data so the `schema` command cannot drift from reality.

Owner decision (fleet-wide): layer, do not replace. `FeedError` keeps its
per-instance structure and delegates its class-level fields to a per-category
sentinel, which is exactly the pattern ADR 0002 sanctions ("A type that needs
per-instance structure delegates `Code`, `ExitCode`, `Hint`, and `Unwrap` to its
class sentinel, so `errors.Is` against the sentinel still matches while the
instance adds detail").

## Target shape

The reference is the `go-cookiecutter` template file
`{{cookiecutter.project_name}}/internal/terr/terr.go`. Reproduce its surface in
`internal/terr/terr.go`:

```go
// Coded is an error that carries a stable machine code, a process exit code,
// and a user-facing remediation hint.
type Coded interface {
    error
    Code() string
    ExitCode() int
    Hint() string
}

// Detailed is an optional interface for errors that carry render-ready
// structured details, surfaced in the error envelope's "details" field.
type Detailed interface {
    ErrorDetails() any
}

type E struct {
    code, msg, hint string
    exit            int
    cause           error
    details         any
}

func New(code string, exit int, hint, msg string) *E    // registers; panics on duplicate code
func Newf(code string, exit int, hint, format string, args ...any) *E // unregistered, one-off
func All() []*E                                          // registration-order copy
func (e *E) Error() string                               // "msg: cause" when a cause is attached
func (e *E) Code() string
func (e *E) ExitCode() int
func (e *E) Hint() string
func (e *E) Unwrap() error
func (e *E) Is(target error) bool                        // true when target is an *E with the same code
func (e *E) Wrap(cause error) *E                         // copy, receiver unchanged
func (e *E) WithDetails(details any) *E                  // copy, receiver unchanged
func (e *E) ErrorDetails() any
```

Semantics that matter and must be tested:

- `New` panics on a duplicate code. Duplicate registration is an init-time
  programmer error and crashing at startup is the correct outcome (ADR 0002).
- Sentinels are immutable. `Wrap` and `WithDetails` return copies; the receiver
  is never mutated, so sentinels are safe to share across goroutines by
  construction.
- `Is` matches by code, so a `Wrap`ped or `WithDetails`ed copy still satisfies
  `errors.Is(err, Sentinel)`.

## Work

### 1. `internal/terr/terr.go` (new)

Port the surface above with doc comments in the repo's style.

### 2. Register the whole-invocation sentinels

`internal/core/errors.go:108-117` currently holds:

```go
ErrUsage            = errors.New("usage error")
ErrConfig           = errors.New("configuration error")
ErrStoreUnavailable = errors.New("store unavailable")
ErrSchemaTooNew     = errors.New("schema version newer than supported")
```

Turn each into a registered `terr.E` carrying its ADR 0001 exit code and a
consumer-facing hint. The codes are public contract (the `schema` command
publishes them in a later ticket), so settle them here and record the final list
in a note on this ticket. Proposed set, adjust only with a stated reason:

| Sentinel              | Code                | Exit |
| --------------------- | ------------------- | ---- |
| `ErrUsage`            | `usage_error`       | 64   |
| `ErrSchemaTooNew`     | `schema_too_new`    | 65   |
| `ErrStoreUnavailable` | `store_unavailable` | 69   |
| `ErrConfig`           | `config_error`      | 78   |

Plus an `internal_error` (70) sentinel for the unclassified fallback, which the
error envelope and `ExitCodeFor` both need.

Where these live is an implementer call: keeping them in `core` avoids an import
cycle risk and keeps `errors.Is(err, core.ErrUsage)` working at ~40 call sites;
a `core` to `terr` import is fine because `terr` imports nothing but `fmt`.
Every sentinel declaration must document why it carries its exit code (ADR
0002).

### 3. Make `FeedError` implement `Coded` and `Detailed`

`core.FeedError` (`internal/core/errors.go:36`) carries `FeedURL`, `Category`,
`Status`, `Message`, `Err`. Add:

- Class delegation: a per-`Category` class sentinel supplies `Code()`,
  `ExitCode()`, and `Hint()`. Feed-scoped categories get their own codes, for
  example `http_error`, `feed_unreachable` (network), `parse_error`,
  `timeout_error`; the whole-invocation categories (`CatUsage`, `CatConfig`,
  `CatStore`, `CatInternal`) delegate to the sentinels from step 2 so one code
  set covers both paths.
- `ErrorDetails() any` returning the render-ready structure the error envelope
  will place under `error.details`:

  ```json
  {"feed_url": "https://example.test/feed.xml", "status": 404}
  ```

  Omit `status` when zero and `feed_url` when empty, matching the current
  `errorPayload` omission behavior (`internal/output/output.go:21-26`).

Do not change `Error()`, `Unwrap()`, or `Detail()`
(`internal/core/errors.go:48`, `:69`, `:75`): the text renderer
(`internal/output/renderer.go:95`) and the stdout `failures` arrays depend on
them.

### 4. Move `ExitCodeFor` from `core` to `output`

Move `core.ExitCodeFor` (`internal/core/errors.go:127`) to
`internal/output/output.go` as `output.ExitCodeFor`, resolving `terr.Coded` via
`errors.As` and falling back to 70 for anything unclassified, as the reference
does:

```go
func ExitCodeFor(err error) int {
    var coded terr.Coded
    if errors.As(err, &coded) {
        return coded.ExitCode()
    }
    return 70
}
```

Call sites to update: `internal/cli/root.go:152`, `:170`, `:202`, and
`internal/cli/exit_conformance_test.go`.

Nil handling: the current `core.ExitCodeFor` returns 0 for a nil error. The
reference's version is never called with nil (the boundary checks first). Keep
whichever contract the call sites need and state which in a note.

### 5. Clear the deferred "family-wide" wording

- `internal/core/errors.go:120` reads "the family-wide taxonomy in
  docs/adr/0001-exit-code-taxonomy.md". Reference the local ADR without the
  family framing. The doc block moves with `ExitCodeFor` anyway.
- `CHANGELOG.md`, the exit-code entry under `## [Unreleased]`, has the same
  framing ("adopting the family-wide taxonomy in [ADR 0001]"). Fix it too.

## Invariants (must not regress)

- The ADR 0001 mapping is bit-for-bit identical: usage 64, schema-too-new 65,
  store-unavailable 69, config 78, internal and unclassified 70. No
  whole-invocation failure returns 1; exit 1 and 2-63 stay reserved for result
  classes.
- No `Coded` type carries `ExitCode() 0`. Per-feed failures are result data
  (they travel in the stdout `failures` arrays and drive the poll/check
  aggregate codes 2 and 3); they are not returned to the boundary as coded
  errors. If a feed-scoped `FeedError` does reach the boundary, that is a bug
  path and it must classify loudly as `internal_error` and exit 70 rather than
  silently exiting 0. State in a note how the implementation guarantees this.
- `errors.Is(err, core.ErrUsage)` and friends keep working at every existing
  call site, including through `Wrap`.
- Stream bytes do not move. This ticket lands entirely behind the existing
  renderer, so every `internal/e2e` golden must be byte-identical afterwards.
  Do not run the e2e suite with `-update`.

## TDD plan

1. (tracer) `terr.New` returns an `E` whose `Code`, `ExitCode`, `Hint`, and
   `Error` report what was passed.
2. `terr.New` with an already-registered code panics.
3. `Wrap` and `WithDetails` return copies: the receiver's `cause` and `details`
   stay nil, and `errors.Is(copy, sentinel)` is true.
4. `Error()` renders `"msg: cause"` when a cause is attached, bare `msg`
   otherwise.
5. `All()` returns registrations in order and is a copy (mutating the result
   does not affect the registry).
6. `errors.As(err, &coded)` recovers each registered sentinel through one and
   two levels of `fmt.Errorf("%w")` wrapping.
7. `core.FeedError` satisfies `terr.Coded` and `terr.Detailed`:
   a table over every `Category` asserts the delegated code, exit code, and that
   `errors.Is(fe, classSentinel)` holds; `ErrorDetails()` returns
   `{feed_url, status}` with the documented omissions.
8. `output.ExitCodeFor` returns the same code as the old `core.ExitCodeFor` for
   every input in the existing conformance table.

## Acceptance Criteria

- `internal/terr` exposes `Coded`, `Detailed`, `E`, `New`, `Newf`, `All`,
  `Wrap`, `WithDetails`, `Is`, `Unwrap` with the reference semantics, covered by
  behaviors 1-6 of the TDD plan (duplicate-code panic and sentinel immutability
  included).
- The four whole-invocation sentinels plus an `internal_error` sentinel are
  registered `terr.E` values, each documenting why it carries its exit code, and
  `terr.All()` enumerates them in registration order.
- `core.FeedError` satisfies `terr.Coded` and `terr.Detailed`: class fields
  delegate to a per-`Category` sentinel (`errors.Is` against the sentinel
  matches) and `ErrorDetails()` returns `{feed_url, status}` with `status`
  omitted when zero and `feed_url` when empty.
- `output.ExitCodeFor` exists, `core.ExitCodeFor` is gone, and every input in
  `internal/cli/exit_conformance_test.go` maps to the identical code as before:
  64/65/69/78/70, with no whole-invocation failure returning 1 or anything in
  2-63.
- No `Coded` type returns `ExitCode() 0`; a note on the ticket states how a
  stray feed-scoped `FeedError` at the boundary classifies (expected:
  `internal_error`, exit 70).
- `internal/cli/exit_conformance_test.go` passes; its assertion that every code
  `ExitCodeFor` produces is declared as data in every command table is unchanged
  or stronger, never weaker.
- No "family" framing remains in `internal/core/errors.go` or `CHANGELOG.md`.
- Every `internal/e2e` golden is byte-identical (the suite was not run with
  `-update`), and `internal/e2e/signal_test.go` passes.
- The final code list is recorded in a note on this ticket, because the next
  tickets publish it as contract.
- `make build` passes (it runs `fmt-check`, `vet`, `lint`, `test`, then
  compile).

## Notes

**2026-07-26T11:25:20Z**

Cross-cutting requirements for this ticket (from the approved plan at .local/planning/adr-adoption.md, section 5):

- Quality gate: `make build` must pass. It runs `fmt-check`, `vet`, `lint`, `test`, then compile, and is the full validation. See docs/build.md.
- Follow TDD per the repo `tdd` and `golang` skills: write the failing test first, one behavior at a time, starting from the tracer behavior named in the design.
- Do NOT make git commits and do NOT create branches. The owner manages all git workflow. Staging is allowed only where a workflow requires it.
- Re-verify every `file:line` reference in the design before relying on it; the tree moves as sibling tickets land. Line numbers were verified 2026-07-26 against a clean tree at commit 0b894e9.
- Record non-obvious decisions and discoveries in docs/specs/001-initial-implementation/learnings.md under this ticket heading, and add a `tk add-note` summary before closing.
- Ticket markdown must lint clean: `markdownlint-cli2 --fix ".tickets/*.md"` then `markdownlint-cli2 ".tickets/*.md"` reporting 0 errors.
- Full plan context, including the seven owner decisions (D1-D7) this work implements, is in .local/planning/adr-adoption.md.
