package model

import (
	"errors"
	"testing"

	"agent/internal/config"
)

func TestRegistryRejectsDuplicateModelIDs(t *testing.T) {
	cfg := testModelsConfig()
	cfg.Registry = append(cfg.Registry, cfg.Registry[0])

	_, err := NewRegistry(cfg)
	if !errors.Is(err, ErrDuplicateModelID) {
		t.Fatalf("NewRegistry() error = %v, want %v", err, ErrDuplicateModelID)
	}
}

func TestPolicySelectsByTrustAndCapability(t *testing.T) {
	registry, err := NewRegistry(testModelsConfig())
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	policy := NewPolicy(registry, map[Role]RoleSettings{
		RoleLeader: {
			ModelID:            "local-chat",
			Temperature:        0.2,
			MaxOutputTokens:    1024,
			TrustLevelRequired: TrustLocalPrivate,
		},
	})

	selection, err := policy.Select(SelectionRequest{Role: RoleLeader, Capabilities: []Capability{CapabilityChat}})
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if selection.Model.ID != "local-chat" || selection.Temperature != 0.2 || selection.MaxOutputTokens != 1024 {
		t.Fatalf("Select() = %+v, want configured leader model", selection)
	}

	_, err = policy.Select(SelectionRequest{Role: RoleLeader, Capabilities: []Capability{CapabilityEmbedding}})
	if !errors.Is(err, ErrNoMatchingModel) {
		t.Fatalf("Select(missing capability) error = %v, want %v", err, ErrNoMatchingModel)
	}
}

func TestPolicyKeepsLeaderAndSubAgentSelectionIndependent(t *testing.T) {
	registry, err := NewRegistry(testModelsConfig())
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	policy := NewPolicy(registry, map[Role]RoleSettings{
		RoleLeader: {
			ModelID:            "local-chat",
			TrustLevelRequired: TrustLocalPrivate,
		},
		RoleResearch: {
			ModelID:            "remote-chat",
			TrustLevelRequired: TrustPublicRemote,
		},
	})

	leader, err := policy.Select(SelectionRequest{Role: RoleLeader, Capabilities: []Capability{CapabilityChat}})
	if err != nil {
		t.Fatalf("Select(leader) error = %v", err)
	}
	research, err := policy.Select(SelectionRequest{Role: RoleResearch, Capabilities: []Capability{CapabilityChat}})
	if err != nil {
		t.Fatalf("Select(research) error = %v", err)
	}
	if leader.Model.ID != "local-chat" {
		t.Fatalf("leader model = %s, want local-chat", leader.Model.ID)
	}
	if research.Model.ID != "remote-chat" {
		t.Fatalf("research model = %s, want remote-chat", research.Model.ID)
	}
}

func TestPolicyRejectsTrustAboveRoleRequirement(t *testing.T) {
	registry, err := NewRegistry(testModelsConfig())
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	policy := NewPolicy(registry, map[Role]RoleSettings{
		RoleLeader: {
			ModelID:            "remote-chat",
			TrustLevelRequired: TrustLocalPrivate,
		},
	})
	_, err = policy.Select(SelectionRequest{Role: RoleLeader, Capabilities: []Capability{CapabilityChat}})
	if !errors.Is(err, ErrNoMatchingModel) {
		t.Fatalf("Select() error = %v, want %v", err, ErrNoMatchingModel)
	}
}

func testModelsConfig() config.ModelsConfig {
	return config.ModelsConfig{
		Providers: map[string]config.ProviderConfig{
			"local": {
				Type:       "openai_compatible",
				TrustLevel: "local_private",
			},
			"public": {
				Type:                  "openai_compatible",
				TrustLevel:            "public_remote",
				RequirePrivacyGateway: true,
			},
		},
		Registry: []config.ModelConfig{
			{
				ID:            "local-chat",
				Provider:      "local",
				Model:         "local-model",
				TrustLevel:    "local_private",
				ContextWindow: 8192,
				Capabilities:  []string{"chat", "json_schema"},
			},
			{
				ID:            "remote-chat",
				Provider:      "public",
				Model:         "remote-model",
				TrustLevel:    "public_remote",
				ContextWindow: 8192,
				Capabilities:  []string{"chat"},
			},
		},
	}
}
