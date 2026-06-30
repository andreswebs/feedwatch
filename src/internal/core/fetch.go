package core

// FetchRequest is a conditional-GET request for a single feed. The validators
// are echoed back as If-None-Match and If-Modified-Since when non-empty.
type FetchRequest struct {
	URL          string
	ETag         string
	LastModified string
}

// FetchResult is the outcome of a feed fetch. On a 304 the body is empty and
// NotModified is true. FinalURL and Permanent let poll rewrite a feed's stored
// URL after a 301 or 308 redirect.
type FetchResult struct {
	NotModified  bool   // true on HTTP 304
	Status       int    // HTTP status code
	FinalURL     string // URL after following redirects
	Permanent    bool   // final hop reached via 301 or 308
	ETag         string // response validator, if any
	LastModified string // response validator, if any
	Body         []byte // response body, decoded to UTF-8
	MIMEType     string // media type from Content-Type
}
