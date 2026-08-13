package command

import (
	"context"
	"log/slog"

	"github.com/andreswebs/feedwatch"
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
func configFrom(ctx context.Context) feedwatch.Config {
	c, _ := ctx.Value(keyConfig).(feedwatch.Config)
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
