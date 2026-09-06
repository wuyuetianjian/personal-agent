package codingagent

import (
	"context"
	"errors"
	"testing"
)

type mockAdapter struct {
	id     string
	cfg    BackendConfig
	health error
	result Result
	runErr error
	ran    bool
}

func (m *mockAdapter) ID() string                   { return m.id }
func (m *mockAdapter) Config() BackendConfig        { return m.cfg }
func (m *mockAdapter) Health(context.Context) error { return m.health }
func (m *mockAdapter) Run(context.Context, Request) (Result, error) {
	m.ran = true
	return m.result, m.runErr
}

func TestRegistrySelectFallsBackFromUnhealthyBackend(t *testing.T) {
	registry := NewRegistry()
	registry.Register(&mockAdapter{
		id: "codex",
		cfg: BackendConfig{
			ID: "codex", Enabled: true, InferenceTrust: InferencePublicRemote,
			PrivacyPolicy: PrivacyDenyConfidential, Capabilities: []Capability{CapabilityCoding},
		},
		health: ErrBackendUnhealthy,
	})
	registry.Register(&mockAdapter{
		id: "claude",
		cfg: BackendConfig{
			ID: "claude", Enabled: true, InferenceTrust: InferencePublicRemote,
			PrivacyPolicy: PrivacyDenyConfidential, Capabilities: []Capability{CapabilityCoding},
		},
	})

	adapter, err := registry.Select(context.Background(), Request{Required: []Capability{CapabilityCoding}}, []string{"codex", "claude"})
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if adapter.ID() != "claude" {
		t.Fatalf("selected %q, want claude", adapter.ID())
	}
}

func TestValidatePolicyDeniesConfidentialPublicInference(t *testing.T) {
	cfg := BackendConfig{
		Enabled: true, InferenceTrust: InferencePublicRemote,
		PrivacyPolicy: PrivacyDenyConfidential, Capabilities: []Capability{CapabilityCoding},
	}
	err := ValidatePolicy(cfg, Request{Privacy: RepositoryConfidential, Required: []Capability{CapabilityCoding}})
	if !errors.Is(err, ErrPrivacyDenied) {
		t.Fatalf("ValidatePolicy() error = %v, want %v", err, ErrPrivacyDenied)
	}
}

func TestResultRequiresEvidenceBeyondSummary(t *testing.T) {
	err := (Result{Summary: "done"}).ValidateEvidence()
	if !errors.Is(err, ErrInsufficientEvidence) {
		t.Fatalf("ValidateEvidence() error = %v, want %v", err, ErrInsufficientEvidence)
	}
}
