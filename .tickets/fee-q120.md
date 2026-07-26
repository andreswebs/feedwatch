---
id: fee-q120
status: closed
deps: [fee-z3bm]
links: []
created: 2026-07-26T11:23:25Z
type: task
priority: 1
assignee: Andre Silva
tags: [adr, cli]
---
# consolidate the exit boundary into Run(args, deps) int and rename internal/cli to internal/command

Consolidate the split exit boundary into the ADR 0003 `Run(args []string, deps Deps) int` seam: rename `internal/cli` to `internal/command`, neutralize the urfave interior, move the signal override and the feed-outcome path into `Run`, shrink `Deps` to the reference-lean shape, and narrow the renderer's `*os.File` dependency to an `Fd()` probe. Depends on the error-envelope ticket so the consolidated boundary emits the final shape once.

## Design

## Context

`docs/adr/0003-cli-structure.md` fixes the framework-free contract: `main` is one
line, `internal/command` owns the CLI surface with entry point
`Run(args []string, deps Deps) int`, no framework type appears in an exported
identifier, `Deps` starts lean, and `Run` owns the exit boundary including the
signal override.

feedwatch today splits that boundary across two places:

- `(d Deps).exitErrHandler()` (`internal/cli/root.go:190`) maps a returned error
  through `ExitCodeFor`, emits one stderr error object, and honors a
  feed-outcome `cliv3.ExitCoder` (`internal/cli/exit.go:8`).
- `cmd/feedwatch/main.go` installs a `cliv3.OsExiter` wrapper (`:44-56`) that
  turns a caught SIGINT or SIGTERM into 128 plus signum, repeats the check after
  a clean run (`:68-76`), and calls `cliv3.HandleExitCoder` (`:77`).

The framework also still prints and exits on its own, which is why the override
has to be installed on a framework global in the first place.

## Owner decision folded in

Full uniform adoption, including the package rename `internal/cli` to
`internal/command`.

## Target shape

The reference is the `go-cookiecutter` template files
`{{cookiecutter.project_name}}/internal/command/run.go` and `.../root.go`:

```go
// Deps carries the injected process environment. It starts lean; a field is
// added only when a test must fake it (Getenv, a clock, terminal detection).
// Arguments are a parameter of Run, not a dependency.
type Deps struct {
    In  io.Reader
    Out io.Writer
    Err io.Writer
}

func Run(args []string, deps Deps) int {
    err := runRoot(context.Background(), args, deps)
    if err == nil {
        return 0
    }

    var coded terr.Coded
    if !errors.As(err, &coded) {
        if usageErr := classifyUsage(err); usageErr != nil {
            err = usageErr
        }
    }

    output.EmitError(deps.Err, err)
    return output.ExitCodeFor(err)
}
```

and the neutralizer, which is what lets `Run` own the boundary at all:

```go
// neutralize disables the framework's own error printing, help-on-error, and
// exit handling on cmd and every command below it: parse errors must return
// to the boundary in run.go untouched, so stderr carries exactly one error
// envelope and the framework never calls os.Exit.
func neutralize(cmd *cli.Command) {
    cmd.ExitErrHandler = func(context.Context, *cli.Command, error) {}
    cmd.OnUsageError = func(_ context.Context, _ *cli.Command, err error, _ bool) error {
        return err
    }
    for _, sub := range cmd.Commands {
        neutralize(sub)
    }
}
```

Target `main`:

```go
func main() {
    // signal.Notify wiring only
    os.Exit(command.Run(os.Args[1:], deps))
}
```

## Work

### 1. Rename the package

`internal/cli` becomes `internal/command`. Every file moves and every import
path in the repo updates. Split the package's files along the ADR 0003 seam: the
contract in `run.go`, the replaceable framework interior in `root.go` and
`commands*.go`.

### 2. `Run` and the neutralizer

Add `Run(args []string, deps Deps) int` and port `neutralize`, applying it to the
root and recursively to every subcommand built by `(d Deps).commands()`
(`internal/cli/root.go:65`). Once neutralized, the framework never prints and
never exits, so:

- `internal/cli/root.go:190` (`exitErrHandler`) is deleted; its logic moves into
  `Run`.
- `internal/cli/root.go:147` (`commandNotFound`) and `:161`
  (`completionShellNotFound`) currently render an error and call
  `cliv3.OsExiter` directly. They must instead surface the error so `Run` emits
  and codes it. Both paths are covered by tests
  (`internal/cli/root_test.go:333`, `:416`), which must keep passing: unknown
  command and unsupported completion shell both stay usage errors, exit 64, with
  one error envelope on stderr.
- `internal/cli/root.go:176` (`onUsageError`) becomes the sanctioned
  string-matching usage classifier of ADR 0002, isolated in the interior. The
  reference calls it `classifyUsage`. ADR 0002 requires it to be "isolated in a
  single function and covered by tests that fail when the framework's wording
  changes"; add such a test if the current coverage does not fail on a wording
  change.

