package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"agent/internal/config"
	"agent/internal/model"
)

func TestBuildConfiguredChatProvider(t *testing.T) {
	cfg := config.Config{
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderConfig{
				"local-ollama": {
					Type:       "openai_compatible",
					BaseURL:    "http://127.0.0.1:11434/v1",
					TrustLevel: "local_private",
				},
			},
			Registry: []config.ModelConfig{{
				ID:         "local-planner",
				Provider:   "local-ollama",
				Model:      "qwen3.8:27b-mlx",
				TrustLevel: "local_private",
				Capabilities: []string{
					string(model.CapabilityChat),
				},
			}},
		},
		Agent: config.AgentConfig{Leader: config.RoleModelConfig{ModelID: "local-planner"}},
	}

	provider, metadata, err := buildConfiguredChatProvider(cfg)
	if err != nil {
		t.Fatalf("buildConfiguredChatProvider() error = %v", err)
	}
	client, ok := provider.(*model.OpenAICompatibleClient)
	if !ok {
		t.Fatalf("provider type = %T, want *model.OpenAICompatibleClient", provider)
	}
	if client.BaseURL != "http://127.0.0.1:11434/v1" || metadata.Model != "qwen3.8:27b-mlx" {
		t.Fatalf("provider = %+v metadata = %+v", client, metadata)
	}
}

func TestBuildWiresConfiguredPublicEscalatorWithPrivacyGateway(t *testing.T) {
	t.Setenv("PACHAT_TEST_PRIVACY_SECRET", "test-secret")
	publicServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("public path = %s, want /chat/completions", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": "public answer"}}},
			"usage":   map[string]any{"prompt_tokens": 5, "completion_tokens": 7},
		})
	}))
	defer publicServer.Close()
	cfg := configForBuilderPlannerTest(t)
	disabled := false
	local := cfg.Models.Providers["local-ollama"]
	local.Enabled = &disabled
	cfg.Models.Providers["local-ollama"] = local
	cfg.Privacy.HMACSecretEnv = "PACHAT_TEST_PRIVACY_SECRET"
	cfg.Privacy.FailClosedForPublicModels = true
	cfg.Models.Providers["public"] = config.ProviderConfig{Enabled: boolPtr(true), Type: "openai_compatible", BaseURL: publicServer.URL, TrustLevel: string(model.TrustPublicRemote), RequirePrivacyGateway: true}
	cfg.Models.Registry = append(cfg.Models.Registry, config.ModelConfig{ID: "public-chat", Provider: "public", Model: "public-model", TrustLevel: string(model.TrustPublicRemote), Capabilities: []string{string(model.CapabilityChat)}})

	rt, err := Build(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	if rt.Escalator == nil {
		t.Fatal("Build() Escalator is nil")
	}
	answer, usage, err := rt.Escalator.Escalate(context.Background(), "hello alice@example.com", nil)
	if err != nil {
		t.Fatalf("Escalate() error = %v", err)
	}
	if answer != "public answer" || usage.InputTokens != 5 || usage.OutputTokens != 7 {
		t.Fatalf("answer=%q usage=%#v", answer, usage)
	}
}

func TestBuildPublicEscalatorRequiresPrivacyGateway(t *testing.T) {
	cfg := configForBuilderPlannerTest(t)
	disabled := false
	local := cfg.Models.Providers["local-ollama"]
	local.Enabled = &disabled
	cfg.Models.Providers["local-ollama"] = local
	cfg.Privacy.HMACSecretEnv = "PACHAT_MISSING_SECRET"
	cfg.Privacy.FailClosedForPublicModels = true
	cfg.Models.Providers["public"] = config.ProviderConfig{Enabled: boolPtr(true), Type: "openai_compatible", BaseURL: "http://127.0.0.1:1/v1", TrustLevel: string(model.TrustPublicRemote), RequirePrivacyGateway: true}
	cfg.Models.Registry = append(cfg.Models.Registry, config.ModelConfig{ID: "public-chat", Provider: "public", Model: "public-model", TrustLevel: string(model.TrustPublicRemote), Capabilities: []string{string(model.CapabilityChat)}})

	_, err := Build(context.Background(), cfg)
	if !errors.Is(err, model.ErrPrivacyGatewayRequired) {
		t.Fatalf("Build() error = %v, want ErrPrivacyGatewayRequired", err)
	}
}

