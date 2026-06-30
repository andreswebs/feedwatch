package poll

import (
	"context"
	"errors"
	"net/url"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/fetch"
	"github.com/andreswebs/feedwatch/internal/parse"
	"github.com/andreswebs/feedwatch/internal/store"
)

// Deps are the collaborators and tuning knobs the poll orchestrator needs. The
// cli layer assembles them from the wired dependencies and resolved config.
type Deps struct {
	Store        store.Store
	Fetcher      fetch.Fetcher
	Parser       parse.Parser
	Clock        core.Clock
	Concurrency  int           // worker-pool size; values below 1 fall back to 1
	PerHostDelay time.Duration // politeness delay between same-host requests

	// DefaultInterval is the fallback poll interval when neither the feed's own
	// interval nor a parsed <ttl> is set. FailureThreshold and MaxBackoff tune
	// the failure lifecycle: consecutive failures before auto-disable, and the
	// backoff ceiling. The cli layer fills these from resolved config.
	DefaultInterval  time.Duration
	FailureThreshold int
	MaxBackoff       time.Duration
}

// feedOutcome is the per-feed result of the fetch-orchestration stage: the feed,
// its fetch result, and its parsed body. err is nil on success or a 304; on a
// 304 the result reports NotModified and parsed carries no items. Consuming and
// persisting these outcomes is the next stage's concern.
type feedOutcome struct {
	feed   core.Feed
	result core.FetchResult
	parsed parse.ParsedFeed
	err    *core.FeedError
}

// orchestrate fetches and parses the given feeds concurrently, returning one
// outcome per feed in input order. Feeds are grouped by host so same-host
// requests are serialized onto a single worker with a per-host politeness
// delay, and the host groups run in parallel under an errgroup bounded by
// Concurrency. A feed's failure is captured into its outcome and never cancels
// siblings; only a cancelled context (an interrupt signal) stops scheduling.
func orchestrate(ctx context.Context, d Deps, feeds []core.Feed) []feedOutcome {
	if len(feeds) == 0 {
		return nil
	}

	type indexed struct {
		feed core.Feed
		idx  int
	}
	groups := make(map[string][]indexed)
	var hostOrder []string
	for i, f := range feeds {
		h := hostOf(f.URL)
		if _, ok := groups[h]; !ok {
			hostOrder = append(hostOrder, h)
		}
		groups[h] = append(groups[h], indexed{feed: f, idx: i})
	}

	concurrency := d.Concurrency
	if concurrency < 1 {
		concurrency = 1
	}

	outcomes := make([]*feedOutcome, len(feeds))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(concurrency)

	for _, h := range hostOrder {
		group := groups[h]
		g.Go(func() error {
			for j, it := range group {
				select {
				case <-gctx.Done():
					return nil
				default:
				}
				if j > 0 && d.PerHostDelay > 0 {
					if !sleepContext(gctx, d.PerHostDelay) {
						return nil
					}
				}
				oc := fetchAndParse(gctx, d, it.feed)
				outcomes[it.idx] = &oc
			}
			return nil
		})
	}
	_ = g.Wait()

	res := make([]feedOutcome, 0, len(feeds))
	for _, oc := range outcomes {
		if oc != nil {
			res = append(res, *oc)
		}
	}
	return res
}

// fetchAndParse runs the conditional-GET fetch and, on a 200, parses the body.
// A 304 returns an outcome with no items and no error.
func fetchAndParse(ctx context.Context, d Deps, f core.Feed) feedOutcome {
	req := core.FetchRequest{URL: f.URL, ETag: f.ETag, LastModified: f.LastModified}
	res, err := d.Fetcher.Fetch(ctx, req)
	if err != nil {
		return feedOutcome{feed: f, err: asFeedError(f.URL, err)}
	}
	if res.NotModified {
		return feedOutcome{feed: f, result: res}
	}
	pf, perr := d.Parser.Parse(ctx, res.Body, f.URL)
	if perr != nil {
		return feedOutcome{feed: f, result: res, err: asFeedError(f.URL, perr)}
	}
	return feedOutcome{feed: f, result: res, parsed: pf}
}

// asFeedError returns the underlying *core.FeedError when the fetch or parse
// layer already classified the failure, falling back to a network-category
// error for an unclassified one.
func asFeedError(feedURL string, err error) *core.FeedError {
	var fe *core.FeedError
	if errors.As(err, &fe) {
		return fe
	}
	return core.NetworkErr(feedURL, err)
}

// hostOf returns the host of a feed URL for politeness grouping, falling back
// to the raw string when it does not parse so unparseable URLs still group
// deterministically.
func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	return u.Host
}

// sleepContext waits for d or until ctx is cancelled, reporting whether the
// full delay elapsed (true) rather than being cut short by cancellation.
func sleepContext(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
