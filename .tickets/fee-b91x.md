---
id: fee-b91x
status: closed
deps: [fee-63n9, fee-chr5]
links: []
created: 2026-06-29T18:45:22Z
type: task
priority: 1
assignee: Andre Silva
parent: fee-8cau
tags: [http]
---

# HTTP client (transport, timeouts, proxy, TLS)

Implement the base fetch.Fetcher over net/http: a configured transport with connect and overall timeouts, user-agent, proxy (explicit or env), custom CA bundle, and minimum TLS version, built with functional options. Conditional GET, SSRF, retry, and charset layer onto this. Refs: docs/cli-design.md (Fetching and HTTP, Configuration).

## Design

Package `src/internal/fetch`. Implements `fetch.Fetcher` over `net/http` with a
configured `*http.Transport`. Constructed with functional options so call sites
name only what they override.

```go
type Fetcher struct{ /* unexported: client, ua, connectTimeout, overall ... */ }

type Option func(*Fetcher)

func WithUserAgent(ua string) Option
func WithConnectTimeout(d time.Duration) Option
func WithTimeout(d time.Duration) Option        // overall per-request deadline
func WithProxy(url string) Option               // explicit; else ProxyFromEnvironment
func WithCABundle(path string) Option
func WithMinTLS(v uint16) Option
func WithCheckRedirect(fn func(req, via) error) Option // SSRF hook (separate ticket)

func New(opts ...Option) (*Fetcher, error)

func (f *Fetcher) Fetch(ctx context.Context, req core.FetchRequest) (core.FetchResult, error)
```

Transport wiring:

- `DialContext` uses the connect timeout; the overall deadline is applied via a
  per-call `context.WithTimeout(ctx, overall)`.
- Proxy: `WithProxy` sets an explicit proxy; otherwise
  `http.ProxyFromEnvironment` (honors `HTTP_PROXY`/`HTTPS_PROXY`/`NO_PROXY`).
- `WithCABundle` loads a PEM file into a `*x509.CertPool` -> `tls.Config.RootCAs`.
- `WithMinTLS` sets `tls.Config.MinVersion` (default TLS 1.2).
- Sends the configured `User-Agent` and `Accept-Encoding` (let stdlib handle
  gzip transparently).

This base ticket establishes `Fetch` returning status, body bytes (raw, decoding
is the charset ticket), `FinalURL`, ETag/Last-Modified headers, and a populated
`core.FetchResult`. Conditional GET, SSRF/redirects, retry, and charset are
layered by their own tickets onto this client.

TDD plan (drive against `net/http/httptest.Server`; no live network):

1. (tracer) `Fetch` of a 200 endpoint returns the body bytes and status 200.
2. the request carries the configured `User-Agent` header.
3. the overall timeout: a handler that sleeps beyond the deadline yields a
   timeout error classified later as `CatTimeout`.
4. `WithMinTLS` is set on the transport (assert via the client's TLS config).

Deep-module note: callers depend only on `fetch.Fetcher`; the transport and TLS
wiring are internal. Cross-epic gate: this lane is the entry task and depends on
the E1 epic (interfaces in `core`).

## Acceptance Criteria

- `New(opts...)` builds a `*Fetcher` implementing `fetch.Fetcher` with functional
  options for user-agent, connect/overall timeouts, proxy, CA bundle, min-TLS.
- Honors standard proxy env vars when no explicit proxy is set; min-TLS defaults
  to 1.2.
- Behaviors 1-4 covered against httptest.
- Supports Req 10 (timeouts, UA, TLS, proxy) and the HTTP base for 11.
  `make validate` passes.

## Notes

**2026-06-29T21:21:13Z**

Implemented the base HTTP Fetcher in src/internal/fetch/http.go. NAMING: concrete type is fetch.Client (NOT Fetcher) because fetch.Fetcher is the existing consumer interface in fetch.go; the design block's 'type Fetcher struct' collides with it. New(...Option) (*Client, error) with WithUserAgent/WithConnectTimeout/WithTimeout/WithProxy/WithCABundle/WithMinTLS/WithCheckRedirect. Transport: DialContext+TLSHandshakeTimeout use the connect timeout; overall deadline is context.WithTimeout in Fetch; proxy defaults to http.ProxyFromEnvironment (honors HTTP(S)_PROXY/NO_PROXY) unless WithProxy set; CA bundle -> x509 pool; MinTLS default tls.VersionTLS12. Fetch returns raw body (no charset decode), Status, FinalURL (resp.Request.URL after redirects), ETag/Last-Modified, and bare MIMEType (mime.ParseMediaType strips charset). Errors classified at the boundary: deadline/net.Timeout -> core.TimeoutErr (CatTimeout), else core.NetworkErr (CatNetwork). WithCheckRedirect is the SSRF hook seam (fee-juo8); conditional-GET request headers (fee-t2ra), retry (fee-r0rt), charset (fee-fp2h), SSRF (fee-juo8) layer on later. Tests: external httptest for body/UA/timeout/unreachable + New rejects bad proxy/missing CA; white-box fetch_internal_test.go asserts transport MinTLS/dialer/CheckRedirect wiring. G304 on os.ReadFile(caBundle) is a justified //nolint:gosec (trusted operator config path).
