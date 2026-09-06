package permission

import (
	"context"
	"testing"
	"time"

	"agent/internal/config"
)

func TestDefaultPolicyDeniesUnknownAndRequiresHighRiskConfirmation(t *testing.T) {
	policy := DefaultPolicy()
	if got := policy.DecisionFor(ActionNetwork); got != DecisionDeny {
		t.Fatalf("network decision = %s, want %s", got, DecisionDeny)
	}
	if got := policy.DecisionFor(ActionBrowserHighRisk); got != DecisionConfirm {
		t.Fatalf("high risk decision = %s, want %s", got, DecisionConfirm)
	}
}

func TestPolicyFromConfigOverridesAction(t *testing.T) {
	policy, err := NewPolicyFromConfig(config.PermissionsConfig{
		Default: "deny",
		Rules: []config.PermissionRuleConfig{
			{Action: string(ActionNetwork), Decision: string(DecisionAllow)},
		},
	})
	if err != nil {
		t.Fatalf("NewPolicyFromConfig() error = %v", err)
	}
	if got := policy.DecisionFor(ActionNetwork); got != DecisionAllow {
		t.Fatalf("network decision = %s, want %s", got, DecisionAllow)
	}
}

func TestEvaluatorCreatesAndHonorsConfirmation(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryConfirmationStore()
	now := time.Date(2026, 9, 6, 1, 2, 3, 0, time.UTC)
	evaluator := Evaluator{Policy: DefaultPolicy(), Confirmations: store, Now: func() time.Time { return now }}
	request := Request{
		ID:             "confirm-1",
		TaskID:         "task-1",
		NodeID:         "node-1",
		Action:         ActionBrowserHighRisk,
		Target:         "https://example.test/delete",
		Risk:           RiskCritical,
		EvidenceIDs:    []string{"ev-1"},
		ProposedEffect: "delete account data",
	}

	result, err := evaluator.Evaluate(ctx, request)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if result.Decision != DecisionConfirm || result.ConfirmationID != request.ID {
		t.Fatalf("result = %+v, want confirmation", result)
	}
	stored, ok := store.RequestByID(request.ID)
	if !ok || stored.ProposedEffect != request.ProposedEffect {
		t.Fatalf("stored confirmation = %+v, %v", stored, ok)
	}
	if err := store.Approve(ctx, request.ID); err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
	result, err = evaluator.Evaluate(ctx, request)
	if err != nil {
		t.Fatalf("Evaluate() after approve error = %v", err)
	}
	if result.Decision != DecisionAllow || result.Reason != "confirmed" {
		t.Fatalf("approved result = %+v", result)
	}
	audit := evaluator.Audit(result, "audit-1")
	if audit.Action != ActionBrowserHighRisk || audit.ProposedEffect != request.ProposedEffect || audit.CreatedAt != now {
		t.Fatalf("audit = %+v", audit)
	}
}
