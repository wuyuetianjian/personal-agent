package api

import (
	"agent/internal/interaction"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOperatorInventoryBoundedAndReadOnly(t *testing.T) {
	s, rt := serviceFixture(t, testSelection())
	id := createChat(t, s)
	for i := 0; i < 61; i++ {
		_, e := rt.DB.SQL.Exec(`INSERT INTO projects VALUES(?,?, 'private','[]','[]','','[]','[]','[]','', '[]')`, fmt.Sprintf("p%03d", i), "label\x1b[31m <script> SECRET_NOT_A_BODY")
		if e != nil {
			t.Fatal(e)
		}
	}
	var before int
	rt.DB.SQL.QueryRow(`SELECT total_changes()`).Scan(&before)
	path := "/api/v1/sessions/" + id + "/operator/projects"
	w := apiRequest(s, "GET", path+"?limit=20", "")
	if w.Code != 200 {
		t.Fatalf("inventory missing: %d %s", w.Code, w.Body.String())
	}
	var v struct {
		Items     []any  `json:"items"`
		Next      string `json:"next_cursor"`
		Namespace string `json:"namespace"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &v); e != nil {
		t.Fatal(e)
	}
	if len(v.Items) != 20 || v.Next == "" || v.Namespace != "runtime-rebuild-v1" {
		t.Fatal(w.Body.String())
	}
	w = apiRequest(s, "GET", path+"?limit=20&cursor="+v.Next, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "p020") || strings.Contains(w.Body.String(), "p000") {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, q := range []string{"limit=0", "limit=51", "limit=1&limit=2", "path=/tmp", "cursor=garbage", "owner=x", "before=x", "filter=unknown", "cursor=" + v.Next + "&filter=active"} {
		if w := apiRequest(s, "GET", path+"?"+q, ""); w.Code != 400 {
			t.Fatalf("query %s: %d", q, w.Code)
		}
	}
	if w := apiRequest(s, "GET", "/api/v1/sessions/missing/operator/projects", ""); w.Code != 404 {
		t.Fatal(w.Code)
	}
	s.options.Resolve = func(_ context.Context, _ string) (ProviderSelection, error) {
		t.Error("inventory resolved/probed provider")
		return ProviderSelection{}, fmt.Errorf("not allowed")
	}
	for _, resource := range []string{"tasks", "workflows", "projects", "knowledge", "models", "skills", "triggers", "notifications", "permissions", "approvals", "audit", "diagnostics", "mcp"} {
		w := apiRequest(s, "GET", "/api/v1/sessions/"+id+"/operator/"+resource, "")
		if w.Code != 200 {
			t.Errorf("%s: %d %s", resource, w.Code, w.Body.String())
		}
	}
	var after int
	rt.DB.SQL.QueryRow(`SELECT total_changes()`).Scan(&after)
	if after != before {
		t.Fatalf("read mutated: %d -> %d", before, after)
	}
}
func TestOperatorSecurity(t *testing.T) {
	s, _ := serviceFixture(t, testSelection())
	id := createChat(t, s)
	path := "/api/v1/sessions/" + id + "/operator/tasks"
	for _, change := range []func(*http.Request){func(r *http.Request) { r.Header.Del("Authorization") }, func(r *http.Request) { r.Host = "evil.test" }, func(r *http.Request) { r.Header.Set("Origin", "https://evil.test") }, func(r *http.Request) { r.RemoteAddr = "10.0.0.1:9" }} {
		r := httptest.NewRequest("GET", "http://127.0.0.1:7319"+path, nil)
		r.RemoteAddr = "127.0.0.1:1"
		r.Header.Set("Authorization", "Bearer secret-token")
		change(r)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code < 400 {
			t.Fatal("security bypass")
		}
	}
	if w := apiRequest(s, "POST", path, `{"request_id":"x"}`); w.Code < 400 {
		t.Fatal("write admitted")
	}
}

func TestOperatorMetadataRedactionScopeAndOwners(t *testing.T) {
	s, rt := serviceFixture(t, testSelection())
	chat := createChat(t, s)
	w := apiRequest(s, "POST", "/api/v1/sessions", `{"request_id":"scope","mode":"default"}`)
	var v SessionView
	json.Unmarshal(w.Body.Bytes(), &v)
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	workspace, e := s.store.Workspace(context.Background(), v.ID)
	if e != nil {
		t.Fatal(e)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, e := rt.DB.SQL.Exec(q, args...); e != nil {
			t.Fatal(e)
		}
	}
	for _, id := range []string{"owned", "other", "unowned", "stale"} {
		exec(`INSERT INTO documents VALUES(?, 'SECRET_URI', ?, 'hash','{"secret":"SECRET_METADATA"}','private',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, id, strings.Repeat("x", 10000)+"SECRET_TAIL")
		if id != "unowned" {
			scope := workspace.ScopeID
			if id == "other" {
				scope = "different-scope"
			}
			exec(`INSERT INTO project_document_scopes(document_id,project_id,content_hash,revision) SELECT document_id,?,'hash',revision FROM document_revisions WHERE document_id=?`, scope, id)
		}
	}
	exec(`UPDATE document_revisions SET revision='revoked' WHERE document_id='stale'`)
	w = apiRequest(s, "GET", "/api/v1/sessions/"+v.ID+"/operator/knowledge", "")
	var p OperatorPage
	json.Unmarshal(w.Body.Bytes(), &p)
	if w.Code != 200 || len(p.Items) != 1 || p.Items[0].ID != "owned" || strings.Contains(w.Body.String(), "SECRET") || len(p.Items[0].Label) > 515 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = apiRequest(s, "GET", "/api/v1/sessions/"+chat+"/operator/knowledge", "")
	json.Unmarshal(w.Body.Bytes(), &p)
	if len(p.Items) != 0 {
		t.Fatal("chat scope leak")
	}
	exec(`INSERT INTO tasks(id,title,input,status,created_at,updated_at,leader_model_id,privacy_class,error_message) VALUES('task','owned task','SECRET_INPUT','completed',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'test','private','SECRET_ERROR')`)
	exec(`INSERT INTO evidence VALUES('ev','task',NULL,'note','SECRET_SOURCE','SECRET_URI','SECRET_CONTENT','hash','SECRET_METADATA','private',CURRENT_TIMESTAMP)`)
	w = apiRequest(s, "GET", "/api/v1/sessions/"+v.ID+"/operator/evidence?owner=task", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"ev"`) || strings.Contains(w.Body.String(), "SECRET") {
		t.Fatal(w.Body.String())
	}
	for _, resource := range []string{"nodes", "checkpoints", "versions", "trigger-state", "trigger-history", "evidence"} {
		w := apiRequest(s, "GET", "/api/v1/sessions/"+chat+"/operator/"+resource+"?owner=missing", "")
		if w.Code != 404 {
			t.Fatalf("%s %d", resource, w.Code)
		}
	}
	exec(`INSERT INTO security_audit_log VALUES('audit','SECRET_ACTOR','test','SECRET_TARGET','done','SECRET_ERROR','SECRET_EVIDENCE',CURRENT_TIMESTAMP)`)
	w = apiRequest(s, "GET", "/api/v1/sessions/"+chat+"/operator/audit", "")
	if strings.Contains(w.Body.String(), "SECRET") || !strings.Contains(w.Body.String(), "incomplete coverage") {
		t.Fatal(w.Body.String())
	}
	// A new locked runtime sees none of this runtime's business records.
	second, _ := serviceFixture(t, testSelection())
	sid := createChat(t, second)
	w = apiRequest(second, "GET", "/api/v1/sessions/"+sid+"/operator/tasks", "")
	json.Unmarshal(w.Body.Bytes(), &p)
	if len(p.Items) != 0 {
		t.Fatal("namespace leak")
	}
}
func TestOperatorModelSnapshotNoSecrets(t *testing.T) {
	p := testSelection()
	p.Model.Provider.APIKeyEnv = "SECRET_KEY"
	p.Endpoint = "http://SECRET_HOST/v1"
	p.ToolsSupported = true
	p.ToolEvidenceID = "evidence"
	rec := OperatorModelSnapshot("test", p, nil)
	raw, _ := json.Marshal(rec)
	if strings.Contains(string(raw), "SECRET") || !strings.Contains(string(raw), "verified_synthetic") {
		t.Fatal(string(raw))
	}
	rec = OperatorModelSnapshot("test", p, fmt.Errorf("SECRET_ERROR"))
	raw, _ = json.Marshal(rec)
	if strings.Contains(string(raw), "SECRET") || rec.State != "configured_unresolved" {
		t.Fatal(string(raw))
	}
}

func TestOperatorStrictCursorAndModelCapabilities(t *testing.T) {
	s, _ := serviceFixture(t, testSelection())
	id := createChat(t, s)
	path := "/api/v1/sessions/" + id + "/operator/diagnostics"
	for _, raw := range []string{fmt.Sprintf(`{"Session":%q,"Resource":"diagnostics","Owner":"","Filter":"","After":9999}`, id), fmt.Sprintf(`{"Session":%q,"Resource":"diagnostics","Owner":"","Filter":"","After":1,"path":"/tmp"}`, id)} {
		w := apiRequest(s, "GET", path+"?cursor="+base64.RawURLEncoding.EncodeToString([]byte(raw)), "")
		if w.Code != 400 {
			t.Fatalf("cursor %d %s", w.Code, w.Body.String())
		}
	}
	p := testSelection()
	raw, _ := json.Marshal(OperatorModelSnapshot("test", p, nil))
	if !strings.Contains(string(raw), "configured_chat") || !strings.Contains(string(raw), "configured_tool_calling") {
		t.Fatal("configured capabilities missing", string(raw))
	}
	r := httptest.NewRequest("POST", "http://127.0.0.1:7319"+path, strings.NewReader(`{}`))
	r.RemoteAddr = "127.0.0.1:9"
	r.Header.Set("Authorization", "Bearer secret-token")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 426 {
		t.Fatal(w.Code)
	}
}

func TestOperatorOpaqueIDsDoNotAliasAfterDisplaySanitization(t *testing.T) {
	s, rt := serviceFixture(t, testSelection())
	id := createChat(t, s)
	for _, key := range []string{"task", "ta\x00sk", strings.Repeat("a", 600)} {
		_, e := rt.DB.SQL.Exec(`INSERT INTO tasks(id,title,input,status,created_at,updated_at,leader_model_id,privacy_class) VALUES(?,'label','','completed',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'test','private')`, key)
		if e != nil {
			t.Fatal(e)
		}
	}
	w := apiRequest(s, "GET", "/api/v1/sessions/"+id+"/operator/tasks", "")
	var p OperatorPage
	json.Unmarshal(w.Body.Bytes(), &p)
	if len(p.Items) != 3 || p.Items[1].ID != "ta\x00sk" || p.Items[2].ID != "[identifier exceeds bound]" {
		t.Fatal(w.Body.String())
	}
}

func TestOperatorApprovalsAreSessionOwnedAndTimestampExplicit(t *testing.T) {
	s, rt := serviceFixture(t, testSelection())
	id := createChat(t, s)
	if e := s.store.CreateSession(context.Background(), "foreign"); e != nil {
		t.Fatal(e)
	}
	for _, session := range []string{id, "foreign"} {
		run, e := s.store.CreateRun(context.Background(), session, session, interaction.Message{Content: "SECRET_BODY"})
		if e != nil {
			t.Fatal(e)
		}
		_, e = rt.DB.SQL.Exec(`INSERT INTO interaction_approvals(id,run_id,action_digest,expires_at) VALUES(?,?,?,?)`, session, run.ID, "digest", int64(2000000000000000000))
		if e != nil {
			t.Fatal(e)
		}
	}
	w := apiRequest(s, "GET", "/api/v1/sessions/"+id+"/operator/approvals", "")
	var p OperatorPage
	json.Unmarshal(w.Body.Bytes(), &p)
	if len(p.Items) != 1 || p.Items[0].ID != id || p.Items[0].Fields[1].Name != "expires_at_unix_nano" || strings.Contains(w.Body.String(), "SECRET") {
		t.Fatal(w.Body.String())
	}
	w = apiRequest(s, "GET", "/api/v1/status", "")
	if !strings.Contains(w.Body.String(), `"manage"`) {
		t.Fatal("operator command absent")
	}
}
