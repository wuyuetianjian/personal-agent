package cli

import (
	"agent/internal/api"
	"agent/internal/model"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestExplicitProviderProbeCLI(t *testing.T) {
	var calls atomic.Int32
	var compliant atomic.Bool
	compliant.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var req struct {
			Messages []model.ChatMessage
			Tools    []model.ToolDefinition
		}
		json.NewDecoder(r.Body).Decode(&req)
		if len(req.Messages) != 1 || len(req.Tools) != 1 {
			t.Error("not synthetic only")
			return
		}
		nonce := strings.TrimPrefix(req.Messages[0].Content, "Call synthetic_probe exactly once with nonce: ")
		w.Header().Set("Content-Type", "text/event-stream")
		if !compliant.Load() {
			fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"supports tools\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			return
		}
		raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "c", "function": map[string]any{"name": "synthetic_probe", "arguments": fmt.Sprintf(`{"nonce":%q}`, nonce)}}}}, "finish_reason": "tool_calls"}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", raw)
	}))
	defer server.Close()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	cfg := `app:
  name: test
storage:
  driver: sqlite
  sqlite:
    path: unused-legacy.db
agent:
  leader:
    model_id: fixture
models:
  default_provider: local
  providers:
    local:
      type: openai_compatible
      base_url_env: PROBE_FIXTURE_URL
      api_key_env: PROBE_FIXTURE_KEY
      trust_level: local_private
  registry:
    - id: fixture
      provider: local
      model: fixture-model
      trust_level: local_private
      context_window: 32000
      capabilities: [chat, tool_calling]
`
	os.WriteFile(configPath, []byte(cfg), 0600)
	t.Setenv("PROBE_FIXTURE_URL", server.URL)
	t.Setenv("PROBE_FIXTURE_KEY", "private-fixture-key")
	parent := t.TempDir()
	ctx := context.Background()
	var out bytes.Buffer
	args := []string{"probe-agent-provider", "--config", configPath, "--model", "fixture", "--runtime-parent", parent}
	initialRuntime, err := api.OpenInteractionRuntime(ctx, parent)
	if err != nil {
		t.Fatal(err)
	}
	unknown, err := agentResolver(configPath, initialRuntime)(ctx, "fixture")
	if err != nil || unknown.ToolsSupported || calls.Load() != 0 {
		t.Fatal("config flag asserted support or resolution probed", err)
	}
	initialRuntime.Close()

	if err := Run(ctx, args, &out); err == nil || calls.Load() != 0 {
		t.Fatalf("missing explicit consent gate: %v %d", err, calls.Load())
	}
	args = append(args, "--send-synthetic")
	if err := Run(ctx, args, &out); err != nil {
		t.Fatal(err, out.String())
	}
	if calls.Load() != 1 || !strings.Contains(out.String(), "synthetic_tool_call_verified") {
		t.Fatalf("not one explicit probe: %d %s", calls.Load(), out.String())
	}
	rt, err := api.OpenInteractionRuntime(ctx, parent)
	if err != nil {
		t.Fatal(err)
	}
	resolve := agentResolver(configPath, rt)
	p, err := resolve(ctx, "fixture")
	if err != nil || !p.ToolsSupported {
		t.Fatalf("evidence not consumed: %v %+v", err, p)
	}
	t.Setenv("PROBE_FIXTURE_KEY", "rotated-private-fixture-key")
	q, err := resolve(ctx, "fixture")
	if err != nil || q.Binding() != p.Binding() {
		t.Fatal("key rotation blocked")
	}
	if calls.Load() != 1 {
		t.Fatal("resolver auto-probed")
	}
	svc, err := api.NewInteractionService(rt, api.InteractionOptions{Token: "fixture", Host: "127.0.0.1:1", DefaultModel: "fixture", Resolve: resolve})
	if err != nil {
		t.Fatal(err)
	}
	svc.Close()
	if calls.Load() != 1 {
		t.Fatal("startup auto-probed")
	}
	rt.Close()
	compliant.Store(false)
	out.Reset()
	if err = Run(ctx, args, &out); err == nil {
		t.Fatal("failed reprobe reported success")
	}
	rt, err = api.OpenInteractionRuntime(ctx, parent)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	p, err = agentResolver(configPath, rt)(ctx, "fixture")
	if err != nil || p.ToolsSupported {
		t.Fatal("failed reprobe retained verification")
	}
	var raw string
	rt.DB.SQL.QueryRow(`SELECT evidence FROM interaction_provider_evidence`).Scan(&raw)
	if strings.Contains(raw, "private-fixture") || strings.Contains(raw, server.URL) || strings.Contains(out.String(), "private-fixture") {
		t.Fatal("secret/endpoint leaked")
	}
}
