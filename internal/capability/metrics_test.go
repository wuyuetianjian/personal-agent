package capability

import (
	"context"
	"testing"
)

func TestResolverUsesRuntimeMetrics(t *testing.T) {
	registry, err := NewRegistry(
		Capability{ID: "a", Kind: KindTool, Enabled: true, Health: HealthHealthy, TrustLevel: "local_private", CostClass: "low", SideEffectLevel: "read_only", PrivacyClasses: []string{"local_private"}},
		Capability{ID: "b", Kind: KindTool, Enabled: true, Health: HealthHealthy, TrustLevel: "local_private", CostClass: "low", SideEffectLevel: "read_only", PrivacyClasses: []string{"local_private"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := (DeterministicResolver{
		Registry: registry,
		Metrics: map[string]RuntimeMetric{
			"b": {Successes: 10},
			"a": {Failures: 10},
		},
	}).Resolve(context.Background(), ResolveRequest{RequiredKind: KindTool, PrivacyClass: "local_private"})
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 || candidates[0].Capability.ID != "b" {
		t.Fatalf("candidates=%#v", candidates)
	}
}
