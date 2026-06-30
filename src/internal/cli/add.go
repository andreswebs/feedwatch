package cli

import (
	"context"
	"errors"
	"net/url"

	cliv3 "github.com/urfave/cli/v3"

	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/fetch"
	"github.com/andreswebs/feedwatch/internal/parse"
	"github.com/andreswebs/feedwatch/internal/store"
)

// AddResult is the add stdout envelope: the canonical feed URL, its alias and
// minimum poll interval when set, and whether this invocation created the
// subscription (false on an idempotent re-add).
type AddResult struct {
	URL      string `json:"url"`
	Alias    string `json:"alias,omitempty"`
	Interval string `json:"interval,omitempty"`
	Created  bool   `json:"created"`
}

// addCommand registers the add subcommand: subscribe to an explicit http(s)
// feed URL after validating it actually parses as a feed, with an optional
// alias and minimum poll interval. Adding an already-subscribed URL is an
// idempotent upsert of its alias and interval.
func (d Deps) addCommand() *cliv3.Command {
	return &cliv3.Command{
		Name:      "add",
		Usage:     "subscribe to an explicit feed URL after validating it parses as a feed",
		ArgsUsage: "URL",
		Arguments: []cliv3.Argument{&cliv3.StringArg{Name: "url"}},
		Flags: []cliv3.Flag{
			&cliv3.StringFlag{Name: "alias", Usage: "short, unique name to reference the feed"},
			&cliv3.DurationFlag{Name: "interval", Usage: "minimum poll interval; 0 uses the configured default"},
		},
		Action: d.addAction,
	}
}

// addAction validates the URL, confirms it fetches and parses as a feed, then
// upserts the subscription and writes the result envelope. A bad URL, an
// unfetchable URL, or a body that does not parse as a feed is a usage failure
// (exit 1) that points the agent at discover; a store failure propagates to the
// boundary as a hard error.
func (d Deps) addAction(ctx context.Context, cmd *cliv3.Command) error {
	cfg := configFrom(ctx)
	r := rendererFrom(ctx)

	feedURL := cmd.StringArg("url")
	if err := validateFeedURL(feedURL); err != nil {
		return err
	}

	rs := newResolver(d, cfg)
	defer rs.Close()
	st, err := rs.Store(ctx)
	if err != nil {
		return err
	}
	fetcher, err := rs.Fetcher()
	if err != nil {
		return err
	}
	parser := rs.Parser()

	if err := validateParsesAsFeed(ctx, fetcher, parser, feedURL); err != nil {
		return err
	}

	created, err := feedIsNew(ctx, st, feedURL)
	if err != nil {
		return err
	}

	feed, err := st.AddFeed(ctx, core.Feed{
		URL:      feedURL,
		Alias:    cmd.String("alias"),
		Interval: cmd.Duration("interval"),
	})
	if err != nil {
		return err
	}

	res := AddResult{URL: feed.URL, Alias: feed.Alias, Created: created}
	if feed.Interval > 0 {
		res.Interval = feed.Interval.String()
	}
	return r.Result(res)
}

// validateFeedURL rejects anything that is not an absolute http(s) URL, so add
// never guesses over the network. A bare host (no scheme) or a non-http scheme
// is a usage error pointing the agent at discover for turning a homepage into a
// feed URL.
func validateFeedURL(raw string) error {
	if !isAbsoluteHTTPURL(raw) {
		return &core.FeedError{
			Category: core.CatUsage,
			Message:  "add requires an absolute http(s) feed URL; run 'feedwatch discover <url>' to find a feed from a homepage",
			Err:      core.ErrUsage,
		}
	}
	return nil
}

// isAbsoluteHTTPURL reports whether raw is an absolute http(s) URL with a host,
// the syntactic feed-URL test shared by add and import.
func isAbsoluteHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// validateParsesAsFeed fetches the URL and confirms the body parses as a feed.
// Both an unfetchable URL and a body that is not a feed (such as an HTML page)
// are reported as usage errors that point at discover, so add subscribes only
// to URLs it could prove are feeds.
func validateParsesAsFeed(ctx context.Context, f fetch.Fetcher, p parse.Parser, feedURL string) error {
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
