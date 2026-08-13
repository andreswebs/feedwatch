package feedwatch

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/andreswebs/feedwatch/core"
)

// PruneRequest bounds stored item history by age, per-feed count, or both.
//
// The fields are pointers so an explicit zero is distinguishable from an absent
// value: KeepDays pointing at 0 prunes everything older than now, while a nil
// KeepDays applies no age policy at all. Pruning is always explicit, so a
// request naming neither policy is a usage error rather than a silent no-op.
type PruneRequest struct {
	KeepDays *int `flag:"keep-days" usage:"tombstone items older than this many days"`
	MaxItems *int `flag:"max-items" usage:"keep at most this many items per feed, tombstoning the rest"`
}

// Validate reports whether the request names at least one non-negative policy.
func (r PruneRequest) Validate() error {
	_, err := r.policy(time.Unix(0, 0))
	return err
}

// policy resolves the request into a core.PrunePolicy relative to now, so the
// rules are stated once and Validate cannot drift from what Prune applies.
func (r PruneRequest) policy(now time.Time) (core.PrunePolicy, error) {
	if r.KeepDays == nil && r.MaxItems == nil {
		return core.PrunePolicy{}, usageErr("prune requires --keep-days and/or --max-items")
	}

	var policy core.PrunePolicy
	if r.KeepDays != nil {
		if *r.KeepDays < 0 {
			return core.PrunePolicy{}, usageErr("--keep-days must not be negative")
		}
		cutoff := now.Add(-time.Duration(*r.KeepDays) * 24 * time.Hour)
		policy.KeepBefore = &cutoff
	}
	if r.MaxItems != nil {
		if *r.MaxItems < 0 {
			return core.PrunePolicy{}, usageErr("--max-items must not be negative")
		}
		policy.MaxPerFeed = *r.MaxItems
	}
	return policy, nil
}

// PruneResult is the prune result envelope: the number of item rows tombstoned.
type PruneResult struct {
	Head
	Pruned int `json:"pruned"`
}

// Prune trims stored item history per the request's policy, preserving each
// item's dedup fingerprint so a pruned item a feed still advertises is never
// re-emitted as new. A request naming no policy is a usage-category failure.
func (a *App) Prune(ctx context.Context, req PruneRequest) (PruneResult, error) {
	policy, err := req.policy(a.clock())
	if err != nil {
		return PruneResult{}, err
	}
	st, err := a.resolveStore(ctx)
	if err != nil {
		return PruneResult{}, err
	}

	pruned, err := st.PruneItems(ctx, policy)
	if err != nil {
		return PruneResult{}, err
	}
	return PruneResult{Head: OKHead(), Pruned: pruned}, nil
}

// RenderText is the optional human-text projection of the same values: the
// prune outcome as a single line. A frontend may ignore it and read Pruned
// directly.
func (r PruneResult) RenderText(w io.Writer, _ bool) error {
	_, err := fmt.Fprintf(w, "pruned %d item(s)\n", r.Pruned)
	return err
}

// usageErr builds a usage-category failure carrying msg. Every frontend maps the
// category to its own encoding: exit 64 in the CLI, HTTP 400 in a server.
func usageErr(msg string) error {
	return &core.FeedError{
		Category: core.CatUsage,
		Message:  msg,
		Err:      core.ErrUsage,
	}
}
