---
id: fee-r0rt
status: closed
deps: [fee-b91x]
links: []
created: 2026-06-29T18:45:23Z
type: task
priority: 1
assignee: Andre Silva
parent: fee-8cau
tags: [http]
---

# Transient in-call retry classification

Add a bounded in-call retry for transient network errors (conn reset, timeout, temporary DNS, 5xx, 429 with Retry-After), never retrying deterministic errors, bounded by the overall deadline. This is separate from the persisted failure lifecycle in E5. Refs: docs/cli-design.md (Failure Handling, Fetching and HTTP).

## Design

Add a bounded in-call retry for transient errors, distinct from the persisted
cross-invocation failure lifecycle (that lives in E5).

- Retry up to `RetryAttempts` (default 3) within the same `Fetch` call, with a
  short backoff between attempts, bounded by the overall deadline.
- Transient classes: connection reset, timeout, temporary DNS failure, `5xx`,
  and `429`. On `429` with `Retry-After`, honor the delay (capped by the overall
  deadline).
- Never retry deterministic errors: `4xx` other than `429`, parse errors (not
  seen here), or SSRF-blocked URLs.

```go
func WithRetry(attempts int, backoff time.Duration) Option
// internal: isTransient(status int, err error) bool
```

TDD plan (httptest handler that fails N times then succeeds):

1. (tracer) a handler returning `503` twice then `200` succeeds within 3
   attempts.
2. a handler always returning `404` is not retried (one request).
3. `429` with `Retry-After: 1` waits then retries (use a fake clock / short
   value); capped by the overall deadline.
4. attempts are bounded: a handler always `503` stops after `RetryAttempts` and
   returns the last error.

Deep-module note: retry wraps `Fetch`; classification is table-tested; the
persisted failure-count/backoff/auto-disable is a separate concern (E5).

## Acceptance Criteria

- Bounded in-call retry (default 3) for transient classes only; honors
  `Retry-After`; never retries deterministic errors; bounded by the overall
  deadline.
- Behaviors 1-4 covered against httptest.
- Supports the retry part of Req 15 (distinct from the persisted lifecycle).
  `make validate` passes.

## Notes

**2026-06-29T22:17:28Z**

Implemented bounded in-call retry as a fetch.Client option (WithRetry(attempts, backoff)), opt-in: New() defaults to attempts=1 (no retry), so existing single-shot tests and callers are unchanged; the cli layer will overlay config.RetryAttempts (default 3) later.

Refactored Client.Fetch into a retry loop over a new single-attempt helper (attempt()). attempt() now only reads/decodes the body for 2xx; 304 stays a bodyless NotModified result; ANY other status returns a status-only FetchResult so the loop classifies it. This introduces HTTP-status->error mapping at the fetch boundary: a non-2xx/non-304 status that is not (or no longer) retried becomes a core.HTTPErr(url, status) (CatHTTP). Base Fetch previously returned 4xx/5xx as a successful result; no existing test or caller relied on that (poll/fee-u0i4 not yet built), and this gives poll a clean contract: 2xx/304 result or an error.

Transient classification (retry.go, isTransient(status, err)): 5xx and 429 statuses; timeout errors; network errors that wrap a net.Error (genuine transport failure). Deterministic (never retried): 4xx!=429, parse errors, and SSRF-blocked redirects (a CatNetwork *FeedError with no transport cause, fe.Err==nil) -> not transient. 429 honors Retry-After (delta-seconds and HTTP-date) over the fixed backoff; the wait is ctx-bounded (sleepContext selects on ctx.Done), so a large Retry-After is capped by the overall deadline. A transient failure on the LAST attempt returns the last error (HTTPErr for 5xx, the wrapped network/timeout err otherwise).

Test seam: unexported f.sleep field (defaults to sleepContext) lets the white-box Retry-After test observe the requested delay without sleeping. Behaviors 1-4 are black-box httptest with fastBackoff=1ms; isTransient is table-tested; parseRetryAfter unit-tested. go test -race clean.
