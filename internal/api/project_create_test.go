package api

import (
	"agent/internal/interaction"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProjectCreateAPI(t *testing.T) {
	for _, mode := range []string{"chat_only", "default", "plan", "acceptEdits"} {
		t.Run(mode, func(t *testing.T) {
			s, rt := serviceFixture(t, testSelection())
			var session SessionView
			created := apiRequest(s, "POST", "/api/v1/sessions", fmt.Sprintf(`{"request_id":"session","mode":%q}`, mode))
			if created.Code != 201 {
				t.Fatal(created.Body.String())
			}
			json.Unmarshal(created.Body.Bytes(), &session)
			s.options.Resolve = func(context.Context, string) (ProviderSelection, error) {
				t.Fatal("create resolved provider")
				return ProviderSelection{}, nil
			}

			authority := func() string {
				t.Helper()
				var raw string
				e := rt.DB.SQL.QueryRow(`SELECT json_array((SELECT json_group_array(json_array(session_id,request_id,binding,mode)) FROM interaction_api_sessions),(SELECT json_group_array(json_array(session_id,scope_id,policy,root_identity)) FROM interaction_workspaces),(SELECT json_group_array(json_array(session_id,policy)) FROM interaction_chat_policies))`).Scan(&raw)
				if e != nil {
					t.Fatal(e)
				}
				return raw
			}
			originalAuthority := authority()
			path := "/api/v1/sessions/" + session.ID
			body := `{"request_id":"create","kind":"project.create","target_id":"","new_value":"新建 <script>"}`
			for _, bad := range []string{strings.Replace(body, `"target_id":""`, `"target_id":"caller"`, 1), strings.Replace(body, `"新建 <script>"`, `"\ud800"`, 1), strings.Replace(body, `"新建 <script>"`, `"\udfff"`, 1), strings.Replace(body, `"新建 <script>"`, `"   "`, 1), strings.TrimSuffix(body, "}") + `,"execution_class":"legacy_policy"}`, strings.TrimSuffix(body, "}") + `,"memory_scope":"grant"}`, strings.TrimSuffix(body, "}") + `,"root":"/"}`} {
				w := apiRequest(s, "POST", path+"/operator-actions/preview", bad)
				if w.Code != 400 {
					t.Fatal(w.Code, w.Body.String())
				}
			}
			w := apiRequest(s, "POST", path+"/operator-actions/preview", body)
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			var p interaction.OperatorOperation
			json.Unmarshal(w.Body.Bytes(), &p)
			if p.ExecutionClass != "metadata_only" {
				t.Fatal(p)
			}
			var n int
			rt.DB.SQL.QueryRow(`SELECT count(*) FROM projects`).Scan(&n)
			if n != 0 {
				t.Fatal("preview created project")
			}
			w = apiRequest(s, "POST", path+"/operator-actions/decision", fmt.Sprintf(`{"request_id":"decision","operation_id":"create","digest":%q,"decision":"confirm"}`, p.Digest))
			if w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"committed"`) {
				t.Fatal(w.Code, w.Body.String())
			}
			for _, id := range []string{"create", "decision"} {
				w = apiRequest(s, "GET", path+"/operator-actions/"+id, "")
				if w.Code != 200 {
					t.Fatal(w.Code)
				}
			}
			w = apiRequest(s, "GET", path+"/operator/projects", "")
			var page OperatorPage
			if e := json.Unmarshal(w.Body.Bytes(), &page); e != nil {
				t.Fatal(e)
			}
			available := false
			for _, a := range page.Actions {
				if a.Action == "create" && a.Availability == "available" {
					available = true
				}
			}
			if !available {
				t.Fatal("create unavailable", w.Body.String())
			}
			if authority() != originalAuthority {
				t.Fatal("create altered scope/root/policy/binding")
			}
			for _, table := range []string{"interaction_runs", "interaction_queue", "interaction_jobs", "interaction_approvals", "workflow_runs", "project_document_scopes", "documents", "document_chunks"} {
				if e := rt.DB.SQL.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); e != nil {
					t.Fatal(e)
				}
				if n != 0 {
					t.Fatal("create wrote", table)
				}
			}
		})
	}
}

func TestProjectCreateSecurityEnvelope(t *testing.T) {
	s, rt := serviceFixture(t, testSelection())
	id := createChat(t, s)
	for _, change := range []func(*http.Request){func(r *http.Request) { r.Header.Del("Authorization") }, func(r *http.Request) { r.Host = "evil.test" }, func(r *http.Request) { r.Header.Set("Origin", "https://evil.test") }, func(r *http.Request) { r.Header.Del("X-Agent-Protocol") }, func(r *http.Request) { r.RemoteAddr = "10.0.0.1:5" }, func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }} {
		r := httptest.NewRequest("POST", "http://127.0.0.1:7319/api/v1/sessions/"+id+"/operator-actions/preview", strings.NewReader(`{"request_id":"create","kind":"project.create","target_id":"","new_value":"new"}`))
		r.RemoteAddr = "127.0.0.1:1"
		r.Header.Set("Authorization", "Bearer secret-token")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Agent-Protocol", InteractionProtocolFlavor)
		change(r)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code < 400 {
			t.Fatal("create bypass", w.Code)
		}
	}
	for _, table := range []string{"projects", "project_execution_classification", "project_catalog_lifecycle", "interaction_operator_operations", "interaction_operator_requests", "security_audit_log"} {
		var n int
		if e := rt.DB.SQL.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); e != nil || n != 0 {
			t.Fatal(table, n, e)
		}
	}
}
