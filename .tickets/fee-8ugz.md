---
id: fee-8ugz
status: closed
deps: []
links: [fee-rgmp]
created: 2026-07-24T20:37:12Z
type: task
priority: 1
assignee: Andre Silva
---
# Migrate Go module from src/ to repo root

# Migrate the feedwatch Go module from src/ to the repo root

## Why this change is needed

The feedwatch repository ships a single Go module whose `go.mod` currently
lives at `src/go.mod` but declares the module path
`github.com/andreswebs/feedwatch`. The Go toolchain and the public module
proxy map that module path to a `go.mod` at the repository root, not under a
`src/` subdirectory. As published today the module is therefore unreachable
through the proxy:

- `go install github.com/andreswebs/feedwatch/cmd/feedwatch@latest` fails.
- pkg.go.dev, govulncheck, and dependabot's proxy path cannot resolve it.

Moving the module contents from `src/` up to the repository root fixes
distribution. Import paths do not change, because the declared module path
already omits `src`, so no `.go` source file needs editing. This is a
mechanical relocation plus fixups to the build system, CI, and docs.

feedwatch has two commands:

- `cmd/feedwatch` — the shipped CLI (the only binary the Makefile packages).
- `cmd/qafixtures` — a manual-QA fixture HTTP server, run via `go run` only
  (`make qa-server`); never cross-compiled or packaged into `dist/`.

Both must continue to compile and run after the move. Because
`go build ./...` and `go test ./...` walk the whole module, `cmd/qafixtures`
is exercised by `make validate` without any special handling.

## Target layout

```text
feedwatch/
  go.mod
  go.sum
  .golangci.yml
  cmd/feedwatch/main.go
  cmd/qafixtures/main.go
  cmd/qafixtures/main_test.go
  cmd/qafixtures/feeds/...
  internal/...
  Makefile, README.md, LICENSE, docs/, bin/, dist/, .github/ ... unchanged
```

## Preconditions

- Work from inside the repository directory (`feedwatch/`).
- Working tree clean apart from this task. Committing, branching, and tagging
  are left to the repository owner; this plan changes files only.
- Confirm the current state: `git -C . ls-files src/go.mod` should print
  `src/go.mod`. The module path is `github.com/andreswebs/feedwatch`
  (`src/go.mod` line 1).

## Step 1 — Move module contents to the repo root

`src/` contains exactly these entries: `.golangci.yml`, `cmd/`, `internal/`,
`go.mod`, `go.sum`. Move all of them up one level with `git mv` so history is
preserved, then remove the now-empty `src/`.

Run from the repo root:

```sh
git mv src/go.mod        go.mod
git mv src/go.sum        go.sum
git mv src/.golangci.yml .golangci.yml
git mv src/cmd           cmd
git mv src/internal      internal
rmdir src
```

Notes:

- `.golangci.yml` is a dotfile; the explicit `git mv` above handles it. Do not
  rely on a glob that would skip dotfiles.
- After this step, `git status` should show the renames and a deleted `src/`
  tree with no leftover files. Run `ls src 2>/dev/null` and confirm it is gone.
- No `.go` file is edited. The module path in `go.mod` stays
  `github.com/andreswebs/feedwatch`; the `-X` version override target in the
  Makefile (`.../internal/version.Override`) is unaffected.

## Step 2 — Makefile

File: `Makefile`.

Only line 2 needs to change. The recipes use `cd $(SRC_DIR) && ...`; pointing
`SRC_DIR` at the repo root keeps every recipe working with a one-line diff and
zero behavioral change (`cd $(CURDIR) && ...` is a no-op).

Line 2, before:

```make
SRC_DIR     := $(CURDIR)/src
```

After:

```make
SRC_DIR     := $(CURDIR)
```

Do not change `CMD_DIR := ./cmd/feedwatch` (line 5): it is already relative to
the module root and stays correct. The `qa-server` target
(`cd $(SRC_DIR) && go run ./cmd/qafixtures ...`, line 71) also stays correct
once `SRC_DIR` is the repo root. No other Makefile line references `src`.

Recommendation on the fleet OPEN question (keep `SRC_DIR` vs. drop it): keep it
as `$(CURDIR)`. It is the minimal, lowest-risk diff and leaves every recipe
untouched. Dropping the variable and all `cd` prefixes is a larger cosmetic
change with no functional benefit; defer it to a separate cleanup if desired.

## Step 3 — GitHub Actions workflows

### `.github/workflows/ci.yml`

Lines 23-24, before:

```yaml
          go-version-file: src/go.mod
          cache-dependency-path: src/go.sum
```

After:

```yaml
          go-version-file: go.mod
          cache-dependency-path: go.sum
```