func TestPublicEscalatorPrivacyGatewayBlocksSecretsBeforeProviderCall(t *testing.T) {
	t.Setenv("PACHAT_TEST_PRIVACY_SECRET", "test-secret")
	calls := 0
	publicServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"content": "should not happen"}}}})
	}))
	defer publicServer.Close()
	cfg := configForBuilderPlannerTest(t)
	disabled := false
	local := cfg.Models.Providers["local-ollama"]
	local.Enabled = &disabled
	cfg.Models.Providers["local-ollama"] = local
	cfg.Privacy.HMACSecretEnv = "PACHAT_TEST_PRIVACY_SECRET"
	cfg.Privacy.FailClosedForPublicModels = true
	cfg.Models.Providers["public"] = config.ProviderConfig{Enabled: boolPtr(true), Type: "openai_compatible", BaseURL: publicServer.URL, TrustLevel: string(model.TrustPublicRemote), RequirePrivacyGateway: true}
	cfg.Models.Registry = append(cfg.Models.Registry, config.ModelConfig{ID: "public-chat", Provider: "public", Model: "public-model", TrustLevel: string(model.TrustPublicRemote), Capabilities: []string{string(model.CapabilityChat)}})
	rt, err := Build(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })

	_, _, err = rt.Escalator.Escalate(context.Background(), "-----BEGIN PRIVATE KEY-----\nabc\n-----END PRIVATE KEY-----", nil)
	if !errors.Is(err, model.ErrPrivacyBlocked) {
		t.Fatalf("Escalate() error = %v, want ErrPrivacyBlocked", err)
	}
	if calls != 0 {
		t.Fatalf("public provider calls = %d, want 0", calls)
	}
}

