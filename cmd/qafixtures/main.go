// Command qafixtures is a controllable HTTP server for the feedwatch manual QA
// plan. It serves embedded RSS/Atom/JSON feed fixtures and
// scripted responses (arbitrary status codes, redirects, latency, conditional
// GET) so that the fetch, poll, failure, and SSRF test cases are reproducible
// over loopback without depending on the public internet.
//
// It is a QA helper, not part of the shipped binary: `make build` compiles only
// ./cmd/feedwatch, while `make vet`/`lint`/`test` still cover this package.
//
// Run it with `make qa-server` or `go run ./cmd/qafixtures` (add `--addr` to
// change the listen address). Routes:
//
//	GET /feeds/<name>          serve an embedded fixture; supports conditional
//	                           GET (ETag/If-None-Match, Last-Modified/
//	                           If-Modified-Since -> 304); ?ct= overrides the
//	                           Content-Type (used by the charset cases).
//	GET /status/<code>         respond with that HTTP status; for 429 a
//	                           ?retry-after= value (default 1) sets Retry-After.
//	GET /flaky/<name>?n=<k>    respond 503 for the first k requests to this
//	                           name, then serve the fixture (transient-retry).
//	GET /redirect?to=<url>&code=<3xx>  redirect to an arbitrary target (default
//	                           302); used for the 301/308 rewrite and the
//	                           public-to-private SSRF cases.
//	GET /slow/<name>?ms=<n>    wait n ms (default 2000), honoring client
//	                           cancellation, then serve the fixture (timeouts,
//	                           SIGINT-mid-poll, ordering).
//	GET /feed.xml, /index.xml  serve a fixture at a discover probe path so the
//	                           path-probing cases find a feed (/feed.xml) and a
//	                           non-feed the validator must reject (/index.xml).
package main

import (
	"embed"
	"flag"
	"fmt"
	"hash/crc32"
	"log"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed feeds
var feedsFS embed.FS

// fixedModTime is the Last-Modified stamp reported for every fixture. Embedded
// files have no modification time, so a constant keeps conditional-GET behavior
// deterministic across runs.
var fixedModTime = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

var (
	flakyMu   sync.Mutex
	flakyHits = map[string]int{}
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8099", "listen address")
	flag.Parse()

	srv := &http.Server{
		Addr:              *addr,
		Handler:           logRequests(newMux()),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("qafixtures listening on http://%s (routes at /)", *addr)
	log.Fatal(srv.ListenAndServe())
}

// newMux builds the fixture route table. It is split out from main so tests can
// exercise the routes without binding a socket.
func newMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/feeds/", handleFeeds)
	mux.HandleFunc("/status/", handleStatus)
	mux.HandleFunc("/flaky/", handleFlaky)
	mux.HandleFunc("/slow/", handleSlow)
	mux.HandleFunc("/redirect", handleRedirect)
	for _, pr := range probeRoutes {
		mux.HandleFunc(pr.path, func(w http.ResponseWriter, r *http.Request) {
			serveFeed(w, r, pr.fixture)
		})
	}
	mux.HandleFunc("/", handleIndex)
	return mux
}

// probeRoutes serve a fixture at the discover probe paths (a subset of
// discover.probePaths) so the manual path-probing cases have something to find:
// /feed.xml returns a real feed (a source="probe" candidate) and /index.xml
// returns a non-feed the validator must drop.
var probeRoutes = []struct {
	path    string
	fixture string
}{
	{"/feed.xml", "rss20.xml"},
	{"/index.xml", "sitemap.xml"},
}

// logRequests echoes each request and the conditional-GET headers, so a tester
// can confirm feedwatch sent If-None-Match / If-Modified-Since.
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		line := fmt.Sprintf("%s %s if-none-match=%q if-modified-since=%q",
			r.Method, r.URL.RequestURI(),
			r.Header.Get("If-None-Match"), r.Header.Get("If-Modified-Since"))
		log.Print(line) //nolint:gosec // G706: a QA fixture server intentionally logs request metadata; not a security boundary
		next.ServeHTTP(w, r)
	})
}

func handleFeeds(w http.ResponseWriter, r *http.Request) {
	serveFeed(w, r, strings.TrimPrefix(r.URL.Path, "/feeds/"))
}