No other `src` reference in this file.

### `.github/workflows/release.yml`

Lines 27-28, before:

```yaml
          go-version-file: src/go.mod
          cache-dependency-path: src/go.sum
```

After:

```yaml
          go-version-file: go.mod
          cache-dependency-path: go.sum
```

SBOM step, line 51, before:

```yaml
        with:
          path: src
```

After (recommended — see rationale below):

```yaml
        with:
          path: .
```

## Step 4 — SBOM scope (resolves fleet OPEN question 2)

Today the SBOM step scans `path: src`, which contained only Go sources
(`go.mod`, `go.sum`, `cmd`, `internal`). After the move there is no `src/`, so
the natural replacement is `path: .`. But the repo root also holds `docs/`,
`bin/`, and `dist/`, and the release job runs `make dist` (which populates
`bin/` and `dist/` with the built binaries and archives) before the SBOM step.
Scanning a bare `.` would make Syft catalog those built binaries and archives,
producing a larger and partly duplicated SBOM.

Recommendation for feedwatch: set `path: .` and add a Syft exclude config so
the SBOM stays scoped to the module source, matching today's behavior. Create
`.syft.yaml` at the repo root:

```yaml
exclude:
  - ./.git/**
  - ./bin/**
  - ./dist/**
  - ./docs/**
  - ./.github/**
```

anchore/sbom-action picks up `.syft.yaml` from the working directory
automatically. With the excludes in place, `path: .` catalogs `go.mod`,
`go.sum`, `cmd/`, and `internal/` — the same footprint as the old `src` scan.
If the owner prefers not to add a config file, `path: .` with no excludes is an
acceptable fallback (larger SBOM, still valid); this is a judgment call for the
first post-migration release and does not block the migration.

Do not change `SYFT_SOURCE_NAME: feedwatch` (line 48) or the output filename;
they use the repo name, not a path.

## Step 5 — Dependabot

File: `.github/dependabot.yml`.

The `gomod` ecosystem points at `/src`. After the move the module lives at the
repo root.

Lines 9-11, before:

```yaml
  - package-ecosystem: gomod
    directory: /src
    schedule:
      interval: weekly
```

After:

```yaml
  - package-ecosystem: gomod
    directory: /
    schedule:
      interval: weekly
```

Leave the `github-actions` entry (`directory: /`) unchanged.

## Step 6 — Repo docs and AGENTS.md

`CLAUDE.md` is a symlink to `AGENTS.md`, so editing `AGENTS.md` covers both.

### `AGENTS.md` (line 9)

Before:

```markdown
- Go source lives under `src/`. All commands run from the project root via
  `make`.
```

After:

```markdown
- Go source lives at the repository root (`go.mod`, `cmd/`, `internal/`). All
  commands run from the project root via `make`.
```

### `docs/build.md` (line 3)

Before:

```markdown
All commands run from the project root via `make`. Go source lives under `src/`.
```

After:

```markdown
All commands run from the project root via `make`. Go source lives at the
repository root.
```

### `docs/cli-design.md` (lines 172, 178-192)

Line 172, before: `... single-responsibility packages under`src/`,` — change
`under`src/`` to `at the repository root`.

In the fenced `text` package-layout block (lines 178-192), strip the `src/`
prefix from every path so the tree reflects the new layout. Before/after for
the block:

Before:

```text
src/cmd/feedwatch/             main: signal-aware context, command tree, exit
src/internal/core/            domain types: Feed, Item, Enclosure, Category,
                              FeedError, sentinel errors (no internal deps)
src/internal/store/           Store interface over core types
src/internal/store/sqlite/    SQLite implementation + embedded migrations
src/internal/store/postgres/  PostgreSQL implementation (deferred)
src/internal/fetch/           HTTP: conditional GET, SSRF guard, retry, charset;
                              Fetcher interface
src/internal/parse/           Parser interface + gofeed impl + normalization
src/internal/poll/            orchestration: fetch, parse, dedup, persist
src/internal/discover/        autodiscovery and common-path probing
src/internal/opml/            OPML import and export
src/internal/output/          result envelope types, JSON/text renderers, color
src/internal/cli/             command tree, flag definitions, Before hook, schema
src/internal/config/          resolved configuration
```

After:

