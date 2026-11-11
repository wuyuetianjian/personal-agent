//go:build !linux

package api

import (
	"agent/internal/interaction"
	"agent/internal/model"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestUnavailableNativeServiceBoundaries(t *testing.T) {
	t.Run("status admission and explicit chat-only", func(t *testing.T) {
		ctx := context.Background()
		rt, err := OpenInteractionRuntime(ctx, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		defer rt.Close()
		root := t.TempDir()
		sentinel := filepath.Join(root, "private.txt")
		marker := filepath.Join(root, "spawned")
		if err = os.WriteFile(sentinel, []byte("private untouched content"), 0600); err != nil {
			t.Fatal(err)
		}
		var streams atomic.Int32
		selection := testSelection()
		selection.ToolsSupported = true
		selection.Provider = interactionStreamFunc(func(context.Context, model.TurnRequest, func(model.ModelEvent) error) (model.TurnResult, error) {
			streams.Add(1)
			t.Error("native refusal invoked provider Stream")
			return model.TurnResult{}, errors.New("must not stream")
		})
		s, err := NewInteractionService(rt, InteractionOptions{Token: "secret-token", Host: "127.0.0.1:7319", Workspace: root, DefaultModel: "test", Resolve: func(context.Context, string) (ProviderSelection, error) { return selection, nil }})
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		count := func(table string) int {
			var n int
			if e := rt.DB.SQL.QueryRow("SELECT count(*) FROM " + table).Scan(&n); e != nil {
				t.Fatal(e)
			}
			return n
		}
		for _, mode := range []string{"plan", "default", "acceptEdits"} {
			body := `{"request_id":"native-` + mode + `","mode":"` + mode + `"}`
			response := apiRequest(s, "POST", "/api/v1/sessions", body)
			if response.Code != 409 || !strings.Contains(response.Body.String(), `"workspace_unavailable_choose_chat_only"`) {
				t.Fatal(response.Code, response.Body.String())
			}
			lookup := func() SessionAdmissionView {
				r := apiRequest(s, "GET", "/api/v1/admissions/native-"+mode, "")
				if r.Code != 200 {
					t.Fatal(r.Code, r.Body.String())
				}
				var a SessionAdmissionView
				if e := json.Unmarshal(r.Body.Bytes(), &a); e != nil {
					t.Fatal(e)
				}
				return a
			}
			a := lookup()
			if a.State != "unresolved" || a.SessionID == "" || a.MCPOptIn == nil || *a.MCPOptIn {
				t.Fatal(a)
			}
			for _, again := range []string{body, `{"request_id":"native-` + mode + `","mode":"chat_only"}`} {
				r := apiRequest(s, "POST", "/api/v1/sessions", again)
				if r.Code != 409 {
					t.Fatal("claim replayed", r.Code, r.Body.String())
				}
				if now := lookup(); !reflect.DeepEqual(now, a) {
					t.Fatal("claim changed", a, now)
				}
			}
			for _, table := range []string{"interaction_sessions", "interaction_api_sessions", "interaction_workspaces", "interaction_runs"} {
				if n := count(table); n != 0 {
					t.Fatal("published native admission", table, n)
				}
			}
		}
		if n := count("interaction_mcp_admissions"); n != 3 {
			t.Fatal("durable refused claims must remain", n)
		}
		response := apiRequest(s, "GET", "/api/v1/status", "")
		if response.Code != 200 {
			t.Fatal(response.Code)
		}
		var status struct {
			Workspace interaction.WorkspaceCapabilities `json:"workspace"`
		}
		if err = json.Unmarshal(response.Body.Bytes(), &status); err != nil {
			t.Fatal(err)
		}
		if status.Workspace.Files.Enabled || status.Workspace.Exec.Enabled || status.Workspace != s.broker.Capabilities() {
			t.Fatal(status)
		}
		response = apiRequest(s, "POST", "/api/v1/sessions", `{"request_id":"explicit-chat","mode":"chat_only"}`)
		if response.Code != 201 {
			t.Fatal(response.Code, response.Body.String())
		}
		var session SessionView
		if err = json.Unmarshal(response.Body.Bytes(), &session); err != nil {
			t.Fatal(err)
		}
		if session.Mode != "chat_only" || session.Files || session.Tools || session.NonSandboxExec {
			t.Fatal(session)
		}
		assembled, err := interaction.NewContextAssembler(s.store).Build(ctx, session.ID, interaction.TokenBudget{Total: 32000, Output: 4000})
		if err != nil || len(assembled.Tools) != 0 {
			t.Fatal(assembled, err)
		}
		if count("interaction_runs") != 0 || count("interaction_workspaces") != 0 || count("interaction_sessions") != 1 || count("interaction_api_sessions") != 1 || streams.Load() != 0 {
			t.Fatal("chat-only gained effects")
		}
		data, e := os.ReadFile(sentinel)
		if e != nil || string(data) != "private untouched content" {
			t.Fatal(string(data), e)
		}
		if _, e = os.Stat(marker); !errors.Is(e, os.ErrNotExist) {
			t.Fatal(e)
		}
	})
	for _, kind := range []string{"exec", "mcp"} {
		t.Run(kind+" startup", func(t *testing.T) {
			ctx := context.Background()
			rt, err := OpenInteractionRuntime(ctx, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer rt.Close()
			root := t.TempDir()
			sentinel := filepath.Join(root, "must-not-inspect")
			marker := filepath.Join(root, "mcp")
			if err = os.WriteFile(sentinel, []byte("not an executable"), 0600); err != nil {
				t.Fatal(err)
			}
			options := InteractionOptions{Token: "secret-token", Host: "127.0.0.1:7319", Workspace: root, DefaultModel: "test", Resolve: func(context.Context, string) (ProviderSelection, error) {
				t.Error("startup called resolver")
				return testSelection(), nil
			}}
			if kind == "exec" {
				options.NonSandboxExec = true
			} else {
				options.MCP = interaction.MCPHostConfig{Enabled: true, Servers: []interaction.MCPServerConfig{{ID: "sentinel", Command: sentinel, Args: []string{"-test.run=^TestAPIMCPPeer$"}, Environment: []string{"PA_MCP_FIXTURE=api", "PA_MCP_MARKER=" + marker}, TimeoutMillis: 1000}}}
				if e := interaction.ValidateMCPConfig(options.MCP); e != nil {
					t.Fatal("fixture must be syntactically valid", e)
				}
			}
			s, e := NewInteractionService(rt, options)
			if s != nil || e == nil {
				if s != nil {
					s.Close()
				}
				t.Fatal("native startup accepted", e)
			}
			if kind == "mcp" && e.Error() != "MCP requires valid trusted configuration and explicit workspace" {
				t.Fatal(e)
			}
			if kind == "exec" && !strings.HasPrefix(e.Error(), "NON-SANDBOXED exec requires an explicit workspace and a supported native platform: ") {
				t.Fatal(e)
			}
			data, e := os.ReadFile(sentinel)
			if e != nil || string(data) != "not an executable" {
				t.Fatal(string(data), e)
			}
			for _, path := range []string{marker, marker + ".launch"} {
				if _, e = os.Stat(path); !errors.Is(e, os.ErrNotExist) {
					t.Fatal(path, e)
				}
			}
			for _, table := range []string{"interaction_mcp_admissions", "interaction_sessions", "interaction_runs"} {
				var n int
				if e := rt.DB.SQL.QueryRow("SELECT count(*) FROM " + table).Scan(&n); e != nil || n != 0 {
					t.Fatal(table, n, e)
				}
			}
		})
	}
}
