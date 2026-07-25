package cli

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"slices"

	cliv3 "github.com/urfave/cli/v3"

	"github.com/andreswebs/feedwatch/internal/config"
)

// globalFlags returns the flags defined on the root and inherited by every
// subcommand. A flag's Value supplies the compiled-in default, Sources supply
// the environment layer, and the command line overrides both, so configuration
// precedence is flags > environment > defaults. Defaults mirror config.Defaults
// and the documented default table.
func globalFlags(base config.Config) []cliv3.Flag {
	return []cliv3.Flag{
		&cliv3.StringFlag{
			Name:    "db",
			Usage:   "store location: a filesystem `PATH` or a postgres:// DSN",
			Sources: cliv3.EnvVars("FEEDWATCH_DB"),
		},
		&cliv3.StringFlag{
			Name:      "format",
			Usage:     "output `FORMAT`: json or text",
			Value:     base.Format,
			Sources:   cliv3.EnvVars("FEEDWATCH_FORMAT"),
			Validator: oneOf("format", "json", "text"),
		},
		&cliv3.StringFlag{
			Name:      "log-level",
			Usage:     "log `LEVEL`: error, warn, info, or debug",
			Value:     levelString(base.LogLevel),
			Validator: oneOf("log-level", "error", "warn", "info", "debug"),
		},
		&cliv3.BoolFlag{
			Name:  "quiet",
			Usage: "raise the log floor to errors only",
			Value: base.Quiet,
		},
		&cliv3.BoolFlag{
			Name:  "no-color",
			Usage: "disable color in text output",
			Value: base.NoColor,
		},
		&cliv3.StringFlag{
			Name:    "user-agent",
			Usage:   "HTTP User-Agent header",
			Value:   base.UserAgent,
			Sources: cliv3.EnvVars("FEEDWATCH_USER_AGENT"),
		},
		&cliv3.IntFlag{
			Name:    "concurrency",
			Usage:   "worker pool size for concurrent polling",
			Value:   base.Concurrency,
			Sources: cliv3.EnvVars("FEEDWATCH_CONCURRENCY"),
		},
		&cliv3.DurationFlag{
			Name:  "connect-timeout",
			Usage: "dial deadline per feed",
			Value: base.ConnectTimeout,
		},
		&cliv3.DurationFlag{
			Name:  "timeout",
			Usage: "overall deadline per feed",
			Value: base.Timeout,
		},
		&cliv3.StringFlag{
			Name:  "proxy",
			Usage: "outbound HTTP proxy `URL`",
			Value: base.Proxy,
		},
		&cliv3.StringFlag{
			Name:      "ca-bundle",
			Usage:     "path to a custom CA bundle `FILE`",
			Value:     base.CABundle,
			TakesFile: true,
		},
		&cliv3.StringFlag{
			Name:      "min-tls",
			Usage:     "minimum TLS `VERSION`: 1.2 or 1.3",
			Value:     tlsString(base.MinTLS),
			Validator: oneOf("min-tls", "1.2", "1.3"),
		},
		&cliv3.BoolFlag{
			Name:  "allow-private",
			Usage: "allow redirects into private address space",
			Value: base.AllowPrivate,
		},
	}
}

// oneOf builds a validator that rejects any value outside the allowed set,
// producing a Go-style lowercase message that surfaces in the usage error.
func oneOf(name string, allowed ...string) func(string) error {
	return func(v string) error {
		if slices.Contains(allowed, v) {
			return nil
		}
		return fmt.Errorf("invalid %s %q: want one of %v", name, v, allowed)
	}
}

// parseLevel maps a validated log-level string to a slog.Level. An unrecognized
// value falls back to info, but the flag validator prevents that in practice.
func parseLevel(s string) slog.Level {
	switch s {
	case "error":
		return slog.LevelError
	case "warn":
		return slog.LevelWarn
	case "debug":
		return slog.LevelDebug
	default:
		return slog.LevelInfo
	}
}

// levelString is the inverse of parseLevel, used to seed the flag default from
// the resolved configuration.
func levelString(l slog.Level) string {
	switch l {
	case slog.LevelError:
		return "error"
	case slog.LevelWarn:
		return "warn"
	case slog.LevelDebug:
		return "debug"
	default:
		return "info"
	}
}

// parseMinTLS maps a validated min-tls string to the crypto/tls version
// constant; any non-"1.3" value resolves to TLS 1.2.
func parseMinTLS(s string) uint16 {
	if s == "1.3" {
		return tls.VersionTLS13
	}
	return tls.VersionTLS12
}

// tlsString is the inverse of parseMinTLS for seeding the flag default.
func tlsString(v uint16) string {
	if v == tls.VersionTLS13 {
		return "1.3"
	}
	return "1.2"
}
