package storage

import (
	"context"
	"path/filepath"
	"testing"
)

func TestInconsistentMigrationHistoryRefusedWithoutRepair(t *testing.T) {
	ctx := context.Background()
	db, e := OpenSQLite(ctx, filepath.Join(t.TempDir(), "old.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if e = Migrate(ctx, db.SQL); e != nil {
		t.Fatal(e)
	}
	if _, e = db.SQL.Exec("INSERT INTO interaction_sessions(id) VALUES ('legacy')"); e != nil {
		t.Fatal(e)
	}
	// The missing ledger entry and table must be refused, not replayed/repaired.
	if _, e = db.SQL.Exec("DELETE FROM schema_migrations WHERE version='0012_interaction.sql'; DROP TABLE interaction_sessions"); e != nil {
		t.Fatal(e)
	}
	if e = Migrate(ctx, db.SQL); e == nil {
		t.Fatal("expected upgrade failure")
	}
	var n int
	if e = db.SQL.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='interaction_sessions'").Scan(&n); e != nil || n != 0 {
		t.Fatalf("partial schema survived: %d %v", n, e)
	}
	if _, e = db.SQL.Exec("SELECT id FROM tasks LIMIT 1"); e != nil {
		t.Fatal(e)
	}
}
func TestInteractionUpgradeEmbeddedPrefixFixture(t *testing.T) {
	ctx := context.Background()
	db, e := OpenSQLite(ctx, filepath.Join(t.TempDir(), "legacy.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	// Build an actual pre-interaction snapshot through the same embedded SQL files.
	if _, e = db.SQL.Exec("CREATE TABLE schema_migrations(version TEXT PRIMARY KEY, applied_at timestamp DEFAULT CURRENT_TIMESTAMP)"); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"0001_initial.sql", "0002_p9_core.sql", "0003_p10_core.sql", "0004_p13_audit.sql", "0005_p14_pre_workflow.sql", "0006_p14_pre_proactive.sql", "0007_side_effect_idempotency.sql", "0008_project_allowed_models.sql", "0009_condition_watcher_state.sql", "0010_notification_policy.sql", "0011_goal_runtime_persistence.sql"} {
		data, e := migrationFiles.ReadFile("migrations/" + name)
		if e != nil {
			t.Fatal(e)
		}
		if e = applyMigration(ctx, db.SQL, name, string(data)); e != nil {
			t.Fatal(e)
		}
	}
	if e = db.CreateCompletedTask(ctx, Task{ID: "legacy", Title: "retained", Input: "old input"}); e != nil {
		t.Fatal(e)
	}
	if e = Migrate(ctx, db.SQL); e != nil {
		t.Fatal(e)
	}
	task, e := db.GetTask(ctx, "legacy")
	if e != nil || task.Input != "old input" {
		t.Fatalf("%+v %v", task, e)
	}
	if e = Migrate(ctx, db.SQL); e != nil {
		t.Fatal(e)
	}
}

func TestJobsCheckpointUpgradePreservesIdentityWithoutInventingPolicy(t *testing.T) {
	ctx := context.Background()
	db, e := OpenSQLite(ctx, filepath.Join(t.TempDir(), "jobs-v22.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if _, e = db.SQL.Exec("CREATE TABLE schema_migrations(version TEXT PRIMARY KEY, applied_at timestamp DEFAULT CURRENT_TIMESTAMP)"); e != nil {
		t.Fatal(e)
	}
	entries, e := migrationFiles.ReadDir("migrations")
	if e != nil {
		t.Fatal(e)
	}
	for _, entry := range entries {
		if entry.Name() > "0022_background_jobs.sql" {
			continue
		}
		raw, e := migrationFiles.ReadFile("migrations/" + entry.Name())
		if e != nil {
			t.Fatal(e)
		}
		if e = applyMigration(ctx, db.SQL, entry.Name(), string(raw)); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = db.SQL.Exec(`INSERT INTO interaction_sessions(id) VALUES('origin'),('execution'); INSERT INTO interaction_jobs(id,origin_session_id,origin_run_id,session_id,run_id,request_id,content,effects,policy,state) VALUES('job','origin','','execution','stable-run','original-request','old intent',0,'legacy-policy','registered')`); e != nil {
		t.Fatal(e)
	}
	if e = Migrate(ctx, db.SQL); e != nil {
		t.Fatal(e)
	}
	var run, request, policy, reason string
	if e = db.SQL.QueryRow("SELECT run_id,request_id,execution_policy,blocked_reason FROM interaction_jobs WHERE id='job'").Scan(&run, &request, &policy, &reason); e != nil {
		t.Fatal(e)
	}
	if run != "stable-run" || request != "original-request" || policy != "" || reason == "" {
		t.Fatal(run, request, policy, reason)
	}
	if _, e = db.SQL.Exec("UPDATE interaction_jobs SET execution_policy='replacement' WHERE id='job'"); e == nil {
		t.Fatal("upgrade allowed policy replacement")
	}
	if e = Migrate(ctx, db.SQL); e != nil {
		t.Fatal("repeat upgrade", e)
	}
}
