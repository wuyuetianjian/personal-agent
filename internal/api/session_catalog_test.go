package api

import (
	"context"
	"fmt"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestPreSessionCatalogBoundedNoResolver(t *testing.T) {
	s, rt := serviceFixture(t, testSelection())
	calls := 0
	s.options.Resolve = func(context.Context, string) (ProviderSelection, error) { calls++; return testSelection(), nil }
	if w := apiRequest(s, "GET", "/api/v1/protocol", ""); w.Code != 200 || labelResponse(t, w.Body.Bytes())["protocol"] != InteractionProtocolFlavor {
		t.Fatal("read-only handshake", w.Code, w.Body.String())
	}
	for i := 0; i < 4; i++ {
		if i == 2 {
			if _, e := rt.DB.SQL.Exec(`INSERT INTO project_execution_classification VALUES('p2','metadata_only'); INSERT INTO projects SELECT 'p2','exact'||char(10)||'2',privacy_class,repository_refs_json,knowledge_scopes_json,memory_scope,allowed_skills_json,allowed_capabilities_json,allowed_coding_agents_json,budget_policy,allowed_models_json FROM projects WHERE id='p0'`); e != nil {
				t.Fatal(e)
			}
		} else {
			labelProject(t, rt, fmt.Sprintf("p%d", i), fmt.Sprintf("exact\n%d", i))
		}
	}
	if _, e := rt.DB.SQL.Exec(`UPDATE project_catalog_lifecycle SET state='archived' WHERE project_id='p1'`); e != nil {
		t.Fatal(e)
	}
	w := apiRequest(s, "GET", "/api/v1/catalog/projects?limit=2", "")
	if w.Code != 200 {
		t.Fatalf("catalog %d %s", w.Code, w.Body.String())
	}
	v := labelResponse(t, w.Body.Bytes())
	items := v["items"].([]any)
	if len(items) != 2 || items[0].(map[string]any)["name"] != "exact\n0" || items[1].(map[string]any)["execution_class"] != "metadata_only" {
		t.Fatal(v)
	}
	cursor := v["next_cursor"].(string)
	w = apiRequest(s, "GET", "/api/v1/catalog/projects?limit=2&cursor="+url.QueryEscape(cursor), "")
	if w.Code != 200 || len(labelResponse(t, w.Body.Bytes())["items"].([]any)) != 1 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, q := range []string{"limit=51", "limit=0", "limit=1&limit=2", "root=/tmp", "before=x", "cursor=bad", "filter=active", "owner=x"} {
		if w := apiRequest(s, "GET", "/api/v1/catalog/projects?"+q, ""); w.Code != 400 {
			t.Fatal(q, w.Code)
		}
	}
	if calls != 0 {
		t.Fatal("selection resolved provider", calls)
	}
	for _, table := range []string{"interaction_sessions", "interaction_mcp_admissions"} {
		var n int
		if e := rt.DB.SQL.QueryRow("SELECT count(*) FROM " + table).Scan(&n); e != nil || n != 0 {
			t.Fatal(table, n, e)
		}
	}
}
func TestPreSessionCatalogSecurity(t *testing.T) {
	s, _ := serviceFixture(t, testSelection())
	for _, kind := range []string{"auth", "host", "origin"} {
		r := httptest.NewRequest("GET", "http://"+s.options.Host+"/api/v1/catalog/projects", nil)
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("Authorization", "Bearer "+s.options.Token)
		if kind == "auth" {
			r.Header.Del("Authorization")
		}
		if kind == "host" {
			r.Host = "evil.test"
		}
		if kind == "origin" {
			r.Header.Set("Origin", "https://evil.test")
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != 401 && w.Code != 403 {
			t.Fatal(kind, w.Code)
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("cache")
		}
	}
}
