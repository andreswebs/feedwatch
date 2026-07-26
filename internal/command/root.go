package command

import (
	"context"
	"fmt"

	cliv3 "github.com/urfave/cli/v3"

	"github.com/andreswebs/feedwatch/internal/config"
	"github.com/andreswebs/feedwatch/internal/core"
	"github.com/andreswebs/feedwatch/internal/output"
)

// rootName is the program name urfave expects as argv[0]. Run receives args
// without it (os.Args[1:]) and the interior prepends it before parsing.
const rootName = "feedwatch"

// runRoot builds and runs the command tree and returns the boundary renderer
// together with the error to classify. It is the interior seam Run calls; the
// renderer is derived from the resolved global flags so Run can emit a
// format-aware error envelope without importing the framework.
func runRoot(ctx context.Context, args []string, deps Deps) (*output.Renderer, error) {
	return runCustom(ctx, args, deps, nil)
}

// runCustom is runRoot with an optional hook that mutates the command tree
// before it is neutralized and run, used only by tests that inject a stub
// subcommand to exercise the boundary.
func runCustom(ctx context.Context, args []string, deps Deps, customize func(*cliv3.Command)) (*output.Renderer, error) {
	var cbErr error
	cmd := newRoot(deps, &cbErr)
	if customize != nil {
		customize(cmd)
	}
	neutralize(cmd)

	runErr := cmd.Run(ctx, append([]string{rootName}, args...))
	r := errRenderer(deps, cmd)

	// A void framework callback (unknown command, unsupported completion shell)
	// records its error in cbErr, since it cannot return one; prefer it so the
	// boundary emits and codes the intended usage error.
	if cbErr != nil {
		return r, cbErr
	}
	return r, runErr
}

// newRoot builds the urfave/cli v3 root command: the global flags every
// subcommand's Before resolves, the Before hook, the subcommand tree, and the
// void callbacks that record usage errors into cbErr. It sets no framework
// exiter and no version printer global; --version is a plain flag handled in
// Before (see version.go).
func newRoot(d Deps, cbErr *error) *cliv3.Command {
	flags := globalFlags(config.Defaults())
	flags = append(flags, &cliv3.BoolFlag{
		Name:    "version",
		Aliases: []string{"v"},
		Usage:   "print version information",
	})

	return &cliv3.Command{
		Name:                  rootName,
		Usage:                 "agent-first watcher for RSS and Atom feeds",
		HideVersion:           true,
		EnableShellCompletion: true,
		ConfigureShellCompletionCommand: func(c *cliv3.Command) {
			neutralize(c)
			c.CommandNotFound = completionShellNotFound(cbErr)
		},
		Flags:           flags,
		Commands:        d.commands(),
		Writer:          d.Out,
		ErrWriter:       d.Err,
		Before:          d.before(),
		Action:          rootAction(),
		OnUsageError:    onUsageError,
		CommandNotFound: commandNotFound(cbErr),
	}
}

// neutralize disables the framework's own error printing, help-on-error, and
// exit handling on cmd and every command below it, so a parse error returns to
// the boundary in Run untouched: stderr carries exactly one error envelope and
// the framework never calls os.Exit. OnUsageError is set to the sanctioned
// usage classifier rather than left to print (ADR 0002, ADR 0003).
func neutralize(cmd *cliv3.Command) {
	cmd.ExitErrHandler = func(context.Context, *cliv3.Command, error) {}
	cmd.OnUsageError = onUsageError
	for _, sub := range cmd.Commands {
		neutralize(sub)
	}
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

// before handles --version first (short-circuiting before any store setup),
// then resolves configuration from flags, environment, and defaults, builds the
// logger and renderer, and stashes all three in the context for actions. A
// --version request writes the version envelope to stdout and returns a
// zero-code exitError so the action never runs; a configuration failure
// (wrapping core.ErrConfig) is returned so the boundary renders it as a config
// error and exits 78 (EX_CONFIG).
func (d Deps) before() cliv3.BeforeFunc {
	return func(ctx context.Context, cmd *cliv3.Command) (context.Context, error) {
		if cmd.Bool("version") {
			_ = writeVersion(d.Out, cmd.String("format"), d.Version)
			return ctx, exitError{code: 0}
		}

		cfg, isDefault := buildConfig(config.Defaults(), cmd)
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

// commandNotFound covers the help-machinery path for an unknown command. It
// cannot return an error, so it records one into cbErr for the boundary to emit
// and code as a usage error (64, EX_USAGE, per ADR 0001), matching rootAction.
func commandNotFound(cbErr *error) cliv3.CommandNotFoundFunc {
	return func(_ context.Context, _ *cliv3.Command, name string) {
		*cbErr = unknownCommandErr(name)
	}
}

// completionShellNotFound handles an unsupported shell token passed to the
// built-in completion command. Without it the unknown token falls through the
// help machinery; recording a usage-category error into cbErr makes the boundary
// emit a single usage JSON error on stderr and exit 64 (EX_USAGE, per ADR 0001),
// mirroring commandNotFound.
func completionShellNotFound(cbErr *error) cliv3.CommandNotFoundFunc {
	return func(_ context.Context, _ *cliv3.Command, name string) {
		*cbErr = &core.FeedError{
			Category: core.CatUsage,
			Message:  fmt.Sprintf("unsupported shell %q; supported shells are bash, zsh, fish, pwsh", name),
			Err:      core.ErrUsage,
		}
	}
}

// onUsageError is the sanctioned usage classifier of ADR 0002: the framework's
// flag-parsing and argument errors are untyped, so the single point where the
// framework surfaces them is turned into a usage-category FeedError that the
// boundary renders as structured JSON and codes 64 (EX_USAGE). It is isolated
// here in the interior and pinned by a wording test so a framework message
// change is caught.
func onUsageError(_ context.Context, _ *cliv3.Command, err error, _ bool) error {
	return &core.FeedError{Category: core.CatUsage, Message: err.Error(), Err: core.ErrUsage}
}

func unknownCommandErr(name string) *core.FeedError {
	return &core.FeedError{Category: core.CatUsage, Message: fmt.Sprintf("unknown command %q", name)}
}

// errRenderer builds a renderer for stderr error emission from the resolved
// global flags, defaulting to the JSON contract. The renderer is derived once
// after the command runs so the boundary in Run can emit a format-aware error
// even for a parse error that surfaced before the Before hook stashed one.
func errRenderer(d Deps, cmd *cliv3.Command) *output.Renderer {
	return output.NewRenderer(cmd.String("format"), d.Out, d.Err, output.ColorPolicy{NoColorFlag: cmd.Bool("no-color")})
}
