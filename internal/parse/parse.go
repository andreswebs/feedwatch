package parse

import (
	"context"

	"github.com/andreswebs/feedwatch/core"
)

// Parser turns a decoded feed body into normalized core items. baseURL is used
// to resolve relative links. Implementations wrap failures as a parse-category
// error.
type Parser interface {
	Parse(ctx context.Context, body []byte, baseURL string) (core.ParsedFeed, error)
}
