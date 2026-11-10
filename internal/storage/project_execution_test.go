package storage

import (
	"path/filepath"
	"testing"
)

func TestProjectExecutionMigrationAndImmutableTombstones(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "class.db")
	db, err := OpenSQLite(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	if _, err = db.SQL.Exec(migrationLedgerDDL); err != nil {
		t.Fatal(err)
	}
	names, err := migrationNames()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if name >= "0027" {
			break
		}
		raw, err := migrationFiles.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if err = applyMigration(ctx, db.SQL, name, string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	const insert = `INSERT INTO projects VALUES('p','untouched','private','["repo"]','["scope"]','memory','["skill"]','["cap"]','["agent"]','budget','["model"]')`
	if _, err = db.SQL.Exec(insert); err != nil {
		t.Fatal(err)
	}
	var generation string
	var rev int
	if err = db.SQL.QueryRow(`SELECT generation,revision FROM project_catalog_lifecycle WHERE project_id='p'`).Scan(&generation, &rev); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, db.SQL); err != nil {
		t.Fatal(err)
	}
	var class string
	if err = db.SQL.QueryRow(`SELECT execution_class FROM project_execution_classification WHERE project_id='p'`).Scan(&class); err != nil || class != "legacy_policy" {
		t.Fatalf("legacy classification = %q: %v", class, err)
	}
	var gotGeneration, policy string
	var gotRev int
	if err = db.SQL.QueryRow(`SELECT generation,revision FROM project_catalog_lifecycle WHERE project_id='p'`).Scan(&gotGeneration, &gotRev); err != nil || gotGeneration != generation || gotRev != rev {
		t.Fatal("lifecycle changed", err)
	}
	if err = db.SQL.QueryRow(`SELECT json_array(name,privacy_class,repository_refs_json,knowledge_scopes_json,memory_scope,allowed_skills_json,allowed_capabilities_json,allowed_coding_agents_json,budget_policy,allowed_models_json) FROM projects WHERE id='p'`).Scan(&policy); err != nil {
		t.Fatal(err)
	}
	if policy != `["untouched","private","[\"repo\"]","[\"scope\"]","memory","[\"skill\"]","[\"cap\"]","[\"agent\"]","budget","[\"model\"]"]` {
		t.Fatal(policy)
	}
	// The future create transaction pre-seeds immutable classification, then INSERTs.
	if _, err = db.SQL.Exec(`INSERT INTO project_execution_classification VALUES('m','metadata_only'); INSERT INTO projects VALUES('m','metadata','local_private','[]','[]','','[]','[]','[]','{}','[]')`); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`INSERT OR REPLACE INTO project_execution_classification VALUES('m','legacy_policy')`, `UPDATE project_execution_classification SET execution_class='legacy_policy' WHERE project_id='m'`, `DELETE FROM project_execution_classification WHERE project_id='m'`, `UPDATE projects SET allowed_models_json='[]' WHERE id='m'`, `UPDATE projects SET id='copy' WHERE id='m'`} {
		if _, err = db.SQL.Exec(q); err == nil {
			t.Fatalf("unsafe mutation allowed: %s", q)
		}
	}
	if _, err = db.SQL.Exec(`UPDATE projects SET name='renamed' WHERE id='m'; DELETE FROM projects WHERE id='m'; INSERT INTO projects VALUES('m','recreated','local_private','[]','[]','','[]','[]','[]','{}','[]')`); err != nil {
		t.Fatal(err)
	}
	if err = db.SQL.QueryRow(`SELECT execution_class FROM project_execution_classification WHERE project_id='m'`).Scan(&class); err != nil || class != "metadata_only" {
		t.Fatal(class, err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	if err = ValidateSQLiteFile(ctx, path); err != nil {
		t.Fatal(err)
	}
	db, err = OpenSQLite(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, db.SQL); err != nil {
		t.Fatal(err)
	}
	if err = db.SQL.QueryRow(`SELECT execution_class FROM project_execution_classification WHERE project_id='m'`).Scan(&class); err != nil || class != "metadata_only" {
		t.Fatal(class, err)
	}
}
