---
id: fee-cmkb
status: closed
deps: [fee-4m17]
links: []
created: 2026-06-29T15:36:08Z
type: task
priority: 0
assignee: Andre Silva
parent: fee-63n9
tags: [cli]
---

# Configuration struct and defaults

Define the resolved configuration value and its defaults (requirements Appendix
A), independent of flag parsing. The `cli` package fills it from urfave flags and
env; every other component receives an immutable Config. Refs: docs/cli-design.md
(Configuration); docs/specs/001-initial-implementation/requirements.md (Appendix A).

## Design

Package `src/internal/config`. No flag parsing here (that is the cli ticket);
this is the resolved value plus the single source of the documented defaults.

```go
type Config struct {
    Store            string        // FEEDWATCH_DB: path or postgres:// DSN
    UserAgent        string
    DefaultInterval  time.Duration // 1h
    Concurrency      int           // 8
    ConnectTimeout   time.Duration // 5s
    Timeout          time.Duration // 30s (overall per-feed)
    PerHostDelay     time.Duration // 1s
    RetryAttempts    int           // 3
    FailureThreshold int           // 10
    MaxBackoff       time.Duration // 24h
    MinTLS           uint16        // tls.VersionTLS12
    Proxy            string
    CABundle         string
    AllowPrivate     bool
    Format           string        // "json" | "text"; default "json"
    NoColor          bool
    LogLevel         slog.Level    // default Info
    Quiet            bool
}

func Defaults() Config           // exactly the requirements Appendix A values
func (c Config) Validate() error // ranges; wraps core.ErrConfig on failure
```

Notes:

- `Defaults()` is the single source of the documented default table; the cli
  ticket copies it and overlays flags/env (precedence flags > env > defaults).
- `MinTLS` is resolved from `"1.2"`/`"1.3"` in the cli layer; Config stores the
  resolved `uint16`.

TDD plan:

1. (tracer) `Defaults()` returns the documented Appendix A values.
2. `Validate()` rejects `Concurrency < 1`, non-positive timeouts, and an unknown
   `Format`, returning an error where `errors.Is(err, core.ErrConfig)`.
3. `Validate()` accepts `Defaults()`.

Deep-module note: Config is a value; precedence and flag mapping are the cli
ticket's concern.

## Acceptance Criteria

- Config, `Defaults()`, `Validate()` defined; `Defaults()` matches requirements
  Appendix A exactly.
- `Validate()` rejects bad ranges as `core.ErrConfig` (behavior 2) and accepts
  defaults (behavior 3).
- No flag or env parsing in this package.
- Supports Req 5. `make validate` passes.

## Notes

**2026-06-29T20:30:26Z**

Implemented config.Config, Defaults(), and Validate() in src/internal/config/config.go (config_test.go uses external config_test pkg, TDD red->green). Defaults() matches requirements Appendix A exactly for the listed settings (concurrency 8, interval 1h, connect 5s, timeout 30s, per-host delay 1s, retries 3, failure threshold 10, max backoff 24h, MinTLS=tls.VersionTLS12, AllowPrivate false, Format json, LogLevel Info). Validate() rejects Concurrency<1, non-positive ConnectTimeout/Timeout, and unknown Format, wrapping core.ErrConfig with %w (verified via errors.Is). No flag/env parsing here per design. Notable decision: Store is left EMPTY in Defaults() and UserAgent defaults to the const 'feedwatch' -- resolving the XDG $XDG_STATE_HOME/feedwatch/feedwatch.db path involves env/filesystem lookups, which is the cli layer's job (fee-7ons), keeping this package a pure value with no env reads. MinTLS stored as resolved uint16; the cli layer maps '1.2'/'1.3' strings. make build passes (vet, lint 0 issues, tests green).
