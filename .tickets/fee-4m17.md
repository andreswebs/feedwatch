---
id: fee-4m17
status: closed
deps: [fee-mls1]
links: []
created: 2026-06-29T15:36:08Z
type: task
priority: 0
assignee: Andre Silva
parent: fee-63n9
tags: [core]
---

# Error taxonomy

Define the typed error model in `src/internal/core`: the Category enum, the
structured FeedError with `Unwrap`, the package sentinels, and the `ExitCodeFor`
helper. This underpins the single error boundary and the 0/1/2/3 exit codes.
Refs: docs/cli-design.md (Error Handling and Logging, Exit Codes).

## Design

Package `src/internal/core` (same package as the domain types).

```go
type Category string

const (
    CatNetwork  Category = "network"
    CatHTTP     Category = "http"
    CatParse    Category = "parse"
    CatTimeout  Category = "timeout"
    CatUsage    Category = "usage"
    CatConfig   Category = "config"
    CatStore    Category = "store"
    CatInternal Category = "internal"
)

type FeedError struct {
    FeedURL  string   // empty for non-feed-scoped errors
    Category Category
    Status   int      // HTTP status when Category == CatHTTP, else 0
    Message  string   // human message; falls back to Err.Error()
    Err      error    // wrapped cause
}

func (e *FeedError) Error() string  // "<category> <feed_url> (status): <message>"
func (e *FeedError) Unwrap() error { return e.Err }

// constructors
func NetworkErr(url string, err error) *FeedError
func HTTPErr(url string, status int, err error) *FeedError
func ParseErr(url string, err error) *FeedError
func TimeoutErr(url string, err error) *FeedError

// sentinels (matched with errors.Is at the boundary)
var (
    ErrUsage            = errors.New("usage error")
    ErrConfig           = errors.New("configuration error")
    ErrStoreUnavailable = errors.New("store unavailable")
    ErrSchemaTooNew     = errors.New("schema version newer than supported")
)

// ExitCodeFor maps a whole-invocation error to a process exit code.
// Usage/config/store/internal -> 1. A purely feed-scoped *FeedError -> 0
// (feed outcomes drive 2/3 from the poll aggregate, not from an error).
func ExitCodeFor(err error) int
```

JSON shapes rendered by the `output` package:

```json
{"error":{"category":"http","feed_url":"...","status":404,"message":"..."}}
{"errors":[ {"category":"...","feed_url":"...","status":0,"message":"..."} ]}
```

TDD plan:

1. (tracer) `errors.As` recovers `*FeedError` through a `%w`-wrapped chain;
   Category and Status are readable.
2. `errors.Is` matches each sentinel through wrapping.
3. `ExitCodeFor`: `ErrUsage`/`ErrConfig`/`ErrStoreUnavailable` map to 1; a
   `CatHTTP` feed-scoped `FeedError` maps to 0.
4. `Error()` is lowercase-leading, no trailing punctuation (Go style).

Deep-module note: callers classify only via `errors.As` / `errors.Is` /
`ExitCodeFor`, never by string matching.

## Acceptance Criteria

- Category, `*FeedError` (with `Unwrap`), constructors, sentinels, `ExitCodeFor`
  defined.
- `errors.As` / `errors.Is` work through `%w` chains (behaviors 1-2).
- `ExitCodeFor` maps sentinels to 1 and feed-scoped errors to 0 (behavior 3).
- Error strings lowercase, no trailing punctuation.
- Supports Req 3 (exit codes; categories network/http/parse/timeout).
  `make validate` passes.

## Notes

**2026-06-29T20:25:23Z**

Implemented error taxonomy in src/internal/core/errors.go (errors_test.go). Added: Category enum (network/http/parse/timeout/usage/config/store/internal); \*FeedError{FeedURL,Category,Status,Message,Err} with Unwrap; constructors NetworkErr/HTTPErr/ParseErr/TimeoutErr (all feed-scoped, wrap cause); sentinels ErrUsage/ErrConfig/ErrStoreUnavailable/ErrSchemaTooNew; ExitCodeFor. Error() renders `'<category> <url> (status): <message>'`, lowercase-leading, no trailing punctuation, Message-then-Err.Error() fallback. ExitCodeFor: nil and purely feed-scoped FeedError (network/http/parse/timeout) -> 0; the four sentinels and FeedError of category usage/config/store/internal -> 1; unknown errors default to 1 (hard whole-invocation failure). Verified errors.As/errors.Is traverse %w chains. make build green. Note for boundary impl (fee-7ons): classify only via errors.As/errors.Is/ExitCodeFor; 2/3 come from the poll aggregate, not from a returned error.
