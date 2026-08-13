package feedwatch

import (
	"context"
	"encoding/json"
	"errors"

	"golang.org/x/sync/errgroup"

	"github.com/andreswebs/feedwatch/core"
	"github.com/andreswebs/feedwatch/store"
)

// CheckRequest selects the feeds to validate. An empty Feeds targets every
// active feed; naming feeds targets exactly those, by URL or alias, regardless
// of status.
type CheckRequest struct {
	Feeds []string `arg:"feed" variadic:"true"`
}

// Validate reports whether the request is usable. Every field is optional, so it
// always succeeds; the method exists so every request type is validated
// uniformly by a frontend. An unknown feed reference is a store-resolution
// failure rather than a syntactic one, so it surfaces from Check.
func (r CheckRequest) Validate() error { return nil }

// CheckFailure is one failed feed in the check envelope: the feed URL, its
// error category, the HTTP status when the category is http (omitted
// otherwise), and the human detail of the failure (always present).
type CheckFailure struct {
	FeedURL  string        `json:"feed_url"`
	Category core.Category `json:"category"`
	Status   int           `json:"status,omitempty"`
	Message  string        `json:"message"`
}

// CheckResult is the check result envelope: how many feeds were checked, how
// many passed, how many failed, and one entry per failed feed. The passing
// count is named passed rather than ok to avoid colliding with the head's ok
// boolean. failures is always present, empty ([]) when nothing failed.
type CheckResult struct {
	Head
	Checked  int            `json:"checked"`
	Passed   int            `json:"passed"`
	Failed   int            `json:"failed"`
	Failures []CheckFailure `json:"failures"`
}

// MarshalJSON coalesces failures so it always serializes as [] rather than null.
func (r CheckResult) MarshalJSON() ([]byte, error) {
	type alias CheckResult
	a := alias(r)
	if a.Failures == nil {
		a.Failures = []CheckFailure{}
	}
	return json.Marshal(a)
}

// ExitCode derives the process exit code from the outcome: 0 when nothing was
// checked or every feed passed, 2 when every feed failed, 3 when some passed
// and some failed.
func (r CheckResult) ExitCode() int {
	if r.Checked == 0 || r.Failed == 0 {
		return 0
	}
	if r.Failed == r.Checked {
		return 2
	}
	return 3
}

// Check fetches and parses each targeted feed concurrently and reports which
// ones are unusable, writing nothing: no items are stored and no feed's failure
// lifecycle advances, so it is the safe way to probe subscriptions.
//
// A named feed that is not subscribed is a hard failure that aborts the whole
// check, since the caller asked about a feed that does not exist. A fetch or
// parse failure is per-feed result data instead: it is reported in Failures and
// never cancels the sibling checks.
func (a *App) Check(ctx context.Context, req CheckRequest) (CheckResult, error) {
	if err := req.Validate(); err != nil {
		return CheckResult{}, err
	}
	st, err := a.resolveStore(ctx)
	if err != nil {
		return CheckResult{}, err
	}
	fetcher, err := a.resolveFetcher()
	if err != nil {
		return CheckResult{}, err
	}
	parser := a.resolveParser()

	feeds, err := checkTargets(ctx, st, req.Feeds)
	if err != nil {
		return CheckResult{}, err
	}

	// Position-indexed slots keep the failures in feed-selection order however
	// the workers interleave.
	feedErrs := make([]*core.FeedError, len(feeds))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(a.cfg.Concurrency)
	for i, f := range feeds {
		g.Go(func() error {
			res, err := fetcher.Fetch(gctx, core.FetchRequest{URL: f.URL})
			if err != nil {
				feedErrs[i] = checkFeedError(f.URL, err)
				return nil
			}
			if _, err := parser.Parse(gctx, res.Body, f.URL); err != nil {
				feedErrs[i] = checkFeedError(f.URL, err)
			}
			return nil
		})
	}
	_ = g.Wait()

	res := CheckResult{Head: OKHead(), Checked: len(feeds)}
	for _, fe := range feedErrs {
		if fe == nil {
			continue
		}
		res.Failed++
		res.Failures = append(res.Failures, CheckFailure{
			FeedURL:  fe.FeedURL,
			Category: fe.Category,
			Status:   fe.Status,
			Message:  fe.Detail(),
		})
	}
	res.Passed = res.Checked - res.Failed
	return res, nil
}

// checkTargets resolves the feeds a check targets: the named references, or
// every active feed when none were named. An unresolvable reference propagates,
// so a typo aborts the check rather than silently narrowing it.
func checkTargets(ctx context.Context, st store.Store, names []string) ([]core.Feed, error) {
	if len(names) == 0 {
		return st.ListFeeds(ctx, core.ListFilter{Status: core.FeedActive})
	}
	feeds := make([]core.Feed, 0, len(names))
	for _, ref := range names {
		f, err := st.GetFeed(ctx, ref)
		if err != nil {
			return nil, err
		}
		feeds = append(feeds, f)
	}
	return feeds, nil
}

// checkFeedError classifies a fetch or parse error as a *core.FeedError,
// extracting the existing one from the chain or falling back to network.
func checkFeedError(feedURL string, err error) *core.FeedError {
	var fe *core.FeedError
	if errors.As(err, &fe) {
		return fe
	}
	return core.NetworkErr(feedURL, err)
}
