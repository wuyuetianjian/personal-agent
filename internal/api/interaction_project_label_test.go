package api

import (
	"agent/internal/interaction"
	"agent/internal/project"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func labelProject(t *testing.T, rt *InteractionRuntime, id, name string) {
	t.Helper()
	if e := (project.Store{DB: rt.DB.SQL}).Save(t.Context(), project.Project{ID: id, Name: name}); e != nil {
		t.Fatal(e)
	}
}
func labelResponse(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if e := json.Unmarshal(raw, &out); e != nil {
		t.Fatal(e)
	}
	return out
}
func TestSessionProjectLabelAdmissionAndReplay(t *testing.T) {
	s, rt := serviceFixture(t, testSelection())
	labelProject(t, rt, "label-a", "Exact\n名称")
	input := `{"request_id":"label-session","mode":"default","catalog_project_id":"label-a"}`
	w := apiRequest(s, "POST", "/api/v1/sessions", input)
	if w.Code != 201 {
		t.Fatalf("label admission: %d %s", w.Code, w.Body.String())
	}
	out := labelResponse(t, w.Body.Bytes())
	label, ok := out["project_label"].(map[string]any)
	if !ok || label["project_id"] != "label-a" || label["name"] != "Exact\n名称" || label["revision"] != float64(1) || label["generation"] == "" {
		t.Fatal(out)
	}
	original := w.Body.String()
	if _, e := rt.DB.SQL.Exec(`UPDATE projects SET name='renamed' WHERE id='label-a'; UPDATE project_catalog_lifecycle SET state='archived',revision=revision+1 WHERE project_id='label-a'`); e != nil {
		t.Fatal(e)
	}
	w = apiRequest(s, "POST", "/api/v1/sessions", input)
	if w.Code != 201 || w.Body.String() != original {
		t.Fatal("replayed snapshot changed", w.Code, w.Body.String())
	}
	if w = apiRequest(s, "POST", "/api/v1/sessions", `{"request_id":"label-session","mode":"default","catalog_project_id":"other"}`); w.Code != 409 {
		t.Fatal("changed intent", w.Code)
	}
	w = apiRequest(s, "GET", "/api/v1/sessions/"+out["id"].(string)+"/snapshot", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	snapshot := labelResponse(t, w.Body.Bytes())["session"].(map[string]any)
	if snapshot["project_label"].(map[string]any)["name"] != "Exact\n名称" {
		t.Fatal(snapshot)
	}
}
func TestSessionAdmissionPublicationAtomic(t *testing.T) {
	for _, table := range []string{"interaction_chat_policies", "interaction_api_sessions", "interaction_api_actions"} {
		t.Run(table, func(t *testing.T) {
			s, rt := serviceFixture(t, testSelection())
			if _, e := rt.DB.SQL.Exec("CREATE TRIGGER fail_owned BEFORE INSERT ON " + table + " BEGIN SELECT RAISE(ABORT,'owned failure'); END"); e != nil {
				t.Fatal(e)
			}
			w := apiRequest(s, "POST", "/api/v1/sessions", `{"request_id":"atomic","mode":"chat_only"}`)
			if w.Code < 400 {
				t.Fatal(w.Code, w.Body.String())
			}
			for _, name := range []string{"interaction_sessions", "interaction_chat_policies", "interaction_api_sessions", "interaction_api_actions"} {
				var n int
				if e := rt.DB.SQL.QueryRow("SELECT count(*) FROM " + name).Scan(&n); e != nil || n != 0 {
					t.Fatalf("partial publication %s count=%d err=%v", name, n, e)
				}
			}
		})
	}
}
func TestSessionProjectLabelRejectsUnavailableAndCallerAuthority(t *testing.T) {
	s, rt := serviceFixture(t, testSelection())
	labelProject(t, rt, "archived", "old")
	if _, e := rt.DB.SQL.Exec(`UPDATE project_catalog_lifecycle SET state='archived' WHERE project_id='archived'`); e != nil {
		t.Fatal(e)
	}
	for i, id := range []string{"archived", "missing"} {
		w := apiRequest(s, "POST", "/api/v1/sessions", fmt.Sprintf(`{"request_id":"bad-%d","mode":"chat_only","catalog_project_id":%q}`, i, id))
		if w.Code != 409 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	for i, field := range []string{`"project_label":{}`, `"scope_id":"label-a"`, `"project_id":"label-a"`, `"catalog_project_id":null`} {
		w := apiRequest(s, "POST", "/api/v1/sessions", fmt.Sprintf(`{"request_id":"authority-%d","mode":"chat_only",%s}`, i, field))
		if w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}

func TestProjectLabelStrictAdmissionQuery(t *testing.T) {
	s, _ := serviceFixture(t, testSelection())
	for _, query := range []string{"?before=x", "?after=0", "?catalog_project_id=x", "?before=x&before=y"} {
		w := apiRequest(s, "POST", "/api/v1/sessions"+query, `{"request_id":"query","mode":"chat_only"}`)
		if w.Code != 400 {
			t.Fatalf("new-session query must refuse: %s %d", query, w.Code)
		}
		w = apiRequest(s, "GET", "/api/v1/admissions/query"+query, "")
		if w.Code != 400 {
			t.Fatalf("admission query must refuse: %s %d", query, w.Code)
		}
	}
}

func TestProjectLabelAllPublicationWritesRollbackAndNoReplay(t *testing.T) {
	for _, mcp := range []bool{false, true} {
		for _, table := range []string{"interaction_sessions", "interaction_workspaces", "interaction_api_sessions", "interaction_session_project_labels", "interaction_api_actions", "terminal_admission"} {
			t.Run(fmt.Sprintf("mcp=%v/%s", mcp, table), func(t *testing.T) {
				rt, e := OpenInteractionRuntime(t.Context(), t.TempDir())
				if e != nil {
					t.Fatal(e)
				}
				defer rt.Close()
				marker := filepath.Join(t.TempDir(), "effect")
				o := mcpAdmissionOptions(t, marker, false)
				var resolves atomic.Int32
				sel := testSelection()
				sel.ToolsSupported = true
				o.Resolve = func(context.Context, string) (ProviderSelection, error) { resolves.Add(1); return sel, nil }
				s, e := NewInteractionService(rt, o)
				if e != nil {
					t.Fatal(e)
				}
				defer s.Close()
				labelProject(t, rt, "p", "label")
				query := "CREATE TRIGGER fail_owned BEFORE INSERT ON " + table + " BEGIN SELECT RAISE(ABORT,'owned failure'); END"
				if table == "terminal_admission" {
					query = "CREATE TRIGGER fail_owned BEFORE UPDATE ON interaction_mcp_admissions WHEN NEW.state='complete' BEGIN SELECT RAISE(ABORT,'owned failure'); END"
				}
				if _, e = rt.DB.SQL.Exec(query); e != nil {
					t.Fatal(e)
				}
				input := fmt.Sprintf(`{"request_id":"atomic-label","mode":"default","mcp":%v,"catalog_project_id":"p"}`, mcp)
				w := apiRequest(s, "POST", "/api/v1/sessions", input)
				if w.Code != 409 {
					t.Fatal(w.Code, w.Body.String())
				}
				for _, name := range []string{"interaction_sessions", "interaction_workspaces", "interaction_api_sessions", "interaction_session_project_labels", "interaction_api_actions"} {
					var n int
					if e = rt.DB.SQL.QueryRow("SELECT count(*) FROM " + name).Scan(&n); e != nil || n != 0 {
						t.Fatal("partial publication", name, n, e)
					}
				}
				if _, e = rt.DB.SQL.Exec("DROP TRIGGER fail_owned"); e != nil {
					t.Fatal(e)
				}
				if w = apiRequest(s, "POST", "/api/v1/sessions", input); w.Code != 409 {
					t.Fatal("replayed failed admission", w.Code)
				}
				if resolves.Load() != 1 {
					t.Fatal("resolver replayed", resolves.Load())
				}
				if mcp {
					data, _ := os.ReadFile(marker + ".launch")
					if strings.Count(string(data), "\n") != 1 {
						t.Fatal("process replay", string(data))
					}
				}
				a, e := s.admission(t.Context(), "atomic-label")
				if e != nil || a.State != "unresolved" || a.ProjectLabel != nil {
					t.Fatal(a, e)
				}
			})
		}
	}
}

func TestProjectLabelAdmissionRestartRetainsExactSnapshotWithoutResolution(t *testing.T) {
	for _, mcp := range []bool{false, true} {
		t.Run(fmt.Sprint(mcp), func(t *testing.T) {
			c := t.Context()
			parent := t.TempDir()
			marker := filepath.Join(t.TempDir(), "effect")
			o := mcpAdmissionOptions(t, marker, false)
			var resolves atomic.Int32
			sel := testSelection()
			sel.ToolsSupported = true
			o.Resolve = func(context.Context, string) (ProviderSelection, error) { resolves.Add(1); return sel, nil }
			rt, e := OpenInteractionRuntime(c, parent)
			if e != nil {
				t.Fatal(e)
			}
			s, e := NewInteractionService(rt, o)
			if e != nil {
				t.Fatal(e)
			}
			defer func() { s.Close(); rt.Close() }()
			labelProject(t, rt, "p", "Original")
			input := fmt.Sprintf(`{"request_id":"lost-ack","mode":"default","mcp":%v,"catalog_project_id":"p"}`, mcp)
			committed := make(chan *httptest.ResponseRecorder, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				recorded := httptest.NewRecorder()
				s.ServeHTTP(recorded, r)
				committed <- recorded
				connection, _, err := w.(http.Hijacker).Hijack()
				if err == nil {
					connection.Close()
				}
			}))
			request, err := http.NewRequest("POST", server.URL+"/api/v1/sessions", strings.NewReader(input))
			if err != nil {
				t.Fatal(err)
			}
			request.Host = "127.0.0.1:7319"
			request.Header.Set("Authorization", "Bearer secret-token")
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-Agent-Protocol", InteractionProtocolFlavor)
			response, err := server.Client().Do(request)
			server.Close()
			if err == nil {
				response.Body.Close()
				t.Fatal("acknowledgement was not lost")
			}
			w := <-committed
			if w.Code != 201 {
				t.Fatal(w.Code, w.Body.String())
			}
			original := w.Body.String()
			out := labelResponse(t, w.Body.Bytes())
			id := out["id"].(string)
			if resolves.Load() != 1 {
				t.Fatal("multiple resolves during one admission", resolves.Load())
			}
			if _, e = rt.DB.SQL.Exec(`UPDATE projects SET name='after' WHERE id='p'; UPDATE project_catalog_lifecycle SET state='archived',revision=revision+1 WHERE project_id='p'`); e != nil {
				t.Fatal(e)
			}
			s.Close()
			rt.Close()
			rt, e = OpenInteractionRuntime(c, parent)
			if e != nil {
				t.Fatal(e)
			}
			s, e = NewInteractionService(rt, o)
			if e != nil {
				t.Fatal(e)
			}
			for range 2 {
				w = apiRequest(s, "POST", "/api/v1/sessions", input)
				if w.Code != 201 || w.Body.String() != original {
					t.Fatal("changed replay", w.Code, w.Body.String())
				}
			}
			a, e := s.admission(c, "lost-ack")
			if e != nil || a.SessionID != id || a.State != "complete" || a.ProjectLabel == nil || a.ProjectLabel.Name != "Original" {
				t.Fatal(a, e)
			}
			if resolves.Load() != 1 {
				t.Fatal("recovery called resolver", resolves.Load())
			}
			if mcp {
				data, _ := os.ReadFile(marker + ".launch")
				if strings.Count(string(data), "\n") != 1 {
					t.Fatal("recovery launched", string(data))
				}
			}
			var n int
			if e = rt.DB.SQL.QueryRow(`SELECT count(*) FROM interaction_sessions`).Scan(&n); e != nil || n != 1 {
				t.Fatal(n, e)
			}
		})
	}
}

func TestProjectLabelArchiveAndRenameDuringPreparation(t *testing.T) {
	for _, mcp := range []bool{false, true} {
		for _, mutation := range []string{"archive", "rename"} {
			t.Run(fmt.Sprintf("%v/%s", mcp, mutation), func(t *testing.T) {
				c := t.Context()
				rt, e := OpenInteractionRuntime(c, t.TempDir())
				if e != nil {
					t.Fatal(e)
				}
				defer rt.Close()
				marker := filepath.Join(t.TempDir(), "effect")
				o := mcpAdmissionOptions(t, marker, mcp)
				entered, release := make(chan struct{}, 1), make(chan struct{})
				sel := testSelection()
				sel.ToolsSupported = true
				if !mcp {
					o.Resolve = func(ctx context.Context, _ string) (ProviderSelection, error) {
						entered <- struct{}{}
						select {
						case <-release:
							return sel, nil
						case <-ctx.Done():
							return ProviderSelection{}, ctx.Err()
						}
					}
				}
				s, e := NewInteractionService(rt, o)
				if e != nil {
					t.Fatal(e)
				}
				defer s.Close()
				labelProject(t, rt, "p", "Original")
				input := fmt.Sprintf(`{"request_id":"staged","mode":"default","mcp":%v,"catalog_project_id":"p"}`, mcp)
				done := make(chan *httptest.ResponseRecorder, 1)
				go func() { done <- apiRequest(s, "POST", "/api/v1/sessions", input) }()
				if mcp {
					awaitMCPMarker(t, marker+".paused")
				} else {
					select {
					case <-entered:
					case <-time.After(5 * time.Second):
						t.Fatal("resolver not reached")
					}
				}
				var n int
				if e = rt.DB.SQL.QueryRow(`SELECT count(*) FROM interaction_sessions`).Scan(&n); e != nil || n != 0 {
					t.Fatal("published staged session", n, e)
				}
				if w := apiRequest(s, "POST", "/api/v1/sessions", input); w.Code != 409 {
					t.Fatal("duplicate in flight", w.Code)
				}
				query := `UPDATE project_catalog_lifecycle SET state='archived',revision=revision+1 WHERE project_id='p'`
				if mutation == "rename" {
					query = `UPDATE projects SET name='Renamed during preparation' WHERE id='p'`
				}
				if _, e = rt.DB.SQL.Exec(query); e != nil {
					t.Fatal(e)
				}
				if mcp {
					if e = os.WriteFile(marker+".release", []byte("go"), 0600); e != nil {
						t.Fatal(e)
					}
				} else {
					close(release)
				}
				var w *httptest.ResponseRecorder
				select {
				case w = <-done:
				case <-time.After(5 * time.Second):
					t.Fatal("admission stuck")
				}
				if mutation == "archive" {
					if w.Code != 409 {
						t.Fatal("archive race accepted", w.Code, w.Body.String())
					}
					if e = rt.DB.SQL.QueryRow(`SELECT count(*) FROM interaction_sessions`).Scan(&n); e != nil || n != 0 {
						t.Fatal("partial archived session", n, e)
					}
				} else {
					if w.Code != 201 {
						t.Fatal(w.Code, w.Body.String())
					}
					label := labelResponse(t, w.Body.Bytes())["project_label"].(map[string]any)
					if label["name"] != "Renamed during preparation" || label["revision"] != float64(2) {
						t.Fatal(label)
					}
				}
				if mcp {
					data, _ := os.ReadFile(marker + ".launch")
					if strings.Count(string(data), "\n") != 1 {
						t.Fatal("duplicate process", string(data))
					}
				}
			})
		}
	}
}

func TestMetadataOnlyLabelDoesNotChangeAuthority(t *testing.T) {
	s, rt := serviceFixture(t, testSelection())
	c := t.Context()
	origin := createChat(t, s)
	p, e := s.store.PrepareOperator(c, origin, interaction.OperatorIntent{RequestID: "create-label", Kind: "project.create", NewValue: "Metadata only"})
	if e != nil {
		t.Fatal(e)
	}
	result, e := s.store.DecideOperator(c, origin, interaction.OperatorDecision{RequestID: "confirm-label", OperationID: p.OperationID, Digest: p.Digest, Decision: "confirm"})
	if e != nil || result.State != "committed" {
		t.Fatal(result, e)
	}
	var policyBefore string
	query := `SELECT json_array(privacy_class,repository_refs_json,knowledge_scopes_json,memory_scope,allowed_skills_json,allowed_capabilities_json,allowed_coding_agents_json,allowed_models_json,budget_policy) FROM projects WHERE id=?`
	if e = rt.DB.SQL.QueryRow(query, p.TargetID).Scan(&policyBefore); e != nil {
		t.Fatal(e)
	}
	for _, mode := range []string{"chat_only", "default", "plan", "acceptEdits"} {
		ids := []string{}
		for _, label := range []string{"", p.TargetID} {
			body, _ := json.Marshal(map[string]any{"request_id": mode + label, "mode": mode, "catalog_project_id": label})
			w := apiRequest(s, "POST", "/api/v1/sessions", string(body))
			if w.Code != 201 {
				t.Fatal(mode, w.Code, w.Body.String())
			}
			ids = append(ids, labelResponse(t, w.Body.Bytes())["id"].(string))
		}
		var a, b string
		q := `SELECT json_array(scope_id,policy,root_identity) FROM interaction_workspaces WHERE session_id=?`
		if mode == "chat_only" {
			q = `SELECT policy FROM interaction_chat_policies WHERE session_id=?`
		}
		if e = rt.DB.SQL.QueryRow(q, ids[0]).Scan(&a); e != nil {
			t.Fatal(e)
		}
		if e = rt.DB.SQL.QueryRow(q, ids[1]).Scan(&b); e != nil || a != b {
			t.Fatal("label changed workspace authority", mode, a, b, e)
		}
	}
	var after string
	if e = rt.DB.SQL.QueryRow(query, p.TargetID).Scan(&after); e != nil || after != policyBefore {
		t.Fatal("project policy changed", e)
	}
	stored, e := (project.Store{DB: rt.DB.SQL}).Get(c, p.TargetID)
	if e != nil || stored.ExecutionAllowed() {
		t.Fatal("metadata became executable", stored, e)
	}
	for _, table := range []string{"interaction_runs", "interaction_queue", "interaction_jobs", "interaction_approvals", "documents", "document_chunks", "project_document_scopes"} {
		var n int
		if e = rt.DB.SQL.QueryRow("SELECT count(*) FROM " + table).Scan(&n); e != nil || n != 0 {
			t.Fatal("label executed", table, n, e)
		}
	}
}

func TestProjectLabelMissingUnknownClassificationRefusesBeforeSetup(t *testing.T) {
	for _, kind := range []string{"missing", "unknown"} {
		t.Run(kind, func(t *testing.T) {
			s, rt := serviceFixture(t, testSelection())
			labelProject(t, rt, "p", "label")
			if _, e := rt.DB.SQL.Exec(`DROP TRIGGER project_execution_immutable_delete; DROP TRIGGER project_execution_immutable_update; PRAGMA ignore_check_constraints=ON;`); e != nil {
				t.Fatal(e)
			}
			query := `DELETE FROM project_execution_classification WHERE project_id='p'`
			if kind == "unknown" {
				query = `UPDATE project_execution_classification SET execution_class='unknown' WHERE project_id='p'`
			}
			if _, e := rt.DB.SQL.Exec(query); e != nil {
				t.Fatal(e)
			}
			s.options.Resolve = func(context.Context, string) (ProviderSelection, error) {
				t.Error("invalid label reached setup")
				return testSelection(), nil
			}
			w := apiRequest(s, "POST", "/api/v1/sessions", `{"request_id":"invalid-class","mode":"default","catalog_project_id":"p","mcp":true}`)
			if w.Code != 409 {
				t.Fatal(w.Code, w.Body.String())
			}
			var n int
			if e := rt.DB.SQL.QueryRow(`SELECT count(*) FROM interaction_mcp_admissions`).Scan(&n); e != nil || n != 0 {
				t.Fatal(n, e)
			}
		})
	}
}

func TestSessionAdmissionCapacityAndCancellationNoPartialPublication(t *testing.T) {
	s, rt := serviceFixture(t, testSelection())
	entered := make(chan struct{}, maxInteractionWorkers)
	s.options.Resolve = func(c context.Context, _ string) (ProviderSelection, error) {
		entered <- struct{}{}
		<-c.Done()
		return ProviderSelection{}, c.Err()
	}
	c, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan int, maxInteractionWorkers)
	for i := range maxInteractionWorkers {
		go func(i int) {
			r := httptest.NewRequest("POST", "http://127.0.0.1:7319/api/v1/sessions", strings.NewReader(fmt.Sprintf(`{"request_id":"pending-%d","mode":"chat_only"}`, i))).WithContext(c)
			r.RemoteAddr = "127.0.0.1:1000"
			r.Header.Set("Authorization", "Bearer secret-token")
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("X-Agent-Protocol", InteractionProtocolFlavor)
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			done <- w.Code
		}(i)
	}
	for range maxInteractionWorkers {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("blocked admission lock")
		}
	}
	w := apiRequest(s, "POST", "/api/v1/sessions", `{"request_id":"overflow","mode":"chat_only"}`)
	if w.Code != 503 {
		t.Fatal(w.Code, w.Body.String())
	}
	cancel()
	for range maxInteractionWorkers {
		select {
		case code := <-done:
			if code < 400 {
				t.Fatal(code)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("cancel blocked")
		}
	}
	var n int
	if e := rt.DB.SQL.QueryRow(`SELECT count(*) FROM interaction_sessions`).Scan(&n); e != nil || n != 0 {
		t.Fatal(n, e)
	}
	if e := rt.DB.SQL.QueryRow(`SELECT count(*) FROM interaction_mcp_admissions WHERE request_id='overflow'`).Scan(&n); e != nil || n != 0 {
		t.Fatal("capacity consumed request", n, e)
	}
}

func TestProjectLabelAdmissionInspectionIsReadOnlyAndOffline(t *testing.T) {
	s, rt := serviceFixture(t, testSelection())
	labelProject(t, rt, "p", "label")
	w := apiRequest(s, "POST", "/api/v1/sessions", `{"request_id":"read-label","mode":"default","catalog_project_id":"p"}`)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	s.options.Resolve = func(context.Context, string) (ProviderSelection, error) {
		t.Error("admission GET resolved provider")
		return ProviderSelection{}, fmt.Errorf("offline")
	}
	var before, after int
	if e := rt.DB.SQL.QueryRow(`SELECT total_changes()`).Scan(&before); e != nil {
		t.Fatal(e)
	}
	a := apiRequest(s, "GET", "/api/v1/admissions/read-label", "")
	b := apiRequest(s, "GET", "/api/v1/admissions/read-label", "")
	if a.Code != 200 || !reflect.DeepEqual(a.Body.Bytes(), b.Body.Bytes()) || !strings.Contains(a.Body.String(), `"project_label"`) {
		t.Fatal(a.Code, a.Body.String(), b.Body.String())
	}
	if e := rt.DB.SQL.QueryRow(`SELECT total_changes()`).Scan(&after); e != nil || before != after {
		t.Fatal("GET mutated", before, after, e)
	}
}

func TestProjectLabelMalformedUnicodeCannotSelectReplacementIdentity(t *testing.T) {
	s, rt := serviceFixture(t, testSelection())
	labelProject(t, rt, "\ufffd", "replacement")
	labelProject(t, rt, "😀", "valid emoji")
	for i, id := range []string{`"\ud800"`, `"\udc00"`, "\"\xff\""} {
		w := apiRequest(s, "POST", "/api/v1/sessions", fmt.Sprintf(`{"request_id":"unicode-%d","mode":"chat_only","catalog_project_id":%s}`, i, id))
		if w.Code != 400 {
			t.Fatal("lossy identity admitted", w.Code, w.Body.String())
		}
	}
	w := apiRequest(s, "POST", "/api/v1/sessions", `{"request_id":"unicode-valid","mode":"chat_only","catalog_project_id":"\ud83d\ude00"}`)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestProjectLabelCorruptReplayRefusesWithoutSetup(t *testing.T) {
	s, rt := serviceFixture(t, testSelection())
	labelProject(t, rt, "p", "label")
	input := `{"request_id":"corrupt-result","mode":"chat_only","catalog_project_id":"p"}`
	if w := apiRequest(s, "POST", "/api/v1/sessions", input); w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, e := rt.DB.SQL.Exec(`UPDATE interaction_api_actions SET result=json_set(result,'$.body.project_label.generation','invalid') WHERE request_id='corrupt-result'`); e != nil {
		t.Fatal(e)
	}
	s.options.Resolve = func(context.Context, string) (ProviderSelection, error) {
		t.Error("corrupt replay setup")
		return testSelection(), nil
	}
	if w := apiRequest(s, "POST", "/api/v1/sessions", input); w.Code != 500 {
		t.Fatal("returned corrupt snapshot", w.Code, w.Body.String())
	}
}

func TestSessionAdmissionInspectionBoundsAndAuthorization(t *testing.T) {
	s, rt := serviceFixture(t, testSelection())
	labelProject(t, rt, "p", "label")
	for _, id := range []string{"", strings.Repeat("x", 129)} {
		w := apiRequest(s, "GET", "/api/v1/admissions/"+id, "")
		if w.Code != 400 {
			t.Fatal("unbounded admission ID", len(id), w.Code)
		}
	}
	r := httptest.NewRequest("POST", "http://127.0.0.1:7319/api/v1/sessions", strings.NewReader(`{"request_id":"unauthorized","mode":"chat_only","catalog_project_id":"p"}`))
	r.RemoteAddr = "127.0.0.1:1000"
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Agent-Protocol", InteractionProtocolFlavor)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
	var n int
	if e := rt.DB.SQL.QueryRow(`SELECT count(*) FROM interaction_mcp_admissions`).Scan(&n); e != nil || n != 0 {
		t.Fatal("unauthenticated claim", n, e)
	}
}

func TestLegacyPartialSessionMappingCannotAcquireLabelOrRelaunch(t *testing.T) {
	for _, mcp := range []bool{false, true} {
		t.Run(fmt.Sprint(mcp), func(t *testing.T) {
			c := t.Context()
			parent := t.TempDir()
			marker := filepath.Join(t.TempDir(), "effect")
			o := mcpAdmissionOptions(t, marker, false)
			rt, e := OpenInteractionRuntime(c, parent)
			if e != nil {
				t.Fatal(e)
			}
			s, e := NewInteractionService(rt, o)
			if e != nil {
				t.Fatal(e)
			}
			defer func() { s.Close(); rt.Close() }()
			input := fmt.Sprintf(`{"request_id":"legacy-partial","mode":"default","mcp":%v}`, mcp)
			w := apiRequest(s, "POST", "/api/v1/sessions", input)
			if w.Code != 201 {
				t.Fatal(w.Code, w.Body.String())
			}
			// Reproduce a pre-ledger partial publication: immutable binding exists but
			// no action acknowledgement or durable claim can justify setup replay.
			if _, e = rt.DB.SQL.Exec(`DELETE FROM interaction_api_actions WHERE request_id='legacy-partial'; DELETE FROM interaction_mcp_admissions WHERE request_id='legacy-partial'`); e != nil {
				t.Fatal(e)
			}
			s.Close()
			rt.Close()
			rt, e = OpenInteractionRuntime(c, parent)
			if e != nil {
				t.Fatal(e)
			}
			o.Resolve = func(context.Context, string) (ProviderSelection, error) {
				t.Error("replayed legacy partial setup")
				return testSelection(), nil
			}
			s, e = NewInteractionService(rt, o)
			if e != nil {
				t.Fatal(e)
			}
			for _, body := range []string{input, `{"request_id":"legacy-partial","mode":"default","catalog_project_id":"anything"}`} {
				w = apiRequest(s, "POST", "/api/v1/sessions", body)
				if w.Code != 409 {
					t.Fatal(w.Code, w.Body.String())
				}
			}
			var n int
			if e = rt.DB.SQL.QueryRow(`SELECT count(*) FROM interaction_sessions`).Scan(&n); e != nil || n != 1 {
				t.Fatal(n, e)
			}
			if e = rt.DB.SQL.QueryRow(`SELECT count(*) FROM interaction_session_project_labels`).Scan(&n); e != nil || n != 0 {
				t.Fatal("adopted legacy session", n, e)
			}
			if mcp {
				data, _ := os.ReadFile(marker + ".launch")
				if strings.Count(string(data), "\n") != 1 {
					t.Fatal("restarted legacy process", string(data))
				}
			}
		})
	}
}

func TestOrdinaryFailedAdmissionRemainsNonReplayableAfterRestart(t *testing.T) {
	c := t.Context()
	parent := t.TempDir()
	rt, e := OpenInteractionRuntime(c, parent)
	if e != nil {
		t.Fatal(e)
	}
	var resolutions atomic.Int32
	o := InteractionOptions{Token: "secret-token", Host: "127.0.0.1:7319", Workspace: t.TempDir(), DefaultModel: "test", Resolve: func(context.Context, string) (ProviderSelection, error) {
		resolutions.Add(1)
		return testSelection(), nil
	}}
	s, e := NewInteractionService(rt, o)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { s.Close(); rt.Close() }()
	labelProject(t, rt, "p", "label")
	if _, e = rt.DB.SQL.Exec(`CREATE TRIGGER fail_owned BEFORE INSERT ON interaction_api_actions BEGIN SELECT RAISE(ABORT,'owned failure'); END`); e != nil {
		t.Fatal(e)
	}
	input := `{"request_id":"failed-plain","mode":"chat_only","catalog_project_id":"p"}`
	w := apiRequest(s, "POST", "/api/v1/sessions", input)
	if w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, e = rt.DB.SQL.Exec(`DROP TRIGGER fail_owned`); e != nil {
		t.Fatal(e)
	}
	a, e := s.admission(c, "failed-plain")
	if e != nil {
		t.Fatal(e)
	}
	s.Close()
	rt.Close()
	rt, e = OpenInteractionRuntime(c, parent)
	if e != nil {
		t.Fatal(e)
	}
	s, e = NewInteractionService(rt, o)
	if e != nil {
		t.Fatal(e)
	}
	b, e := s.admission(c, "failed-plain")
	if e != nil || b.State != "revoked" || b.SessionID != a.SessionID || b.ProjectLabel != nil {
		t.Fatal(b, e)
	}
	if w = apiRequest(s, "POST", "/api/v1/sessions", input); w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	if resolutions.Load() != 1 {
		t.Fatal("retry resolved again", resolutions.Load())
	}
	var n int
	if e = rt.DB.SQL.QueryRow(`SELECT count(*) FROM interaction_sessions`).Scan(&n); e != nil || n != 0 {
		t.Fatal("recovered partial session", n, e)
	}
}
