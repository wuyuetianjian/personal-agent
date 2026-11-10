package api

import (
	"agent/internal/interaction"
	"agent/internal/model"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type interactionStreamFunc func(context.Context, model.TurnRequest, func(model.ModelEvent) error) (model.TurnResult, error)

func (f interactionStreamFunc) Stream(c context.Context, r model.TurnRequest, e func(model.ModelEvent) error) (model.TurnResult, error) {
	return f(c, r, e)
}
func testSelection() ProviderSelection {
	return ProviderSelection{Endpoint: "http://127.0.0.1:8000/v1", Model: model.ModelMetadata{ID: "test", ProviderID: "fake", Model: "fake", ContextWindow: 32000, TrustLevel: model.TrustLocalPrivate, Provider: model.ProviderMetadata{Type: "fixture", Enabled: true, TrustLevel: model.TrustLocalPrivate}}, Provider: interactionStreamFunc(func(ctx context.Context, r model.TurnRequest, emit func(model.ModelEvent) error) (model.TurnResult, error) {
		if err := emit(model.ModelEvent{Kind: model.EventTextDelta, Text: "你好"}); err != nil {
			return model.TurnResult{}, err
		}
		return model.TurnResult{Content: "你好"}, nil
	})}
}

func serviceFixture(t *testing.T, p ProviderSelection) (*InteractionService, *InteractionRuntime) {
	t.Helper()
	rt, e := OpenInteractionRuntime(context.Background(), t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	s, e := NewInteractionService(rt, InteractionOptions{Token: "secret-token", Host: "127.0.0.1:7319", Workspace: t.TempDir(), DefaultModel: "test", Resolve: func(context.Context, string) (ProviderSelection, error) { return p, nil }})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close(); rt.Close() })
	return s, rt
}
func apiRequest(s *InteractionService, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://127.0.0.1:7319"+path, strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:1000"
	r.Header.Set("Authorization", "Bearer secret-token")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Agent-Protocol", InteractionProtocolFlavor)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}
func createChat(t *testing.T, s *InteractionService) string {
	t.Helper()
	w := apiRequest(s, "POST", "/api/v1/sessions", `{"request_id":"create","mode":"chat_only"}`)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var v SessionView
	if e := json.Unmarshal(w.Body.Bytes(), &v); e != nil {
		t.Fatal(e)
	}
	return v.ID
}
func awaitState(t *testing.T, s *InteractionService, id, state string) Snapshot {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		w := apiRequest(s, "GET", "/api/v1/sessions/"+id+"/snapshot", "")
		var v Snapshot
		if e := json.Unmarshal(w.Body.Bytes(), &v); e != nil {
			t.Fatal(e, w.Body.String())
		}
		if len(v.Runs) > 0 && (v.Runs[len(v.Runs)-1].State == state || v.Runs[len(v.Runs)-1].Phase == state) {
			return v
		}
		time.Sleep(time.Millisecond * 5)
	}
	t.Fatal("state not reached", state)
	return Snapshot{}
}
func TestInteractionSecurityAndChat(t *testing.T) {
	s, _ := serviceFixture(t, testSelection())
	for _, change := range []func(*http.Request){func(r *http.Request) { r.Header.Del("Authorization") }, func(r *http.Request) { r.Host = "evil.test" }, func(r *http.Request) { r.Header.Set("Origin", "https://evil.test") }, func(r *http.Request) { r.RemoteAddr = "10.0.0.1:9" }, func(r *http.Request) { r.URL.RawQuery = "token=secret-token" }} {
		r := httptest.NewRequest("GET", "http://127.0.0.1:7319/api/v1/status", nil)
		r.RemoteAddr = "127.0.0.1:1000"
		r.Header.Set("Authorization", "Bearer secret-token")
		change(r)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code < 400 {
			t.Fatal("security bypass", w.Code)
		}
	}
	if w := apiRequest(s, "GET", "/api/tasks", ""); w.Code != 404 {
		t.Fatal("legacy route exposed")
	}
	id := createChat(t, s)
	if w := apiRequest(s, "POST", "/api/v1/sessions/"+id+"/messages", `{"request_id":"m1","content":"hello"}`); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	v := awaitState(t, s, id, "completed")
	if v.Runs[0].Text != "你好" || v.Cursor < 3 {
		t.Fatal(v)
	}
	raw, _ := json.Marshal(v)
	for _, secret := range []string{"APIKey", "BaseURL", "checkpoint", "secret-token"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("internal leaked", string(raw))
		}
	}
	w := apiRequest(s, "POST", "/api/v1/sessions/"+id+"/messages", `{"request_id":"m1","content":"changed"}`)
	if w.Code != 409 {
		t.Fatal("request ID reused with changed input", w.Code)
	}
	if w = apiRequest(s, "POST", "/api/v1/sessions", `{"request_id":"escape","mode":"default","root":"/"}`); w.Code != 400 {
		t.Fatal("client root accepted")
	}
}
func TestInteractionCancelPausedApprovalAndExactReview(t *testing.T) {
	p := testSelection()
	p.ToolsSupported = true
	p.Provider = interactionStreamFunc(func(ctx context.Context, r model.TurnRequest, emit func(model.ModelEvent) error) (model.TurnResult, error) {
		scope := ""
		for _, m := range r.Messages {
			if strings.HasPrefix(m.Content, "Workspace scope: ") {
				scope = strings.Split(strings.TrimPrefix(m.Content, "Workspace scope: "), ".")[0]
			}
		}
		sum := sha256.Sum256([]byte("old"))
		args, _ := json.Marshal(interaction.PatchArguments{Scope: scope, Path: "note.txt", Preimage: fmt.Sprintf("%x", sum), Content: "new"})
		return model.TurnResult{ToolCalls: []model.ToolCall{{ID: "edit", Name: "apply_patch", Arguments: string(args)}}}, nil
	})
	s, _ := serviceFixture(t, p)
	if !interaction.NewWorkspaceBroker(s.store).Capabilities().Files.Enabled {
		t.Skip("native file adapter unavailable")
	}
	if e := os.WriteFile(filepath.Join(s.options.Workspace, "note.txt"), []byte("old"), 0600); e != nil {
		t.Fatal(e)
	}
	w := apiRequest(s, "POST", "/api/v1/sessions", `{"request_id":"create","mode":"default"}`)
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	var session SessionView
	json.Unmarshal(w.Body.Bytes(), &session)
	w = apiRequest(s, "POST", "/api/v1/sessions/"+session.ID+"/messages", `{"request_id":"m","content":"edit"}`)
	if w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	v := awaitState(t, s, session.ID, "waiting_approval")
	run := v.Runs[0]
	if run.Approval == nil {
		t.Fatal(v)
	}
	w = apiRequest(s, "GET", "/api/v1/approvals/"+run.Approval.ID+"/review", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"old_content":"old"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = apiRequest(s, "POST", "/api/v1/runs/"+run.ID+"/cancel", `{"request_id":"cancel"}`)
	if w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	awaitState(t, s, session.ID, "cancelled")
	b, _ := os.ReadFile(filepath.Join(s.options.Workspace, "note.txt"))
	if string(b) != "old" {
		t.Fatal("cancelled edit executed")
	}
}
func TestInteractionRealSSEAndProviderResumeBinding(t *testing.T) {
	s, _ := serviceFixture(t, testSelection())
	id := createChat(t, s)
	server := httptest.NewServer(s)
	defer server.Close()
	s.options.Host = strings.TrimPrefix(server.URL, "http://")
	req, _ := http.NewRequest("GET", server.URL+"/api/v1/sessions/"+id+"/events?after=99", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req = req.WithContext(ctx)
	resp, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	b := make([]byte, 8192)
	n, e := resp.Body.Read(b)
	if e != nil && e != io.EOF {
		t.Fatal(e)
	}
	if !strings.Contains(string(b[:n]), "event: snapshot") {
		t.Fatal(string(b[:n]))
	}
	s.options.Host = "127.0.0.1:7319"
	s.options.Resolve = func(context.Context, string) (ProviderSelection, error) {
		p := testSelection()
		p.Endpoint = "http://127.0.0.1:9999/v1"
		return p, nil
	}
	w := apiRequest(s, "POST", "/api/v1/sessions/"+id+"/messages", `{"request_id":"m","content":"private"}`)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "provider_changed") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestSnapshotIsExactEventPrefixDuringStreaming(t *testing.T) {
	p := testSelection()
	p.Provider = interactionStreamFunc(func(ctx context.Context, r model.TurnRequest, emit func(model.ModelEvent) error) (model.TurnResult, error) {
		for i := 0; i < 40; i++ {
			if e := emit(model.ModelEvent{Kind: model.EventTextDelta, Text: "字"}); e != nil {
				return model.TurnResult{}, e
			}
			time.Sleep(time.Millisecond)
		}
		return model.TurnResult{Content: strings.Repeat("字", 40)}, nil
	})
	s, _ := serviceFixture(t, p)
	id := createChat(t, s)
	apiRequest(s, "POST", "/api/v1/sessions/"+id+"/messages", `{"request_id":"stream","content":"hello"}`)
	for i := 0; i < 30; i++ {
		snap, e := s.snapshot(context.Background(), id)
		if e != nil {
			t.Fatal(e)
		}
		evs, e := s.store.Events(context.Background(), id, 0, 1000)
		if e != nil {
			t.Fatal(e)
		}
		var want RunView
		for _, ev := range evs {
			if ev.Sequence <= snap.Cursor {
				applyWire(&want, projectEvent(ev))
			}
		}
		a, _ := json.Marshal(snap.Runs[0])
		b, _ := json.Marshal(want)
		if string(a) != string(b) {
			t.Fatalf("snapshot/cursor mismatch %s vs %s", a, b)
		}
	}
	awaitState(t, s, id, "completed")
}
func TestCancelIsVisibleBeforeExecutorStops(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	p := testSelection()
	p.Provider = interactionStreamFunc(func(context.Context, model.TurnRequest, func(model.ModelEvent) error) (model.TurnResult, error) {
		close(entered)
		<-release
		return model.TurnResult{Content: "late"}, nil
	})
	s, _ := serviceFixture(t, p)
	defer close(release)
	id := createChat(t, s)
	w := apiRequest(s, "POST", "/api/v1/sessions/"+id+"/messages", `{"request_id":"m","content":"hello"}`)
	var ack map[string]string
	json.Unmarshal(w.Body.Bytes(), &ack)
	<-entered
	w = apiRequest(s, "POST", "/api/v1/runs/"+ack["run_id"]+"/cancel", `{"request_id":"c"}`)
	if w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	snap, e := s.snapshot(context.Background(), id)
	if e != nil {
		t.Fatal(e)
	}
	if snap.Runs[0].State != "running" || snap.Runs[0].Phase != "cancelling" {
		t.Fatal("cancel intent not visible", snap.Runs[0])
	}
}
func TestRejectDuplicateAndWrongRouteJSONFields(t *testing.T) {
	s, _ := serviceFixture(t, testSelection())
	for _, body := range []string{`{"request_id":"one","request_id":"two","mode":"chat_only"}`, `{"request_id":"one","mode":"chat_only","decision":"approved"}`} {
		if w := apiRequest(s, "POST", "/api/v1/sessions", body); w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
func TestHistoryPagesExposeOlderRunsWithoutReplacingCurrentCursor(t *testing.T) {
	s, _ := serviceFixture(t, testSelection())
	id := createChat(t, s)
	for i := 0; i < snapshotRunLimit+2; i++ {
		r, e := s.store.CreateRun(context.Background(), id, fmt.Sprintf("q%d", i), interaction.Message{Content: fmt.Sprintf("message %d", i)})
		if e != nil {
			t.Fatal(e)
		}
		if _, e = s.store.Transition(context.Background(), r.ID, r.Version, interaction.RunCancelled); e != nil {
			t.Fatal(e)
		}
	}
	w := apiRequest(s, "GET", "/api/v1/sessions/"+id+"/history", "")
	var page struct {
		Runs       []RunView `json:"runs"`
		NextBefore *int64    `json:"next_before"`
		Cursor     int64     `json:"cursor"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &page); e != nil {
		t.Fatal(e)
	}
	if w.Code != 200 || len(page.Runs) != snapshotRunLimit || page.NextBefore == nil {
		t.Fatal(w.Code, w.Body.String())
	}
	next := apiRequest(s, "GET", fmt.Sprintf("/api/v1/sessions/%s/history?before=%d", id, *page.NextBefore), "")
	var older Snapshot
	json.Unmarshal(next.Body.Bytes(), &older)
	if len(older.Runs) != 2 || older.Runs[0].Input != "message 0" || older.Cursor != page.Cursor {
		t.Fatal(next.Body.String())
	}
}
func TestChatOnlyReopenResumePinsProviderAndAllowsCredentialRotation(t *testing.T) {
	parent := t.TempDir()
	ctx := context.Background()
	rt, e := OpenInteractionRuntime(ctx, parent)
	if e != nil {
		t.Fatal(e)
	}
	selection := testSelection()
	options := InteractionOptions{Token: "secret-token", Host: "127.0.0.1:7319", DefaultModel: "test", Resolve: func(context.Context, string) (ProviderSelection, error) { return selection, nil }}
	s, e := NewInteractionService(rt, options)
	if e != nil {
		t.Fatal(e)
	}
	id := createChat(t, s)
	// Durable admitted, not yet started: crash/restart must not auto-send anything.
	run, e := s.store.CreateRun(ctx, id, "admitted", interaction.Message{Content: "private saved context"})
	if e != nil {
		t.Fatal(e)
	}
	s.Close()
	rt.Close()
	rt, e = OpenInteractionRuntime(ctx, parent)
	if e != nil {
		t.Fatal(e)
	}
	defer rt.Close()
	s, e = NewInteractionService(rt, options)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	selection.Endpoint = "http://127.0.0.1:9999/v1"
	w := apiRequest(s, "POST", "/api/v1/runs/"+run.ID+"/resume", `{"request_id":"resume1"}`)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "provider_changed") {
		t.Fatal(w.Code, w.Body.String())
	}
	selection = testSelection()
	selection.Model.Provider.RequirePrivacyGateway = true
	w = apiRequest(s, "POST", "/api/v1/runs/"+run.ID+"/resume", `{"request_id":"resume2"}`)
	if w.Code != 409 {
		t.Fatal("trust redirect accepted", w.Body.String())
	}
	selection = testSelection()
	selection.Model.Provider.APIKeyEnv = "ROTATED_KEY"
	w = apiRequest(s, "POST", "/api/v1/runs/"+run.ID+"/resume", `{"request_id":"resume3"}`)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	awaitState(t, s, id, "completed")
}
func TestProjectionKeepsCancellingAcrossLateToolResult(t *testing.T) {
	r := RunView{State: "running", Phase: "cancelling"}
	applyWire(&r, WireEvent{Type: "tool.completed", Data: map[string]any{"content": "observed", "is_error": false}})
	if r.Phase != "cancelling" {
		t.Fatal(r)
	}
}
func TestRejectLegacyFlavorBeforeAnyMutation(t *testing.T) {
	s, rt := serviceFixture(t, testSelection())
	r := httptest.NewRequest("POST", "http://127.0.0.1:7319/api/v1/sessions", strings.NewReader(`{"request_id":"legacy","mode":"chat_only"}`))
	r.RemoteAddr = "127.0.0.1:1000"
	r.Header.Set("Authorization", "Bearer secret-token")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 426 {
		t.Fatal("legacy mutation admitted", w.Code, w.Body.String())
	}
	var n int
	rt.DB.SQL.QueryRow("SELECT count(*) FROM interaction_sessions").Scan(&n)
	if n != 0 {
		t.Fatal("mutated before flavor check")
	}
}
