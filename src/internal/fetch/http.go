package fetch

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/andreswebs/feedwatch/internal/core"
)

const (
	defaultUserAgent      = "feedwatch"
	defaultConnectTimeout = 5 * time.Second
	defaultTimeout        = 30 * time.Second
	defaultRetryAttempts  = 1
	defaultRetryBackoff   = 500 * time.Millisecond
)

// Client is a Fetcher that retrieves feeds over a configured net/http
// transport, decoding response bodies to UTF-8. Conditional GET, the SSRF
// guard, and retry are layered onto this base client by their own components;
// callers depend only on the Fetcher interface.
type Client struct {
	userAgent      string
	connectTimeout time.Duration
	overall        time.Duration
	proxy          string
	caBundle       string
	minTLS         uint16
	allowPrivate   bool
	retryAttempts  int
	retryBackoff   time.Duration
	resolve        resolver
	checkRedirect  func(req *http.Request, via []*http.Request) error
	sleep          func(ctx context.Context, d time.Duration) error

	client *http.Client
}

// Option configures a Client at construction so call sites name only what
// they override.
type Option func(*Client)

// WithUserAgent sets the User-Agent header sent on every request.
func WithUserAgent(ua string) Option {
	return func(f *Client) { f.userAgent = ua }
}

// WithConnectTimeout bounds the dial (connect) phase of each request.
func WithConnectTimeout(d time.Duration) Option {
	return func(f *Client) { f.connectTimeout = d }
}

// WithTimeout sets the overall per-request deadline covering connect, TLS,
// headers, and body read.
func WithTimeout(d time.Duration) Option {
	return func(f *Client) { f.overall = d }
}

// WithProxy routes requests through an explicit proxy URL. When unset, the
// standard HTTP_PROXY/HTTPS_PROXY/NO_PROXY environment variables are honored.
func WithProxy(rawURL string) Option {
	return func(f *Client) { f.proxy = rawURL }
}

// WithCABundle loads an additional PEM certificate bundle as the trust roots
// for TLS verification.
func WithCABundle(path string) Option {
	return func(f *Client) { f.caBundle = path }
}

// WithMinTLS sets the minimum negotiated TLS version (e.g. tls.VersionTLS12).
func WithMinTLS(v uint16) Option {
	return func(f *Client) { f.minTLS = v }
}

// WithCheckRedirect installs the client's redirect policy, the hook the SSRF
// guard uses to re-check each resolved hop. When unset, the built-in SSRF guard
// is installed.
func WithCheckRedirect(fn func(req *http.Request, via []*http.Request) error) Option {
	return func(f *Client) { f.checkRedirect = fn }
}

// WithRetry enables a bounded in-call retry for transient failures. attempts is
// the total number of attempts within a single Fetch (1 disables retry);
// backoff is the fixed delay between attempts, overridden by a 429 Retry-After.
// This is distinct from the persisted cross-invocation failure lifecycle.
func WithRetry(attempts int, backoff time.Duration) Option {
	return func(f *Client) {
		f.retryAttempts = attempts
		f.retryBackoff = backoff
	}
}

// WithAllowPrivate lifts the SSRF redirect restriction, letting a public URL
// redirect into private address space. Directly-supplied private URLs are
// always allowed regardless of this setting.
func WithAllowPrivate(allow bool) Option {
	return func(f *Client) { f.allowPrivate = allow }
}

