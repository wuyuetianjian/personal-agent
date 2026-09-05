package storage

import (
	"context"
	"path/filepath"
	"testing"
)

func TestMigrateSQLite(t *testing.T) {
	ctx := context.Background()
	db, err := OpenSQLite(ctx, filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatalf("OpenSQLite() error = %v", err)
	}
	defer db.Close()

	if err := Migrate(ctx, db.SQL); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	if _, err := db.SQL.ExecContext(ctx, `SELECT id, status FROM tasks LIMIT 1`); err != nil {
		t.Fatalf("tasks table query error = %v", err)
	}
	if _, err := db.SQL.ExecContext(ctx, `SELECT id, allowed_domains_json FROM browser_sessions LIMIT 1`); err != nil {
		t.Fatalf("browser_sessions table query error = %v", err)
	}
}

func TestCreateCompletedTask(t *testing.T) {
	ctx := context.Background()
	db, err := OpenSQLite(ctx, filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatalf("OpenSQLite() error = %v", err)
	}
	defer db.Close()
	if err := Migrate(ctx, db.SQL); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	answer := "No-op task completed."
	confidence := 1.0
	if err := db.CreateCompletedTask(ctx, Task{
		ID:              "task-1",
		Title:           "smoke",
		Input:           "smoke",
		LeaderModelID:   "local-planner",
		FinalAnswer:     &answer,
		FinalConfidence: &confidence,
	}); err != nil {
		t.Fatalf("CreateCompletedTask() error = %v", err)
	}
	count, err := db.TaskCount(ctx)
	if err != nil {
		t.Fatalf("TaskCount() error = %v", err)
	}
	if count != 1 {
		t.Fatalf("TaskCount() = %d, want 1", count)
	}
}
