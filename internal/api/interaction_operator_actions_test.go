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

func TestOperatorActionAPI(t *testing.T) {
	s, rt := serviceFixture(t, testSelection())
	id := createChat(t, s)
	var second SessionView
	json.Unmarshal(apiRequest(s, "POST", "/api/v1/sessions", `{"request_id":"second","mode":"chat_only"}`).Body.Bytes(), &second)
	id2 := second.ID
	s.options.Resolve = func(context.Context, string) (ProviderSelection, error) {
		t.Fatal("metadata action invoked provider resolver")
		return ProviderSelection{}, nil
	}
	if _, e := rt.DB.SQL.Exec(`INSERT INTO projects VALUES('p','old','private','[]','[]','','[]','[]','[]','','[]')`); e != nil {
		t.Fatal(e)
	}
	path := "/api/v1/sessions/" + id + "/operator-actions"
	body := `{"request_id":"preview","kind":"project.rename","target_id":"p","new_value":"new"}`
	w := apiRequest(s, "POST", path+"/preview", body)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var p interaction.OperatorOperation
	if e := json.Unmarshal(w.Body.Bytes(), &p); e != nil {
		t.Fatal(e)
	}
	if p.State != "pending" {
		t.Fatal(p)
	}
	for _, b := range []string{`{"request_id":"x","kind":"project.rename","target_id":"p","new_value":"n","policy":"oops"}`, `{"request_id":"x","request_id":"y","kind":"project.rename","target_id":"p","new_value":"n"}`, `null`, body + ` {}`, strings.Replace(body, `"new"`, `null`, 1)} {
		if w := apiRequest(s, "POST", path+"/preview", b); w.Code != 400 {
			t.Fatal(b, w.Code)
		}
	}
	for _, q := range []string{"?after=1", "?before=1", "?owner=p", "?x=x"} {
		if w := apiRequest(s, "GET", path+"/preview"+q, ""); w.Code != 400 {
			t.Fatal(q, w.Code)
		}
	}
	if w := apiRequest(s, "GET", "/api/v1/sessions/"+id2+"/operator-actions/preview", ""); w.Code != 404 {
		t.Fatal(w.Code)
	}
	decision := fmt.Sprintf(`{"request_id":"confirm","operation_id":"preview","digest":%q,"decision":"confirm"}`, p.Digest)
	w = apiRequest(s, "POST", path+"/decision", decision)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"committed"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	// Simulate lost response: only ledger lookup resolves; no second mutation.
	w = apiRequest(s, "GET", path+"/confirm", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"committed"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	var n int
	rt.DB.SQL.QueryRow(`SELECT count(*) FROM security_audit_log WHERE status='committed'`).Scan(&n)
	if n != 1 {
		t.Fatal(n)
	}
}
func TestOperatorActionSecurityEnvelope(t *testing.T) {
	s, _ := serviceFixture(t, testSelection())
	id := createChat(t, s)
	for _, change := range []func(*http.Request){func(r *http.Request) { r.Header.Del("Authorization") }, func(r *http.Request) { r.Host = "evil.test" }, func(r *http.Request) { r.Header.Set("Origin", "https://evil.test") }, func(r *http.Request) { r.Header.Del("X-Agent-Protocol") }, func(r *http.Request) { r.RemoteAddr = "10.0.0.1:5" }, func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }} {
		r := httptest.NewRequest("POST", "http://127.0.0.1:7319/api/v1/sessions/"+id+"/operator-actions/preview", strings.NewReader(`{"request_id":"p","kind":"project.rename","target_id":"p","new_value":"new"}`))
		r.RemoteAddr = "127.0.0.1:1"
		r.Header.Set("Authorization", "Bearer secret-token")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Agent-Protocol", InteractionProtocolFlavor)
		change(r)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code < 400 {
			t.Fatal("bypass")
		}
	}
}

