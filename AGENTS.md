# AGENTS.md

feedwatch is an agent-first command-line tool for watching RSS and Atom feeds:
it fetches, parses, normalizes, stores, deduplicates, and queries feed content,
leaving all content intelligence to the calling agent.

## Essentials

- Go source lives under `src/`. All commands run from the project root via
  `make`.
- After any code change, run `make build`. It is the full quality gate
  (`fmt-check`, `vet`, `lint`, `test`, then compile) and must pass before a task
  is complete. Details: [docs/build.md](docs/build.md).
- Work is driven by tickets in `.tickets/`, managed with the `tk` CLI. Read the
  ticket before starting and record decisions as you go. Details:
  [docs/tickets.md](docs/tickets.md).

## Reference

Load these on demand for the task at hand:

| Topic                                 | File                                         |
| ------------------------------------- | -------------------------------------------- |
| Build targets and the quality gate    | [docs/build.md](docs/build.md)               |
| Ticket workflow and Markdown rules     | [docs/tickets.md](docs/tickets.md)           |
| Design rationale and architecture      | [docs/cli-design.md](docs/cli-design.md)     |
| Functional requirements (EARS)         | [docs/specs/001-initial-implementation/requirements.md](docs/specs/001-initial-implementation/requirements.md) |
| CLI usage reference (commands, flags)  | [docs/usage.md](docs/usage.md)               |
| Manual QA plan (full CLI surface)      | [docs/specs/001-initial-implementation/manual-qa.md](docs/specs/001-initial-implementation/manual-qa.md)       |
| Implementation learnings, newest last  | [docs/specs/001-initial-implementation/learnings.md](docs/specs/001-initial-implementation/learnings.md)       |

When you solve a non-obvious problem or make a design decision during a ticket,
append it to [docs/specs/001-initial-implementation/learnings.md](docs/specs/001-initial-implementation/learnings.md) under that ticket's heading.
