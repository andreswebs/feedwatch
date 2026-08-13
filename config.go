package feedwatch

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/andreswebs/feedwatch/core"
)

// Config is the resolved, immutable configuration handed to New and from there
// to every component. A frontend assembles it from its own input surface (the
// CLI overlays flags and environment on top of Defaults, with precedence flags
// > env > defaults); no library package parses flags or environment.
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

// The store backends feedwatch can name. The value is reported verbatim by
// migrate, so it is part of the output contract.
const (
	// BackendSQLite names the default filesystem backend.
	BackendSQLite = "sqlite"
	// BackendPostgres names the remote backend a postgres:// DSN selects.
	BackendPostgres = "postgres"
)

// Backend names the store driver this configuration selects, classifying the
// store location by URL scheme: a postgres:// (or postgresql://) DSN selects the
// Postgres backend, anything else is a SQLite filesystem path, including the
// empty value that means the default location.
func (c Config) Backend() string {
	if strings.HasPrefix(c.Store, "postgres://") || strings.HasPrefix(c.Store, "postgresql://") {
		return BackendPostgres
	}
	return BackendSQLite
}

// Defaults returns the configuration applied when a setting is not overridden,
// matching the documented default table (requirements Appendix A). It is the
// single source of those defaults; a frontend overlays its own input on top.
//
// Store is left empty here, meaning "use the tool-owned default location".
// Resolving that location is StorePath's job, so this value stays free of
// environment and filesystem lookups.
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
// it to exit 78 (EX_CONFIG).
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

// StorePath resolves the store location this configuration selects. A non-empty
// Store is used verbatim, so a postgres:// DSN or an explicit path passes
// through untouched and stays strict about missing directories. An empty Store
// means the tool-owned default from DefaultStorePath, whose parent directory is
// created here so a fresh machine needs no manual setup.
//
// It is called on first store use rather than at construction, so a use case
// that never opens a store creates nothing.
func (c Config) StorePath() (string, error) {
	if c.Store != "" {
		return c.Store, nil
	}
	path := DefaultStorePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", &core.FeedError{
			Category: core.CatStore,
			Message:  err.Error(),
			Err:      core.ErrStoreUnavailable,
		}
	}
	return path, nil
}

// DefaultStorePath returns the tool-owned default SQLite location, following the
// XDG Base Directory spec: $XDG_STATE_HOME/feedwatch/feedwatch.db, falling back
// to ~/.local/state/feedwatch/feedwatch.db, and to a relative feedwatch/ when
// there is no home directory. It creates nothing.
func DefaultStorePath() string {
	if x := os.Getenv("XDG_STATE_HOME"); x != "" {
		return filepath.Join(x, "feedwatch", "feedwatch.db")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".local", "state", "feedwatch", "feedwatch.db")
	}
	return filepath.Join("feedwatch", "feedwatch.db")
}
