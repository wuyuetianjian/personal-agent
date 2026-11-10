package storage

import (
	"path/filepath"
	"testing"
)

func TestSessionProjectLabelMigrationPreservesLegacyAdmissionAndNoAssociation(t *testing.T) {
	c := t.Context()
	db, e := OpenSQLite(c, filepath.Join(t.TempDir(), "owned.db"))
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
		if name >= "0028" {
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
	if _, e = db.SQL.Exec(`INSERT INTO interaction_sessions(id) VALUES('legacy'); INSERT INTO interaction_chat_policies VALUES('legacy','exact-policy'); INSERT INTO interaction_api_sessions VALUES('legacy','request','exact-binding','chat_only'); INSERT INTO interaction_mcp_admissions VALUES('request','exact-fingerprint','legacy','starting')`); e != nil {
		t.Fatal(e)
	}
	if e = Migrate(c, db.SQL); e != nil {
		t.Fatal(e)
	}
	var kind, state, fp, policy, binding string
	var n int
	if e = db.SQL.QueryRow(`SELECT kind,state,fingerprint FROM interaction_mcp_admissions WHERE request_id='request'`).Scan(&kind, &state, &fp); e != nil || kind != "mcp" || state != "starting" || fp != "exact-fingerprint" {
		t.Fatal(kind, state, fp, e)
	}
	if e = db.SQL.QueryRow(`SELECT p.policy,s.binding FROM interaction_chat_policies p JOIN interaction_api_sessions s ON s.session_id=p.session_id WHERE p.session_id='legacy'`).Scan(&policy, &binding); e != nil || policy != "exact-policy" || binding != "exact-binding" {
		t.Fatal(policy, binding, e)
	}
	if e = db.SQL.QueryRow(`SELECT count(*) FROM interaction_session_project_labels`).Scan(&n); e != nil || n != 0 {
		t.Fatal("invented legacy label", n, e)
	}
	if e = ValidateSchema(c, db.SQL); e != nil {
		t.Fatal(e)
	}
	if _, e = db.SQL.Exec(`DROP TRIGGER interaction_project_label_no_update`); e != nil {
		t.Fatal(e)
	}
	if e = ValidateSchema(c, db.SQL); e == nil {
		t.Fatal("recognized missing immutability guard")
	}
}
