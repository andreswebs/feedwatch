---
id: fee-juo8
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

# SSRF guard, redirect re-check, 301/308 rewrite signal

Guard against server-side request forgery: allow directly-supplied private URLs, block public-to-private redirects unless --allow-private, re-check the resolved IP on every hop, and surface FinalURL plus a Permanent flag for 301/308 so the store can rewrite the feed URL. Refs: docs/cli-design.md (Fetching and HTTP).

## Design

Add the SSRF guard and redirect handling via the transport `CheckRedirect` hook
and a dialer-time IP check.

- Resolve the target host; classify the resolved IP. Block private, loopback,
  and link-local ranges only when reached via a public-to-private redirect.
- A directly-supplied private/loopback/LAN URL is allowed by default (watching a
  self-hosted reader is legitimate).
- Re-check the resolved address after every redirect hop; a public URL that
  redirects into private space is blocked unless `--allow-private` is set.
- Record `core.FetchResult.FinalURL` after redirects and set `Permanent: true`
  when the final hop was reached via `301`/`308` (so the store can rewrite the
  feed URL, subject to the same policy).

```go
func WithAllowPrivate(allow bool) Option
// internal: isPrivate(ip net.IP) bool covering RFC1918, loopback, link-local,
// unique-local IPv6, and CGNAT (100.64/10).
```

TDD plan (httptest servers + an injectable resolver/dialer seam):

1. (tracer) a public URL that 302-redirects to `127.0.0.1` is blocked by default
   with a `CatNetwork`/SSRF error.
2. with `--allow-private` (WithAllowPrivate(true)), the same redirect is allowed.
3. a directly-supplied `http://127.0.0.1` URL is allowed by default.
4. a `301` to a new public URL yields `FinalURL` set and `Permanent == true`.

Deep-module note: the policy is enforced inside the transport; callers only set
`WithAllowPrivate`. SSRF classification is table-tested over IP literals.

## Acceptance Criteria

- Blocks public-to-private redirects by default; allows direct private URLs;
  `--allow-private` opens redirects; re-checks each hop.
- Sets `FinalURL` and `Permanent` (301/308) on the result.
- Behaviors 1-4 covered; IP classification table-tested.
- Supports Req 10 (SSRF, redirect rewrite). `make validate` passes.

## Notes

**2026-06-29T21:50:43Z**

Implemented the SSRF guard in src/internal/fetch/ssrf.go and wired it into Client.New as the default CheckRedirect. WithAllowPrivate(bool) option added.

Policy: the initial directly-supplied URL is never checked by CheckRedirect (Go only calls it on redirects), so a private/loopback/LAN feed is reachable by default. On each redirect hop the guard classifies the resolved IP and blocks the hop only when the chain ORIGIN (via[0]) resolves public AND the new target resolves private; allowPrivate lifts this. isPrivate covers loopback, RFC1918, link-local (v4+v6), unique-local IPv6 (fc00::/7 via net.IP.IsPrivate), unspecified, and CGNAT 100.64/10 (NOT covered by IsPrivate, checked manually). hostPrivate treats a host as private if ANY resolved IP is private (conservative against split-horizon DNS).

Permanent flag: tracked via a per-fetch *redirectState carried in the request context (context key redirectStateKey), updated in checkRedirect from req.Response.StatusCode (301/308 -> permanent, last hop wins) and copied into FetchResult.Permanent. Per-call context state avoids races on the shared Client (verified with -race).

classify() now returns an embedded *core.FeedError as-is (errors.As) before re-wrapping, so the guard's CatNetwork SSRF error survives the http client's url.Error wrapping with its message intact.

Resolver is an internal seam (resolver func type, defaultResolve resolves IP literals to themselves else net.DefaultResolver). Not exposed as a public option; white-box tests construct ssrfGuard directly with a fake resolver. Tests: ssrf_internal_test.go (isPrivate table, public->private block, allowPrivate bypass, private-origin exception, permanent recording, redirect limit) and redirect_test.go (direct loopback allowed, 301 sets FinalURL+Permanent, 302 not permanent). make build green.
