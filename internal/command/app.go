package command

import (
	"context"

	"github.com/andreswebs/feedwatch"
)

// app builds the library App an action delegates to, from the configuration the
// Before hook resolved plus any options Deps carries. The seam is expressed in
// public library options, so injecting a double here and injecting one from
// outside the module take the same path. The library's advisories are routed to
// the renderer, which is what turns them into NDJSON warning lines on stderr.
// The caller owns the App and must Close it.
func (d Deps) app(ctx context.Context) (*feedwatch.App, error) {
	opts := append([]feedwatch.Option{
		feedwatch.WithClock(d.Clock),
		feedwatch.WithWarner(rendererFrom(ctx).Warn),
	}, d.opts...)
	return feedwatch.New(configFrom(ctx), opts...)
}
