package feedwatch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/andreswebs/feedwatch/core"
	"github.com/andreswebs/feedwatch/internal/testsupport"
)

// TestResolveStoreHonorsInjectedStore asserts through the internal resolver that
// WithStore short-circuits backend selection entirely: the injected value is
// returned and no database file appears at the default location.
func TestResolveStoreHonorsInjectedStore(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_STATE_HOME", xdg)
	injected := testsupport.NewInMemoryStore(core.SystemClock)

	app, err := New(Defaults(), WithStore(injected))
	if err != nil {
		t.Fatalf("New = %v, want nil", err)
	}
	t.Cleanup(func() { _ = app.Close() })

	got, err := app.resolveStore(context.Background())
	if err != nil {
		t.Fatalf("resolveStore = %v, want nil", err)
	}
	if got != injected {
		t.Errorf("resolveStore returned %#v, want the injected store", got)
	}
	if _, err := os.Stat(filepath.Join(xdg, "feedwatch")); !os.IsNotExist(err) {
		t.Errorf("stat default store dir: %v, want not-exist", err)
	}
}

// TestResolveStoreMigratesOnce pins the "any command applies pending migrations
// idempotently" contract at the App level, and that the work happens once per
// App rather than on every resolution.
func TestResolveStoreMigratesOnce(t *testing.T) {
	injected := testsupport.NewInMemoryStore(core.SystemClock)
	injected.SetSchema(0, 3)

	app, err := New(Defaults(), WithStore(injected))
	if err != nil {
		t.Fatalf("New = %v, want nil", err)
	}
	t.Cleanup(func() { _ = app.Close() })

	for i := range 2 {
		if _, err := app.resolveStore(context.Background()); err != nil {
			t.Fatalf("resolveStore call %d = %v, want nil", i+1, err)
		}
	}

	pending, err := injected.Pending(context.Background())
	if err != nil {
		t.Fatalf("Pending = %v, want nil", err)
	}
	if pending != 0 {
		t.Errorf("pending migrations = %d, want 0", pending)
	}
	version, err := injected.SchemaVersion(context.Background())
	if err != nil {
		t.Fatalf("SchemaVersion = %v, want nil", err)
	}
	if version != 3 {
		t.Errorf("schema version = %d, want 3 (migrated exactly once)", version)
	}
}

// TestResolveStoreClosesOnlyWhatItOpened covers the owned-store half of the
// Close contract: a store the App opened is closed, and closing twice is safe.
func TestResolveStoreClosesOnlyWhatItOpened(t *testing.T) {
	cfg := Defaults()
	cfg.Store = filepath.Join(t.TempDir(), "feedwatch.db")

	app, err := New(cfg)
	if err != nil {
		t.Fatalf("New = %v, want nil", err)
	}
	if _, err := app.resolveStore(context.Background()); err != nil {
		t.Fatalf("resolveStore = %v, want nil", err)
	}
	for i := range 2 {
		if err := app.Close(); err != nil {
			t.Fatalf("Close() call %d = %v, want nil", i+1, err)
		}
	}
}

// TestPostgresDSNFailsAtFirstUse pins where the deferred backend surfaces: a
// config-category error on first store use, not at construction.
func TestPostgresDSNFailsAtFirstUse(t *testing.T) {
	cfg := Defaults()
	cfg.Store = "postgres://user@host/feedwatch"

	app, err := New(cfg)
	if err != nil {
		t.Fatalf("New with a postgres DSN = %v, want nil (no I/O at construction)", err)
	}
	t.Cleanup(func() { _ = app.Close() })

	_, err = app.resolveStore(context.Background())
	if err == nil {
		t.Fatal("resolveStore = nil error, want a config error")
	}
	var fe *core.FeedError
	if !errors.As(err, &fe) {
		t.Fatalf("errors.As(err, *core.FeedError) = false; err = %v", err)
	}
	if fe.Category != core.CatConfig {
		t.Errorf("category = %q, want %q", fe.Category, core.CatConfig)
	}
	if !errors.Is(err, core.ErrConfig) {
		t.Errorf("errors.Is(err, core.ErrConfig) = false, want true")
	}
}

// TestResolveFetcherAndParserHonorInjection asserts the ports are seams: an
// injected fetcher and parser are handed back rather than production ones.
func TestResolveFetcherAndParserHonorInjection(t *testing.T) {
	fetcher := testsupport.NewFakeFetcher()
	parser := testsupport.NewFakeParser()

	app, err := New(Defaults(), WithFetcher(fetcher), WithParser(parser))
	if err != nil {
		t.Fatalf("New = %v, want nil", err)
	}
	t.Cleanup(func() { _ = app.Close() })

	gotFetcher, err := app.resolveFetcher()
	if err != nil {
		t.Fatalf("resolveFetcher = %v, want nil", err)
	}
	if gotFetcher != Fetcher(fetcher) {
		t.Errorf("resolveFetcher returned %#v, want the injected fetcher", gotFetcher)
	}
	if got := app.resolveParser(); got != Parser(parser) {
		t.Errorf("resolveParser returned %#v, want the injected parser", got)
	}
}

// TestResolveFetcherBuildsFromConfig covers the default path: with no injection
// the fetcher is built from the config, once.
func TestResolveFetcherBuildsFromConfig(t *testing.T) {
	app, err := New(Defaults())
	if err != nil {
		t.Fatalf("New = %v, want nil", err)
	}
	t.Cleanup(func() { _ = app.Close() })

	first, err := app.resolveFetcher()
	if err != nil {
		t.Fatalf("resolveFetcher = %v, want nil", err)
	}
	if first == nil {
		t.Fatal("resolveFetcher returned a nil Fetcher")
	}
	second, err := app.resolveFetcher()
	if err != nil {
		t.Fatalf("resolveFetcher (second) = %v, want nil", err)
	}
	if first != second {
		t.Error("resolveFetcher built a second fetcher, want the cached one")
	}
}

// TestWarnerReceivesAdvisories covers the warning port: an advisory reaches the
// wired Warner verbatim, and dropping it is safe when none is wired.
func TestWarnerReceivesAdvisories(t *testing.T) {
	type advisory struct {
		code, message, hint string
		details             any
	}
	var got []advisory

	app, err := New(Defaults(), WithWarner(func(code, message, hint string, details any) {
		got = append(got, advisory{code, message, hint, details})
	}))
	if err != nil {
		t.Fatalf("New = %v, want nil", err)
	}
	t.Cleanup(func() { _ = app.Close() })

	app.warnf("feed_auto_disabled", "feed disabled after 10 failures", "run feedwatch enable", 10)

	if len(got) != 1 {
		t.Fatalf("warner received %d advisories, want 1", len(got))
	}
	want := advisory{"feed_auto_disabled", "feed disabled after 10 failures", "run feedwatch enable", 10}
	if got[0] != want {
		t.Errorf("advisory = %+v, want %+v", got[0], want)
	}

	quiet, err := New(Defaults())
	if err != nil {
		t.Fatalf("New = %v, want nil", err)
	}
	t.Cleanup(func() { _ = quiet.Close() })
	quiet.warnf("code", "message", "", nil) // must not panic with no warner wired
}
