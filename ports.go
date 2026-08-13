package feedwatch

import (
	"context"

	"github.com/andreswebs/feedwatch/core"
)

// Fetcher retrieves a single feed over HTTP, honoring conditional-GET
// validators and reporting a 304 without a body. Implementations decode the body
// to UTF-8 and classify failures as network, http, or timeout errors.
//
// It is declared here, alongside Store and Parser, so an embedder can name the
// port it replaces; the shipped adapter stays internal.
type Fetcher interface {
	// Fetch retrieves one feed body, sending the request's conditional-GET
	// validators. A 304 is reported in the result as not-modified, with no body.
	Fetch(ctx context.Context, req core.FetchRequest) (core.FetchResult, error)
}

// Parser turns a decoded feed body into a normalized core.ParsedFeed. baseURL
// resolves relative links.
type Parser interface {
	// Parse normalizes a decoded feed body into a core.ParsedFeed, resolving
	// relative links against baseURL. An unparseable body is a parse-category
	// error.
	Parse(ctx context.Context, body []byte, baseURL string) (core.ParsedFeed, error)
}

// Warner receives a non-fatal advisory raised during a use case: a stable
// machine code, a human message, an optional remediation hint, and optional
// per-instance details. A frontend renders it (the CLI as an NDJSON warning line
// on stderr); the library never writes to a stream itself.
type Warner func(code, message, hint string, details any)
