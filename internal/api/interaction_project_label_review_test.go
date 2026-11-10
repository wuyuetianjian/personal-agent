package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestStoredProjectLabelExactWireReplay(t *testing.T) {
	variants := []string{"empty-name", "valid-surrogate-pair", "high-surrogate", "low-surrogate", "invalid-utf8", "unknown-field", "revision-string", "revision-fraction"}
	for _, key := range []string{"project_id", "generation", "revision", "name"} {
		for _, kind := range []string{"missing", "null", "duplicate"} {
			variants = append(variants, kind+"-"+key)
		}
	}
	for _, variant := range variants {
		t.Run(variant, func(t *testing.T) {
			s, rt := serviceFixture(t, testSelection())
			name := ""
			switch variant {
			case "valid-surrogate-pair":
				name = "😀"
			case "high-surrogate", "low-surrogate", "invalid-utf8":
				name = "\ufffd"
			}
			labelProject(t, rt, "p", name)
			input := `{"request_id":"strict-replay","mode":"chat_only","catalog_project_id":"p"}`
			w := apiRequest(s, "POST", "/api/v1/sessions", input)
			if w.Code != 201 {
				t.Fatal(w.Code, w.Body.String())
			}
			var record string
			if e := rt.DB.SQL.QueryRow(`SELECT result FROM interaction_api_actions WHERE request_id='strict-replay'`).Scan(&record); e != nil {
				t.Fatal(e)
			}
			var out struct {
				Body struct {
					Label json.RawMessage `json:"project_label"`
				} `json:"body"`
			}
			if e := json.Unmarshal([]byte(record), &out); e != nil {
				t.Fatal(e)
			}
			original := string(out.Body.Label)
			modified := original
			want := 500
			if variant == "empty-name" {
				want = 201
			} else if variant == "valid-surrogate-pair" {
				want = 201
				modified = strings.Replace(original, `"name":"😀"`, `"name":"\ud83d\ude00"`, 1)
			} else if variant == "high-surrogate" {
				modified = strings.Replace(original, `"name":"�"`, `"name":"\ud800"`, 1)
			} else if variant == "low-surrogate" {
				modified = strings.Replace(original, `"name":"�"`, `"name":"\udc00"`, 1)
			} else if variant == "invalid-utf8" {
				modified = strings.Replace(original, `"name":"�"`, "\"name\":\"\xff\"", 1)
			} else if strings.HasPrefix(variant, "duplicate-") {
				key := strings.TrimPrefix(variant, "duplicate-")
				modified = fmt.Sprintf(`{%q:null,`, key) + original[1:]
			} else {
				var fields map[string]any
				if e := json.Unmarshal(out.Body.Label, &fields); e != nil {
					t.Fatal(e)
				}
				switch {
				case strings.HasPrefix(variant, "missing-"):
					delete(fields, strings.TrimPrefix(variant, "missing-"))
				case strings.HasPrefix(variant, "null-"):
					fields[strings.TrimPrefix(variant, "null-")] = nil
				case variant == "unknown-field":
					fields["scope_id"] = "forged"
				case variant == "revision-string":
					fields["revision"] = "1"
				case variant == "revision-fraction":
					fields["revision"] = 1.5
				}
				raw, e := json.Marshal(fields)
				if e != nil {
					t.Fatal(e)
				}
				modified = string(raw)
			}
			if variant != "empty-name" && modified == original {
				t.Fatal("fixture did not change raw label")
			}
			if _, e := rt.DB.SQL.Exec(`UPDATE interaction_api_actions SET result=? WHERE request_id='strict-replay'`, strings.Replace(record, original, modified, 1)); e != nil {
				t.Fatal(e)
			}
			s.options.Resolve = func(context.Context, string) (ProviderSelection, error) {
				t.Error("stored replay resolved provider")
				return ProviderSelection{}, fmt.Errorf("offline")
			}
			var before, after int
			if e := rt.DB.SQL.QueryRow(`SELECT total_changes()`).Scan(&before); e != nil {
				t.Fatal(e)
			}
			w = apiRequest(s, "POST", "/api/v1/sessions", input)
			if w.Code != want {
				t.Errorf("%s replay: got %d want %d: %s", variant, w.Code, want, w.Body.String())
			}
			if want == 201 {
				label := labelResponse(t, w.Body.Bytes())["project_label"].(map[string]any)
				if label["name"] != name {
					t.Fatal("changed exact valid name", label)
				}
			}
			if e := rt.DB.SQL.QueryRow(`SELECT total_changes()`).Scan(&after); e != nil || after != before {
				t.Fatal("replay wrote state", before, after, e)
			}
		})
	}
}

func TestSessionCreateQueryGuardUsesDispatchCanonicalPath(t *testing.T) {
	for _, path := range []string{"/api/v1/sessions", "/api/v1/sessions/", "/api/v1/sessions///", "//api/v1/sessions", "///api/v1/sessions///"} {
		for _, query := range []string{"?before=x", "?after=0", "?before=x&after=0"} {
			t.Run(path+query, func(t *testing.T) {
				s, rt := serviceFixture(t, testSelection())
				resolves := 0
				s.options.Resolve = func(context.Context, string) (ProviderSelection, error) { resolves++; return testSelection(), nil }
				input := `{"request_id":"query-route","mode":"chat_only"}`
				w := apiRequest(s, "POST", path+query, input)
				if w.Code != 400 {
					t.Errorf("query route admitted: %d %s", w.Code, w.Body.String())
				}
				if resolves != 0 {
					t.Error("refused query reached resolver", resolves)
				}
				for _, table := range []string{"interaction_sessions", "interaction_mcp_admissions", "interaction_api_actions"} {
					var n int
					if e := rt.DB.SQL.QueryRow("SELECT count(*) FROM " + table).Scan(&n); e != nil || n != 0 {
						t.Error("refused query published", table, n, e)
					}
				}
				// Prove the spelling is an admitted route without a query, not merely a 404.
				w = apiRequest(s, "POST", path, input)
				if w.Code != 201 {
					t.Fatal("canonical dispatch changed", w.Code, w.Body.String())
				}
			})
		}
	}
}
