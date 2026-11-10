package storage

import (
	"path/filepath"
	"testing"
)

func TestSkillExecutionMigrationPreservesModelRunsAndGuards(t *testing.T) {
	ctx := t.Context()
	db, e := OpenSQLite(ctx, filepath.Join(t.TempDir(), "execution.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if _, e = db.SQL.Exec(migrationLedgerDDL); e != nil {
		t.Fatal(e)
	}
	names, _ := migrationNames()
	for _, name := range names {
		if name >= "0030" {
			break
		}
		raw, _ := migrationFiles.ReadFile("migrations/" + name)
		if e = applyMigration(ctx, db.SQL, name, string(raw)); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = db.SQL.Exec(`INSERT INTO interaction_sessions VALUES('s',0); INSERT INTO interaction_runs VALUES('r','s','req','{}','completed',1)`); e != nil {
		t.Fatal(e)
	}
	if e = Migrate(ctx, db.SQL); e != nil {
		t.Fatal(e)
	}
	if e = Migrate(ctx, db.SQL); e != nil {
		t.Fatal(e)
	}
	var kind string
	if e = db.SQL.QueryRow(`SELECT execution_kind FROM interaction_runs WHERE id='r'`).Scan(&kind); e != nil || kind != "model" {
		t.Fatal(kind, e)
	}
	if _, e = db.SQL.Exec(`UPDATE interaction_runs SET execution_kind='skill_readonly_v1' WHERE id='r'`); e == nil {
		t.Fatal("mutable routing")
	}
	if _, e = db.SQL.Exec(`DROP TRIGGER interaction_skill_runs_immutable`); e != nil {
		t.Fatal(e)
	}
	if e = ValidateSchema(ctx, db.SQL); e == nil {
		t.Fatal("missing guard accepted")
	}
}
