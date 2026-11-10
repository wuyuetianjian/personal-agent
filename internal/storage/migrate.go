package storage

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

func Migrate(ctx context.Context, db *sql.DB) error {
	// Refuse before creating even the migration ledger. Runtime startup also
	// performs this check using a read-only connection before enabling WAL.
	if err := ValidateSchema(ctx, db); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, migrationLedgerDDL); err != nil {
		return err
	}
	names, err := migrationNames()
	if err != nil {
		return err
	}

	for _, name := range names {
		applied, err := migrationApplied(ctx, db, name)
		if err != nil {
			return err
		}
		if applied {
			continue
		}
		content, err := migrationFiles.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		if err := applyMigration(ctx, db, name, string(content)); err != nil {
			return err
		}
	}
	return nil
}

func migrationApplied(ctx context.Context, db *sql.DB, version string) (bool, error) {
	var existing string
	err := db.QueryRowContext(ctx, `SELECT version FROM schema_migrations WHERE version = ?`, version).Scan(&existing)
	if err == nil {
		return true, nil
	}
	if err == sql.ErrNoRows {
		return false, nil
	}
	return false, err
}

// applyMigration commits schema changes and the ledger entry together. A failed
// statement must not leave an unrecorded, partially applied schema.
func applyMigration(ctx context.Context, db *sql.DB, name, content string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, content); err != nil {
		return fmt.Errorf("apply migration %s: %w", name, err)
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO schema_migrations(version) VALUES (?)", name); err != nil {
		return err
	}
	return tx.Commit()
}
