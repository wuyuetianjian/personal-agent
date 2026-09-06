package mcp

import "testing"

func TestReadOnlyToolBecomesCapability(t *testing.T) {
	r := NewRegistry()
	r.AddServer(Server{ID: "local", Enabled: true, TrustLevel: "local_private", PrivacyClasses: []string{"private"}})
	r.AddTool(Tool{ServerID: "local", Name: "metrics", ReadOnly: true, SideEffectLevel: "read_only"})
	got := r.Capabilities()
	if len(got) != 1 || got[0].ID != "mcp.local.metrics" {
		t.Fatalf("got=%#v", got)
	}
}