### 3. Signal override into `Run`

Today: `cmd/feedwatch/main.go:25-35` captures the signal into a buffered channel
and cancels the context; `:44-56` wraps `cliv3.OsExiter` to override the code
with 128 plus signum; `:68-76` repeats the check for the clean-run path where the
exiter was never called.

Target: the signal-capture channel becomes a `Deps` field (or a `Run`
parameter), `main` keeps only `signal.Notify` plus the goroutine that cancels,
and `Run` applies the override to its own return value. With the framework
exiter neutralized there is exactly one return path, so the two-path complexity
in `main` collapses. The comment at `main.go:37-43` explains the
happens-before reasoning that keeps the caught signal observable; carry that
reasoning into the new location.

Both exits must survive: SIGINT to 130 and SIGTERM to 143, on both the
caught-signal and clean-run paths. `internal/e2e/signal_test.go` must pass
unchanged. Do not edit that file to accommodate the refactor; if it fails, the
refactor is wrong.

### 4. Feed-outcome path off the framework interface

`exitError` (`internal/cli/exit.go:8`) implements `cliv3.ExitCoder` (`ExitCode()`
plus `Exit()`), which is a framework type in the seam. It becomes a plain
unexported type that `Run` recognizes with `errors.As` and translates to the
requested code while emitting nothing further. Its whole purpose is that poll and
check have already written their envelope to stdout and the boundary must add
nothing (`internal/cli/poll.go:122`, `internal/cli/check.go:144`,
`internal/cli/root.go:195-199`).

Guard: `internal/e2e` `all_failed` must still exit 2 and `partial` exit 3, with
their stdout unchanged, and `internal/cli/root_test.go:169`
(`TestActionExitCodePartial`) must pass.

### 5. Lean `Deps`

`internal/cli/root.go:23` currently carries `Cfg`, `Log`, `Store`, `Fetch`,
`Parse`, `Clock`, `Version`, and `In`/`Out`/`Err` as `*os.File`. Target:
`{In io.Reader, Out, Err io.Writer}` plus only what a test must fake, which for
feedwatch is `Clock` and `Version`.

`Store`, `Fetch`, and `Parse` are lazily resolved already
(`internal/cli/resolve.go`, `newResolver`), and `Log` is built in the `Before`
hook (`internal/cli/root.go:100`), so the fields are test-only injection points.
Dropping them means reworking every test that fakes a store onto the remaining
seam. That is the bulk of the mechanical work in this ticket; budget for it.
`Cfg` is `config.Defaults()` at the only production call site
(`cmd/feedwatch/main.go:58`) and is overlaid from flags anyway
(`buildConfig`, `internal/cli/root.go:113`), so it can move inside.

### 6. Narrow the `*os.File` dependency

`output.NewRenderer` (`internal/output/renderer.go:47`) takes `out, err *os.File`
to resolve per-stream color via `ResolveColor` (`internal/output/color.go`),
which is why `Deps` holds `*os.File`. Narrow it to an
`interface{ Fd() uintptr }` probe, or resolve color once before the renderer is
built and pass the two booleans. A buffer that does not implement the probe must
resolve to no color, which is the correct answer for a non-terminal anyway.

This is the precondition for the in-process golden-triple harness in the next
ticket, so do not skip it or work around it with an `*os.File` temp file.

### 7. Re-site the framework globals

`internal/cli/version.go:21` (`installVersionPrinter`) mutates the framework
global `cliv3.VersionPrinter` from the construction point. ADR 0003 says
framework package globals are not mutated and the framework's own printing is
neutralized inside the interior. Move this inside the interior (and prefer a
non-global route if urfave v3 offers one). `--version` must still emit the JSON
contract on stdout and the human line under `--format text`, pinned by
`internal/cli/root_test.go:90` and `:119` and by
`internal/e2e/testdata/version.stdout`.

## Invariants (must not regress)

- Signal exits: SIGINT to 130, SIGTERM to 143, both paths.
  `internal/e2e/signal_test.go` passes unchanged.
- Feed-outcome codes: `all_failed` exits 2, `partial` exits 3, stdout unchanged,
  nothing extra on stderr.
- Every ADR 0001 code is unchanged; `internal/cli/exit_conformance_test.go`
  passes.
- Shell completion still works (`internal/cli/root.go:47-50`,
  `internal/cli/root_test.go:326`, `:352`).
- Unknown command and unsupported completion shell stay usage errors, exit 64,
  one envelope on stderr (`internal/cli/root_test.go:333`, `:416`).
- Bare invocation still prints help and exits 0
  (`rootAction`, `internal/cli/root.go:135-142`).
