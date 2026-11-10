//go:build linux

package api

import (
	"agent/internal/interaction"
	"agent/internal/model"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecAPIReviewRequiresAuthAndExactOptIn(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	marker := filepath.Join(root, "marker")
	selection := testSelection()
	selection.ToolsSupported = true
	selection.Provider = interactionStreamFunc(func(ctx context.Context, r model.TurnRequest, emit func(model.ModelEvent) error) (model.TurnResult, error) {
		if r.Messages[len(r.Messages)-1].Role == "tool" {
			return model.TurnResult{Content: "done"}, nil
		}
		scope := ""
		for _, m := range r.Messages {
			if strings.HasPrefix(m.Content, "Workspace scope: ") {
				scope = strings.Split(strings.TrimPrefix(m.Content, "Workspace scope: "), ".")[0]
			}
		}
		raw, _ := json.Marshal(interaction.ExecArguments{Scope: scope, Program: "/bin/sh", Argv: []string{"-c", `printf 'safe 你好' > "$1"; printf 'visible'`, "fixture", marker}, Cwd: root, Environment: map[string]string{"CUSTOM": "explicit value"}, TimeoutMillis: 1000})
		return model.TurnResult{ToolCalls: []model.ToolCall{{ID: "exec-call", Name: "exec", Arguments: string(raw)}}}, nil
	})
	rt, err := OpenInteractionRuntime(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	// JSON demonstrates old service options ignoring opt-in before implementation.
	var options InteractionOptions
	if err = json.Unmarshal([]byte(`{"NonSandboxExec":true}`), &options); err != nil {
		t.Fatal(err)
	}
	options.Token = "secret-token"
	options.Host = "127.0.0.1:7319"
	options.Workspace = root
	options.DefaultModel = "test"
	options.Resolve = func(context.Context, string) (ProviderSelection, error) { return selection, nil }
	s, err := NewInteractionService(rt, options)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	session := namedSession(t, s, "exec", "acceptEdits")
	postReviewMessage(t, s, session, "run", "exec")
	snap := awaitState(t, s, session, "waiting_approval")
	approval := snap.Runs[0].Approval
	if _, err = os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("ran before approval", err)
	}
	response := apiRequest(s, "GET", "/api/v1/approvals/"+approval.ID+"/review", "")
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	var review map[string]json.RawMessage
	json.Unmarshal(response.Body.Bytes(), &review)
	if string(review["kind"]) != `"exec"` || !strings.Contains(string(review["exec"]), "NON-SANDBOXED") || !strings.Contains(string(review["exec"]), "explicit value") || !strings.Contains(string(review["exec"]), "printf 'safe 你好'") {
		t.Fatal(response.Body.String())
	}
	unauth := httptest.NewRequest("GET", "http://127.0.0.1:7319/api/v1/approvals/"+approval.ID+"/review", nil)
	unauth.RemoteAddr = "127.0.0.1:1000"
	out := httptest.NewRecorder()
	s.ServeHTTP(out, unauth)
	if out.Code != 401 {
		t.Fatal(out.Code)
	}
	result := apiRequest(s, "POST", "/api/v1/approvals/"+approval.ID+"/decision", `{"request_id":"approve","decision":"approved","digest":"`+approval.Digest+`"}`)
	if result.Code != 202 {
		t.Fatal(result.Code, result.Body.String())
	}
	awaitState(t, s, session, "completed")
	raw, err := os.ReadFile(marker)
	if err != nil || string(raw) != "safe 你好" {
		t.Fatal(string(raw), err)
	}
	bad := apiRequest(s, "POST", "/api/v1/sessions", `{"request_id":"bad","mode":"default","non_sandbox_exec":true}`)
	if bad.Code != 400 {
		t.Fatal("client changed opt-in", bad.Code)
	}
}
