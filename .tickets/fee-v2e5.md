---
id: fee-v2e5
status: closed
deps: [fee-8eph]
links: []
created: 2026-06-29T19:33:04Z
type: task
priority: 3
assignee: Andre Silva
parent: fee-299l
tags: [docs]
---

# User documentation (README, usage, scheduling)

Write user docs once the command surface is stable: README with quickstart and the one-shot model, a usage/reference derived from schema, the cron/systemd scheduling recipe, and the agent-contract note. Refs: docs/cli-design.md (Scheduling, Discoverability).

## Design

User-facing documentation, written once the command surface is stable.

- `README.md`: what feedwatch is (agent-first feed sensor), install, the
  one-shot model, and a quickstart (`add`, `poll`, `items`).
- A usage/reference doc generated or derived from `schema`/`--help`, covering
  every command, the global flags, exit codes, and env vars.
- The scheduling recipe (Req 18): cron and systemd-timer examples driving `poll`,
  appending JSONL and logging stderr.
- A note on the agent contract: JSON on stdout, structured errors on stderr,
  granular exit codes, `schema` for discovery.

TDD plan (docs ticket; verification via linting and examples):

- All Markdown passes `markdownlint-cli2 --config ~/.markdownlint.yaml` clean.
- Code/command snippets are fenced; example invocations match the real CLI
  (spot-checked against `--help`/`schema`).

Deep-module note: documentation; no production code. Depends on the commands and
`schema` being final.

## Acceptance Criteria

- README + usage/reference doc + scheduling recipe + agent-contract note, all
  markdownlint-clean and consistent with the real CLI.
- Supports Req 18 (scheduling recipe) and overall discoverability.
- `make validate` passes (docs do not break the build).

## Notes

**2026-06-30T00:32:01Z**

Wrote user docs: expanded README.md (what feedwatch is, one-shot model, install via make build/go install, quickstart, agent-contract note) and new docs/usage.md (full command reference, global flags table, exit codes 0/1/2/3/130/143, env vars, item model + valid --fields names, cron and systemd-timer scheduling recipes for Req 18). All examples spot-checked against the real binary (migrate, migrate --status, list, items, poll envelopes match exactly). Only 4 FEEDWATCH_* env vars are actually wired (DB, FORMAT, USER_AGENT, CONCURRENCY) plus HTTP(S)_PROXY/NO_PROXY, NO_COLOR, TERM, XDG_STATE_HOME -- documented exactly those, not the broader set the design doc sketches. markdownlint-cli2 clean (0 errors, project .markdownlint.yaml auto-discovered); make build green. This unblocks fee-299l (E9 epic).
