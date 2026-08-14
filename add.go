package feedwatch

import (
	"context"
	"errors"
	"net/url"
	"time"

	"github.com/andreswebs/feedwatch/core"
	"github.com/andreswebs/feedwatch/store"
)

// AddRequest names the feed to subscribe to, with an optional short alias and a
// minimum poll interval. A zero interval leaves the feed on the configured
// default.
type AddRequest struct {
	URL      string        `arg:"url"`
	Alias    string        `flag:"alias" usage:"short, unique name to reference the feed"`
	Interval time.Duration `flag:"interval" usage:"minimum poll interval; 0 uses the configured default"`
	Tags     []string      `flag:"tag" usage:"tag to assign (repeatable); omitted preserves existing tags on a re-add, given replaces them; use 'tag --clear' to remove every tag"`
}

// Validate rejects anything that is not an absolute http(s) URL, so add never
// guesses over the network. A bare host (no scheme) or a non-http scheme is a
// usage failure pointing the agent at discover for turning a homepage into a
// feed URL. Tag names are checked here too, before any fetch or store call, so
// an unusable tag never leaves a subscription behind.
func (r AddRequest) Validate() error {
	if !isAbsoluteHTTPURL(r.URL) {
		return usageErr("add requires an absolute http(s) feed URL; " +
			"run 'feedwatch discover <url>' to find a feed from a homepage")
	}
	return core.ValidateTags(r.Tags)
}

// AddResult is the add result envelope: the canonical feed URL, its alias and
// minimum poll interval when set, and whether this invocation created the
// subscription (false on an idempotent re-add).
type AddResult struct {
	Head
	URL      string   `json:"url"`
	Alias    string   `json:"alias,omitempty"`
	Interval string   `json:"interval,omitempty"`
	Tags     []string `json:"tags,omitempty"`
	Created  bool     `json:"created"`
}

// Add subscribes to an explicit feed URL in three steps: the URL is validated
// syntactically, proven to fetch and parse as a feed, and only then upserted. A
// bad URL, an unfetchable one, or a body that is not a feed is a usage-category
// failure that points at discover, so a subscription always names something add
// could prove is a feed. Adding an already-subscribed URL is an idempotent
// upsert of its alias and interval, reported with created false. Tags follow
// omitted-preserves, given-replaces: a re-add without any tag keeps the feed in
// its lanes, while a given set replaces the stored one outright.
func (a *App) Add(ctx context.Context, req AddRequest) (AddResult, error) {
	if err := req.Validate(); err != nil {
		return AddResult{}, err
	}
	fetcher, err := a.resolveFetcher()
	if err != nil {
		return AddResult{}, err
	}
	if err := validateParsesAsFeed(ctx, fetcher, a.resolveParser(), req.URL); err != nil {
		return AddResult{}, err
	}

	st, err := a.resolveStore(ctx)
	if err != nil {
		return AddResult{}, err
	}
	created, err := feedIsNew(ctx, st, req.URL)
	if err != nil {
		return AddResult{}, err
	}

	feed, err := st.AddFeed(ctx, core.Feed{
		URL:      req.URL,
		Alias:    req.Alias,
		Interval: req.Interval,
		Tags:     req.Tags,
	})
	if err != nil {
		return AddResult{}, err
	}

	// The upsert sets tags on create and leaves them alone on a re-add, so an
	// omitted --tag preserves the stored set. A given --tag replaces it on
	// either path through this one write.
	if len(req.Tags) > 0 {
		if err := st.SetTags(ctx, feed.URL, req.Tags); err != nil {
			return AddResult{}, err
		}
		if feed, err = st.GetFeed(ctx, feed.URL); err != nil {
			return AddResult{}, err
		}
	}

	res := AddResult{Head: OKHead(), URL: feed.URL, Alias: feed.Alias, Tags: feed.Tags, Created: created}
	if feed.Interval > 0 {
		res.Interval = feed.Interval.String()
	}
	return res, nil
}

// isAbsoluteHTTPURL reports whether raw is an absolute http(s) URL with a host,
// the syntactic feed-URL test shared by add, discover, and OPML import.
func isAbsoluteHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// validateParsesAsFeed fetches the URL and confirms the body parses as a feed.
// Both an unfetchable URL and a body that is not a feed (such as an HTML page)
// are reported as usage failures that point at discover, so add subscribes only
// to URLs it could prove are feeds.
func validateParsesAsFeed(ctx context.Context, f Fetcher, p Parser, feedURL string) error {
	res, err := f.Fetch(ctx, core.FetchRequest{URL: feedURL})
	if err != nil {
		return &core.FeedError{
			FeedURL:  feedURL,
			Category: core.CatUsage,
			Message:  "could not fetch " + feedURL + " to validate it as a feed",
			Err:      err,
		}
	}
	if _, err := p.Parse(ctx, res.Body, feedURL); err != nil {
		return &core.FeedError{
			FeedURL:  feedURL,
			Category: core.CatUsage,
			Message:  feedURL + " does not parse as a feed; run 'feedwatch discover " + feedURL + "' to find its feeds",
			Err:      err,
		}
	}
	return nil
}

// feedIsNew reports whether the feed is not yet subscribed, distinguishing a
// not-found feed (a fresh subscription) from a real store failure. A not-found
// GetFeed returns a usage-category *FeedError; any other error propagates.
func feedIsNew(ctx context.Context, st store.Store, feedURL string) (bool, error) {
	_, err := st.GetFeed(ctx, feedURL)
	if err == nil {
		return false, nil
	}
	var fe *core.FeedError
	if errors.As(err, &fe) && fe.Category == core.CatUsage {
		return true, nil
	}
	return false, err
}
