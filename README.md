# feedwatch

feedwatch is an agent-first command-line tool for watching RSS and Atom feeds.
Its primary user is an AI agent, not a human at a terminal, so every command
emits structured JSON on stdout, reports failures as structured objects on
stderr, and returns meaningful exit codes.

feedwatch is pure, deterministic plumbing: it fetches, parses, normalizes,
stores, deduplicates, and queries feed items. It makes no LLM calls and needs no
API keys or credentials. All content intelligence (summarizing, ranking, triage)
is left to the calling agent, which reasons over the clean JSON feedwatch
returns.

## Execution model

feedwatch is stateful and one-shot. There is no daemon and no blocking loop.
Each command is a short-lived invocation that reads and writes persistent state,
then exits. "Watch" describes the purpose (subscription state persists across
invocations), not a running process: an agent, cron, or a systemd timer decides
when to poll.

Because state persists between calls, `poll` reports only what is new since the
previous poll and handles deduplication and conditional-GET bookkeeping
internally.

```sh
feedwatch add https://blog.example/feed.xml
feedwatch poll        # reports new items, marks them seen
feedwatch poll        # immediately again: nothing new
```

## Install

feedwatch is a self-contained binary with no runtime dependencies. Build it from
source with Go 1.26 or newer:

```sh
make build            # produces bin/feedwatch-<os>-<arch> for the host platform
make build-all        # cross-compiles every supported platform into bin/
```

`make build` runs the full quality gate (format check, vet, lint, test) before
compiling. To install directly with the Go toolchain instead:

```sh
cd src && go install ./cmd/feedwatch
```

Put the resulting binary on your `PATH` as `feedwatch`. The examples below assume
that.

## Quickstart

```sh
# Subscribe to a feed, optionally giving it a short alias.
feedwatch add https://blog.go.dev/feed.atom --alias godev

# Poll due feeds; new items are returned and marked seen.
feedwatch poll
# {"polled":1,"skipped":0,"new_items":3,"items":[...]}

# Re-query stored history at any time, with filters.
feedwatch items --feed godev --since 7d --limit 50

# List subscriptions and their health.
feedwatch list
```

To turn a site homepage into a feed URL, call `discover` first (it never
guesses over the network during `add`):

```sh
feedwatch discover https://example.com
# {"candidates":[{"title":"Blog","url":".../feed.xml","source":"autodiscovery"}]}
feedwatch add https://example.com/feed.xml
```

## The agent contract

feedwatch is built to be driven by a program:

- stdout carries pure result JSON in a consistent envelope. Piping stdout into
  `jq` never chokes on a diagnostic. `--format text` switches to human-friendly
  tables for the occasional interactive use.
- stderr carries structured JSON log lines and structured error objects,
  including per-feed failures with a category (`network`, `http`, `parse`,
  `timeout`) and message.
- Exit codes let an agent branch without parsing output: `0` success, `1` usage
  or configuration error, `2` all targeted feeds failed, `3` partial success.
- `feedwatch schema` emits a machine-readable description of every command (its
  arguments, flags, exit codes, and an output JSON Schema) so an agent can
  discover the full contract without guessing.

See [docs/usage.md](docs/usage.md) for the complete command and flag reference,
exit codes, environment variables, and the cron and systemd scheduling recipes.

## Documentation

- [docs/usage.md](docs/usage.md) - command reference, global flags, exit codes,
  environment variables, and scheduling recipes.
- [docs/cli-design.md](docs/cli-design.md) - the design rationale and the
  agent-first principles behind the tool.

## Authors

**Andre Silva** - [@andreswebs](https://github.com/andreswebs)

## License

This project is licensed under the [GPL-3.0-or-later](LICENSE).