func TestOperatorActionsStoredOnlyAllSessionModes(t *testing.T) {
	for _, mode := range []string{"chat_only", "default", "plan", "acceptEdits"} {
		t.Run(mode, func(t *testing.T) {
			s, rt := serviceFixture(t, testSelection())
			var session SessionView
			created := apiRequest(s, "POST", "/api/v1/sessions", fmt.Sprintf(`{"request_id":"mode-session","mode":%q}`, mode))
			if created.Code != 201 {
				t.Fatal(created.Code, created.Body.String())
			}
			if e := json.Unmarshal(created.Body.Bytes(), &session); e != nil {
				t.Fatal(e)
			}
			if _, e := rt.DB.SQL.Exec(`INSERT INTO projects VALUES('p','old','private','[]','[]','','[]','[]','[]','','[]')`); e != nil {
				t.Fatal(e)
			}
			s.options.Resolve = func(context.Context, string) (ProviderSelection, error) {
				t.Fatal("metadata route entered live resolver for " + mode)
				return ProviderSelection{}, nil
			}
			path := "/api/v1/sessions/" + session.ID + "/operator-actions"
			w := apiRequest(s, "POST", path+"/preview", `{"request_id":"p","kind":"project.rename","target_id":"p","new_value":"new"}`)
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			var p interaction.OperatorOperation
			if e := json.Unmarshal(w.Body.Bytes(), &p); e != nil {
				t.Fatal(e)
			}
			w = apiRequest(s, "GET", path+"/p", "")
			if w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"pending"`) {
				t.Fatal(w.Code, w.Body.String())
			}
			w = apiRequest(s, "POST", path+"/decision", fmt.Sprintf(`{"request_id":"d","operation_id":"p","digest":%q,"decision":"confirm"}`, p.Digest))
			if w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"committed"`) {
				t.Fatal(w.Code, w.Body.String())
			}
			for _, id := range []string{"p", "d"} {
				w = apiRequest(s, "GET", path+"/"+id, "")
				if w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"committed"`) {
					t.Fatal(w.Code, w.Body.String())
				}
			}
			for _, action := range []string{"archive", "restore"} {
				value := "archived"
				if action == "restore" {
					value = "active"
				}
				w = apiRequest(s, "POST", path+"/preview", fmt.Sprintf(`{"request_id":%q,"kind":%q,"target_id":"p","new_value":%q}`, action, "project."+action, value))
				if w.Code != 200 {
					t.Fatal(w.Code, w.Body.String())
				}
				if e := json.Unmarshal(w.Body.Bytes(), &p); e != nil {
					t.Fatal(e)
				}
				w = apiRequest(s, "POST", path+"/decision", fmt.Sprintf(`{"request_id":%q,"operation_id":%q,"digest":%q,"decision":"confirm"}`, action+"-d", action, p.Digest))
				if w.Code != 200 {
					t.Fatal(w.Code, w.Body.String())
				}
				for _, request := range []string{action, action + "-d"} {
					w = apiRequest(s, "GET", path+"/"+request, "")
					if w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"committed"`) {
						t.Fatal(w.Code, w.Body.String())
					}
				}
			}

		})
	}
}