// serveFeed writes an embedded fixture with conditional-GET support. The
// Content-Type follows the file extension unless overridden with ?ct=.
func serveFeed(w http.ResponseWriter, r *http.Request, name string) {
	data, err := feedsFS.ReadFile(path.Join("feeds", path.Clean("/" + name)[1:]))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	etag := fmt.Sprintf("%q", fmt.Sprintf("fixture-%08x", crc32.ChecksumIEEE(data)))
	w.Header().Set("ETag", etag)
	w.Header().Set("Last-Modified", fixedModTime.Format(http.TimeFormat))

	if inm := r.Header.Get("If-None-Match"); inm != "" && inm == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if ims := r.Header.Get("If-Modified-Since"); ims != "" {
		if t, perr := http.ParseTime(ims); perr == nil && !fixedModTime.After(t) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}

	ct := r.URL.Query().Get("ct")
	if ct == "" {
		ct = contentType(name)
	}
	w.Header().Set("Content-Type", ct)
	writeBody(w, data)
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	code, err := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/status/"))
	if err != nil || code < 100 || code > 599 {
		http.Error(w, "status must be 100-599", http.StatusBadRequest)
		return
	}
	if code == http.StatusTooManyRequests {
		retryAfter := r.URL.Query().Get("retry-after")
		if retryAfter == "" {
			retryAfter = "1"
		}
		w.Header().Set("Retry-After", retryAfter)
	}
	w.WriteHeader(code)
	writeBody(w, []byte(fmt.Sprintf("status %d\n", code)))
}

// handleFlaky returns 503 for the first n requests to a given name (default 2),
// then serves the fixture, exercising in-call transient retry and recovery.
func handleFlaky(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/flaky/")
	n := atoiDefault(r.URL.Query().Get("n"), 2)
	key := fmt.Sprintf("%s|%d", name, n)

	flakyMu.Lock()
	hits := flakyHits[key]
	flakyHits[key]++
	flakyMu.Unlock()

	if hits < n {
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusServiceUnavailable)
		writeBody(w, []byte("flaky: 503\n"))
		return
	}
	serveFeed(w, r, name)
}

// handleSlow waits before serving, honoring client cancellation so an aborted
// poll (timeout or SIGINT) is observable.
func handleSlow(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/slow/")
	ms := atoiDefault(r.URL.Query().Get("ms"), 2000)
	select {
	case <-time.After(time.Duration(ms) * time.Millisecond):
		serveFeed(w, r, name)
	case <-r.Context().Done():
	}
}

func handleRedirect(w http.ResponseWriter, r *http.Request) {
	to := r.URL.Query().Get("to")
	if to == "" {
		http.Error(w, "missing ?to=<url>", http.StatusBadRequest)
		return
	}
	code := atoiDefault(r.URL.Query().Get("code"), http.StatusFound)
	if code < 300 || code > 399 {
		http.Error(w, "code must be 3xx", http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, to, code) //nolint:gosec // G710: redirecting to an arbitrary target is the intended behavior for the SSRF and 301/308 test cases
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	entries, err := feedsFS.ReadDir("feeds")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var b strings.Builder
	b.WriteString("qafixtures routes:\n")
	b.WriteString("  /feeds/<name>[?ct=...]\n")
	b.WriteString("  /status/<code>[?retry-after=N]\n")
	b.WriteString("  /flaky/<name>?n=<k>\n")
	b.WriteString("  /slow/<name>?ms=<n>\n")
	b.WriteString("  /redirect?to=<url>&code=<3xx>\n")
	for _, pr := range probeRoutes {
		fmt.Fprintf(&b, "  %s (probe -> %s)\n", pr.path, pr.fixture)
	}
	b.WriteString("\nfixtures:\n")
	for _, e := range entries {
		fmt.Fprintf(&b, "  /feeds/%s\n", e.Name())
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	writeBody(w, []byte(b.String()))
}

func contentType(name string) string {
	switch {
	case strings.HasSuffix(name, "atom.xml"), strings.HasSuffix(name, ".atom"):
		return "application/atom+xml"
	case strings.HasSuffix(name, ".json"):
		return "application/feed+json"
	case strings.HasSuffix(name, ".html"):
		return "text/html; charset=utf-8"
	case strings.HasSuffix(name, ".opml"):
		return "text/x-opml"
	case strings.HasPrefix(name, "rss"), strings.HasSuffix(name, ".rss"):
		return "application/rss+xml"
	default:
		return "application/xml"
	}
}

func atoiDefault(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
}

func writeBody(w http.ResponseWriter, b []byte) {
	if _, err := w.Write(b); err != nil { //nolint:gosec // G705: serves controlled local QA fixtures over loopback, never untrusted content
		log.Printf("write error: %v", err)
	}
}
