// Package migrate applies plain SQL files in name order, each in its own transaction.
package migrate

import (
	"context"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
)

// One fixed key, so two deploys that start together apply each file once.
const lockKey = 7_265_731_642

const bookkeeping = `
create schema if not exists terka;
create table if not exists terka.schema_migrations (
	version text primary key,
	applied_at timestamptz not null default now()
);`

// Apply runs every *.sql file in files that is not yet recorded and returns the versions it ran.
// A recorded version with no file is left alone: that is what an older image sees after a rollback.
func Apply(ctx context.Context, conn *pgx.Conn, files fs.FS) (applied []string, err error) {
	if _, err := conn.Exec(ctx, "select pg_advisory_lock($1)", lockKey); err != nil {
		return nil, fmt.Errorf("take the migration lock: %w", err)
	}
	defer func() {
		// A background context: the caller's may already be done, and the lock must still go.
		if _, unlockErr := conn.Exec(context.Background(), "select pg_advisory_unlock($1)", lockKey); unlockErr != nil && err == nil {
			err = fmt.Errorf("release the migration lock: %w", unlockErr)
		}
	}()

	if _, err := conn.Exec(ctx, bookkeeping, pgx.QueryExecModeSimpleProtocol); err != nil {
		return nil, fmt.Errorf("prepare terka.schema_migrations: %w", err)
	}
	rows, err := conn.Query(ctx, "select version from terka.schema_migrations")
	if err != nil {
		return nil, err
	}
	done, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}

	names, err := fs.Glob(files, "*.sql")
	if err != nil {
		return nil, err
	}
	slices.Sort(names)
	for _, name := range names {
		version := strings.TrimSuffix(name, ".sql")
		if slices.Contains(done, version) {
			continue
		}
		if err := applyFile(ctx, conn, files, name, version); err != nil {
			return applied, err
		}
		applied = append(applied, version)
	}
	return applied, nil
}

func applyFile(ctx context.Context, conn *pgx.Conn, files fs.FS, name, version string) error {
	body, err := fs.ReadFile(files, name)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		// The simple protocol lets one file hold many statements, including function bodies.
		if _, err := tx.Exec(ctx, string(body), pgx.QueryExecModeSimpleProtocol); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, "insert into terka.schema_migrations (version) values ($1)", version); err != nil {
			return fmt.Errorf("%s: record version: %w", name, err)
		}
		return nil
	})
}
