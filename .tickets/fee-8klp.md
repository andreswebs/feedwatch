---
id: fee-8klp
status: closed
deps: []
links: [fee-r1kt]
created: 2026-07-04T12:21:07Z
type: feature
priority: 3
assignee: Andre Silva
tags: [poll, cli]
---
# poll: include failure detail message in failures[] entries

## Context

First-customer feedback (v0.0.1): a poll failure entry is currently

```json
{ "feed_url": "https://example.com/feed", "category": "network", "status": 0 }
```

For `category: "parse"` the customer wants what failed to parse; for
`category: "network"` the error type (timeout, DNS, connection refused). That
detail decides whether an agent retries, disables, or removes the feed. The
information already exists in the process: the full human message goes to
stderr via `r.Errors(feedErrs)`, but the stdout envelope, the programmatic
contract, drops it.

## Investigation notes

- `PollFailure` (`src/internal/cli/poll.go`) carries only `FeedURL`,
  `Category`, `Status`. It is built from `*core.FeedError`, which also has
  `Message string` and the wrapped cause `Err error`
  (`src/internal/core/errors.go`).
- What the cause contains today:
  - network: the `net/http` error string (DNS lookup failure, connection
    refused, reset), from `core.NetworkErr` in
    `src/internal/fetch/http.go:235` and the SSRF guard messages in
    `src/internal/fetch/ssrf.go`.
  - timeout: already its own category (`core.TimeoutErr`,
    `src/internal/fetch/http.go:298`), so "timeout vs other network error" is
    already distinguishable by `category`.
  - http: `server returned HTTP <status>` plus the `status` field.
  - parse: gofeed's parse error, via `core.ParseErr`
    (`src/internal/parse/gofeed.go:40`).
- `FeedError.Error()` renders `<category> <feed_url> (status): <message>`;
  reusing it in the envelope would duplicate the `feed_url`/`category`/
  `status` fields. The envelope needs the bare detail only.

## Design

1. Add a detail accessor on `core.FeedError`
   (`src/internal/core/errors.go`), so the "message or wrapped cause"
   fallback logic lives in one place (the same rule `Error()` applies):

    ```go
    // Detail returns the bare human detail of the error: the explicit
    // Message, falling back to the wrapped cause. It omits the category,
    // feed URL, and status head that Error() prepends, for callers that
    // carry those as structured fields already.
    func (e *FeedError) Detail() string {
        if e.Message != "" {
            return e.Message
        }
        if e.Err != nil {
            return e.Err.Error()
        }
        return ""
    }
    ```

2. Extend `PollFailure` in `src/internal/cli/poll.go`:

    ```go
    type PollFailure struct {
        FeedURL  string        `json:"feed_url"`
        Category core.Category `json:"category"`
        Status   int           `json:"status,omitempty"`
        Message  string        `json:"message"`
    }
    ```

    Populate `Message: fe.Detail()` in the `pollAction` loop. Keep `message`
    non-omitempty so the key is always present (agent-first contract:
    deterministic shape). The `schema poll` output updates automatically via
    `jsonschema.Reflect(PollResult{})`.

3. If the `check` command (fee-r1kt) has landed, use the same `Detail()`
   helper for its `failures[].message`; if this ticket lands first, note in
   fee-r1kt that the helper exists.

4. Docs: update the poll failures example in `docs/usage.md` to show
   `message`, with one network and one parse example, and note that `timeout`
   is its own category (no need to parse the message to detect timeouts).

## TDD plan

1. `src/internal/core/errors_test.go`: `Detail()` prefers `Message`, falls
   back to `Err.Error()`, returns empty for neither.
2. `src/internal/cli/poll_test.go` (httptest): a feed serving HTML yields a
   `parse` failure whose `message` is non-empty (contains gofeed's detect
   failure text); a connection-refused feed yields a `network` failure with a
   non-empty `message`; an HTTP 500 feed yields `category:"http"`,
   `status:500`, and `message` containing `500`.
3. Schema: `schema poll` lists `message` as a required property of the
   failures element.

## Acceptance criteria

- Every `failures[]` entry in the poll envelope carries a `message` with the
  underlying error detail; existing fields unchanged.
- Detail logic is shared via `FeedError.Detail()`, not duplicated.
- `docs/usage.md` updated.
- `make build` passes.

## Notes

**2026-07-04T15:47:05Z**

Added Detail() method on *core.FeedError in core/errors.go (prefers Message, falls back to Err.Error(), returns empty for neither). Added Message string field to PollFailure in cli/poll.go, populated via fe.Detail() in shapePollResult. Updated two e2e golden files (all_failed/poll.stdout, partial/poll.stdout) to include the message field. Updated docs/usage.md to document message in the failures[] contract and show a two-failure example. Note for fee-r1kt: Detail() helper already exists in core.FeedError.
