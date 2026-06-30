package testsupport

import (
	"context"
	"errors"
	"sync"

	"github.com/andreswebs/feedwatch/internal/core"
)

// FakeFetcher is a programmable fetch.Fetcher double. It returns a canned
// core.FetchResult (or canned error) for a registered URL and a network-category
// error for any other URL, and records every request for later assertion.
type FakeFetcher struct {
	mu       sync.Mutex
	results  map[string]core.FetchResult
	errs     map[string]error
	requests map[string][]core.FetchRequest
}

// NewFakeFetcher returns an empty FakeFetcher with no registered URLs.
func NewFakeFetcher() *FakeFetcher {
	return &FakeFetcher{
		results:  make(map[string]core.FetchResult),
		errs:     make(map[string]error),
		requests: make(map[string][]core.FetchRequest),
	}
}

// Register sets the canned result returned for url. FinalURL defaults to url
// when the caller leaves it empty.
func (f *FakeFetcher) Register(url string, res core.FetchResult) {
	if res.FinalURL == "" {
		res.FinalURL = url
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.results[url] = res
}

// RegisterError makes Fetch return err for url, overriding any canned result.
func (f *FakeFetcher) RegisterError(url string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errs[url] = err
}

// Fetch returns the canned result or error for req.URL, recording the request.
// An unregistered URL yields a network-category *core.FeedError.
func (f *FakeFetcher) Fetch(_ context.Context, req core.FetchRequest) (core.FetchResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests[req.URL] = append(f.requests[req.URL], req)

	if err, ok := f.errs[req.URL]; ok {
		return core.FetchResult{}, err
	}
	if res, ok := f.results[req.URL]; ok {
		return res, nil
	}
	return core.FetchResult{}, core.NetworkErr(req.URL, errors.New("no canned result registered"))
}

// Requests returns the requests Fetch received for url, in order.
func (f *FakeFetcher) Requests(url string) []core.FetchRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]core.FetchRequest(nil), f.requests[url]...)
}
