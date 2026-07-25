package fetch

import (
	"context"

	"github.com/andreswebs/feedwatch/internal/core"
)

// Fetcher retrieves a single feed over HTTP, honoring conditional-GET
// validators and reporting a 304 without a body. Implementations decode the
// body to UTF-8 and classify failures as network, http, or timeout errors.
type Fetcher interface {
	Fetch(ctx context.Context, req core.FetchRequest) (core.FetchResult, error)
}
