package skill

import (
	"errors"
	"testing"

	"agent/internal/capability"
)

func TestCandidateGeneratorCreatesDraftOnly(t *testing.T) {
	m := CandidateFromIntent("local research", []string{"memory.search"})
	if m.Status != StatusDraft || m.ID == "" || m.Requires.Capabilities[0] != "memory.search" {
		t.Fatalf("candidate=%#v", m)
	}
}

func TestActivateWithEvalGate(t *testing.T) {
	caps, _ := capability.NewRegistry(capability.Capability{ID: "memory.search", Enabled: true})
	registry := NewRegistry()
	m := CandidateFromIntent("local research", []string{"memory.search"})
	if err := registry.Add(m, caps); err != nil {
		t.Fatal(err)
	}
	if err := ActivateWithEval(registry, m.ID, m.Version, EvalResult{Passed: false}); !errors.Is(err, ErrEvalGateFailed) {
		t.Fatalf("ActivateWithEval() error=%v", err)
	}
	if err := ActivateWithEval(registry, m.ID, m.Version, EvalResult{Passed: true, Score: 0.9}); err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.Get(m.ID); !ok {
		t.Fatal("skill was not activated after passing eval")
	}
}
