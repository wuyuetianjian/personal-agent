package capability

import (
	"context"
	"errors"
	"testing"
)

func TestRegistryAndResolverApplyPolicy(t *testing.T) {
	r, err := NewRegistry(
		Capability{ID: "local", Kind: KindRetrieval, Enabled: true, Health: HealthHealthy, TrustLevel: "local_private", PrivacyClasses: []string{"private"}, CostClass: "low", SideEffectLevel: "read_only"},
		Capability{ID: "remote", Kind: KindRetrieval, Enabled: true, Health: HealthHealthy, TrustLevel: "public_remote", PrivacyClasses: []string{"private"}, CostClass: "high", SideEffectLevel: "read_only"},
		Capability{ID: "down", Kind: KindRetrieval, Enabled: true, Health: HealthUnavailable, TrustLevel: "local_private", PrivacyClasses: []string{"private"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	got, err := (DeterministicResolver{Registry: r}).Resolve(context.Background(), ResolveRequest{RequiredKind: KindRetrieval, PrivacyClass: "private", MaxTrustLevel: "local_private"})
	if err != nil || len(got) != 1 || got[0].Capability.ID != "local" {
		t.Fatalf("Resolve() = %#v, %v", got, err)
	}
}

func TestRegistryRejectsDuplicate(t *testing.T) {
	r, err := NewRegistry(Capability{ID: "same"})
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(r.Register(Capability{ID: "same"}), ErrDuplicateID) {
		t.Fatal("duplicate capability was accepted")
	}
}
