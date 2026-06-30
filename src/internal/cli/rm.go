package cli

import (
	"context"

	cliv3 "github.com/urfave/cli/v3"
)

// RmResult is the rm stdout envelope: the canonical URL of the removed
// subscription.
type RmResult struct {
	Removed string `json:"removed"`
}

// rmCommand registers the rm subcommand: unsubscribe a feed resolved by its
// exact URL or unique alias, cascading to its stored items.
func (d Deps) rmCommand() *cliv3.Command {
	return &cliv3.Command{
		Name:      "rm",
		Usage:     "unsubscribe a feed by URL or unique alias, removing its stored items",
		ArgsUsage: "URL|ALIAS",
		Arguments: []cliv3.Argument{&cliv3.StringArg{Name: "ref"}},
		Action:    d.rmAction,
	}
}

// rmAction resolves the ref to a subscription, removes it (cascading items),
// and writes the result envelope reporting its canonical URL. An unknown ref is
// a usage failure (exit 1); a store failure propagates to the boundary as a hard
// error. Resolving first means the reported URL is canonical even when the ref
// was an alias, and removal of an unknown ref is reported rather than silently
// succeeding (the store's RemoveFeed is a no-op on a missing feed).
func (d Deps) rmAction(ctx context.Context, cmd *cliv3.Command) error {
	cfg := configFrom(ctx)
	r := rendererFrom(ctx)

	ref := cmd.StringArg("ref")

	rs := newResolver(d, cfg)
	defer rs.Close()
	st, err := rs.Store(ctx)
	if err != nil {
		return err
	}

	feed, err := st.GetFeed(ctx, ref)
	if err != nil {
		return err
	}

	if err := st.RemoveFeed(ctx, feed.URL); err != nil {
		return err
	}
	return r.Result(RmResult{Removed: feed.URL})
}
