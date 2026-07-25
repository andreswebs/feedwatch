package config_test

import (
	"crypto/tls"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/andreswebs/feedwatch/internal/config"
	"github.com/andreswebs/feedwatch/internal/core"
)

func TestDefaultsMatchAppendixA(t *testing.T) {
	d := config.Defaults()

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
	if err := config.Defaults().Validate(); err != nil {
		t.Fatalf("Defaults().Validate() = %v, want nil", err)
	}
}

func TestValidateRejectsBadRanges(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*config.Config)
	}{
		{"zero concurrency", func(c *config.Config) { c.Concurrency = 0 }},
		{"negative concurrency", func(c *config.Config) { c.Concurrency = -1 }},
		{"zero connect timeout", func(c *config.Config) { c.ConnectTimeout = 0 }},
		{"negative timeout", func(c *config.Config) { c.Timeout = -time.Second }},
		{"unknown format", func(c *config.Config) { c.Format = "yaml" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := config.Defaults()
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
	c := config.Defaults()
	c.Format = "text"
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate() with text format = %v, want nil", err)
	}
}
