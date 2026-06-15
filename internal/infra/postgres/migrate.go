package postgres

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
)

// startupMigrations are small, idempotent schema fix-ups applied on every API
// server start. The base schema lives in migrations/001_init.sql, which only
// runs on a *fresh* postgres volume (docker-entrypoint-initdb.d). Existing
// deployments keep their volume across rebuilds, so any post-001 schema change
// must also be expressed here to reach them.
//
// Every statement MUST be idempotent (safe to run repeatedly).
var startupMigrations = []struct {
	name string
	stmt string
}{
	{
		// Email is now optional: accounts may exist without one and bind later.
		// DROP NOT NULL is a no-op if the column is already nullable.
		name: "users.email drop not null",
		stmt: `ALTER TABLE users ALTER COLUMN email DROP NOT NULL`,
	},
	{
		// Normalise any legacy empty-string emails to NULL so the UNIQUE
		// constraint allows multiple unbound accounts (Postgres permits many
		// NULLs but only one '').
		name: "users.email empty -> null",
		stmt: `UPDATE users SET email = NULL WHERE email = ''`,
	},
}

// RunStartupMigrations applies idempotent schema fix-ups. It is safe to call on
// both fresh and existing databases.
func RunStartupMigrations(ctx context.Context, db *sqlx.DB) error {
	for _, m := range startupMigrations {
		if _, err := db.ExecContext(ctx, m.stmt); err != nil {
			return fmt.Errorf("migration %q: %w", m.name, err)
		}
	}
	return nil
}
