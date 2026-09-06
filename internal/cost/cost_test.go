package cost

import (
	"testing"

	"agent/internal/config"
	"agent/internal/model"
)

func TestPriceBookFromRegistryAndEstimate(t *testing.T) {
	registry, err := model.NewRegistry(config.ModelsConfig{
		Providers: map[string]config.ProviderConfig{
			"local": {Type: "openai_compatible", TrustLevel: string(model.TrustLocalPrivate)},
		},
		Registry: []config.ModelConfig{
			{
				ID:            "planner",
				Provider:      "local",
				Model:         "planner-model",
				TrustLevel:    string(model.TrustLocalPrivate),
				ContextWindow: 1024,
				Capabilities:  []string{string(model.CapabilityChat)},
				Pricing:       config.PricingConfig{InputPer1M: 2, OutputPer1M: 6},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	usage, err := Estimator{Prices: NewPriceBookFromRegistry(registry)}.Estimate(Usage{
		TaskID:       "task-1",
		ModelID:      "planner",
		InputTokens:  500_000,
		OutputTokens: 250_000,
	})
	if err != nil {
		t.Fatalf("Estimate() error = %v", err)
	}
	if usage.EstimatedCostUSD != 2.5 {
		t.Fatalf("EstimatedCostUSD = %v, want 2.5", usage.EstimatedCostUSD)
	}
	if usage.BillableUnits != 750_000 {
		t.Fatalf("BillableUnits = %v, want 750000", usage.BillableUnits)
	}
}

func TestEnforcerSoftAndHardLimits(t *testing.T) {
	enforcer := Enforcer{}
	soft := enforcer.Check(Budget{SoftLimitUSD: 1, HardLimitUSD: 2}, Usage{EstimatedCostUSD: 1.25})
	if soft.Status != LimitSoftHit {
		t.Fatalf("soft status = %s, want %s", soft.Status, LimitSoftHit)
	}
	hard := enforcer.Check(Budget{SoftLimitUSD: 1, HardLimitUSD: 2}, Usage{EstimatedCostUSD: 2.01})
	if hard.Status != LimitHardHit {
		t.Fatalf("hard status = %s, want %s", hard.Status, LimitHardHit)
	}
	tokens := enforcer.Check(Budget{MaxInputTokens: 100}, Usage{InputTokens: 101})
	if tokens.Status != LimitHardHit {
		t.Fatalf("token status = %s, want %s", tokens.Status, LimitHardHit)
	}
}

func TestApplyUsage(t *testing.T) {
	budget := ApplyUsage(Budget{CurrentCostUSD: 1}, Usage{
		InputTokens:      10,
		OutputTokens:     20,
		EstimatedCostUSD: 0.5,
	})
	if budget.CurrentCostUSD != 1.5 || budget.CurrentInputTokens != 10 || budget.CurrentOutputTokens != 20 {
		t.Fatalf("budget = %+v", budget)
	}
}
