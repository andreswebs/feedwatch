package feedwatch_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/andreswebs/feedwatch"
	"github.com/andreswebs/feedwatch/core"
	"github.com/andreswebs/feedwatch/internal/testsupport"
)

// countingStore counts Close calls so a test can assert that an injected store
// stays the embedder's to close.
type countingStore struct {
	*testsupport.InMemoryStore
	closes int
}

func (s *countingStore) Close() error {
	s.closes++
	return nil
}

// TestNewReturnsUsableApp is the tracer: the documented default path takes a
// config and no options.
func TestNewReturnsUsableApp(t *testing.T) {
	app, err := feedwatch.New(feedwatch.Defaults())
	if err != nil {
		t.Fatalf("New(Defaults()) = %v, want nil", err)
	}
	if app == nil {
		t.Fatal("New(Defaults()) returned a nil App")
	}
	if err := app.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
}

// TestNewRejectsInvalidConfig pins that New validates, so an unusable config
// fails at construction with a config-category error rather than at first use.
func TestNewRejectsInvalidConfig(t *testing.T) {
	app, err := feedwatch.New(feedwatch.Config{})
	if err == nil {
		t.Fatal("New(Config{}) = nil error, want a config error")
	}
	if !errors.Is(err, core.ErrConfig) {
		t.Errorf("errors.Is(err, core.ErrConfig) = false, want true; err = %v", err)
	}
	if app != nil {
		t.Errorf("New returned a non-nil App alongside an error")
	}
}

// TestNewPerformsNoIO pins the construction contract: New opens no store and
// creates no directory, so a use case that never touches the store (discover)
// leaves no database behind.
func TestNewPerformsNoIO(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_STATE_HOME", xdg)

	app, err := feedwatch.New(feedwatch.Defaults())
	if err != nil {
		t.Fatalf("New(Defaults()) = %v, want nil", err)
	}
	if err := app.Close(); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}

	entries, err := os.ReadDir(xdg)
	if err != nil {
		t.Fatalf("read %q: %v", xdg, err)
	}
	if len(entries) != 0 {
		t.Errorf("New created %d entries under %q, want none", len(entries), xdg)
	}
	if _, err := os.Stat(filepath.Join(xdg, "feedwatch")); !os.IsNotExist(err) {
		t.Errorf("stat default store dir: %v, want not-exist", err)
	}
}

// TestCloseIsIdempotentAndSparesInjectedStore pins the ownership rule: Close
// releases only what the App opened, and repeating it is safe.
func TestCloseIsIdempotentAndSparesInjectedStore(t *testing.T) {
	injected := &countingStore{InMemoryStore: testsupport.NewInMemoryStore(core.SystemClock)}

	app, err := feedwatch.New(feedwatch.Defaults(), feedwatch.WithStore(injected))
	if err != nil {
		t.Fatalf("New = %v, want nil", err)
	}
	for i := range 2 {
		if err := app.Close(); err != nil {
			t.Fatalf("Close() call %d = %v, want nil", i+1, err)
		}
	}
	if injected.closes != 0 {
		t.Errorf("injected store Close called %d times, want 0", injected.closes)
	}
}
