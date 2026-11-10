package cli

import (
	"agent/internal/interaction"
	"agent/internal/project"
	"agent/internal/storage"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestPortableProjectImportCannotProvisionPolicy(t *testing.T) {
	db, err := storage.OpenSQLite(t.Context(), filepath.Join(t.TempDir(), "portable.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = storage.Migrate(t.Context(), db.SQL); err != nil {
		t.Fatal(err)
	}
	if _, err = db.SQL.Exec(`INSERT INTO project_execution_classification VALUES('m','metadata_only');INSERT INTO projects VALUES('m','original','local_private','[]','[]','','[]','[]','[]','{}','[]')`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"copy", "m"} {
		for _, class := range []string{"", "metadata_only", "legacy_policy", "unknown"} {
			raw := `{"id":"` + id + `","name":"overwrite","execution_class":"` + class + `"}`
			if class == "" {
				raw = `{"id":"` + id + `","name":"overwrite"}`
			}
			var p project.Project
			if err = json.Unmarshal([]byte(raw), &p); err != nil {
				t.Fatal(err)
			}
			a := portableArtifact{Type: portableArtifactType, Version: portableArtifactVersion, Projects: []project.Project{p}}
			if err = importPortableArtifact(t.Context(), db.SQL, a); err == nil {
				t.Errorf("imported project policy: %s", raw)
			}
		}
	}
	var name string
	var n int
	if err = db.SQL.QueryRow(`SELECT count(*) FROM projects`).Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if err = db.SQL.QueryRow(`SELECT name FROM projects WHERE id='m'`).Scan(&name); err != nil || name != "original" {
		t.Fatal(name, err)
	}
}

func TestPortableMetadataProjectExportRefuses(t *testing.T) {
	db, err := storage.OpenSQLite(t.Context(), filepath.Join(t.TempDir(), "export.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = storage.Migrate(t.Context(), db.SQL); err != nil {
		t.Fatal(err)
	}
	if _, err = db.SQL.Exec(`INSERT INTO project_execution_classification VALUES('m','metadata_only');INSERT INTO projects VALUES('m','metadata','local_private','[]','[]','','[]','[]','[]','{}','[]')`); err != nil {
		t.Fatal(err)
	}
	if _, err = buildPortableArtifact(t.Context(), db.SQL, 1); err == nil {
		t.Fatal("unsupported metadata export succeeded")
	}
}

func TestOperatorCreatedProjectPortableRefusal(t *testing.T) {
	c := t.Context()
	db, e := storage.OpenSQLite(c, filepath.Join(t.TempDir(), "created.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if e = storage.Migrate(c, db.SQL); e != nil {
		t.Fatal(e)
	}
	s := interaction.NewStore(db.SQL)
	if e = s.CreateSession(c, "s"); e != nil {
		t.Fatal(e)
	}
	if e = s.BindChatOnly(c, "s", interaction.ChatOnlyPolicy{}); e != nil {
		t.Fatal(e)
	}
	if _, e = db.SQL.Exec(`INSERT INTO interaction_api_sessions VALUES('s','s','binding','chat_only')`); e != nil {
		t.Fatal(e)
	}
	p, e := s.PrepareOperator(c, "s", interaction.OperatorIntent{RequestID: "create", Kind: "project.create", NewValue: "owned create"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.DecideOperator(c, "s", interaction.OperatorDecision{RequestID: "confirm", OperationID: p.OperationID, Digest: p.Digest, Decision: "confirm"}); e != nil {
		t.Fatal(e)
	}
	row, e := (project.Store{DB: db.SQL}).Get(c, p.TargetID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = buildPortableArtifact(c, db.SQL, 100); e == nil {
		t.Fatal("created metadata exported")
	}
	for _, id := range []string{p.TargetID, "copy"} {
		for _, class := range []string{"", "metadata_only", "legacy_policy"} {
			row.ID = id
			row.ExecutionClass = class
			if e = importPortableArtifact(c, db.SQL, portableArtifact{Type: portableArtifactType, Version: portableArtifactVersion, Projects: []project.Project{row}}); e == nil {
				t.Fatal("created project imported", class)
			}
		}
	}
	var n int
	db.SQL.QueryRow(`SELECT count(*) FROM projects`).Scan(&n)
	if n != 1 {
		t.Fatal(n)
	}
}
