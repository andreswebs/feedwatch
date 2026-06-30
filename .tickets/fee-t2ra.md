---
id: fee-t2ra
status: closed
deps: [fee-b91x]
links: []
created: 2026-06-29T18:45:22Z
type: task
priority: 1
assignee: Andre Silva
parent: fee-8cau
tags: [http]
---

# Conditional GET (ETag, If-Modified-Since, 304)

Add conditional-request support to the fetcher: send If-None-Match and If-Modified-Since from the request validators, map 304 to NotModified with no body, and capture response validators on 200. Refs: docs/cli-design.md (Poll Semantics, Fetching and HTTP).

## Design

Layer conditional requests onto the `fetch.Fetcher`.

- When `core.FetchRequest.ETag` is set, send `If-None-Match`; when
  `LastModified` is set, send `If-Modified-Since`.
- On `304 Not Modified`, return `core.FetchResult{NotModified: true}` with no
  body and skip any further processing.
- On `200`, capture the response `ETag` and `Last-Modified` into the result.
- The fetch layer reports validators; persisting only-the-changed validator and
  never overwriting a stored one with empty is the store/poll concern (see the
  poll tickets), not done here.

TDD plan (httptest endpoints asserting request headers):

1. (tracer) given a stored ETag, the request carries `If-None-Match`, and a
   handler returning `304` yields `FetchResult.NotModified == true`, no body.
2. given a stored Last-Modified, the request carries `If-Modified-Since`.
3. a `200` response surfaces the new `ETag`/`Last-Modified` in the result.
4. no stored validators -> no conditional headers sent.

Deep-module note: conditional logic lives inside `Fetch`; callers just pass and
read validators on `core.FetchRequest`/`core.FetchResult`.

## Acceptance Criteria

- Sends `If-None-Match`/`If-Modified-Since` from request validators; maps `304`
  to `NotModified` with no body; captures response validators on `200`.
- Behaviors 1-4 covered against httptest.
- Supports Req 10 (mandatory conditional GET). `make validate` passes.

## Notes

**2026-06-29T21:31:46Z**

Conditional GET layered into Client.Fetch (http.go). Sends If-None-Match from req.ETag and If-Modified-Since from req.LastModified only when non-empty (header presence, not empty values, is asserted in the no-validators test). On HTTP 304 returns core.FetchResult{NotModified:true, Status:304, FinalURL:...} early WITHOUT reading the body — so the body close defer still runs but io.ReadAll is skipped. 200 path already captured ETag/Last-Modified into the result (fee-b91x), so no change there. New tests in conditional_test.go (package fetch_test) cover all 4 behaviors via httptest. Persisting only-changed validators / never overwriting stored with empty remains a store/poll concern, not done here.
