package codingagent

import (
	"context"
	"errors"
	"testing"
)

func TestRunnerRejectsSelfReportedCompletionWithoutEvidence(t *testing.T) {
	adapter := &mockAdapter{
		id: "codex",
		cfg: BackendConfig{
			ID: "codex", Enabled: true, InferenceTrust: InferenceLocalPrivate,
			PrivacyPolicy: PrivacyDenyConfidential, Capabilities: []Capability{CapabilityCoding},
			AllowDirectWrites: true,
		},
		result: Result{BackendID: "codex", Summary: "completed"},
	}
	registry := NewRegistry()
	registry.Register(adapter)
	_, err := (Runner{Registry: registry}).Run(context.Background(), Request{
		RepositoryPath:    t.TempDir(),
		AllowWrite:        true,
		AllowDirectWrites: true,
		Required:          []Capability{CapabilityCoding},
	})
	if !errors.Is(err, ErrInsufficientEvidence) {
		t.Fatalf("Run() error = %v, want %v", err, ErrInsufficientEvidence)
	}
}
