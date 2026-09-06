package capability

import (
	"context"
	"fmt"
	"sort"
)

type DeterministicResolver struct {
	Registry *Registry
}

func (r DeterministicResolver) Resolve(ctx context.Context, req ResolveRequest) ([]Candidate, error) {
	if r.Registry == nil {
		return nil, fmt.Errorf("capability registry is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	allowed := makeSet(req.AllowedIDs)
	denied := makeSet(req.DeniedIDs)
	var out []Candidate
	for _, item := range r.Registry.List(req.RequiredKind) {
		if !item.Enabled || item.Health == HealthUnavailable || item.Health == HealthUnknown {
			continue
		}
		if len(allowed) > 0 && !allowed[item.ID] || denied[item.ID] {
			continue
		}
		if req.MaxTrustLevel != "" && rank(item.TrustLevel) > rank(req.MaxTrustLevel) {
			continue
		}
		if req.MaxCostClass != "" && rank(item.CostClass) > rank(req.MaxCostClass) {
			continue
		}
		if req.MaxSideEffect != "" && rank(item.SideEffectLevel) > rank(req.MaxSideEffect) {
			continue
		}
		if req.PrivacyClass != "" && !contains(item.PrivacyClasses, req.PrivacyClass) {
			continue
		}
		if !containsAll(item.Tags, req.RequiredTags) {
			continue
		}
		score := 1.0
		if item.Health == HealthHealthy {
			score += 3
		}
		if item.TrustLevel == "local_private" {
			score += 2
		}
		if item.CostClass == "low" {
			score += 1
		}
		out = append(out, Candidate{Capability: item, Score: score, Reason: "enabled, healthy, and policy compatible"})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score == out[j].Score {
			return out[i].Capability.ID < out[j].Capability.ID
		}
		return out[i].Score > out[j].Score
	})
	return out, nil
}

func makeSet(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		out[value] = true
	}
	return out
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func containsAll(values, wanted []string) bool {
	for _, value := range wanted {
		if !contains(values, value) {
			return false
		}
	}
	return true
}

func rank(value string) int {
	switch value {
	case "low", "local_private", "read_only":
		return 1
	case "medium", "trusted_remote", "write":
		return 2
	case "high", "public_remote", "destructive":
		return 3
	default:
		return 2
	}
}