```text
cmd/feedwatch/             main: signal-aware context, command tree, exit
internal/core/             domain types: Feed, Item, Enclosure, Category,
                           FeedError, sentinel errors (no internal deps)
internal/store/            Store interface over core types
internal/store/sqlite/     SQLite implementation + embedded migrations
internal/store/postgres/   PostgreSQL implementation (deferred)
internal/fetch/            HTTP: conditional GET, SSRF guard, retry, charset;
                           Fetcher interface
internal/parse/            Parser interface + gofeed impl + normalization
internal/poll/             orchestration: fetch, parse, dedup, persist
internal/discover/         autodiscovery and common-path probing
internal/opml/             OPML import and export
internal/output/           result envelope types, JSON/text renderers, color
internal/cli/              command tree, flag definitions, Before hook, schema
internal/config/           resolved configuration
```

Realign the description column to the new (shorter) path column as shown; exact
column width is cosmetic as long as it stays a readable `text` block.

Line 666 (the "Package layout" table row) reads
``Small acyclic packages under `src/`; ...`` — change `under`src/`` to
`at the repository root`.

### `docs/specs/001-initial-implementation/manual-qa.md` (lines 84-85, 124-125)

This is a living QA doc (not historical). Update the four `src/cmd/qafixtures`
references to `cmd/qafixtures`:

- Line 84: ``ships one at `src/cmd/qafixtures` `` -> ``ships one at
  `cmd/qafixtures` ``
- Line 85: ``embedded under `src/cmd/qafixtures/feeds` `` -> ``embedded under
  `cmd/qafixtures/feeds` ``
- Line 124: ``The server (`src/cmd/qafixtures`)`` -> ``The server
  (`cmd/qafixtures`)``
- Line 125: ``a file under `src/cmd/qafixtures/feeds` `` -> ``a file under
  `cmd/qafixtures/feeds` ``

## Step 7 — Deliberately NOT changed

These `src/` hits from a repo-wide grep must be left alone:

- `docs/specs/learnings.md` (lines 22, 310, 345, 1301, 1384-1385, 1406, 1432):
  a historical, append-only implementation log describing the state of the repo
  at the time each entry was written. Per the project convention for historical
  specs, do not rewrite history. Optionally append a single dated note at the
  end of the file pointing readers to this migration (module now at the repo
  root), but do not edit the existing entries.
- `docs/research/urfave-cli.reference.md` (lines 281, 634): the `src/` there is
  part of external module paths in prose, unrelated to feedwatch's layout.
- `docs/cli-design.md` any prose unrelated to the layout tree already covered
  above needs no change.
- `.claude/skills/ccc/SKILL.md` (line 45): a generic example argument
  (`--path 'src/api/*'`), not a feedwatch path.
- `.local/research/*.md` and `.local/prompts/*.md`: untracked (not in git) and
  they reference the newsboat project's own `src/`, not feedwatch. Out of
  scope.
- `bin/` and `dist/`: build outputs, not edited by hand.

Re-run the sweep after editing and confirm only the intentional
exclusions remain:

```sh
grep -rn 'src/' --exclude-dir=.git --exclude-dir=.tickets --exclude-dir=.local \
  --exclude-dir=bin --exclude-dir=dist .
```

Expect hits only in `docs/specs/learnings.md`, `docs/research/urfave-cli.reference.md`,
and `.claude/skills/ccc/SKILL.md`.

## Step 8 — Fleet workspace glue (do with the repo owner)

The fleet workspace file `go.work` (one level above the repo, in the
`go-projects` workspace root) lists `./feedwatch/src`. Once this migration
lands it must become `./feedwatch`:

Before:

```text
 ./feedwatch/src
```

After:

```text
 ./feedwatch
```

This file is outside the feedwatch repository; coordinate the edit with the
workspace owner (or run `go work use ./feedwatch` after removing the old
entry). Until it is updated, workspace-mode builds from the fleet root will not
see feedwatch at its new location. Building inside the feedwatch repo with
`make` is unaffected.

The fleet-root `AGENTS.md`/`CLAUDE.md` states each module lives at
`<project>/src/`; that is a fleet-wide doc updated as part of the overall
rollout, not in this repo.

## Verification

Run from the repo root, in order:

1. Layout sanity:

   ```sh
   test ! -e src && test -f go.mod && test -d cmd/feedwatch && \
     test -d cmd/qafixtures && test -d internal && echo layout-ok
   ```

2. Module resolves and is tidy:

   ```sh
   go build ./...
   go vet ./...
   ```

3. Full quality gate (fmt-check, vet, lint, test) and build:

   ```sh
   make validate
   make build
   ```

   `make build` produces `bin/feedwatch-<os>-<arch>`.

4. Run the shipped binary:

   ```sh
   ./bin/feedwatch-$(go env GOOS)-$(go env GOARCH) version
   ./bin/feedwatch-$(go env GOOS)-$(go env GOARCH) schema
   ```

5. Run the second command (qafixtures) to confirm it still builds and runs:

   ```sh
   make qa-server QA_ARGS="--help"
   ```

   or bind it briefly: `make qa-server` then stop it. It must start without a
   build error.

