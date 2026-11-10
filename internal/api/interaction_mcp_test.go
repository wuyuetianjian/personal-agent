package api

import (
	"agent/internal/interaction"
	"agent/internal/mcpfixture"
	"agent/internal/model"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAPIMCPPeer(t *testing.T) {
	if os.Getenv("PA_MCP_FIXTURE") == "api" {
		mcpfixture.Serve()
		os.Exit(0)
	}
}
func TestMCPAPIExplicitSessionOptInAndRestartRevocation(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	marker := filepath.Join(root, "effects")
	exe, _ := os.Executable()
	options := InteractionOptions{Token: "secret-token", Host: "127.0.0.1:7319", Workspace: root, DefaultModel: "test", MCP: interaction.MCPHostConfig{Enabled: true, Servers: []interaction.MCPServerConfig{{ID: "owned", Command: exe, Args: []string{"-test.run=^TestAPIMCPPeer$"}, Environment: []string{"PA_MCP_FIXTURE=api", "PA_MCP_MARKER=" + marker}, TimeoutMillis: 3000}}}}
	selection := testSelection()
	selection.ToolsSupported = true
	selection.Provider = interactionStreamFunc(func(ctx context.Context, r model.TurnRequest, emit func(model.ModelEvent) error) (model.TurnResult, error) {
		if r.Messages[len(r.Messages)-1].Role == "tool" {
			return model.TurnResult{Content: "done"}, nil
		}
		for _, tool := range r.Tools {
			if strings.HasPrefix(tool.Function.Name, "mcp_") {
				return model.TurnResult{ToolCalls: []model.ToolCall{{ID: "owned", Name: tool.Function.Name, Arguments: `{"value":"api"}`}}}, nil
			}
		}
		return model.TurnResult{Content: "no MCP capability"}, nil
	})
	options.Resolve = func(context.Context, string) (ProviderSelection, error) { return selection, nil }
	parent := t.TempDir()
	rt, e := OpenInteractionRuntime(ctx, parent)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { rt.Close() }()
	s, e := NewInteractionService(rt, options)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { s.Close() }()
	if r := apiRequest(s, "GET", "/api/v1/status", ""); r.Code != 200 || !strings.Contains(r.Body.String(), `"configured"`) {
		t.Fatal(r.Code, r.Body.String())
	}
	plain := namedSession(t, s, "plain", "default")
	postReviewMessage(t, s, plain, "plain-run", "try")
	snap := awaitState(t, s, plain, "completed")
	if len(snap.Runs[0].Tools) != 0 {
		t.Fatal("default session gained MCP")
	}
	if _, e = os.Stat(marker + ".launch"); !os.IsNotExist(e) {
		t.Fatal("inspection/default session launched", e)
	}
	response := apiRequest(s, "POST", "/api/v1/sessions", `{"request_id":"mcp-session","mode":"acceptEdits","mcp":true}`)
	if response.Code != 201 {
		t.Fatal(response.Code, response.Body.String())
	}
	var v SessionView
	json.Unmarshal(response.Body.Bytes(), &v)
	postReviewMessage(t, s, v.ID, "first", "mcp")
	snap = awaitState(t, s, v.ID, "waiting_approval")
	a := snap.Runs[0].Approval
	review := apiRequest(s, "GET", "/api/v1/approvals/"+a.ID+"/review", "")
	if review.Code != 200 || !strings.Contains(review.Body.String(), `"kind":"mcp"`) {
		t.Fatal(review.Code, review.Body.String())
	}
	// Restart preserves exact immutable review but must not relaunch or execute it.
	s.Close()
	rt.Close()
	rt, e = OpenInteractionRuntime(ctx, parent)
	if e != nil {
		t.Fatal(e)
	}
	s, e = NewInteractionService(rt, options)
	if e != nil {
		t.Fatal(e)
	}
	review = apiRequest(s, "GET", "/api/v1/approvals/"+a.ID+"/review", "")
	if review.Code != 200 || !strings.Contains(review.Body.String(), "approval revoked") {
		t.Fatal(review.Code, review.Body.String())
	}
	approve := apiRequest(s, "POST", "/api/v1/approvals/"+a.ID+"/decision", `{"request_id":"approve-old","decision":"approved","digest":"`+a.Digest+`"}`)
	if approve.Code != 409 {
		t.Fatal("accepted revoked generation", approve.Code, approve.Body.String())
	}
	deny := apiRequest(s, "POST", "/api/v1/approvals/"+a.ID+"/decision", `{"request_id":"deny-old","decision":"denied","digest":"`+a.Digest+`"}`)
	if deny.Code != 202 {
		t.Fatal("cannot end revoked run", deny.Code, deny.Body.String())
	}
	awaitState(t, s, v.ID, "failed")
	data, _ := os.ReadFile(marker + ".launch")
	if strings.Count(string(data), "\n") != 1 {
		t.Fatal("restart auto-launch", string(data))
	}
	if _, e = os.Stat(marker); !os.IsNotExist(e) {
		t.Fatal("old approval executed", e)
	}
	// New explicit session and new call can progress; repeated decision cannot replay.
	response = apiRequest(s, "POST", "/api/v1/sessions", `{"request_id":"mcp-new","mode":"default","mcp":true}`)
	if response.Code != 201 {
		t.Fatal(response.Code, response.Body.String())
	}
	json.Unmarshal(response.Body.Bytes(), &v)
	postReviewMessage(t, s, v.ID, "new-call", "mcp")
	snap = awaitState(t, s, v.ID, "waiting_approval")
	a = snap.Runs[0].Approval
	body := `{"request_id":"approve-new","decision":"approved","digest":"` + a.Digest + `"}`
	for i := 0; i < 2; i++ {
		response = apiRequest(s, "POST", "/api/v1/approvals/"+a.ID+"/decision", body)
		if response.Code != 202 {
			t.Fatal(response.Code, response.Body.String())
		}
	}
	awaitState(t, s, v.ID, "completed")
	data, _ = os.ReadFile(marker)
	if strings.Count(string(data), "\n") != 1 {
		t.Fatal("repeated effect", string(data))
	}
}

func mcpAdmissionOptions(t *testing.T, marker string, paused bool) InteractionOptions {
	t.Helper()
	exe, _ := os.Executable()
	selection := testSelection()
	selection.ToolsSupported = true
	env := []string{"PA_MCP_FIXTURE=api", "PA_MCP_MARKER=" + marker}
	if paused {
		env = append(env, "PA_MCP_PAUSE_DISCOVERY=1")
	}
	return InteractionOptions{Token: "secret-token", Host: "127.0.0.1:7319", Workspace: t.TempDir(), DefaultModel: "test", Resolve: func(context.Context, string) (ProviderSelection, error) { return selection, nil }, MCP: interaction.MCPHostConfig{Enabled: true, Servers: []interaction.MCPServerConfig{{ID: "owned", Command: exe, Args: []string{"-test.run=^TestAPIMCPPeer$"}, Environment: env, TimeoutMillis: 3000}}}}
}
func awaitMCPMarker(t *testing.T, path string) {
	t.Helper()
	until := time.Now().Add(5 * time.Second)
	for {
		if _, e := os.Stat(path); e == nil {
			return
		}
		if time.Now().After(until) {
			t.Fatal("missing marker", path)
		}
		time.Sleep(time.Millisecond)
	}
}
func TestMCPAdmissionFailuresNeverRelaunch(t *testing.T) {
	for _, table := range []string{"interaction_workspaces", "interaction_api_sessions", "interaction_api_actions", "state_write_failure"} {
		t.Run(table, func(t *testing.T) {
			ctx := context.Background()
			parent := t.TempDir()
			marker := filepath.Join(t.TempDir(), "effect")
			o := mcpAdmissionOptions(t, marker, false)
			rt, e := OpenInteractionRuntime(ctx, parent)
			if e != nil {
				t.Fatal(e)
			}
			s, e := NewInteractionService(rt, o)
			if e != nil {
				t.Fatal(e)
			}
			defer func() { s.Close(); rt.Close() }()
			target := table
			if table == "state_write_failure" {
				target = "interaction_workspaces"
			}
			_, e = rt.DB.SQL.Exec("CREATE TRIGGER fail_admission BEFORE INSERT ON " + target + " BEGIN SELECT RAISE(FAIL,'owned failure'); END")
			if table == "state_write_failure" {
				if _, err := rt.DB.SQL.Exec("CREATE TRIGGER fail_state BEFORE UPDATE ON interaction_mcp_admissions BEGIN SELECT RAISE(FAIL,'owned state failure'); END"); err != nil {
					t.Fatal(err)
				}
			}
			if e != nil {
				t.Fatal(e)
			}
			body := `{"request_id":"retry-start","mode":"default","mcp":true}`
			first := apiRequest(s, "POST", "/api/v1/sessions", body)
			if first.Code < 400 {
				t.Fatal(first.Code, first.Body.String())
			}
			_, e = rt.DB.SQL.Exec("DROP TRIGGER fail_admission")
			if e != nil {
				t.Fatal(e)
			}
			var stableID string
			for restart := 0; restart < 2; restart++ {
				if restart == 1 {
					if table == "state_write_failure" {
						if _, err := rt.DB.SQL.Exec("DROP TRIGGER fail_state"); err != nil {
							t.Fatal(err)
						}
					}
					s.Close()
					rt.Close()
					rt, e = OpenInteractionRuntime(ctx, parent)
					if e != nil {
						t.Fatal(e)
					}
					s, e = NewInteractionService(rt, o)
					if e != nil {
						t.Fatal(e)
					}
				}
				for _, input := range []string{body, `{"request_id":"retry-start","mode":"plan","mcp":true}`, `{"request_id":"retry-start","mode":"chat_only"}`} {
					reply := apiRequest(s, "POST", "/api/v1/sessions", input)
					if reply.Code != 409 {
						t.Errorf("retry status %d %s", reply.Code, reply.Body.String())
					}
					data, _ := os.ReadFile(marker + ".launch")
					if strings.Count(string(data), "\n") != 1 {
						t.Fatalf("admission replay launched again: %q", data)
					}
				}
				inspect := apiRequest(s, "GET", "/api/v1/admissions/retry-start", "")
				if inspect.Code != 200 || !strings.Contains(inspect.Body.String(), `"session_id"`) || !strings.Contains(inspect.Body.String(), "new request") {
					t.Errorf("missing readonly recovery: %d %s", inspect.Code, inspect.Body.String())
				}
				var admission struct {
					SessionID string `json:"session_id"`
					State     string `json:"state"`
				}
				if err := json.Unmarshal(inspect.Body.Bytes(), &admission); err != nil {
					t.Fatal(err)
				}
				if stableID == "" {
					stableID = admission.SessionID
				}
				if admission.SessionID != stableID {
					t.Fatal("admission identity changed")
				}
				if restart == 1 && admission.State != "revoked" {
					t.Fatal("interrupted admission not revoked", admission.State)
				}
				if restart == 0 && table == "state_write_failure" && admission.State != "starting" {
					t.Fatal("not testing unfinished durable claim", admission.State)
				}
			}
			next := apiRequest(s, "POST", "/api/v1/sessions", `{"request_id":"fresh-explicit","mode":"default","mcp":true}`)
			if next.Code != 201 {
				t.Fatal(next.Code, next.Body.String())
			}
			data, _ := os.ReadFile(marker + ".launch")
			if strings.Count(string(data), "\n") != 2 {
				t.Fatal(string(data))
			}
		})
	}
}
func TestMCPAdmissionPausedDoesNotBlockControlsAndShutdown(t *testing.T) {
	ctx := context.Background()
	rt, e := OpenInteractionRuntime(ctx, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer rt.Close()
	marker := filepath.Join(t.TempDir(), "effect")
	o := mcpAdmissionOptions(t, marker, true)
	selection := testSelection()
	selection.ToolsSupported = true
	selection.Provider = interactionStreamFunc(func(ctx context.Context, r model.TurnRequest, emit func(model.ModelEvent) error) (model.TurnResult, error) {
		for _, tool := range r.Tools {
			if strings.HasPrefix(tool.Function.Name, "mcp_") {
				return model.TurnResult{ToolCalls: []model.ToolCall{{ID: "review", Name: tool.Function.Name, Arguments: `{"value":"owned"}`}}}, nil
			}
		}
		return model.TurnResult{Content: "done"}, nil
	})
	o.Resolve = func(context.Context, string) (ProviderSelection, error) { return selection, nil }
	s, e := NewInteractionService(rt, o)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if err := os.WriteFile(marker+".release", []byte("go"), 0600); err != nil {
		t.Fatal(err)
	}
	ready := apiRequest(s, "POST", "/api/v1/sessions", `{"request_id":"ready","mode":"default","mcp":true}`)
	if ready.Code != 201 {
		t.Fatal(ready.Body.String())
	}
	var view SessionView
	json.Unmarshal(ready.Body.Bytes(), &view)
	postReviewMessage(t, s, view.ID, "ready-call", "mcp")
	snapshot := awaitState(t, s, view.ID, "waiting_approval")
	reviewPath := "/api/v1/approvals/" + snapshot.Runs[0].Approval.ID + "/review"
	os.Remove(marker + ".release")
	os.Remove(marker + ".paused")
	plain := namedSession(t, s, "plain", "default")
	run, e := s.store.CreateRun(ctx, plain, "pending", interaction.Message{Content: "cancel"})
	if e != nil {
		t.Fatal(e)
	}
	body := `{"request_id":"paused","mode":"default","mcp":true}`
	done := make(chan int, 1)
	go func() { done <- apiRequest(s, "POST", "/api/v1/sessions", body).Code }()
	awaitMCPMarker(t, marker+".paused")
	for _, check := range []struct {
		method, path, body string
		code               int
	}{{"POST", "/api/v1/runs/" + run.ID + "/cancel", `{"request_id":"cancel-other"}`, 202}, {"GET", "/api/v1/status", "", 200}, {"GET", reviewPath, "", 200}, {"GET", "/api/v1/sessions/" + plain + "/snapshot", "", 200}, {"POST", "/api/v1/sessions", body, 409}} {
		got := make(chan int, 1)
		go func() { got <- apiRequest(s, check.method, check.path, check.body).Code }()
		select {
		case code := <-got:
			if code != check.code {
				t.Errorf("%s status %d", check.path, code)
			}
		case <-time.After(500 * time.Millisecond):
			t.Error("unrelated control blocked", check.path)
		}
	}
	closed := make(chan struct{})
	go func() { s.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Error("shutdown blocked on paused admission")
	}
	select {
	case code := <-done:
		if code < 400 {
			t.Error("admitted during shutdown", code)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("admission leaked")
	}
	data, _ := os.ReadFile(marker + ".launch")
	if strings.Count(string(data), "\n") != 2 {
		t.Fatal("duplicate launch", string(data))
	}
}

func TestMCPAdmissionMustPersistClaimBeforeLaunch(t *testing.T) {
	ctx := context.Background()
	rt, e := OpenInteractionRuntime(ctx, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer rt.Close()
	marker := filepath.Join(t.TempDir(), "effect")
	s, e := NewInteractionService(rt, mcpAdmissionOptions(t, marker, false))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if _, e = rt.DB.SQL.Exec("CREATE TRIGGER fail_claim BEFORE INSERT ON interaction_mcp_admissions BEGIN SELECT RAISE(FAIL,'owned failure'); END"); e != nil {
		t.Fatal(e)
	}
	body := `{"request_id":"claim","mode":"default","mcp":true}`
	reply := apiRequest(s, "POST", "/api/v1/sessions", body)
	if reply.Code != 500 {
		t.Fatal(reply.Code, reply.Body.String())
	}
	if _, e = os.Stat(marker + ".launch"); !os.IsNotExist(e) {
		t.Fatal("launched before durable claim", e)
	}
	if _, e = rt.DB.SQL.Exec("DROP TRIGGER fail_claim"); e != nil {
		t.Fatal(e)
	}
	first := apiRequest(s, "POST", "/api/v1/sessions", body)
	if first.Code != 201 {
		t.Fatal(first.Code, first.Body.String())
	}
	second := apiRequest(s, "POST", "/api/v1/sessions", body)
	if second.Code != 201 || second.Body.String() != first.Body.String() {
		t.Fatal("lost original outcome", second.Body.String())
	}
	data, _ := os.ReadFile(marker + ".launch")
	if strings.Count(string(data), "\n") != 1 {
		t.Fatal("completed admission replayed", string(data))
	}
}

func TestMCPAdmissionConcurrentServiceCloseJoinsLifetime(t *testing.T) {
	ctx := context.Background()
	rt, e := OpenInteractionRuntime(ctx, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer rt.Close()
	s, e := NewInteractionService(rt, mcpAdmissionOptions(t, filepath.Join(t.TempDir(), "effect"), false))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	// Hold one admission lifetime at the same wait group boundary used by the
	// real paused-peer tests, so both Close callers must join it deterministically.
	s.wg.Add(1)
	closed := make(chan struct{}, 2)
	go func() { s.Close(); closed <- struct{}{} }()
	<-s.ctx.Done()
	go func() { s.Close(); closed <- struct{}{} }()
	early := false
	select {
	case <-closed:
		early = true
	case <-time.After(50 * time.Millisecond):
	}
	s.wg.Done()
	count := 2
	if early {
		t.Error("concurrent Close returned before admission cleanup")
		count = 1
	}
	for i := 0; i < count; i++ {
		select {
		case <-closed:
		case <-time.After(time.Second):
			t.Fatal("Close did not finish")
		}
	}
}

func TestMCPAdmissionInspectionEscapesOpaqueRequestID(t *testing.T) {
	ctx := context.Background()
	rt, e := OpenInteractionRuntime(ctx, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer rt.Close()
	marker := filepath.Join(t.TempDir(), "effect")
	s, e := NewInteractionService(rt, mcpAdmissionOptions(t, marker, false))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	reply := apiRequest(s, "POST", "/api/v1/sessions", `{"request_id":"/owned/id/","mode":"default","mcp":true}`)
	if reply.Code != 201 {
		t.Fatal(reply.Body.String())
	}
	inspected := apiRequest(s, "GET", "/api/v1/admissions/%2Fowned%2Fid%2F", "")
	if inspected.Code != 200 || !strings.Contains(inspected.Body.String(), `"request_id":"/owned/id/"`) {
		t.Fatal("opaque request inspection unavailable", inspected.Code, inspected.Body.String())
	}
	data, _ := os.ReadFile(marker + ".launch")
	if strings.Count(string(data), "\n") != 1 {
		t.Fatal("inspection launched", string(data))
	}
}
