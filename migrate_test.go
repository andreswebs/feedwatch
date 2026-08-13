package feedwatch_test

import (
	"context"
	"testing"

	"github.com/andreswebs/feedwatch"
	"github.com/andreswebs/feedwatch/core"
	"github.com/andreswebs/feedwatch/internal/testsupport"
)

// newMigrateApp builds an App over a store reporting pending migrations, so the
// applied count has something to report.
func newMigrateApp(t *testing.T, pending int) *feedwatch.App {
	t.Helper()

	st := testsupport.NewInMemoryStore(core.SystemClock)
	st.SetSchema(0, pending)
	app, err := feedwatch.New(feedwatch.Defaults(), feedwatch.WithStore(st))
	if err != nil {
		t.Fatalf("New = %v, want nil", err)
	}
	t.Cleanup(func() { _ = app.Close() })
	return app
}

// TestMigrateReportsTruthfulAppliedCount pins why Migrate bypasses the
// apply-once-per-App guard: were it to run behind the guard, the migrations
// would already be applied and the reported count would always be zero.
func TestMigrateReportsTruthfulAppliedCount(t *testing.T) {
	app := newMigrateApp(t, 3)
	ctx := context.Background()

	first, err := app.Migrate(ctx)
	if err != nil {
		t.Fatalf("Migrate = %v, want nil", err)
	}
	if first.Applied != 3 {
		t.Errorf("Applied = %d, want 3", first.Applied)
	}
	if first.StoreSchemaVersion != 3 {
		t.Errorf("StoreSchemaVersion = %d, want 3", first.StoreSchemaVersion)
	}

	second, err := app.Migrate(ctx)
	if err != nil {
		t.Fatalf("second Migrate = %v, want nil", err)
	}
	if second.Applied != 0 {
		t.Errorf("second Applied = %d, want 0: nothing was left pending", second.Applied)
	}
	if second.StoreSchemaVersion != first.StoreSchemaVersion {
		t.Errorf("version drifted: %d then %d", first.StoreSchemaVersion, second.StoreSchemaVersion)
	}
}

// TestMigrationStatusAppliesThenReports covers that status ensures the schema
// first, so it reports nothing pending and names the backend the configuration
// selects.
func TestMigrationStatusAppliesThenReports(t *testing.T) {
	app := newMigrateApp(t, 2)

	st, err := app.MigrationStatus(context.Background())
	if err != nil {
		t.Fatalf("MigrationStatus = %v, want nil", err)
	}
	if st.Pending != 0 {
		t.Errorf("Pending = %d, want 0 after ensuring the schema", st.Pending)
	}
	if st.StoreSchemaVersion != 2 {
		t.Errorf("StoreSchemaVersion = %d, want 2", st.StoreSchemaVersion)
	}
	if st.Backend != feedwatch.BackendSQLite {
		t.Errorf("Backend = %q, want %q", st.Backend, feedwatch.BackendSQLite)
	}
}

// TestMigrationStatusReportsConfiguredBackend covers that the reported backend
// is decided by the resolved store location's URL scheme.
func TestMigrationStatusReportsConfiguredBackend(t *testing.T) {
	cfg := feedwatch.Defaults()
	cfg.Store = "postgres://user@host/feedwatch"

	app, err := feedwatch.New(cfg)
	if err != nil {
		t.Fatalf("New = %v, want nil", err)
	}
	t.Cleanup(func() { _ = app.Close() })

	// Postgres is deferred, so status cannot reach a store; the classification
	// itself is what this asserts, through the configuration.
	if got := cfg.Backend(); got != feedwatch.BackendPostgres {
		t.Errorf("Backend() = %q, want %q", got, feedwatch.BackendPostgres)
	}
	if _, err := app.MigrationStatus(context.Background()); err == nil {
		t.Error("MigrationStatus on a postgres DSN = nil error, want a config error")
	}
}
