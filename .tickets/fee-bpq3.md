---
id: fee-bpq3
status: closed
deps: [fee-etoi]
links: []
created: 2026-06-30T03:43:29Z
type: bug
priority: 3
tags: [cli]
---

# completion: unknown shell exits 3 with no error object

An unrecognized `completion <shell>` argument exits `3` with no error object on stderr, violating the output contract. Found during manual QA (TC-CMD-003); see `docs/qa.result.bak.md` BUG-008.

## Design

Observed: `completion pwsh` works (bash/zsh/fish/pwsh all emit scripts), but `completion powershell` (or any unknown shell token) exits `3` with empty stdout and empty stderr.

Two problems:

1. Exit `3` means "partial success" for feed-targeting commands (`docs/usage.md` exit codes); a bad argument is a usage error and should exit `1`.
2. A hard failure must write a single JSON error object to stderr (REQ 2 / TC-OUT-003); here nothing is written.

```sh
feedwatch completion powershell; echo $?     # exit 3, no stdout, no stderr
feedwatch completion pwsh; echo $?           # exit 0, script emitted
```

## Acceptance Criteria

- `completion <unknown-shell>` exits `1` with a single JSON error object (category `usage`) on stderr and nothing on stdout.
- Supported shells (`bash`, `zsh`, `fish`, `pwsh`) continue to emit their scripts and exit `0`.

## Implementation Plan

The `completion` command is the urfave/cli v3 built-in (the root sets `EnableShellCompletion: true` in `src/internal/cli/root.go`). An unknown shell token has no matching subcommand and the built-in command has no `Action`, so it falls through to the help machinery and ends in `Exit(msg, 3)`, which the exit boundary treats as an already-reported outcome and emits nothing.

1. In `NewRootCommand` (`src/internal/cli/root.go`), add the `ConfigureShellCompletionCommand` hook to attach a `CommandNotFound` handler to the completion command, mirroring the existing root-level `commandNotFound`:

   ```go
   ConfigureShellCompletionCommand: func(c *cliv3.Command) {
       c.CommandNotFound = func(_ context.Context, cmd *cliv3.Command, name string) {
           r := d.errRenderer(cmd)
           _ = r.Error(&core.FeedError{
               Category: core.CatUsage,
               Message:  fmt.Sprintf("unsupported shell %q; supported shells are bash, zsh, fish, pwsh", name),
               Err:      core.ErrUsage,
           })
           cliv3.OsExiter(1)
       }
   },
   ```

2. The real shell subcommands (`bash`, `zsh`, `fish`, `pwsh`) keep their own actions and continue to emit scripts and exit `0`.

Verification:

- Regression test next to `TestShellCompletionEnabled` in `root_test.go`: `completion powershell` exits `1` with a single `usage`-category JSON error on stderr and empty stdout; `completion pwsh` still emits a script and exits `0`.
- `make build` green; learnings entry.

## Notes

**2026-06-30T03:43:29Z**

Source: manual QA report docs/qa.result.bak.md BUG-008 (TC-CMD-003). Severity Low, Priority P2.

**2026-06-30T13:36:11Z**

Fixed via ConfigureShellCompletionCommand hook in NewRootCommand (src/internal/cli/root.go): attaches a CommandNotFound handler to the built-in completion command that renders a single usage-category JSON *FeedError on stderr and exits 1. Previously an unknown shell token fell through help machinery to Exit(_,3) which the boundary treated as already-reported (empty stderr). Real shells (bash/zsh/fish/pwsh) keep their actions, exit 0. Tests added in root_test.go: TestCompletionUnknownShellIsUsageError, TestCompletionKnownShellEmitsScript. make build green.
