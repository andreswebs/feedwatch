package testsupport

import (
	"context"
	"errors"
	"sync"

	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/parse"
)

// FakeParser is a programmable parse.Parser double. It returns a canned
// parse.ParsedFeed (or canned error) keyed by the baseURL passed to Parse, and a
// parse-category error for any unregistered base URL.
type FakeParser struct {
	mu    sync.Mutex
	feeds map[string]parse.ParsedFeed
	errs  map[string]error
}

// NewFakeParser returns an empty FakeParser with no registered base URLs.
func NewFakeParser() *FakeParser {
	return &FakeParser{
		feeds: make(map[string]parse.ParsedFeed),
		errs:  make(map[string]error),
	}
}

// Register sets the parsed feed returned for baseURL.
func (p *FakeParser) Register(baseURL string, feed parse.ParsedFeed) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.feeds[baseURL] = feed
}

// RegisterError makes Parse return err for baseURL, overriding any canned feed.
func (p *FakeParser) RegisterError(baseURL string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.errs[baseURL] = err
}

// Parse returns the canned feed or error for baseURL. The body is ignored; an
// unregistered base URL yields a parse-category *core.FeedError.
func (p *FakeParser) Parse(_ context.Context, _ []byte, baseURL string) (parse.ParsedFeed, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if err, ok := p.errs[baseURL]; ok {
		return parse.ParsedFeed{}, err
	}
	if feed, ok := p.feeds[baseURL]; ok {
		return feed, nil
	}
	return parse.ParsedFeed{}, core.ParseErr(baseURL, errors.New("no canned feed registered"))
}
