package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	cliv3 "github.com/urfave/cli/v3"

	"github.com/andreswebs/feedwatch/internal/config"
	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/fetch"
	"github.com/andreswebs/feedwatch/internal/output"
	"github.com/andreswebs/feedwatch/internal/parse"
	"github.com/andreswebs/feedwatch/internal/store"
)

// Deps are the dependencies wired into the command tree by main. The cli
// package owns all flag, environment, logging, color, and exit handling; main
// only constructs Deps, runs the root, and relays the exit code.
type Deps struct {
	Cfg     config.Config
	Log     *slog.Logger
	Store   store.Store
	Fetch   fetch.Fetcher
	Parse   parse.Parser
	Clock   core.Clock
	Version string
	In      *os.File
	Out     *os.File
	Err     *os.File
}

// NewRootCommand builds the urfave/cli v3 root command: the global flags every
// subcommand inherits, the Before hook that resolves configuration, logging,
// and color, and the exit boundary that maps errors to exit codes and emits
// structured JSON error objects on stderr.
func NewRootCommand(d Deps) *cliv3.Command {
	installVersionPrinter(d.Version)

	cmd := &cliv3.Command{
		Name:                  "feedwatch",
		Usage:                 "agent-first watcher for RSS and Atom feeds",
		Version:               d.Version,
		EnableShellCompletion: true,
		ConfigureShellCompletionCommand: func(c *cliv3.Command) {
			c.CommandNotFound = d.completionShellNotFound()
		},
		Flags:           globalFlags(d.Cfg),
		Commands:        d.commands(),
		Writer:          d.Out,
		ErrWriter:       d.Err,
		Before:          d.before(),
		Action:          rootAction(),
		ExitErrHandler:  d.exitErrHandler(),
		OnUsageError:    onUsageError,
		CommandNotFound: d.commandNotFound(),
	}
	return cmd
}

// commands builds the subcommand tree wired with the dependencies in Deps.
func (d Deps) commands() []*cliv3.Command {
	return []*cliv3.Command{
		d.migrateCommand(),
		d.pollCommand(),
		d.checkCommand(),
		d.addCommand(),
		d.listCommand(),
		d.rmCommand(),
		d.enableCommand(),
		d.disableCommand(),
		d.itemsCommand(),
		d.pruneCommand(),
		d.discoverCommand(),
		d.importCommand(),
		d.exportCommand(),
		d.schemaCommand(),
	}
}

// before resolves configuration from flags, environment, and defaults, builds
// the logger and renderer, and stashes all three in the context for actions. A
// configuration failure (wrapping core.ErrConfig) is returned so the boundary
// renders it as a config error and exits 1.
func (d Deps) before() cliv3.BeforeFunc {
	return func(ctx context.Context, cmd *cliv3.Command) (context.Context, error) {
		cfg, isDefault := buildConfig(d.Cfg, cmd)
		if err := cfg.Validate(); err != nil {
			return ctx, err
		}
		if isDefault && backendName(cfg.Store) == "sqlite" {
			if err := ensureStoreDir(cfg.Store); err != nil {
				return ctx, err
			}
		}

		logger := NewLogger(d.Err, cfg.Format, cfg.LogLevel, cfg.Quiet)
		renderer := output.NewRenderer(cfg.Format, d.Out, d.Err, output.ColorPolicy{NoColorFlag: cfg.NoColor})

		ctx = context.WithValue(ctx, keyConfig, cfg)
		ctx = context.WithValue(ctx, keyLogger, logger)
		ctx = context.WithValue(ctx, keyRenderer, renderer)
		return ctx, nil
	}
}

// buildConfig overlays the resolved flag values (already merged with environment
// and defaults by the framework) onto the base configuration. Fields without a
// global flag keep their base values.
func buildConfig(base config.Config, cmd *cliv3.Command) (config.Config, bool) {
	c := base
	store, isDefault := resolveStorePath(cmd.String("db"))
	c.Store = store
	c.UserAgent = cmd.String("user-agent")
	c.Concurrency = cmd.Int("concurrency")
	c.ConnectTimeout = cmd.Duration("connect-timeout")
	c.Timeout = cmd.Duration("timeout")
	c.MinTLS = parseMinTLS(cmd.String("min-tls"))
	c.Proxy = cmd.String("proxy")
	c.CABundle = cmd.String("ca-bundle")
	c.AllowPrivate = cmd.Bool("allow-private")
	c.Format = cmd.String("format")
	c.NoColor = cmd.Bool("no-color")
	c.LogLevel = parseLevel(cmd.String("log-level"))
	c.Quiet = cmd.Bool("quiet")
	return c, isDefault
}