- All `internal/e2e` goldens are byte-identical: this is a structural refactor,
  not a contract change. Do not run with `-update`.

## TDD plan

1. (tracer) `Run([]string{"--version"}, deps)` with buffer streams returns 0 and
   writes the version envelope to the `Out` buffer.
2. `Run` with an unknown command returns 64 and writes exactly one error
   envelope to `Err`, nothing to `Out`.
3. `Run` with a bad flag returns 64 through the isolated usage classifier, and a
   test fails if the framework's wording changes.
4. A neutralized tree never writes to the real process streams and never exits:
   assert the framework printed nothing by driving `Run` with buffers on a
   parse-error path.
5. An action returning `exitError{3}` makes `Run` return 3 with nothing added to
   `Err`.
6. A caught signal makes `Run` return 130 or 143 regardless of the underlying
   error, unit-tested by feeding the injected channel.
7. `NewRenderer` with a non-`Fd()` writer resolves to no color.

## Acceptance Criteria

- `internal/cli` is renamed `internal/command`, with the contract in `run.go`
  and the framework interior in `root.go` and `commands*.go` per ADR 0003.
- `command.Run(args []string, deps Deps) int` exists and is the single exit
  boundary: it emits the error envelope to `deps.Err`, chooses the code via
  `output.ExitCodeFor`, translates the feed-outcome type, and applies the signal
  override.
- `main` touches the process environment only: `signal.Notify` wiring plus one
  `os.Exit(command.Run(os.Args[1:], deps))`. No error inspection, no
  `cliv3.OsExiter`, no `cliv3.HandleExitCoder`.
- The urfave interior is neutralized (cleared `ExitErrHandler`, error-returning
  `OnUsageError`) on the root and recursively on every subcommand; the framework
  prints nothing and never exits.
- No urfave type appears in an exported identifier of `internal/command`, and the
  framework is imported only in the interior files, verified by an import grep or
  a small lint test. `exitError` is unexported and no longer implements
  `cliv3.ExitCoder`.
- `Deps` is `{In io.Reader, Out, Err io.Writer, Clock, Version}`; the lazy
  `Store`, `Fetch`, `Parse`, `Log`, and `Cfg` fields are gone.
- `output.NewRenderer` no longer requires `*os.File`: it takes an
  `interface{ Fd() uintptr }` probe (or pre-resolved color booleans), and a
  writer without `Fd()` resolves to no color, pinned by a test.
- The framework `VersionPrinter` global is no longer mutated from the
  construction point; `--version` still emits the JSON contract on stdout and the
  human line under `--format text`.
- Non-regression, all passing unchanged: `internal/e2e/signal_test.go` (130 and
  143 on both the caught-signal and clean-run paths), the `internal/e2e` golden
  suite byte-identical with no `-update` run, `all_failed` exit 2 and `partial`
  exit 3, `exit_conformance_test.go`, and the completion, unknown-command,
  bad-flag, and bare-invocation tests.
- Every remaining test passes, rewritten only where it constructed the root
  directly or faked a dropped dependency.
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

**2026-07-26T16:43:16Z**

Consolidated the exit boundary into command.Run(args, deps) int and renamed internal/cli -> internal/command. main is now signal.Notify wiring plus one os.Exit(command.Run(os.Args[1:], deps)); no cliv3.OsExiter/HandleExitCoder/VersionPrinter mutation anywhere. run.go holds the framework-free contract (Run, Deps, finish, watchSignal, boundaryError); root.go holds the urfave interior (newRoot, neutralize, runRoot/runCustom). neutralize clears ExitErrHandler and sets the sanctioned usage classifier (onUsageError) on the root and recursively. Deps shrank to {In io.Reader, Out, Err io.Writer, Clock, Version, Signal} plus unexported store/fetch/parse test seams; Cfg/Log dropped (config.Defaults() used internally). exitError is a plain type (no ExitCoder). --version is now a plain --version/-v flag (HideVersion) handled in Before via a zero-code exitError, so no VersionPrinter global. output.NewRenderer/ResolveColor take io.Writer and probe Fd()/Stat() (buffers -> no color). CommandSchema renamed to Schema to avoid the command.Command*stutter. Every per-command test helper funnels through one drive() that runs the real Run; runResult.exited removed (== code!=0). New tests: TestRunSignalOverridesExitCode (130/143 override), TestExitErrorIsNotFrameworkExitCoder, TestContractFilesDoNotImportFramework, TestUnknownFlagUsageWording, output no-color-for-non-file-writer. All e2e goldens byte-identical (no -update); signal_test.go unchanged. make build green (fmt/vet/lint 0 issues/test/compile). Note: run.go must NOT take a*cli.Command param (would leak the framework into the contract) - the signal-override test composes watchSignal+runCustom+finish directly instead.
