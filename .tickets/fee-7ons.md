---
id: fee-7ons
status: closed
deps: [fee-chr5, fee-cmkb, fee-po72, fee-l45i]
links: []
created: 2026-06-29T15:36:08Z
type: task
priority: 0
assignee: Andre Silva
parent: fee-63n9
tags: [cli]
---

# CLI skeleton: urfave/cli v3 root, global flags, exit handling

Stand up the urfave/cli v3 command tree every subcommand plugs into: root
command, global/inherited flags, the Before hook (config, logging, color), the
exit boundary (ExitCoder plus ExitErrHandler), usage-error and unknown-command
interception, the JSON version printer, and shell completion. Refs:
docs/cli-design.md (CLI Framework, Input and Output Contract, Error Handling and
Logging, Architecture); docs/research/urfave-cli.reference.md.

## Design

Files: `src/internal/cli/root.go` (`NewRootCommand`), `src/internal/cli/exit.go`
(`exitError`), `src/cmd/feedwatch/main.go` (signal ctx -> Run ->
`HandleExitCoder`).

```go
func NewRootCommand(d Deps) *cli.Command

type Deps struct {
    Cfg     config.Config
    Log     *slog.Logger
    Store   store.Store
    Fetch   fetch.Fetcher
    Parse   parse.Parser
    Clock   core.Clock
    Version string
    Out, Err *os.File
}

type exitError struct{ code int }

func (e exitError) Error() string { return "" }
func (e exitError) ExitCode() int { return e.code }
func (e exitError) Exit() string  { return "" } // implements cli.ExitCoder
```

Global flags on the root, inherited by all subcommands (urfave ref: Flags, Value
sources, Lifecycle hooks):

```text
--db             StringFlag, Sources EnvVars FEEDWATCH_DB
--format         StringFlag default "json", Validator json|text, EnvVars FEEDWATCH_FORMAT
--log-level      StringFlag default "info", Validator error|warn|info|debug
--quiet          BoolFlag
--no-color       BoolFlag         (NO_COLOR handled in output, not as a Source)
--user-agent     StringFlag, EnvVars FEEDWATCH_USER_AGENT
--concurrency    IntFlag default 8, EnvVars FEEDWATCH_CONCURRENCY
--connect-timeout DurationFlag default 5s
--timeout        DurationFlag default 30s
--proxy          StringFlag       (also honors HTTP(S)_PROXY via the transport)
--ca-bundle      StringFlag, TakesFile
--min-tls        StringFlag default "1.2", Validator 1.2|1.3
--allow-private  BoolFlag
```

Before hook: build `config.Config` from flags (precedence flags > env >
defaults via urfave `Value` + `Sources`), `Validate()` it (failure wraps
`core.ErrConfig`), construct the `*slog.Logger` to `Err`, resolve color, stash
config and logger in the context for actions, and return the derived context
(urfave ref: Lifecycle hooks Before).

Exit handling (urfave ref: Exit codes and errors): `cmd.Run` returns an error;
`main` calls `cli.HandleExitCoder`. An action writes its result via
`output.Renderer` then returns nil (exit 0) or `exitError{2|3}`; hard errors are
returned wrapping a `core` sentinel or `*FeedError`, mapped to exit 1 via
`core.ExitCodeFor`. A custom `ExitErrHandler` emits the JSON error object to
`Err` (not urfave's default text) then calls `cli.OsExiter`. `OnUsageError` and
`CommandNotFound` emit a usage-category JSON error and exit 1.

Version and completion: `HideVersion` stays false; `VersionPrinter` overridden to
emit `{"version","commit","go"}` as JSON (human line under `--format text`). No
version subcommand. `EnableShellCompletion: true`.

`main.go`:

```go
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer stop()
err := cli.NewRootCommand(deps).Run(ctx, os.Args)
cli.HandleExitCoder(err)
```

TDD plan (drive `cmd.Run` with injected Out/Err buffers, `cli.OsExiter`
capturing the code, a fake Clock, and stub actions; no real store/network):

1. (tracer) unknown command -> JSON error object on Err, captured exit 1.
2. invalid `--format` value -> exit 1 usage error.
3. stub action returns `exitError{3}` -> its envelope on Out, captured exit 3.
4. stub action returns a config `*FeedError` -> exit 1, JSON error category
   `config`.
5. `--version` -> JSON version object on Out, exit 0.
6. `--quiet` suppresses info logs; `--log-level=debug` emits debug to Err.
7. `NO_COLOR` or non-terminal Err -> no ANSI in text mode.
8. precedence: `--concurrency` flag overrides `FEEDWATCH_CONCURRENCY` env
   overrides default.

Deep-module note: the package exposes only `NewRootCommand`; all wiring is
internal; `main` holds only signal ctx + Run + `HandleExitCoder`.

## Acceptance Criteria

- `NewRootCommand` builds the urfave/cli v3 root with the global flags above and
  the Before hook.
- Exit boundary: `exitError` implements `cli.ExitCoder`; `ExitErrHandler` emits
  JSON error objects; `OnUsageError`/`CommandNotFound` map to exit 1.
- `--version` emits JSON; shell completion enabled; no version subcommand.
- Behaviors 1-8 covered by tests through `cmd.Run`; config precedence flags > env
  > defaults verified.
- `main` holds only signal ctx + Run + `HandleExitCoder`.
- Supports Req 2, 3, 5, 19 and part of 4. `make validate` passes.

## Notes

**2026-06-29T20:51:30Z**

Implemented the urfave/cli v3 CLI skeleton in internal/cli (root.go, flags.go, context.go, version.go, storepath.go, exit.go) plus cmd/feedwatch/main.go.

Key points for the next person:

- NewRootCommand(Deps) builds the root: global flags (inherited; Local defaults false), Before hook (resolves config from flags>env>defaults, validates, builds logger+renderer, stashes config/logger/renderer in ctx via unexported ctxKey accessors configFrom/loggerFrom/rendererFrom), Action, ExitErrHandler, OnUsageError, CommandNotFound.
- Exit boundary: exitError{code} implements cli.ExitCoder (caught first in ExitErrHandler -> just OsExiter, no stderr) for poll outcomes 2/3. Any other error is a hard failure: feedErrorFor() coerces it to a \*core.FeedError and renders one JSON error object on stderr, code via core.ExitCodeFor (1).
- Unknown command is intercepted by rootAction (leftover positional arg) since urfave only calls CommandNotFound via the help path; CommandNotFound is set too as belt-and-suspenders. Bare invocation prints root help, exit 0.
- --version: HideVersion stays false, Command.Version=d.Version (MUST be non-empty or urfave force-hides it; main passes 'dev'). Global cli.VersionPrinter is overridden (documented controlled-global exception alongside OsExiter/ErrWriter) to emit {version,commit,go} JSON or a text line. commit comes from runtime/debug build info vcs.revision (empty under 'go run'/go test, stamped in compiled binaries); go is runtime.Version().
- Store path: resolveStorePath() does the XDG default ($XDG_STATE_HOME/feedwatch/feedwatch.db -> ~/.local/state/...); a postgres:// DSN or explicit path passes through. This was the config-layer TODO assigned to fee-7ons.
- Tests are white-box (package cli) so they can drive cmd.Run with a stub subcommand and read unexported accessors; streams are temp files (non-TTY => color off) and OsExiter is captured. Behaviors 1-8 from the ticket all covered.
- Subcommands (add/poll/etc.) plug into root.Commands; their actions read configFrom/loggerFrom/rendererFrom(ctx).