6. Cross-compile packaging still works (optional but recommended before a
   release):

   ```sh
   make dist VERSION=v0.0.0-test
   ```

   Confirm `dist/` gets archives plus `SHA256SUMS.txt`, then `make clean`.

7. External proxy resolvability — only meaningful AFTER the owner merges and
   pushes a NEW tag (old tags were never proxy-resolvable, so nothing needs
   retracting). From a scratch directory outside the workspace:

   ```sh
   cd "$(mktemp -d)"
   GOPROXY=https://proxy.golang.org GOFLAGS=-mod=mod \
     go install github.com/andreswebs/feedwatch/cmd/feedwatch@latest
   ```

   It should download and install the CLI. Note the command lives at
   `cmd/feedwatch`, so the install path is `.../feedwatch/cmd/feedwatch`, not
   the bare module path.

## Rollback

Every change is a file move or text edit. To roll back before the owner
commits: `git checkout -- .` and `git restore --staged .` to unstage the
renames, or `git mv` the four entries back under `src/` and revert the
Makefile, workflow, dependabot, and doc edits. No data, tags, or published
artifacts are touched by this plan, so rollback is purely local.

## Resolution of fleet-draft OPEN questions for feedwatch

1. Makefile `SRC_DIR`: keep it, set to `$(CURDIR)` (minimal diff, no recipe
   changes). See Step 2.
2. SBOM at the repo root: `path: .` plus a `.syft.yaml` exclude of
   `bin/`, `dist/`, `docs/`, `.github/`, `.git/` to preserve today's scope. See
   Step 4. Bare `path: .` is an acceptable fallback.
3. Nix source filtering: not applicable — feedwatch has no Nix packaging.
4. Library-at-root: not applicable — feedwatch is a command module, no exported
   library packages.
5. funraiser include/skip: not applicable to feedwatch.
6. Rollout: feedwatch is a mechanical, standard-shape repo (per the draft's
   rollout it follows the proven recipe). One PR for this repo.
7. Post-migration README follow-up: recommended. Add a short "Install" line to
   `README.md`:
   `go install github.com/andreswebs/feedwatch/cmd/feedwatch@latest`. This can
   ride in the same PR or a follow-up; it is not required for the module to
   resolve.
8. Cookiecutter wording cleanup: not applicable to feedwatch.

## Markdown hygiene

After writing any Markdown, run
`markdownlint-cli2 --config ~/.markdownlint.yaml --fix` on the edited files
(the repo also has a local `.markdownlint.yaml`; prefer it if present at the
repo root). No em-dashes, no emojis, no machine-local paths in committed docs.


## Notes

**2026-07-24T21:00:45Z**

FLEET DECISIONS finalized (see .local/planning/go-module-root-migration.md
"Decisions"). All match this plan; no step changes required:

- Makefile: minimal diff — `SRC_DIR := $(CURDIR)`, keep the cd prefixes.
- SBOM: `path: .` plus a root `.syft.yaml` excluding ./bin/**, ./dist/**,
  ./docs/**, ./.git/**.
- `.github/dependabot.yml`: gomod `directory: /src` -> `/` (this plan already
  flagged it, including the `directory:` line).

**2026-07-24T23:49:34Z**

Migrated the Go module from src/ to the repo root. git mv of go.mod, go.sum, .golangci.yml, cmd/, internal/ (history preserved); removed empty src/. Fixups: Makefile SRC_DIR := $(CURDIR); ci.yml + release.yml go-version-file/cache-dependency-path -> go.mod/go.sum; release.yml SBOM path: . with new root .syft.yaml excluding bin/dist/docs/.github/.git; dependabot gomod directory /src -> /; docs (AGENTS.md, build.md, cli-design.md layout tree + table, manual-qa.md qafixtures paths). NOT edited: learnings.md history and urfave-cli reference prose (external module paths). Two non-obvious gotchas fixed: (1) with go.mod now at the VCS root, bare 'go build' stamps a real pseudo-version instead of (devel), breaking the e2e version golden -- fixed by building the e2e binary with -buildvcs=false so it exercises the 'dev' fallback deterministically; the shipped binary is unaffected (make always sets version.Override via -ldflags -X). (2) golangci-lint cached findings by old /workspace/src/ paths; 'golangci-lint cache clean' + 'go clean -cache' cleared the false positives. Verified: make build green, --version/schema work, make qa-server QA_ARGS=--help builds/runs. Step 8 (fleet go.work './feedwatch/src' -> './feedwatch') is outside this repo and left for the workspace owner, as is committing/tagging.
