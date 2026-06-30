package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	cliv3 "github.com/urfave/cli/v3"

	"github.com/andreswebs/feedwatch/internal/core"
)

// PruneResult is the prune stdout envelope: the number of item rows tombstoned.
type PruneResult struct {
	Pruned int `json:"pruned"`
}

// pruneCommand registers the prune subcommand: trim stored item history by age
// and/or per-feed count while preserving each item's dedup fingerprint.
func (d Deps) pruneCommand() *cliv3.Command {
	return &cliv3.Command{
		Name:  "prune",
		Usage: "trim stored item history by age and/or per-feed count, preserving dedup",
		Flags: []cliv3.Flag{
			&cliv3.IntFlag{Name: "keep-days", Usage: "tombstone items older than this many days"},
			&cliv3.IntFlag{Name: "max-items", Usage: "keep at most this many items per feed, tombstoning the rest"},
		},
		Action: d.pruneAction,
	}
}

// pruneAction builds a core.PrunePolicy from the flags and tombstones the matching
// rows via the store, reporting the count. Pruning is always explicit: at least
// one of --keep-days or --max-items must be given, so a bare invocation is a usage
// error (exit 1) rather than a silent no-op. A store failure propagates to the
// boundary as a hard error.
func (d Deps) pruneAction(ctx context.Context, cmd *cliv3.Command) error {
	cfg := configFrom(ctx)
	r := rendererFrom(ctx)

	now := orSystemClock(d.Clock)()
	policy, err := buildPrunePolicy(cmd, now)
	if err != nil {
		return err
	}

	rs := newResolver(d, cfg)
	defer rs.Close()
	st, err := rs.Store(ctx)
	if err != nil {
		return err
	}

	pruned, err := st.PruneItems(ctx, policy)
	if err != nil {
		return err
	}
	return r.Result(PruneResult{Pruned: pruned})
}

// buildPrunePolicy translates the flags into a core.PrunePolicy. --keep-days N
// sets the cutoff to N days before now; --max-items N caps the live rows per
// feed. Each is applied only when its flag is set, so --keep-days 0 (prune
// everything older than now) is distinguishable from the flag being absent.
// Negative values are usage errors.
func buildPrunePolicy(cmd *cliv3.Command, now time.Time) (core.PrunePolicy, error) {
	keepSet, maxSet := cmd.IsSet("keep-days"), cmd.IsSet("max-items")
	if !keepSet && !maxSet {
		return core.PrunePolicy{}, usageErr("prune requires --keep-days and/or --max-items")
	}

	var policy core.PrunePolicy
	if keepSet {
		days := cmd.Int("keep-days")
		if days < 0 {
			return core.PrunePolicy{}, usageErr("--keep-days must not be negative")
		}
		cutoff := now.Add(-time.Duration(days) * 24 * time.Hour)
		policy.KeepBefore = &cutoff
	}
	if maxSet {
		maxItems := cmd.Int("max-items")
		if maxItems < 0 {
			return core.PrunePolicy{}, usageErr("--max-items must not be negative")
		}
		policy.MaxPerFeed = maxItems
	}
	return policy, nil
}

// RenderText writes the prune outcome as a single human-readable line under
// --format text.
func (r PruneResult) RenderText(w io.Writer, _ bool) error {
	_, err := fmt.Fprintf(w, "pruned %d item(s)\n", r.Pruned)
	return err
}
