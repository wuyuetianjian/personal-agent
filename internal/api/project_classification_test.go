package api

import (
	"agent/internal/project"
	"encoding/json"
	"strings"
	"testing"
)

func TestProjectReadersCarryExecutionClassification(t *testing.T) {
	s, rt := serviceFixture(t, testSelection())
	id := createChat(t, s)
	if _, err := rt.DB.SQL.Exec(`INSERT INTO project_execution_classification VALUES('m','metadata_only');INSERT INTO projects VALUES('m','metadata','local_private','[]','[]','','[]','[]','[]','{}','[]')`); err != nil {
		t.Fatal(err)
	}
	rows, err := listProjectRows(t.Context(), rt.DB.SQL, listQuery{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ExecutionClass != project.ExecutionMetadataOnly || rows[0].AllowsModel("anything") {
		t.Fatalf("legacy API lost classification: %+v", rows)
	}
	raw, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"execution_class":"metadata_only"`) {
		t.Fatal(string(raw))
	}
	w := apiRequest(s, "GET", "/api/v1/sessions/"+id+"/operator/projects", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"value":"metadata_only"`) || !strings.Contains(w.Body.String(), "no tools/models/workflows/knowledge isolation enabled") {
		t.Fatal(w.Code, w.Body.String())
	}
	// Creation now exposes only metadata-only preview, never a policy input.
	w = apiRequest(s, "POST", "/api/v1/sessions/"+id+"/operator-actions/preview", `{"request_id":"create","kind":"project.create","target_id":"","new_value":"new"}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"execution_class":"metadata_only"`) {
		t.Fatal("create lost classification", w.Code, w.Body.String())
	}
}

func TestProjectReadersRefuseCorruptClassification(t *testing.T) {
	for _, kind := range []string{"missing", "unknown"} {
		t.Run(kind, func(t *testing.T) {
			s, rt := serviceFixture(t, testSelection())
			id := createChat(t, s)
			if kind == "unknown" {
				if _, err := rt.DB.SQL.Exec(`PRAGMA ignore_check_constraints=ON;INSERT INTO project_execution_classification VALUES('p','future');PRAGMA ignore_check_constraints=OFF;INSERT INTO projects VALUES('p','invalid','private','[]','[]','','[]','[]','[]','{}','[]')`); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := rt.DB.SQL.Exec(`INSERT INTO projects VALUES('p','invalid','private','[]','[]','','[]','[]','[]','{}','[]')`); err != nil {
					t.Fatal(err)
				}
				var ddl string
				if err := rt.DB.SQL.QueryRow(`SELECT sql FROM sqlite_schema WHERE name='project_execution_immutable_delete'`).Scan(&ddl); err != nil {
					t.Fatal(err)
				}
				if _, err := rt.DB.SQL.Exec(`DROP TRIGGER project_execution_immutable_delete;DELETE FROM project_execution_classification WHERE project_id='p';` + ddl); err != nil {
					t.Fatal(err)
				}
			}
			var before, after int
			if err := rt.DB.SQL.QueryRow(`SELECT total_changes()`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			if _, err := listProjectRows(t.Context(), rt.DB.SQL, listQuery{Limit: 20}); err == nil {
				t.Fatal("legacy reader accepted corrupt classification")
			}
			w := apiRequest(s, "GET", "/api/v1/sessions/"+id+"/operator/projects", "")
			if w.Code != 500 {
				t.Fatal(w.Code, w.Body.String())
			}
			if err := rt.DB.SQL.QueryRow(`SELECT total_changes()`).Scan(&after); err != nil || before != after {
				t.Fatal(before, after, err)
			}
		})
	}
}