// New builds a Client from the given options. It returns an error when the
// proxy URL is malformed or the CA bundle cannot be loaded.
func New(opts ...Option) (*Client, error) {
	f := &Client{
		userAgent:      defaultUserAgent,
		connectTimeout: defaultConnectTimeout,
		overall:        defaultTimeout,
		minTLS:         tls.VersionTLS12,
		retryAttempts:  defaultRetryAttempts,
		retryBackoff:   defaultRetryBackoff,
	}
	for _, opt := range opts {
		opt(f)
	}

	if f.retryAttempts < 1 {
		f.retryAttempts = 1
	}
	if f.retryBackoff <= 0 {
		f.retryBackoff = defaultRetryBackoff
	}
	if f.sleep == nil {
		f.sleep = sleepContext
	}
	if f.resolve == nil {
		f.resolve = defaultResolve
	}
	if f.checkRedirect == nil {
		guard := &ssrfGuard{allowPrivate: f.allowPrivate, resolve: f.resolve}
		f.checkRedirect = guard.checkRedirect
	}

	tlsConfig := &tls.Config{MinVersion: f.minTLS}
	if f.caBundle != "" {
		pool, err := loadCABundle(f.caBundle)
		if err != nil {
			return nil, err
		}
		tlsConfig.RootCAs = pool
	}

	proxyFn := http.ProxyFromEnvironment
	if f.proxy != "" {
		u, err := url.Parse(f.proxy)
		if err != nil {
			return nil, fmt.Errorf("fetch: invalid proxy URL %q: %w", f.proxy, err)
		}
		proxyFn = http.ProxyURL(u)
	}

	transport := &http.Transport{
		Proxy:                 proxyFn,
		DialContext:           (&net.Dialer{Timeout: f.connectTimeout}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   f.connectTimeout,
		ExpectContinueTimeout: time.Second,
		TLSClientConfig:       tlsConfig,
	}

	f.client = &http.Client{
		Transport:     transport,
		CheckRedirect: f.checkRedirect,
	}
	return f, nil
}

// Fetch performs a GET for the requested feed, applying the overall deadline
// and returning a populated FetchResult. Request validators are echoed as
// If-None-Match and If-Modified-Since, and a 304 yields a bodyless result with
// NotModified set. The body is decoded to UTF-8 by an explicit charset
// precedence (BOM, XML declaration, Content-Type, lossy fallback) before it is
// returned, so the parser always receives UTF-8.
func (f *Client) Fetch(ctx context.Context, req core.FetchRequest) (core.FetchResult, error) {
	ctx, cancel := context.WithTimeout(ctx, f.overall)
	defer cancel()

	var lastErr error
	delay := time.Duration(0)
	for attempt := 0; attempt < f.retryAttempts; attempt++ {
		if attempt > 0 {
			if err := f.sleep(ctx, delay); err != nil {
				if lastErr != nil {
					return core.FetchResult{}, lastErr
				}
				return core.FetchResult{}, classify(req.URL, err)
			}
		}

		res, retryAfter, err := f.attempt(ctx, req)
		last := attempt == f.retryAttempts-1
		switch {
		case err != nil:
			if last || !isTransient(0, err) {
				return core.FetchResult{}, err
			}
			lastErr, delay = err, f.retryBackoff
		case res.NotModified || (res.Status >= 200 && res.Status < 300):
			return res, nil
		default:
			httpErr := core.HTTPErr(req.URL, res.Status, fmt.Errorf("server returned HTTP %d", res.Status))
			if last || !isTransient(res.Status, nil) {
				return core.FetchResult{}, httpErr
			}
			lastErr, delay = httpErr, f.retryBackoff
			if retryAfter > 0 {
				delay = retryAfter
			}
		}
	}
	return core.FetchResult{}, lastErr
}

// attempt performs a single GET and maps the outcome to a FetchResult. A 304
// yields a bodyless NotModified result and a 2xx yields a fully decoded body;
// any other status is returned with only its Status populated so the retry loop
// can classify it, along with the parsed Retry-After delay on a 429.
func (f *Client) attempt(ctx context.Context, req core.FetchRequest) (core.FetchResult, time.Duration, error) {
	rs := &redirectState{}
	ctx = context.WithValue(ctx, redirectStateKey, rs)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, req.URL, nil)
	if err != nil {
		return core.FetchResult{}, 0, core.NetworkErr(req.URL, err)
	}
	httpReq.Header.Set("User-Agent", f.userAgent)
	if req.ETag != "" {
		httpReq.Header.Set("If-None-Match", req.ETag)
	}
	if req.LastModified != "" {
		httpReq.Header.Set("If-Modified-Since", req.LastModified)
	}

	resp, err := f.client.Do(httpReq)
	if err != nil {
		return core.FetchResult{}, 0, classify(req.URL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotModified {
		return core.FetchResult{
			NotModified: true,
			Status:      resp.StatusCode,
			FinalURL:    resp.Request.URL.String(),
			Permanent:   rs.permanent,
		}, 0, nil
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var retryAfter time.Duration
		if resp.StatusCode == http.StatusTooManyRequests {
			retryAfter = parseRetryAfter(resp.Header.Get("Retry-After"))
		}
		return core.FetchResult{
			Status:    resp.StatusCode,
			FinalURL:  resp.Request.URL.String(),
			Permanent: rs.permanent,
		}, retryAfter, nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return core.FetchResult{}, 0, classify(req.URL, err)
	}

	utf8Body, mt := decodeBody(body, resp.Header.Get("Content-Type"))
	return core.FetchResult{
		Status:       resp.StatusCode,
		FinalURL:     resp.Request.URL.String(),
		Permanent:    rs.permanent,
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
		Body:         utf8Body,
		MIMEType:     mt,
	}, 0, nil
}

// classify maps a transport-level error to a feed-scoped FeedError, separating
// deadline and timeout failures from other network failures.
func classify(feedURL string, err error) *core.FeedError {
	var fe *core.FeedError
	if errors.As(err, &fe) {
		return fe
	}
	var netErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
		return core.TimeoutErr(feedURL, err)
	}
	return core.NetworkErr(feedURL, err)
}

// mediaType strips any parameters (such as charset) from a Content-Type header,
// leaving the bare media type. An unparseable value is returned verbatim.
func mediaType(contentType string) string {
	if contentType == "" {
		return ""
	}
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return contentType
	}
	return mt
}

// loadCABundle reads a PEM file into a certificate pool used as the TLS trust
// roots.
func loadCABundle(path string) (*x509.CertPool, error) {
	// The path is operator-supplied configuration (--ca-bundle / FEEDWATCH_*),
	// never network or item input, so reading it is intentional and trusted.
	pem, err := os.ReadFile(path) //nolint:gosec // G304: trusted operator config path

	if err != nil {
		return nil, fmt.Errorf("fetch: reading CA bundle %q: %w", path, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("fetch: no certificates found in CA bundle %q", path)
	}
	return pool, nil
}
