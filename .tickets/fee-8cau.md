---
id: fee-8cau
status: closed
deps: [fee-63n9, fee-b91x, fee-t2ra, fee-juo8, fee-r0rt, fee-fp2h]
links: []
created: 2026-06-29T15:36:07Z
type: epic
priority: 1
assignee: Andre Silva
tags: [http]
---

# E3: Fetching and HTTP

Epic. HTTP client, conditional GET, SSRF guard with per-hop redirect re-check and 301/308 rewrite, proxy/CA/min-TLS, transient retry classification, charset decode, and concurrency/politeness primitives. Covers requirements 10 and 11, the retry part of 15, and the charset part of 14. Refs: docs/cli-design.md (Fetching and HTTP, Concurrency and Politeness, Parsing and Robustness).

## Notes

**2026-06-29T22:21:36Z**

Epic complete: all five children closed with deliverables landed in src/internal/fetch (http.go client, fetch.go/conditional GET, retry.go classification, ssrf.go guard + redirect rewrite, charset.go decode), all with passing tests. Closing the epic.

**2026-06-29T22:21:53Z**

Closed as a verified dependency gate (no new code), same pattern as fee-63n9/E1. All five children closed and integrate under green 'make build'; fetch tests pass with -race. Verified the HTTP handoff contract for the downstream poll lane: core.FetchRequest carries ETag/LastModified (conditional GET, req 10); core.FetchResult carries NotModified (304 skip-parse), FinalURL+Permanent (301/308 stored-URL rewrite), ETag/LastModified validators, UTF-8-decoded Body (charset of req 14), and MIMEType. SSRF guard with per-hop redirect re-check, proxy/CA/min-TLS, and transient retry classification (retry of req 15) all delivered by children. The 'internal/fetch' package depends only on 'internal/core' (acyclic). NOTE: requirement 11 (worker pool, per-host serialization+delay, stable-order aggregation) is NOT in this epic's code; only the config knobs (Concurrency=8, PerHostDelay=1s) and a concurrency-safe Client exist here. That orchestration is owned by the poll lane fee-u0i4 (internal/poll is currently just doc.go), which E3 blocks. Closing E3 does not by itself make discover/add/poll ready: they are also blocked by E4 (fee-2heq) and E2 (fee-gyos).

**2026-06-29T22:23:08Z**

CORRECTION to the previous note's last bullet: fee-0l84 (discover) depends ONLY on the two parsing/fetching gates [fee-8cau, fee-2heq], not on E2. With E4 (fee-2heq) also now closed, fee-0l84 (discover) is READY. The add (fee-4q22) and poll-orchestration lanes are the ones additionally blocked by E2 (fee-gyos). So this gate close did contribute to unblocking discover.
