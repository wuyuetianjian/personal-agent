package storage

import (
	"path/filepath"
	"testing"
)

func TestProjectCatalogMigrationBackfillLegacyInsertAndReopen(t *testing.T) {
	c := t.Context()
	path := filepath.Join(t.TempDir(), "catalog.db")
	db, e := OpenSQLite(c, path)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { db.Close() }()
	if _, e = db.SQL.Exec(migrationLedgerDDL); e != nil {
		t.Fatal(e)
	}
	names, e := migrationNames()
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range names {
		if name >= "0026" {
			break
		}
		raw, e := migrationFiles.ReadFile("migrations/" + name)
		if e != nil {
			t.Fatal(e)
		}
		if e = applyMigration(c, db.SQL, name, string(raw)); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = db.SQL.Exec(`INSERT INTO projects VALUES('p','literal [archived]','private','["tool"]','["skill"]','business-not-scope','["mcp"]','["cap"]','["path"]','budget','["model"]'); INSERT INTO interaction_sessions(id) VALUES('s'); INSERT INTO interaction_operator_operations VALUES('old-p','s','{"historic":"pending"}','pending','{"historic":"pending"}'),('old-d','s','{"historic":"preview"}','committed','{"historic":"terminal"}')`); e != nil {
		t.Fatal(e)
	}
	var before string
	var snapshot string
	// Column names are taken from the preserved legacy schema, independent of ordering.
	rows, e := db.SQL.Query(`SELECT * FROM projects WHERE id='p'`)
	if e != nil {
		t.Fatal(e)
	}
	cols, _ := rows.Columns()
	rows.Close()
	snapshot = "SELECT json_array("
	for i, col := range cols {
		if i > 0 {
			snapshot += ","
		}
		snapshot += col
	}
	snapshot += ") FROM projects WHERE id='p'"
	if e = db.SQL.QueryRow(snapshot).Scan(&before); e != nil {
		t.Fatal(e)
	}
	if e = Migrate(c, db.SQL); e != nil {
		t.Fatal(e)
	}
	var generation, state, after string
	var rev int
	if e = db.SQL.QueryRow(`SELECT generation,revision,state FROM project_catalog_lifecycle WHERE project_id='p'`).Scan(&generation, &rev, &state); e != nil || len(generation) != 32 || rev != 1 || state != "active" {
		t.Fatal(generation, rev, state, e)
	}
	if e = db.SQL.QueryRow(snapshot).Scan(&after); e != nil || before != after {
		t.Fatal(before, after, e)
	}
	for id, want := range map[string]string{"old-p": `{"historic":"pending"}`, "old-d": `{"historic":"terminal"}`} {
		var raw string
		if e = db.SQL.QueryRow(`SELECT result FROM interaction_operator_operations WHERE id=?`, id).Scan(&raw); e != nil || raw != want {
			t.Fatal(raw, e)
		}
	}
	if _, e = db.SQL.Exec(`INSERT INTO projects VALUES('legacy','legacy','private','[]','[]','','[]','[]','[]','','[]')`); e != nil {
		t.Fatal(e)
	}
	if _, e = db.SQL.Exec(`UPDATE project_catalog_lifecycle SET state='archived',revision=revision+1 WHERE project_id='p'`); e != nil {
		t.Fatal(e)
	}
	if e = db.Close(); e != nil {
		t.Fatal(e)
	}
	if e = ValidateSQLiteFile(c, path); e != nil {
		t.Fatal(e)
	}
	db, e = OpenSQLite(c, path)
	if e != nil {
		t.Fatal(e)
	}
	if e = Migrate(c, db.SQL); e != nil {
		t.Fatal(e)
	}
	var gen2 string
	if e = db.SQL.QueryRow(`SELECT generation,revision,state FROM project_catalog_lifecycle WHERE project_id='p'`).Scan(&gen2, &rev, &state); e != nil || gen2 != generation || rev != 2 || state != "archived" {
		t.Fatal(gen2, rev, state, e)
	}
	if e = db.SQL.QueryRow(`SELECT state FROM project_catalog_lifecycle WHERE project_id='legacy'`).Scan(&state); e != nil || state != "active" {
		t.Fatal(state, e)
	}
}
