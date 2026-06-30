package cli

import (
	"context"
	"log/slog"

	"github.com/andreswebs/feedwatch/internal/config"
	"github.com/andreswebs/feedwatch/internal/output"
)

// ctxKey is the unexported key type for values the Before hook stashes for
// actions, so other packages cannot collide with these keys.
type ctxKey int

const (
	keyConfig ctxKey = iota
	keyLogger
	keyRenderer
)

// configFrom returns the resolved configuration placed by the Before hook.
func configFrom(ctx context.Context) config.Config {
	c, _ := ctx.Value(keyConfig).(config.Config)
	return c
}

// loggerFrom returns the logger placed by the Before hook.
func loggerFrom(ctx context.Context) *slog.Logger {
	l, _ := ctx.Value(keyLogger).(*slog.Logger)
	return l
}

// rendererFrom returns the output renderer placed by the Before hook.
func rendererFrom(ctx context.Context) *output.Renderer {
	r, _ := ctx.Value(keyRenderer).(*output.Renderer)
	return r
}
