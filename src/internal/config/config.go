package config

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"time"

	"github.com/andreswebs/feedwatch/internal/core"
)

// Config is the resolved, immutable configuration handed to every component.
// The cli package assembles it from flags, environment, and Defaults (with
// precedence flags > env > defaults); no other package parses flags or env.
type Config struct {
	Store            string        // FEEDWATCH_DB: filesystem path or postgres:// DSN
	UserAgent        string        // FEEDWATCH_USER_AGENT
	DefaultInterval  time.Duration // default poll interval
	Concurrency      int           // worker pool size
	ConnectTimeout   time.Duration // dial deadline
	Timeout          time.Duration // overall per-feed deadline
	PerHostDelay     time.Duration // politeness delay between same-host requests
	RetryAttempts    int           // in-call transient retry attempts
	FailureThreshold int           // consecutive failures before auto-disable
	MaxBackoff       time.Duration // ceiling for failure backoff
	MinTLS           uint16        // minimum TLS version, e.g. tls.VersionTLS12
	Proxy            string        // outbound HTTP proxy URL
	CABundle         string        // path to a custom CA bundle
	AllowPrivate     bool          // allow redirects into private address space
	Format           string        // "json" | "text"
	NoColor          bool          // disable color in text format
	LogLevel         slog.Level    // slog level for stderr logs
	Quiet            bool          // raise the log floor to errors only
}

// DefaultUserAgent is sent when no user agent is configured.
const DefaultUserAgent = "feedwatch"

// Defaults returns the configuration applied when a setting is not overridden,
// matching the documented default table (requirements Appendix A). It is the
// single source of those defaults; the cli layer overlays flags and env on top.
//
// Store is left empty here: resolving the default state path is the cli layer's
// concern, so this value stays free of environment and filesystem lookups.
func Defaults() Config {
	return Config{
		UserAgent:        DefaultUserAgent,
		DefaultInterval:  time.Hour,
		Concurrency:      8,
		ConnectTimeout:   5 * time.Second,
		Timeout:          30 * time.Second,
		PerHostDelay:     time.Second,
		RetryAttempts:    3,
		FailureThreshold: 10,
		MaxBackoff:       24 * time.Hour,
		MinTLS:           tls.VersionTLS12,
		AllowPrivate:     false,
		Format:           "json",
		NoColor:          false,
		LogLevel:         slog.LevelInfo,
		Quiet:            false,
	}
}

// Validate reports whether the resolved configuration is usable. A failure
// wraps core.ErrConfig so the boundary can classify it with errors.Is and map
// it to exit 1.
func (c Config) Validate() error {
	if c.Concurrency < 1 {
		return fmt.Errorf("%w: concurrency must be at least 1, got %d", core.ErrConfig, c.Concurrency)
	}
	if c.ConnectTimeout <= 0 {
		return fmt.Errorf("%w: connect timeout must be positive, got %s", core.ErrConfig, c.ConnectTimeout)
	}
	if c.Timeout <= 0 {
		return fmt.Errorf("%w: timeout must be positive, got %s", core.ErrConfig, c.Timeout)
	}
	switch c.Format {
	case "json", "text":
	default:
		return fmt.Errorf("%w: unknown format %q, want json or text", core.ErrConfig, c.Format)
	}
	return nil
}
