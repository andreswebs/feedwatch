---
id: fee-ev4k
status: closed
deps: []
links: []
created: 2026-06-30T01:28:21Z
type: task
priority: 1
assignee: Andre Silva
parent: fee-299l
tags: [build]
---

# Version stamping: -X injection, version package, reproducible build flags

Make the version computed by the Makefile actually get stamped into the binary.

Today the Makefile computes `VERSION` via `git describe` but discards it:
`LDFLAGS` is only `-s -w`, and `cmd/feedwatch/main.go` declares
`var version = "dev"` that nothing ever overrides. As a result `make build` and
even `make dist` release archives all report `version: "dev"`, and the
`-X main.version=...` injection promised in that file's comment never happens.
The consumer side (the `--version` JSON `{version, commit, go}` contract in
`internal/cli/version.go`) is already wired and stays as-is; only the producer
side (compute -> inject -> resolve) is missing.

## Design

Add a dedicated version package as the single source of truth, inject the
computed `VERSION` into it at link time, and source the CLI's version from it.
Module path is `github.com/andreswebs/feedwatch`. The existing `Deps`
dependency-injection seam in `internal/cli` is preserved.

### 1. New package `src/internal/version/version.go`

A single source of truth with a three-tier fallback:

```go
package version

import "runtime/debug"

var Override = ""

func Current() string {
    if Override != "" {
        return Override
    }
    // BuildInfo.Main.Version is populated by `go install ...@vX.Y.Z` but
    // reports "(devel)" for bare `go build`.
    if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
        return bi.Main.Version
    }
    return "dev"
}
```

Resolution order: link-time `Override` (set by `make build`/`make dist`) ->
`debug.ReadBuildInfo().Main.Version` (set by `go install ...@vX.Y.Z`) ->
`"dev"` (bare `go build`/`go test`).

### 2. New test `src/internal/version/version_test.go`

Three cases: `Override` wins; empty `Override` falls through to `"dev"`; the
fallback is never the empty string. Each saves and restores `Override` via
`t.Cleanup`.

```go
package version

import "testing"

func TestCurrent_Override(t *testing.T) {
    old := Override
    t.Cleanup(func() { Override = old })

    Override = "v9.9.9"
    got := Current()
    if got != "v9.9.9" {
        t.Errorf("Current() = %q, want %q", got, "v9.9.9")
    }
}

func TestCurrent_DevFallback(t *testing.T) {
    old := Override
    t.Cleanup(func() { Override = old })

    Override = ""
    got := Current()
    if got != "dev" {
        t.Errorf("Current() = %q, want %q", got, "dev")
    }
}

func TestCurrent_OverrideEmptyStringFallsThrough(t *testing.T) {
    old := Override
    t.Cleanup(func() { Override = old })

    Override = ""
    got := Current()
    if got == "" {
        t.Error("Current() returned empty string; expected non-empty fallback")
    }
    if got != "dev" {
        t.Errorf("Current() = %q, want %q", got, "dev")
    }
}
```

### 3. Makefile build-flag changes

Add a `BUILDFLAGS` variable and extend `LDFLAGS`:

```make
LDFLAGS     := -s -w -buildid= -X github.com/andreswebs/feedwatch/internal/version.Override=$(VERSION)
BUILDFLAGS  := -trimpath
```

Apply `$(BUILDFLAGS)` everywhere a build or run is invoked:

- `build-local`: `go build $(BUILDFLAGS) -ldflags="$(LDFLAGS)" ...`
- the `build-target` template (cross-compile recipes): same addition.
- `run`: `go run $(BUILDFLAGS) -ldflags="$(LDFLAGS)" ...`

`-buildid=` and `-trimpath` make builds reproducible; `-X` injects the
computed `VERSION` into the new package's `Override`. The existing `VERSION ?=`
line and `dist` staging are unchanged.

### 4. Wire `cmd/feedwatch/main.go` to the version package

Remove the unmanaged `var version = "dev"` and its stale comment. Source the
version from the package instead, keeping the `Deps` seam intact:

```go
import "github.com/andreswebs/feedwatch/internal/version"
// ...
deps := cli.Deps{
    // ...
    Version: version.Current(),
    // ...
}
```

`internal/cli` (the `Deps.Version` field, `installVersionPrinter`,
`writeVersion`, and the `vcs.revision` commit lookup) needs no change.

### Design notes

- `version.Current()` is called at the `main.go` composition root and threaded
  through the existing `Deps.Version` field, rather than called deeper in the
  command tree. This keeps the single source of truth while preserving the
  testable wiring already in place.
- The `commit` field in the `--version` output is independent of this change:
  it already comes from `debug.ReadBuildInfo()` VCS settings (`vcs.revision`)
  and is deliberately kept.

## Acceptance Criteria

- `src/internal/version` package exists with `Override`/`Current()` and tests;
  `make test` passes.
- Makefile sets `BUILDFLAGS := -trimpath` and `LDFLAGS` includes
  `-buildid=` and `-X github.com/andreswebs/feedwatch/internal/version.Override=$(VERSION)`,
  applied to `build-local`, the `build-target` template, and `run`.
- `cmd/feedwatch/main.go` no longer declares `var version`; it calls
  `version.Current()`.
- `make build` produces a binary whose `feedwatch --version` reports the
  `git describe` value (e.g. a tag or `v0.0.0-dev`), not `dev`.
- A bare `go build` / `go test` binary still reports `dev` (fallback intact).
- `make validate` passes (fmt-check, vet, lint, test).

## Notes

**2026-06-30T01:37:57Z**

Added src/internal/version package (Override var + Current() three-tier fallback: link-time Override -> debug.ReadBuildInfo Main.Version (skips empty and '(devel)') -> 'dev') with version_test.go (override-wins, dev-fallback, non-empty). Makefile: LDFLAGS now '-s -w -buildid= -X github.com/andreswebs/feedwatch/internal/version.Override=$(VERSION)' and new BUILDFLAGS := -trimpath, applied to build-local, the build-target cross-compile template, and run. main.go drops 'var version' and sets Deps.Version = version.Current(); internal/cli (Deps.Version, version printer, vcs.revision commit) unchanged. Verified: 'make build' -> --version reports the git describe value (dd320e4-dirty here, no tags yet), bare 'go build' still reports 'dev'. make build (full validate) green.
