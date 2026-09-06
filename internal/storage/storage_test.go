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

func TestMigrateFromP13Snapshot(t *testing.T) {
	ctx := context.Background()
	db, err := OpenSQLite(ctx, filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatalf("OpenSQLite() error = %v", err)
	}
	defer db.Close()
	if _, err := db.SQL.ExecContext(ctx, `CREATE TABLE schema_migrations (version text primary key, applied_at timestamp not null DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"0001_initial.sql", "0002_p9_core.sql", "0003_p10_core.sql", "0004_p13_audit.sql"} {
		content, err := migrationFiles.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.SQL.ExecContext(ctx, string(content)); err != nil {
			t.Fatalf("apply snapshot migration %s: %v", name, err)
		}
		if _, err := db.SQL.ExecContext(ctx, `INSERT INTO schema_migrations (version) VALUES (?)`, name); err != nil {
			t.Fatal(err)
		}
	}
	if err := Migrate(ctx, db.SQL); err != nil {
		t.Fatalf("Migrate() from P13 snapshot error = %v", err)
	}
	if _, err := db.SQL.ExecContext(ctx, `SELECT dependencies_json, dag_version FROM workflow_nodes LIMIT 1`); err != nil {
		t.Fatalf("workflow node P14 columns missing: %v", err)
	}
	if _, err := db.SQL.ExecContext(ctx, `SELECT id, status FROM confirmation_requests LIMIT 1`); err != nil {
		t.Fatalf("confirmation_requests missing: %v", err)
	}
}