func TestProjectCatalogInventoryAndStrictUnicode(t *testing.T) {
	s, rt := serviceFixture(t, testSelection())
	id := createChat(t, s)
	if _, e := rt.DB.SQL.Exec(`INSERT INTO projects VALUES('p','literal [archived]','private','[]','[]','','[]','[]','[]','','[]')`); e != nil {
		t.Fatal(e)
	}
	path := "/api/v1/sessions/" + id
	w := apiRequest(s, "GET", path+"/operator/projects?filter=active", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"active"`) || !strings.Contains(w.Body.String(), `"name":"privacy"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, bad := range []string{`"\ud800"`, `"\udc00"`, `"\ud800x"`} {
		w = apiRequest(s, "POST", path+"/operator-actions/preview", `{"request_id":"bad","kind":"project.rename","target_id":"p","new_value":`+bad+`}`)
		if w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}

func TestProjectCatalogFiltersCursorIntegrityAndUnavailableActions(t *testing.T) {
	s, rt := serviceFixture(t, testSelection())
	id := createChat(t, s)
	path := "/api/v1/sessions/" + id + "/operator/projects"
	for _, p := range []string{"a", "b", "c"} {
		if _, e := rt.DB.SQL.Exec(`INSERT INTO projects VALUES(?,?,'private','[]','[]','','[]','[]','[]','','[]')`, p, p); e != nil {
			t.Fatal(e)
		}
	}
	var before, after int
	if e := rt.DB.SQL.QueryRow(`SELECT total_changes()`).Scan(&before); e != nil {
		t.Fatal(e)
	}
	w := apiRequest(s, "GET", path+"?limit=1&filter=active", "")
	var page OperatorPage
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if e := json.Unmarshal(w.Body.Bytes(), &page); e != nil {
		t.Fatal(e)
	}
	if page.NextCursor == "" {
		t.Fatal("missing cursor")
	}
	for _, a := range page.Actions {
		if a.Action == "use" || a.Action == "update" {
			if a.Availability != "not_implemented" || !strings.Contains(a.Reason, "No ") {
				t.Fatal(a)
			}
		}
	}
	w = apiRequest(s, "GET", path+"?limit=1&filter=archived&cursor="+page.NextCursor, "")
	if w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = apiRequest(s, "GET", path+"?filter=archived", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"items":[]`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if e := rt.DB.SQL.QueryRow(`SELECT total_changes()`).Scan(&after); e != nil || before != after {
		t.Fatal(before, after, e)
	}
	if _, e := rt.DB.SQL.Exec(`DELETE FROM project_catalog_lifecycle WHERE project_id='b'`); e != nil {
		t.Fatal(e)
	}
	w = apiRequest(s, "GET", path, "")
	if w.Code != 500 {
		t.Fatal("missing sidecar silently hidden", w.Code, w.Body.String())
	}
}

func TestProjectCatalogInventoryPreservesEmbeddedControlLabelExactly(t *testing.T) {
	s, rt := serviceFixture(t, testSelection())
	id := createChat(t, s)
	label := "name\x00tail\u202e" + strings.Repeat("界", 520)
	if _, e := rt.DB.SQL.Exec(`INSERT INTO projects VALUES('p',?,'private','[]','[]','','[]','[]','[]','','[]')`, label); e != nil {
		t.Fatal(e)
	}
	w := apiRequest(s, "GET", "/api/v1/sessions/"+id+"/operator/projects", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var page OperatorPage
	if e := json.Unmarshal(w.Body.Bytes(), &page); e != nil {
		t.Fatal(e)
	}
	if len(page.Items) != 1 || page.Items[0].Label != label {
		t.Fatalf("bounded exact label lost controls or Unicode: %#v", page.Items)
	}
}

func TestProjectCatalogOversizedIdentityCannotAliasLiteralPlaceholder(t *testing.T) {
	for _, oversized := range []string{strings.Repeat("x", 513), strings.Repeat("界", 171)} {
		t.Run(fmt.Sprintf("bytes-%d-runes-%d", len(oversized), len([]rune(oversized))), func(t *testing.T) {
			s, rt := serviceFixture(t, testSelection())
			id := createChat(t, s)
			literal := "[identifier exceeds bound]"
			boundary := strings.Repeat("v", 512)
			for _, record := range [][2]string{{oversized, "Oversized real project"}, {literal, "Unrelated literal-placeholder project"}, {boundary, "Exact boundary project"}} {
				if _, e := rt.DB.SQL.Exec(`INSERT INTO projects VALUES(?,?,'private','[]','[]','','[]','[]','[]','','[]')`, record[0], record[1]); e != nil {
					t.Fatal(e)
				}
			}
			path := "/api/v1/sessions/" + id
			var before, after int
			if e := rt.DB.SQL.QueryRow(`SELECT total_changes()`).Scan(&before); e != nil {
				t.Fatal(e)
			}
			for _, query := range []string{"", "?limit=1", "?filter=active"} {
				w := apiRequest(s, "GET", path+"/operator/projects"+query, "")
				if w.Code != 500 || strings.Contains(w.Body.String(), `"items"`) {
					t.Fatal("unrepresentable project emitted an actionable alias", w.Code, w.Body.String())
				}
			}
			if e := rt.DB.SQL.QueryRow(`SELECT total_changes()`).Scan(&after); e != nil || before != after {
				t.Fatal("GET wrote", before, after, e)
			}
			body, _ := json.Marshal(interaction.OperatorIntent{RequestID: "oversized", Kind: "project.archive", TargetID: oversized, NewValue: "archived"})
			if w := apiRequest(s, "POST", path+"/operator-actions/preview", string(body)); w.Code != 400 {
				t.Fatal(w.Code, w.Body.String())
			}
			body, _ = json.Marshal(interaction.OperatorIntent{RequestID: "literal", Kind: "project.archive", TargetID: literal, NewValue: "archived"})
			w := apiRequest(s, "POST", path+"/operator-actions/preview", string(body))
			if w.Code != 200 {
				t.Fatal("literal ID blacklisted", w.Code, w.Body.String())
			}
			var p interaction.OperatorOperation
			if e := json.Unmarshal(w.Body.Bytes(), &p); e != nil {
				t.Fatal(e)
			}
			w = apiRequest(s, "POST", path+"/operator-actions/decision", fmt.Sprintf(`{"request_id":"literal-d","operation_id":"literal","digest":%q,"decision":"confirm"}`, p.Digest))
			if w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"committed"`) {
				t.Fatal(w.Code, w.Body.String())
			}
			for target, want := range map[string]string{oversized: "active", literal: "archived"} {
				var state string
				if e := rt.DB.SQL.QueryRow(`SELECT state FROM project_catalog_lifecycle WHERE project_id=?`, target).Scan(&state); e != nil || state != want {
					t.Fatal(state, want, e)
				}
			}
			if _, e := rt.DB.SQL.Exec(`DELETE FROM projects WHERE id=?`, oversized); e != nil {
				t.Fatal(e)
			}
			w = apiRequest(s, "GET", path+"/operator/projects", "")
			var page OperatorPage
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			if e := json.Unmarshal(w.Body.Bytes(), &page); e != nil || len(page.Items) != 2 || page.Items[0].ID != literal || page.Items[1].ID != boundary {
				t.Fatal(page, e)
			}
		})
	}
}
