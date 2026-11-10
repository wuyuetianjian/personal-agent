package api

import (
	"agent/internal/interaction"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestKnowledgeAPIStoredOnlyLifecycle(t *testing.T) {
	for _, mode := range []string{"chat_only", "default", "plan", "acceptEdits"} {
		t.Run(mode, func(t *testing.T) {
			s, rt := serviceFixture(t, testSelection())
			var session SessionView
			w := apiRequest(s, "POST", "/api/v1/sessions", fmt.Sprintf(`{"request_id":"s","mode":%q}`, mode))
			if w.Code != 201 {
				t.Fatal(w.Body.String())
			}
			json.Unmarshal(w.Body.Bytes(), &session)
			s.options.Resolve = func(context.Context, string) (ProviderSelection, error) {
				t.Fatal("knowledge resolved live provider")
				return ProviderSelection{}, nil
			}
			path := "/api/v1/sessions/" + session.ID + "/knowledge"
			body := `{"request_id":"p","kind":"ingest","document_id":"","source_kind":"text","source":"fixture","content":"orchid local evidence"}`
			w = apiRequest(s, "POST", path+"/preview", body)
			if mode == "chat_only" || mode == "plan" {
				if w.Code != 403 {
					t.Fatal(w.Code, w.Body.String())
				}
				if mode == "plan" {
					if w := apiRequest(s, "GET", path, ""); w.Code != 200 {
						t.Fatal(w.Code, w.Body.String())
					}
				}
				return
			}
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			var p interaction.KnowledgeOperation
			json.Unmarshal(w.Body.Bytes(), &p)
			for _, bad := range []string{strings.Replace(body, `"p"`, `".."`, 1), strings.Replace(body, `"content":`, `"scope":"other","content":`, 1), strings.Replace(body, `"content":`, `"kind":"ingest","content":`, 1), body + `{}`, strings.Replace(body, `"fixture"`, `null`, 1), strings.Replace(body, `"fixture"`, "\"\xff\"", 1), strings.Replace(body, `"orchid local evidence"`, fmt.Sprintf("%q", strings.Repeat("x", 65537)), 1)} {
				if w := apiRequest(s, "POST", path+"/preview", bad); w.Code != 400 {
					t.Fatal("body", w.Code, w.Body.String())
				}
			}
			for _, q := range []string{"?scope=x", "?limit=0", "?limit=51", "?limit=1&limit=2", "?cursor=bad", "?owner=x", "?after=x"} {
				if w := apiRequest(s, "GET", path+q, ""); w.Code != 400 {
					t.Fatal(q, w.Code, w.Body.String())
				}
			}
			w = apiRequest(s, "POST", path+"/decision", fmt.Sprintf(`{"request_id":"d","operation_id":"p","digest":%q,"decision":"confirm"}`, p.Digest))
			if w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"committed"`) {
				t.Fatal(w.Code, w.Body.String())
			}
			var before, after int
			rt.DB.SQL.QueryRow(`SELECT total_changes()`).Scan(&before)
			for _, route := range []string{path, path + "/operations/p", path + "/operations/d", path + "/search?q=orchid&limit=8"} {
				w = apiRequest(s, "GET", route, "")
				if w.Code != 200 {
					t.Fatal(route, w.Code, w.Body.String())
				}
				if !strings.Contains(w.Body.String(), p.DocumentID) {
					t.Fatal(w.Body.String())
				}
				if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Content-Security-Policy") == "" {
					t.Fatal("envelope")
				}
			}
			rt.DB.SQL.QueryRow(`SELECT total_changes()`).Scan(&after)
			if before != after {
				t.Fatal("reads mutated")
			}
		})
	}
}

func TestKnowledgeStrictUnicodeAndPaths(t *testing.T) {
	s, _ := serviceFixture(t, testSelection())
	var v SessionView
	json.Unmarshal(apiRequest(s, "POST", "/api/v1/sessions", `{"request_id":"s","mode":"default"}`).Body.Bytes(), &v)
	path := "/api/v1/sessions/" + v.ID + "/knowledge"
	for _, content := range []string{`\ud800`, `\udfff`, `\ud800x`, `\ud800\ud800`} {
		body := `{"request_id":"p","kind":"ingest","document_id":"","source_kind":"text","source":"label","content":"` + content + `"}`
		if w := apiRequest(s, "POST", path+"/preview", body); w.Code != 400 {
			t.Fatal(content, w.Code, w.Body.String())
		}
	}
	for _, route := range []string{path + "/operations/.", path + "/operations/..", path + "/search?q=x&q=y", path + "/search?q=x&owner=s", path + "?cursor="} {
		if w := apiRequest(s, "GET", route, ""); w.Code != 400 {
			t.Fatal(route, w.Code, w.Body.String())
		}
	}
}

func TestKnowledgeSourceRefusalAndEnvelope(t *testing.T) {
	s, rt := serviceFixture(t, testSelection())
	var v SessionView
	json.Unmarshal(apiRequest(s, "POST", "/api/v1/sessions", `{"request_id":"s","mode":"default"}`).Body.Bytes(), &v)
	path := "/api/v1/sessions/" + v.ID + "/knowledge"
	for i, source := range []string{"../outside", "/etc/passwd", ".git/config", "missing.txt"} {
		body := fmt.Sprintf(`{"request_id":"file%d","kind":"ingest","document_id":"","source_kind":"file","source":%q,"content":""}`, i, source)
		w := apiRequest(s, "POST", path+"/preview", body)
		if w.Code != 403 || !strings.Contains(w.Body.String(), "knowledge_source_refused") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	var n int
	rt.DB.SQL.QueryRow(`SELECT count(*) FROM interaction_knowledge_operations`).Scan(&n)
	if n != 0 {
		t.Fatal("refused sources staged", n)
	}
	for _, method := range []string{"GET", "POST"} {
		for _, change := range []func(*http.Request){func(r *http.Request) { r.Header.Del("Authorization") }, func(r *http.Request) { r.Host = "evil.test" }, func(r *http.Request) { r.Header.Set("Origin", "https://evil.test") }, func(r *http.Request) { r.RemoteAddr = "10.0.0.1:9" }, func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }} {
			r := httptest.NewRequest(method, "http://127.0.0.1:7319"+path, strings.NewReader(`{}`))
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
}

func TestKnowledgeActionAvailability(t *testing.T) {
	for _, mode := range []string{"default", "plan", "chat_only"} {
		s, _ := serviceFixture(t, testSelection())
		var v SessionView
		json.Unmarshal(apiRequest(s, "POST", "/api/v1/sessions", fmt.Sprintf(`{"request_id":"s","mode":%q}`, mode)).Body.Bytes(), &v)
		var p OperatorPage
		w := apiRequest(s, "GET", "/api/v1/sessions/"+v.ID+"/operator/knowledge", "")
		json.Unmarshal(w.Body.Bytes(), &p)
		found := false
		for _, a := range p.Actions {
			if a.Action == "ingest" {
				found = true
				if (a.Availability == "available") != (mode == "default") {
					t.Fatal(mode, a)
				}
			}
		}
		if !found {
			t.Fatal("missing implemented knowledge action")
		}
	}
}

func TestKnowledgeDurableOriginalIDRecoveryAndCursorOwnership(t *testing.T) {
	s, rt := serviceFixture(t, testSelection())
	var v, other SessionView
	for i, target := range []*SessionView{&v, &other} {
		w := apiRequest(s, "POST", "/api/v1/sessions", fmt.Sprintf(`{"request_id":"s%d","mode":"default"}`, i))
		if w.Code != 201 {
			t.Fatal(w.Body.String())
		}
		json.Unmarshal(w.Body.Bytes(), target)
	}
	path := "/api/v1/sessions/" + v.ID + "/knowledge"
	body := `{"request_id":"original","kind":"ingest","document_id":"","source_kind":"text","source":"label","content":"orchid original"}`
	w := apiRequest(s, "POST", path+"/preview", body)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var p interaction.KnowledgeOperation
	json.Unmarshal(w.Body.Bytes(), &p)
	// Discard the actual committed acknowledgement, then restart the locked runtime.
	decision := fmt.Sprintf(`{"request_id":"original-decision","operation_id":"original","digest":%q,"decision":"confirm"}`, p.Digest)
	if w := apiRequest(s, "POST", path+"/decision", decision); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	opts := s.options
	opts.Resolve = func(context.Context, string) (ProviderSelection, error) {
		t.Fatal("durable knowledge lookup resolved provider")
		return ProviderSelection{}, nil
	}
	parent := filepath.Dir(rt.Directory)
	s.Close()
	rt.Close()
	opened, e := OpenInteractionRuntime(context.Background(), parent)
	if e != nil {
		t.Fatal(e)
	}
	defer opened.Close()
	reopened, e := NewInteractionService(opened, opts)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	for _, id := range []string{"original", "original-decision"} {
		w = apiRequest(reopened, "GET", path+"/operations/"+id, "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"committed"`) {
			t.Fatal(w.Code, w.Body.String())
		}
		if w := apiRequest(reopened, "GET", "/api/v1/sessions/"+other.ID+"/knowledge/operations/"+id, ""); w.Code != 404 {
			t.Fatal("foreign recovery", w.Code)
		}
	}
	if w := apiRequest(reopened, "POST", path+"/decision", decision); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := apiRequest(reopened, "POST", path+"/decision", strings.Replace(decision, `"confirm"`, `"cancel"`, 1)); w.Code != 409 {
		t.Fatal("changed decision", w.Code)
	}
	if w := apiRequest(reopened, "POST", path+"/preview", strings.Replace(body, "orchid original", "changed", 1)); w.Code != 409 {
		t.Fatal("changed intent", w.Code)
	}
	var n int
	opened.DB.SQL.QueryRow(`SELECT count(*) FROM security_audit_log WHERE action='knowledge.ingest' AND status='committed'`).Scan(&n)
	if n != 1 {
		t.Fatal("duplicate effect", n)
	}
	w = apiRequest(reopened, "GET", path+"?limit=1", "")
	var list KnowledgePage
	json.Unmarshal(w.Body.Bytes(), &list)
	if w.Code != 200 || len(list.Items) != 1 || list.NextCursor == "" {
		t.Fatal(w.Body.String())
	}
	if w := apiRequest(reopened, "GET", "/api/v1/sessions/"+other.ID+"/knowledge?cursor="+list.NextCursor, ""); w.Code != 400 {
		t.Fatal("foreign cursor", w.Code)
	}
	// A source revision grant cannot revive stale evidence after out-of-band drift.
	if _, e := opened.DB.SQL.Exec(`UPDATE document_revisions SET revision='stale' WHERE document_id=?`, p.DocumentID); e != nil {
		t.Fatal(e)
	}
	w = apiRequest(reopened, "GET", path+"/search?q=orchid", "")
	var result KnowledgeSearch
	json.Unmarshal(w.Body.Bytes(), &result)
	if w.Code != 200 || len(result.Matches) != 0 {
		t.Fatal(w.Body.String())
	}
	w = apiRequest(reopened, "GET", path, "")
	if !strings.Contains(w.Body.String(), `"state":"stale"`) {
		t.Fatal(w.Body.String())
	}
}
