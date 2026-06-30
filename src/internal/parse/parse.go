package parse

import (
	"context"
	"time"

	"github.com/andreswebs/feedwatch/internal/core"
)

// ParsedFeed is the normalized result of parsing a feed body: the items mapped
// onto core types, the feed's title, and its declared TTL, when present.
type ParsedFeed struct {
	Title string        // feed title, empty when absent
	TTL   time.Duration // declared poll interval; 0 when absent
	Items []core.Item
}

// Parser turns a decoded feed body into normalized core items. baseURL is used
// to resolve relative links. Implementations wrap failures as a parse-category
// error.
type Parser interface {
	Parse(ctx context.Context, body []byte, baseURL string) (ParsedFeed, error)
}
