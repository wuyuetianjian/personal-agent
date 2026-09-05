package browser

import (
	"context"
	"time"
)

type Tool interface {
	Plan(ctx context.Context, action Action) (PermissionRequest, error)
	Execute(ctx context.Context, action Action) (Result, error)
	Observe(ctx context.Context, sessionID string) (Observation, error)
}

type RuntimeAdapter interface {
	Execute(ctx context.Context, action Action) (Observation, error)
}

type GovernedTool struct {
	Sessions   SessionManager
	Classifier PermissionClassifier
	Runtime    RuntimeAdapter
	Evidence   EvidenceBuilder
}

func (t GovernedTool) Plan(ctx context.Context, action Action) (PermissionRequest, error) {
	if err := ctx.Err(); err != nil {
		return PermissionRequest{}, err
	}
	return t.Classifier.Classify(action), nil
}

func (t GovernedTool) Execute(ctx context.Context, action Action) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if t.Sessions == nil {
		return failedResult(action, DecisionDenied, "session_manager_missing", "browser session manager is required"), nil
	}
	if t.Runtime == nil {
		return failedResult(action, DecisionDenied, "runtime_missing", "browser runtime adapter is required"), nil
	}

	policyTarget := action.URL
	if policyTarget == "" {
		policyTarget = action.Domain
	}
	if err := t.Sessions.AllowDomain(action.SessionID, policyTarget, time.Now().UTC()); err != nil {
		return failedResult(action, DecisionDenied, "session_policy_denied", err.Error()), nil
	}

	decision := t.Classifier.Decide(action, true)
	if decision != DecisionAllowed {
		return Result{
			Action:     action,
			Decision:   decision,
			RecordedAt: time.Now().UTC(),
		}, nil
	}

	observation, err := t.Runtime.Execute(ctx, action)
	if err != nil {
		return failedResult(action, decision, "runtime_failed", err.Error()), nil
	}

	return Result{
		Action:      action,
		Observation: observation,
		Decision:    decision,
		RecordedAt:  time.Now().UTC(),
	}, nil
}

func (t GovernedTool) Observe(ctx context.Context, sessionID string) (Observation, error) {
	if err := ctx.Err(); err != nil {
		return Observation{}, err
	}
	return Observation{}, nil
}

func failedResult(action Action, decision PermissionDecision, category string, message string) Result {
	return Result{
		Action:   action,
		Decision: decision,
		Error: &ActionError{
			Category: category,
			Message:  message,
		},
		RecordedAt: time.Now().UTC(),
	}
}
