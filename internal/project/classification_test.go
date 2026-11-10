package project

import (
	"agent/internal/storage"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestClassifiedProjectDomainAdmission(t *testing.T) {
	// Removing the execution-class gate would allow each named invalid project.
	for _, class := range []string{"metadata_only", "unknown", "", "legacy_policy"} {
		var p Project
		if err := json.Unmarshal([]byte(`{"id":"p","execution_class":"`+class+`"}`), &p); err != nil {
			t.Fatal(err)
		}
		want := false // Even spoofed legacy_policy JSON is not trusted execution authority.
		if p.AllowsSkill("s") != want || p.AllowsModel("m") != want || p.AllowsCapability("c") != want || p.AllowsCodingAgent("a") != want {
			t.Errorf("class %q admitted incorrectly: %+v", class, p)
		}
	}
	if !(Project{}).AllowsSkill("s") {
		t.Fatal("no-project legacy behavior changed")
	}
}

func TestSaveRefusesImportedProjectAuthority(t *testing.T) {
	db, err := storage.OpenSQLite(context.Background(), filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = storage.Migrate(t.Context(), db.SQL); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{"id":"copy"}`, `{"id":"copy","execution_class":"legacy_policy"}`, `{"id":"copy","execution_class":"metadata_only"}`, `{"id":"copy","execution_class":"future"}`} {
		var p Project
		if err = json.Unmarshal([]byte(raw), &p); err != nil {
			t.Fatal(err)
		}
		if err = (Store{DB: db.SQL}).Save(t.Context(), p); err == nil {
			t.Errorf("imported policy accepted: %s", raw)
		}
	}
	var count int
	if err = db.SQL.QueryRow(`SELECT count(*) FROM projects`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("unexpected rows %d: %v", count, err)
	}
}

func TestProjectClassificationDirectDomainAndStoreRoundtrip(t *testing.T) {
	for _, class := range []string{"", "metadata_only", "unknown", "legacy_policy"} {
		p := Project{ID: "named", ExecutionClass: class}
		want := class == ExecutionLegacyPolicy
		if p.ExecutionAllowed() != want || p.AllowsCapability("c") != want || p.AllowsModel("m") != want || p.AllowsSkill("s") != want || p.AllowsCodingAgent("a") != want {
			t.Fatalf("class %q admitted incorrectly", class)
		}
	}
	db, err := storage.OpenSQLite(t.Context(), filepath.Join(t.TempDir(), "round.db"))
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
	store := Store{DB: db.SQL}
	p, err := store.Get(t.Context(), "m")
	if err != nil {
		t.Fatal(err)
	}
	if p.ExecutionClass != ExecutionMetadataOnly || p.ExecutionAllowed() {
		t.Fatal(p)
	}
	list, err := store.List(t.Context(), 20)
	if err != nil || len(list) != 1 || list[0].ExecutionClass != ExecutionMetadataOnly {
		t.Fatal(list, err)
	}
	for _, id := range []string{"m", "copy"} {
		p.ID = id
		if err = store.Save(t.Context(), p); err == nil {
			t.Fatal("metadata Save accepted", id)
		}
		raw, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		var decoded Project
		if err = json.Unmarshal(raw, &decoded); err != nil {
			t.Fatal(err)
		}
		// Spoofing either recognized or absent class after decoding retains taint.
		for _, class := range []string{"", ExecutionLegacyPolicy, ExecutionMetadataOnly} {
			decoded.ExecutionClass = class
			if err = store.Save(t.Context(), decoded); err == nil {
				t.Fatal("serialized copy promoted", id, class)
			}
		}
	}
	for _, class := range []string{"", ExecutionLegacyPolicy} {
		if err = store.Save(t.Context(), Project{ID: "m", ExecutionClass: class, Name: "overwrite"}); err == nil {
			t.Fatal("metadata overwritten")
		}
	}
	if _, err = db.SQL.Exec(`DELETE FROM projects WHERE id='m'`); err != nil {
		t.Fatal(err)
	}
	if err = store.Save(t.Context(), Project{ID: "m", Name: "recreated"}); err == nil {
		t.Fatal("metadata tombstone promoted")
	}
}

func TestClassificationCorruptionFailsClosedWithoutRepair(t *testing.T) {
	for _, kind := range []string{"missing", "unknown"} {
		t.Run(kind, func(t *testing.T) {
			db, err := storage.OpenSQLite(t.Context(), filepath.Join(t.TempDir(), "corrupt.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if err = storage.Migrate(t.Context(), db.SQL); err != nil {
				t.Fatal(err)
			}
			s := Store{DB: db.SQL}
			if kind == "unknown" {
				if _, err = db.SQL.Exec(`PRAGMA ignore_check_constraints=ON;INSERT INTO project_execution_classification VALUES('p','future');PRAGMA ignore_check_constraints=OFF;INSERT INTO projects VALUES('p','original','private','[]','[]','','[]','[]','[]','{}','[]')`); err != nil {
					t.Fatal(err)
				}
			} else {
				if err = s.Save(t.Context(), Project{ID: "p", Name: "original"}); err != nil {
					t.Fatal(err)
				}
				// Deliberately damage only this owned fixture's data, then restore exact
				// trigger DDL. Schema recognition is not a data-integrity attestation.
				var ddl string
				if err = db.SQL.QueryRow(`SELECT sql FROM sqlite_schema WHERE name='project_execution_immutable_delete'`).Scan(&ddl); err != nil {
					t.Fatal(err)
				}
				if _, err = db.SQL.Exec(`DROP TRIGGER project_execution_immutable_delete;DELETE FROM project_execution_classification WHERE project_id='p';` + ddl); err != nil {
					t.Fatal(err)
				}
			}
			if err = storage.ValidateSchema(t.Context(), db.SQL); err != nil {
				t.Fatal(err)
			}
			var before, after int
			if err = db.SQL.QueryRow(`SELECT total_changes()`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			if _, err = s.Get(t.Context(), "p"); err == nil {
				t.Fatal("Get repaired or accepted corrupt classification")
			}
			if _, err = s.List(t.Context(), 20); err == nil {
				t.Fatal("List hid or accepted corrupt classification")
			}
			if err = s.Save(t.Context(), Project{ID: "p", Name: "overwrite"}); err == nil {
				t.Fatal("Save repaired corrupt classification")
			}
			if err = db.SQL.QueryRow(`SELECT total_changes()`).Scan(&after); err != nil || before != after {
				t.Fatal(before, after, err)
			}
		})
	}
}
