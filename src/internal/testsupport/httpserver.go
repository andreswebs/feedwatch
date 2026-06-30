package testsupport

import (
	"net/http"
	"net/http/httptest"
	"sync"
)

// Endpoint is a canned HTTP response for a path served by a FeedServer. A 200
// status and an "application/rss+xml" content type are used when those fields
// are left zero. When ETag or LastModified is set, a request carrying the
// matching If-None-Match or If-Modified-Since validator is answered with 304.
type Endpoint struct {
	Body         string
	ContentType  string
	ETag         string
	LastModified string
	Status       int
}

// FeedServer is an httptest.Server wrapper that serves registered feed bodies
// with conditional-GET semantics, counts hits per path, and records the last
// request headers, mirroring a conditional-GET test harness. It is safe for
// concurrent use.
type FeedServer struct {
	server    *httptest.Server
	mu        sync.Mutex
	endpoints map[string]Endpoint
	hits      map[string]int
	lastReq   map[string]http.Header
}

// NewFeedServer starts a FeedServer with no registered endpoints.
func NewFeedServer() *FeedServer {
	fs := &FeedServer{
		endpoints: make(map[string]Endpoint),
		hits:      make(map[string]int),
		lastReq:   make(map[string]http.Header),
	}
	fs.server = httptest.NewServer(http.HandlerFunc(fs.handle))
	return fs
}

func (fs *FeedServer) handle(w http.ResponseWriter, r *http.Request) {
	fs.mu.Lock()
	fs.hits[r.URL.Path]++
	fs.lastReq[r.URL.Path] = r.Header.Clone()
	ep, ok := fs.endpoints[r.URL.Path]
	fs.mu.Unlock()

	if !ok {
		http.NotFound(w, r)
		return
	}

	if validatorMatches(r, ep) {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	if ep.ETag != "" {
		w.Header().Set("ETag", ep.ETag)
	}
	if ep.LastModified != "" {
		w.Header().Set("Last-Modified", ep.LastModified)
	}
	ct := ep.ContentType
	if ct == "" {
		ct = "application/rss+xml"
	}
	w.Header().Set("Content-Type", ct)
	status := ep.Status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = w.Write([]byte(ep.Body))
}

// validatorMatches reports whether the request's conditional-GET validators
// match the endpoint's, so the server should answer 304.
func validatorMatches(r *http.Request, ep Endpoint) bool {
	if ep.ETag != "" && r.Header.Get("If-None-Match") == ep.ETag {
		return true
	}
	if ep.LastModified != "" && r.Header.Get("If-Modified-Since") == ep.LastModified {
		return true
	}
	return false
}

// Register sets the canned response served for path (e.g. "/feed.xml").
func (fs *FeedServer) Register(path string, ep Endpoint) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.endpoints[path] = ep
}

// URL returns the absolute URL for a registered path.
func (fs *FeedServer) URL(path string) string { return fs.server.URL + path }

// Hits returns how many requests path has received.
func (fs *FeedServer) Hits(path string) int {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.hits[path]
}

// LastRequest returns the headers of the most recent request to path, or nil
// when path has not been requested.
func (fs *FeedServer) LastRequest(path string) http.Header {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.lastReq[path]
}

// Close shuts down the underlying server.
func (fs *FeedServer) Close() { fs.server.Close() }
