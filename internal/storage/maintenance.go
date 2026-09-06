package storage

import (
	"context"
	"database/sql"
	"time"
)

func IntegrityCheck(ctx context.Context, db *sql.DB) (string, error) {
	var result string
	err := db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&result)
	return result, err
}

func Vacuum(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `VACUUM`)
	return err
}

func WALCheckpoint(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	return err
}

func DeleteEventsBefore(ctx context.Context, db *sql.DB, before time.Time) (int64, error) {
	result, err := db.ExecContext(ctx, `DELETE FROM events WHERE received_at < ?`, before.UTC())
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
