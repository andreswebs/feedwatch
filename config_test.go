package feedwatch_test

import (
	"crypto/tls"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/andreswebs/feedwatch"
	"github.com/andreswebs/feedwatch/core"
)

func TestDefaultsMatchAppendixA(t *testing.T) {
	d := feedwatch.Defaults()

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"Concurrency", d.Concurrency, 8},
		{"DefaultInterval", d.DefaultInterval, time.Hour},
		{"ConnectTimeout", d.ConnectTimeout, 5 * time.Second},
		{"Timeout", d.Timeout, 30 * time.Second},
		{"PerHostDelay", d.PerHostDelay, time.Second},
		{"RetryAttempts", d.RetryAttempts, 3},
		{"FailureThreshold", d.FailureThreshold, 10},
		{"MaxBackoff", d.MaxBackoff, 24 * time.Hour},
		{"MinTLS", d.MinTLS, uint16(tls.VersionTLS12)},
		{"AllowPrivate", d.AllowPrivate, false},
		{"Format", d.Format, "json"},
		{"NoColor", d.NoColor, false},
		{"LogLevel", d.LogLevel, slog.LevelInfo},
		{"Quiet", d.Quiet, false},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("Defaults().%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

func TestValidateAcceptsDefaults(t *testing.T) {
	if err := feedwatch.Defaults().Validate(); err != nil {
		t.Fatalf("Defaults().Validate() = %v, want nil", err)
	}
}

func TestValidateRejectsBadRanges(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*feedwatch.Config)
	}{
		{"zero concurrency", func(c *feedwatch.Config) { c.Concurrency = 0 }},
		{"negative concurrency", func(c *feedwatch.Config) { c.Concurrency = -1 }},
		{"zero connect timeout", func(c *feedwatch.Config) { c.ConnectTimeout = 0 }},
		{"negative timeout", func(c *feedwatch.Config) { c.Timeout = -time.Second }},
		{"unknown format", func(c *feedwatch.Config) { c.Format = "yaml" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := feedwatch.Defaults()
			tc.mutate(&c)
			err := c.Validate()
			if err == nil {
				t.Fatalf("Validate() = nil, want error")
			}
			if !errors.Is(err, core.ErrConfig) {
				t.Errorf("errors.Is(err, core.ErrConfig) = false, want true; err = %v", err)
			}
		})
	}
}

func TestValidateAcceptsTextFormat(t *testing.T) {
	c := feedwatch.Defaults()
	c.Format = "text"
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate() with text format = %v, want nil", err)
	}
}

// TestDefaultStorePath pins the resolution order of the tool-owned default
// location: XDG_STATE_HOME first, then the home directory.
func TestDefaultStorePath(t *testing.T) {
	xdg := t.TempDir()
	home := t.TempDir()

	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{
			name: "XDG_STATE_HOME wins",
			env:  map[string]string{"XDG_STATE_HOME": xdg, "HOME": home},
			want: filepath.Join(xdg, "feedwatch", "feedwatch.db"),
		},
		{
			name: "home directory fallback",
			env:  map[string]string{"XDG_STATE_HOME": "", "HOME": home},
			want: filepath.Join(home, ".local", "state", "feedwatch", "feedwatch.db"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			if got := feedwatch.DefaultStorePath(); got != tc.want {
				t.Errorf("DefaultStorePath() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestStorePathExplicitValuePassesThrough pins that a configured location is
// used verbatim and creates no directory, so an explicit path stays strict.
func TestStorePathExplicitValuePassesThrough(t *testing.T) {
	const dsn = "postgres://user@host/feedwatch"
	c := feedwatch.Defaults()
	c.Store = dsn

	got, err := c.StorePath()
	if err != nil {
		t.Fatalf("StorePath() = %v, want nil", err)
	}
	if got != dsn {
		t.Errorf("StorePath() = %q, want %q unchanged", got, dsn)
	}
}

// TestStorePathCreatesDefaultDirectory covers the first-run contract: resolving
// the tool-owned default creates its parent directory so a fresh machine needs
// no manual setup.
func TestStorePathCreatesDefaultDirectory(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_STATE_HOME", xdg)

	got, err := feedwatch.Defaults().StorePath()
	if err != nil {
		t.Fatalf("StorePath() = %v, want nil", err)
	}
	want := filepath.Join(xdg, "feedwatch", "feedwatch.db")
	if got != want {
		t.Fatalf("StorePath() = %q, want %q", got, want)
	}
	info, err := os.Stat(filepath.Dir(want))
	if err != nil {
		t.Fatalf("default store dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("%q is not a directory", filepath.Dir(want))
	}
}
