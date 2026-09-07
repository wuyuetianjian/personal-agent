package runtime

import (
	"context"
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
