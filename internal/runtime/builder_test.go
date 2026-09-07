package runtime

import (
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
