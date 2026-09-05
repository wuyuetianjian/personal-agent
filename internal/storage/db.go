package storage

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"time"

	"agent/internal/config"

	_ "modernc.org/sqlite"
)

var ErrUnsupportedDriver = errors.New("unsupported storage driver")

type DB struct {
	SQL *sql.DB
}

type PostgresConnector interface {
	Open(ctx context.Context, dsn string) (*sql.DB, error)
}

func Open(ctx context.Context, cfg config.StorageConfig) (*DB, error) {
	switch cfg.Driver {
	case "sqlite":
		if err := os.MkdirAll(filepath.Dir(cfg.SQLite.Path), 0o755); err != nil {
			return nil, err
		}
		db, err := sql.Open("sqlite", cfg.SQLite.Path)
		if err != nil {
			return nil, err
		}
		if err := db.PingContext(ctx); err != nil {
			db.Close()
			return nil, err
		}
		return &DB{SQL: db}, nil
	default:
		return nil, ErrUnsupportedDriver
	}
}

func OpenSQLite(ctx context.Context, path string) (*DB, error) {
	return Open(ctx, config.StorageConfig{
		Driver: "sqlite",
		SQLite: config.SQLiteConfig{Path: path},
	})
}

func (db *DB) Close() error {
	if db == nil || db.SQL == nil {
		return nil
	}
	return db.SQL.Close()
}

type Task struct {
	ID              string
	Title           string
	Input           string
	Status          string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	CompletedAt     *time.Time
	LeaderModelID   string
	PrivacyClass    string
	FinalAnswer     *string
	FinalConfidence *float64
	ErrorCategory   *string
	ErrorMessage    *string
}

func (db *DB) CreateCompletedTask(ctx context.Context, task Task) error {
	now := time.Now().UTC()
	if task.CreatedAt.IsZero() {
		task.CreatedAt = now
	}
	if task.UpdatedAt.IsZero() {
		task.UpdatedAt = now
	}
	if task.CompletedAt == nil {
		completedAt := now
		task.CompletedAt = &completedAt
	}
	if task.Status == "" {
		task.Status = "completed"
	}
	if task.PrivacyClass == "" {
		task.PrivacyClass = "local_private"
	}
	_, err := db.SQL.ExecContext(ctx, `
INSERT INTO tasks (
  id, title, input, status, created_at, updated_at, completed_at, cancelled_at,
  leader_model_id, privacy_class, final_answer, final_confidence, error_category, error_message
) VALUES (?, ?, ?, ?, ?, ?, ?, NULL, ?, ?, ?, ?, ?, ?)
`, task.ID, task.Title, task.Input, task.Status, task.CreatedAt, task.UpdatedAt, task.CompletedAt,
		task.LeaderModelID, task.PrivacyClass, task.FinalAnswer, task.FinalConfidence, task.ErrorCategory, task.ErrorMessage)
	return err
}

func (db *DB) TaskCount(ctx context.Context) (int, error) {
	var count int
	err := db.SQL.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks`).Scan(&count)
	return count, err
}
