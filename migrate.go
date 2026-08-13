package feedwatch

import "context"

// MigrateApplied is the bare migrate result envelope: how many migrations were
// applied and the resulting store schema version (named store_schema_version to
// avoid colliding with the head's schema_version, the output-contract version).
type MigrateApplied struct {
	Head
	Applied            int `json:"applied"`
	StoreSchemaVersion int `json:"store_schema_version"`
}

// MigrateStatus is the migration-status result envelope: the applied store
// schema version, how many migrations remain, and which backend is in use.
type MigrateStatus struct {
	Head
	StoreSchemaVersion int    `json:"store_schema_version"`
	Pending            int    `json:"pending"`
	Backend            string `json:"backend"`
}

// Migrate applies pending schema migrations and reports how many it applied,
// along with the resulting store schema version.
//
// Unlike every other use case, it opens the store without the App's
// apply-once-on-first-use guard: were the migrations already applied behind that
// guard, the reported count would always be zero rather than what this call did.
func (a *App) Migrate(ctx context.Context) (MigrateApplied, error) {
	st, err := a.resolveStoreUnmigrated()
	if err != nil {
		return MigrateApplied{}, err
	}

	applied, err := st.Migrate(ctx)
	if err != nil {
		return MigrateApplied{}, err
	}
	a.markMigrated()

	version, err := st.SchemaVersion(ctx)
	if err != nil {
		return MigrateApplied{}, err
	}
	return MigrateApplied{Head: OKHead(), Applied: applied, StoreSchemaVersion: version}, nil
}

// MigrationStatus reports the store schema version, the pending migration count,
// and the backend in use. It applies pending migrations first, like every other
// use case, so the reported version is the one the use cases will actually run
// against and pending is zero on a healthy store.
func (a *App) MigrationStatus(ctx context.Context) (MigrateStatus, error) {
	st, err := a.resolveStore(ctx)
	if err != nil {
		return MigrateStatus{}, err
	}

	version, err := st.SchemaVersion(ctx)
	if err != nil {
		return MigrateStatus{}, err
	}
	pending, err := st.Pending(ctx)
	if err != nil {
		return MigrateStatus{}, err
	}
	return MigrateStatus{
		Head:               OKHead(),
		StoreSchemaVersion: version,
		Pending:            pending,
		Backend:            a.backend(),
	}, nil
}

// backend names the store driver this App's configuration selects, for the one
// envelope that reports it.
func (a *App) backend() string { return a.cfg.Backend() }
