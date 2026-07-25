package cli

import (
	"os"
	"path/filepath"

	"github.com/andreswebs/feedwatch/internal/core"
)

// resolveStorePath turns the resolved --db value into the store location and
// reports whether it returned the tool-owned default. A non-empty value (from
// the flag or FEEDWATCH_DB) is used verbatim and reported as not default, so a
// postgres:// DSN or an explicit path passes through untouched and stays strict
// about missing directories. When unset, the default SQLite location follows
// the XDG Base Directory spec: $XDG_STATE_HOME/feedwatch/feedwatch.db, falling
// back to ~/.local/state/feedwatch/feedwatch.db, and is reported as default so
// the caller may create its parent directory.
func resolveStorePath(flagVal string) (path string, isDefault bool) {
	if flagVal != "" {
		return flagVal, false
	}
	if x := os.Getenv("XDG_STATE_HOME"); x != "" {
		return filepath.Join(x, "feedwatch", "feedwatch.db"), true
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".local", "state", "feedwatch", "feedwatch.db"), true
	}
	return filepath.Join("feedwatch", "feedwatch.db"), true
}

// ensureStoreDir creates the parent directory of the tool-owned default store
// path so a fresh machine needs no manual setup. It is only called for the
// default location; an explicit --db path stays strict and a missing
// intermediate directory there remains a store error. A creation failure is
// mapped to a store-category error so the boundary renders it consistently.
func ensureStoreDir(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return &core.FeedError{
			Category: core.CatStore,
			Message:  err.Error(),
			Err:      core.ErrStoreUnavailable,
		}
	}
	return nil
}
