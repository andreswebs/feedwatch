package cli

import (
	"context"

	cliv3 "github.com/urfave/cli/v3"
)

// MigrateStatus is the migrate --status envelope: the applied schema version,
// how many migrations remain, and which backend is in use.
type MigrateStatus struct {
	SchemaVersion int    `json:"schema_version"`
	Pending       int    `json:"pending"`
	Backend       string `json:"backend"`
}

// MigrateApplied is the bare migrate envelope: how many migrations were applied
// and the resulting schema version.
type MigrateApplied struct {
	Applied       int `json:"applied"`
	SchemaVersion int `json:"schema_version"`
}

// migrateCommand registers the migrate subcommand: bare migrate applies pending
// migrations and reports the count; migrate --status ensures the schema is
// current and reports the resulting version, pending count, and backend. Both
// paths apply pending migrations, honoring the "any command applies pending
// migrations idempotently" contract; --status differs only in what it reports.
// The command is thin; all logic lives in the store.
func (d Deps) migrateCommand() *cliv3.Command {
	return &cliv3.Command{
		Name:  "migrate",
		Usage: "apply or inspect schema migrations",
		Flags: []cliv3.Flag{
			&cliv3.BoolFlag{
				Name:  "status",
				Usage: "report schema version, pending count, and backend without applying",
			},
		},
		Action: d.migrateAction,
	}
}

// migrateAction opens the store selected by the resolved --db and either reports
// migration status or applies pending migrations, rendering the result as JSON.
// Store and config failures propagate to the boundary, which maps them to exit 1.
func (d Deps) migrateAction(ctx context.Context, cmd *cliv3.Command) error {
	cfg := configFrom(ctx)
	r := rendererFrom(ctx)

	st, backend, err := openStore(cfg, d.Clock)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	if cmd.Bool("status") {
		if _, err := st.Migrate(ctx); err != nil {
			return err
		}
		version, err := st.SchemaVersion(ctx)
		if err != nil {
			return err
		}
		pending, err := st.Pending(ctx)
		if err != nil {
			return err
		}
		return r.Result(MigrateStatus{SchemaVersion: version, Pending: pending, Backend: backend})
	}

	applied, err := st.Migrate(ctx)
	if err != nil {
		return err
	}
	version, err := st.SchemaVersion(ctx)
	if err != nil {
		return err
	}
	return r.Result(MigrateApplied{Applied: applied, SchemaVersion: version})
}
