package skill

import (
	"agent/internal/capability"
	"testing"
)

func TestValidateAndMatchActiveSkill(t *testing.T) {
	caps, _ := capability.NewRegistry(capability.Capability{ID: "memory.search", Enabled: true})
	r := NewRegistry()
	m := Manifest{ID: "local.lookup", Version: "1.0.0", Name: "Local lookup", Description: "lookup local history", Status: StatusActive, Requires: Requirements{Capabilities: []string{"memory.search"}}, Privacy: PrivacyPolicy{MaxExternalTrust: "local_private"}, Workflow: Workflow{Nodes: []Node{{ID: "history", Capability: "memory.search"}}}}
	if err := r.Add(m, caps); err != nil {
		t.Fatal(err)
	}
	got := r.Match(MatchRequest{Intent: "history", RequiredCapabilities: []string{"memory.search"}})
	if len(got) != 1 || got[0].Manifest.ID != m.ID {
		t.Fatalf("matches = %#v", got)
	}
}

func TestValidateRejectsUnknownCapability(t *testing.T) {
	caps, err := capability.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	err = Validate(Manifest{ID: "bad", Version: "1.0.0", Name: "bad", Workflow: Workflow{Nodes: []Node{{ID: "x", Capability: "missing"}}}}, caps)
	if err == nil {
		t.Fatal("unknown capability accepted")
	}
}
