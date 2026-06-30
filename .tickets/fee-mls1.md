---
id: fee-mls1
status: closed
deps: []
links: []
created: 2026-06-29T15:28:42Z
type: task
priority: 0
assignee: Andre Silva
parent: fee-63n9
tags: [cli]
---

# Project scaffolding and tooling

Complete the Go project skeleton so every later ticket has a place to land and
`make validate` runs green on a wired-but-empty tree. The module, `src/` layout,
Makefile, and dependabot already exist; this adds the internal package skeleton,
the golangci-lint config, and the CI workflow. Refs: docs/cli-design.md
(Architecture); docs/plan.bak.md (E1).

## Design

Repo facts already present: module `github.com/andreswebs/feedwatch`
(`src/go.mod`), Go 1.26, Makefile (`build`/`test`/`test-race`/`vet`/`fmt`/
`fmt-check`/`lint`/`validate`, `CGO_ENABLED=0`), `src/cmd/feedwatch/main.go`
stub, `.github/dependabot.yml`.

Add a `doc.go` (package comment per the Architecture section) in each package:

```text
src/internal/{core,store,store/sqlite,fetch,parse,poll,discover,opml,output,cli,config}
```

Add `src/.golangci.yml` enabling:

```text
govet, staticcheck, errcheck, errorlint, revive, ineffassign, unused,
gofmt, goimports, gosec, bodyclose, sqlclosecheck
```

Add `.github/workflows/ci.yml`: on push and PR, `setup-go` 1.26, run
`make validate` and `make build`, with the module cache.

Third-party deps (`urfave/cli/v3`, `mmcdole/gofeed`, `modernc.org/sqlite`) are
added per-lane when first used, to keep `go.mod` tidy; this ticket only sets up
structure and tooling.

TDD plan (tooling ticket, little unit logic; verification via the toolchain):

- `make validate` passes on the skeleton (zero tests is acceptable).
- `make build` produces `bin/feedwatch-<os>-<arch>`.
- CI runs the same on GitHub.

Deep-module note: boundaries follow the Architecture section; no `util`/`common`
grab-bag packages.

## Acceptance Criteria

- `make validate` passes on the skeleton (fmt-check, vet, lint, test).
- `make build` compiles to `bin/`.
- CI workflow runs `make validate` and `make build` on push and PR.
- golangci-lint config present and enforced; no inline lint suppressions.
- Package skeleton matches the Architecture package list.
- Supports Req 20 (self-contained CGO-free binary, minimal deps).

## Notes

**2026-06-29T20:18:40Z**

Scaffolding complete. Added: 11 internal package dirs each with a doc.go package comment (core, store, store/sqlite, fetch, parse, poll, discover, opml, output, cli, config) matching the Architecture section; src/.golangci.yml (golangci-lint v2 schema: version: "2", linters.default none + explicit enable of bodyclose/errcheck/errorlint/gosec/govet/ineffassign/revive/sqlclosecheck/staticcheck/unused, gofmt+goimports under formatters:); .github/workflows/ci.yml (push to main + PR; setup-go 1.26; installs golangci-lint v2.12.2 via official script; runs make validate + make build). NOTE: store/postgres dir intentionally NOT created (deferred, fee-bsge). Also added a one-line package comment to cmd/feedwatch/main.go to satisfy revive package-comments. Third-party deps deferred per-lane; go.sum absent so CI cache-dependency-path warns harmlessly until first dep lands. make build is green. See docs/specs/001-initial-implementation/learnings.md for the golangci-lint v1->v2 config gotcha.

**2026-06-29T20:21:19Z**

User moved .github/workflows/ci.yml to ci.yml.disabled to keep CI disabled for now; planning to refactor the workflow later. The file content is unchanged; rename back to ci.yml to re-enable.
