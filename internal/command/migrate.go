package command

import (
	"context"

	cliv3 "github.com/urfave/cli/v3"
)

// migrateCommand registers the migrate subcommand: bare migrate applies pending
// migrations and reports the count; migrate --status ensures the schema is
// current and reports the resulting version, pending count, and backend. Both
// paths apply pending migrations, honoring the "any command applies pending
// migrations idempotently" contract; --status differs only in what it reports.
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

// migrateAction delegates to the library's migrate use case, selecting the
// status shape when --status is given, and renders the envelope. Store and
// config failures propagate to the boundary, which maps them to exit 69 (store
// unavailable) or 78 (config) respectively.
func (d Deps) migrateAction(ctx context.Context, cmd *cliv3.Command) error {
	app, err := d.app(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = app.Close() }()

	r := rendererFrom(ctx)
	if cmd.Bool("status") {
		res, err := app.MigrationStatus(ctx)
		if err != nil {
			return err
		}
		return r.Result(res)
	}

	res, err := app.Migrate(ctx)
	if err != nil {
		return err
	}
	return r.Result(res)
}