func TestBuildWiresConfiguredProviderIntoPlanner(t *testing.T) {
	cfg := configForBuilderPlannerTest(t)

	rt, err := Build(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	if rt.ChatProvider == nil {
		t.Fatal("Build() ChatProvider is nil")
	}
	if rt.Planner == nil {
		t.Fatal("Build() Planner is nil")
	}
	planner, ok := rt.Planner.(BoundedModelPlanner)
	if !ok {
		t.Fatalf("Planner type = %T, want BoundedModelPlanner", rt.Planner)
	}
	if planner.Provider != rt.ChatProvider || planner.Model.ID != rt.ChatModel.ID {
		t.Fatalf("Planner provider/model not wired from Runtime chat provider")
	}
	if planner.MaxNodes != 4 || planner.MaxOutputTokens != 1024 || planner.Temperature != 0.2 {
		t.Fatalf("Planner config = %+v, want configured bounds", planner)
	}
}

func TestBuildWiresConfiguredSubAgentProviders(t *testing.T) {
	cfg := configForBuilderPlannerTest(t)
	cfg.Models.Providers["reasoning-provider"] = config.ProviderConfig{Type: "openai_compatible", BaseURL: "http://127.0.0.1:11435/v1", TrustLevel: "local_private"}
	cfg.Models.Registry = append(cfg.Models.Registry, config.ModelConfig{
		ID:           "reasoning-model",
		Provider:     "reasoning-provider",
		Model:        "reasoning-subagent",
		TrustLevel:   "local_private",
		Capabilities: []string{string(model.CapabilityChat)},
	})
	cfg.Agent.SubAgents = map[string]config.RoleModelConfig{
		"reasoning": {ModelID: "reasoning-model", Temperature: 0.1, MaxOutputTokens: 512},
	}

	rt, err := Build(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	agent, ok := rt.SubAgents["reasoning"]
	if !ok {
		t.Fatal("Build() did not wire reasoning subagent")
	}
	if agent.Model.ID != "reasoning-model" {
		t.Fatalf("reasoning model id = %q, want reasoning-model", agent.Model.ID)
	}
	client, ok := agent.Provider.(*model.OpenAICompatibleClient)
	if !ok {
		t.Fatalf("reasoning provider type = %T, want *model.OpenAICompatibleClient", agent.Provider)
	}
	if client.BaseURL != "http://127.0.0.1:11435/v1" {
		t.Fatalf("reasoning provider base url = %q", client.BaseURL)
	}
}

func TestBuildSubAgentPublicProviderRequiresPrivacyGateway(t *testing.T) {
	cfg := configForBuilderPlannerTest(t)
	cfg.Privacy.HMACSecretEnv = "PACHAT_MISSING_SUBAGENT_SECRET"
	cfg.Privacy.FailClosedForPublicModels = true
	cfg.Models.Providers["public-subagent"] = config.ProviderConfig{Enabled: boolPtr(true), Type: "openai_compatible", BaseURL: "http://127.0.0.1:1/v1", TrustLevel: string(model.TrustPublicRemote), RequirePrivacyGateway: true}
	cfg.Models.Registry = append(cfg.Models.Registry, config.ModelConfig{ID: "public-reasoning", Provider: "public-subagent", Model: "public-reasoning", TrustLevel: string(model.TrustPublicRemote), Capabilities: []string{string(model.CapabilityChat)}})
	cfg.Agent.SubAgents = map[string]config.RoleModelConfig{"reasoning": {ModelID: "public-reasoning"}}

	_, err := Build(context.Background(), cfg)
	if !errors.Is(err, model.ErrPrivacyGatewayRequired) {
		t.Fatalf("Build() error = %v, want ErrPrivacyGatewayRequired", err)
	}
}

func TestBuildLeavesPlannerNilWhenProviderUnavailable(t *testing.T) {
	cfg := configForBuilderPlannerTest(t)
	disabled := false
	provider := cfg.Models.Providers["local-ollama"]
	provider.Enabled = &disabled
	cfg.Models.Providers["local-ollama"] = provider

	rt, err := Build(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	if rt.ChatProvider != nil {
		t.Fatal("Build() ChatProvider is non-nil for disabled provider")
	}
	if rt.Planner != nil {
		t.Fatal("Build() Planner is non-nil for disabled provider")
	}
}

func TestBuildLeavesPlannerNilWithoutJSONSchemaCapability(t *testing.T) {
	cfg := configForBuilderPlannerTest(t)
	cfg.Models.Registry[0].Capabilities = []string{string(model.CapabilityChat)}

	rt, err := Build(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	if rt.ChatProvider == nil {
		t.Fatal("Build() ChatProvider is nil")
	}
	if rt.Planner != nil {
		t.Fatal("Build() Planner is non-nil without json_schema capability")
	}
}

func configForBuilderPlannerTest(t *testing.T) config.Config {
	t.Helper()
	return config.Config{
		Storage: config.StorageConfig{
			Driver: "sqlite",
			SQLite: config.SQLiteConfig{Path: filepath.Join(t.TempDir(), "agent.db")},
		},
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderConfig{
				"local-ollama": {
					Type:       "openai_compatible",
					BaseURL:    "http://127.0.0.1:11434/v1",
					TrustLevel: "local_private",
				},
			},
			Registry: []config.ModelConfig{{
				ID:         "local-planner",
				Provider:   "local-ollama",
				Model:      "qwen3.8:27b-mlx",
				TrustLevel: "local_private",
				Capabilities: []string{
					string(model.CapabilityChat),
					string(model.CapabilityJSONSchema),
				},
			}},
		},
		Agent: config.AgentConfig{
			Leader:  config.RoleModelConfig{ModelID: "local-planner", Temperature: 0.2, MaxOutputTokens: 1024},
			Planner: config.PlannerConfig{MaxNodes: 4},
		},
	}
}

func boolPtr(value bool) *bool {
	return &value
}
