package storage

import (
	"path/filepath"
	"testing"
)

func TestSkillCatalogMigrationExactPrefixAndLegacyIsolation(t *testing.T) {
	c := t.Context()
	db, e := OpenSQLite(c, filepath.Join(t.TempDir(), "catalog.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if _, e = db.SQL.Exec(migrationLedgerDDL); e != nil {
		t.Fatal(e)
	}
	names, e := migrationNames()
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range names {
		if name >= "0029" {
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
	if _, e = db.SQL.Exec(`INSERT INTO skills VALUES('legacy','name','description','v1','active','legacy-source',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP); INSERT INTO skill_versions VALUES('legacy','v1','exact-legacy-manifest','exact-legacy-hash',CURRENT_TIMESTAMP)`); e != nil {
		t.Fatal(e)
	}
	if e = ValidateSchema(c, db.SQL); e != nil {
		t.Fatal(e)
	}
	if e = Migrate(c, db.SQL); e != nil {
		t.Fatal(e)
	}
	if e = Migrate(c, db.SQL); e != nil {
		t.Fatal(e)
	}
	var status, raw, hash string
	var n int
	if e = db.SQL.QueryRow(`SELECT s.status,v.manifest_json,v.checksum FROM skills s JOIN skill_versions v ON v.skill_id=s.id WHERE s.id='legacy'`).Scan(&status, &raw, &hash); e != nil || status != "active" || raw != "exact-legacy-manifest" || hash != "exact-legacy-hash" {
		t.Fatal(status, raw, hash, e)
	}
	for _, table := range []string{"interaction_skill_catalog", "interaction_skill_revisions", "interaction_skill_operations", "interaction_skill_requests", "interaction_skill_history"} {
		if e = db.SQL.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); e != nil || n != 0 {
			t.Fatal(table, n, e)
		}
	}
	if e = ValidateSchema(c, db.SQL); e != nil {
		t.Fatal(e)
	}
	if _, e = db.SQL.Exec(`DROP TRIGGER interaction_skill_revision_immutable`); e != nil {
		t.Fatal(e)
	}
	if e = ValidateSchema(c, db.SQL); e == nil {
		t.Fatal("missing immutability guard recognized")
	}
	if e = Migrate(c, db.SQL); e == nil {
		t.Fatal("altered schema repaired")
	}
}