// rootAction handles invocations that resolve to no subcommand: a leftover
// positional argument is an unknown command (a usage error), and bare
// invocation prints help and exits 0.
func rootAction() cliv3.ActionFunc {
	return func(_ context.Context, cmd *cliv3.Command) error {
		if cmd.Args().Present() {
			return unknownCommandErr(cmd.Args().First())
		}
		return cliv3.ShowRootCommandHelp(cmd)
	}
}

// commandNotFound covers the help-machinery path for an unknown command,
// emitting the same usage error shape and exit 1 as rootAction.
func (d Deps) commandNotFound() cliv3.CommandNotFoundFunc {
	return func(_ context.Context, cmd *cliv3.Command, name string) {
		r := d.errRenderer(cmd)
		_ = r.Error(unknownCommandErr(name))
		cliv3.OsExiter(1)
	}
}

// completionShellNotFound handles an unsupported shell token passed to the
// built-in completion command. Without it the unknown token falls through the
// help machinery to Exit(_, 3), which the boundary treats as an already-reported
// outcome and emits nothing. Mirroring commandNotFound, it renders a single
// usage-category JSON error on stderr and exits 1.
func (d Deps) completionShellNotFound() cliv3.CommandNotFoundFunc {
	return func(_ context.Context, cmd *cliv3.Command, name string) {
		r := d.errRenderer(cmd)
		_ = r.Error(&core.FeedError{
			Category: core.CatUsage,
			Message:  fmt.Sprintf("unsupported shell %q; supported shells are bash, zsh, fish, pwsh", name),
			Err:      core.ErrUsage,
		})
		cliv3.OsExiter(1)
	}
}

// onUsageError converts a flag-parsing or argument error into a usage-category
// FeedError so the boundary renders it as structured JSON and exits 1.
func onUsageError(_ context.Context, _ *cliv3.Command, err error, _ bool) error {
	return &core.FeedError{Category: core.CatUsage, Message: err.Error(), Err: core.ErrUsage}
}

func unknownCommandErr(name string) *core.FeedError {
	return &core.FeedError{Category: core.CatUsage, Message: fmt.Sprintf("unknown command %q", name)}
}

// exitErrHandler is the single boundary that turns a returned error into a
// process exit code and the lone stderr error emission. A value implementing
// cli.ExitCoder (an exitError carrying a feed-outcome code) is a normal,
// already-reported outcome: it sets the code and writes nothing more. Any other
// error is a hard, whole-invocation failure rendered once as a JSON error
// object, with the code derived from core.ExitCodeFor.
func (d Deps) exitErrHandler() cliv3.ExitErrHandlerFunc {
	return func(_ context.Context, cmd *cliv3.Command, err error) {
		if err == nil {
			return
		}
		var coder cliv3.ExitCoder
		if errors.As(err, &coder) {
			cliv3.OsExiter(coder.ExitCode())
			return
		}
		r := d.errRenderer(cmd)
		_ = r.Error(feedErrorFor(err))
		cliv3.OsExiter(core.ExitCodeFor(err))
	}
}

// errRenderer builds a renderer for stderr error emission from the resolved
// flags, defaulting to the JSON contract. It is used on error paths that may
// run before the Before hook has stashed a renderer in the context.
func (d Deps) errRenderer(cmd *cliv3.Command) *output.Renderer {
	return output.NewRenderer(cmd.String("format"), d.Out, d.Err, output.ColorPolicy{NoColorFlag: cmd.Bool("no-color")})
}

// feedErrorFor coerces any error into a *core.FeedError for stderr rendering. A
// FeedError in the chain is used directly; otherwise a sentinel is mapped to
// its category, falling back to internal.
func feedErrorFor(err error) *core.FeedError {
	var fe *core.FeedError
	if errors.As(err, &fe) {
		return fe
	}
	cat := core.CatInternal
	switch {
	case errors.Is(err, core.ErrUsage):
		cat = core.CatUsage
	case errors.Is(err, core.ErrConfig):
		cat = core.CatConfig
	case errors.Is(err, core.ErrStoreUnavailable), errors.Is(err, core.ErrSchemaTooNew):
		cat = core.CatStore
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		// A residual cancellation or deadline is a graceful interrupt, not an
		// internal failure; main owns the 128+signal exit code. Classify it as
		// timeout so it never surfaces as an internal error on stderr.
		cat = core.CatTimeout
	}
	return &core.FeedError{Category: cat, Message: err.Error()}
}
