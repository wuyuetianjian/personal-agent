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
	Sessions     SessionManager
	Classifier   PermissionClassifier
	Runtime      RuntimeAdapter
	Evidence     EvidenceBuilder
	EvidenceBus  BrowserEvidenceBus
	Confirmation ConfirmationChecker
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

	proposed := Result{Action: action, Decision: t.Classifier.Decide(action, true), RecordedAt: time.Now().UTC()}
	_ = t.publish(ctx, EvidenceActionProposed, proposed, false)

	policyTarget := action.URL
	if policyTarget == "" {
		policyTarget = action.Domain
	}
	if err := t.Sessions.AllowDomain(action.SessionID, policyTarget, time.Now().UTC()); err != nil {
		result := failedResult(action, DecisionDenied, "session_policy_denied", err.Error())
		_ = t.publish(ctx, EvidenceActionDenied, result, false)
		return result, nil
	}

	decision := t.Classifier.Decide(action, true)
	if decision != DecisionAllowed {
		result := Result{
			Action:     action,
			Decision:   decision,
			RecordedAt: time.Now().UTC(),
		}
		if decision == DecisionRequiresConfirmation || decision == DecisionRequiresExplicit {
			if t.isConfirmed(ctx, action) {
				decision = DecisionAllowed
			} else {
				_ = t.publish(ctx, EvidenceActionDenied, result, false)
				return result, nil
			}
		} else {
			_ = t.publish(ctx, EvidenceActionDenied, result, false)
			return result, nil
		}
	}

	approved := Result{Action: action, Decision: decision, RecordedAt: time.Now().UTC()}
	_ = t.publish(ctx, EvidenceActionApproved, approved, false)

	observation, err := t.Runtime.Execute(ctx, action)
	if err != nil {
		category := EvidenceActionFailed
		cancelled := false
		if ctx.Err() != nil {
			category = EvidenceCancelled
			cancelled = true
		}
		result := failedResult(action, decision, "runtime_failed", err.Error())
		_ = t.publish(ctx, category, result, cancelled)
		return result, nil
	}

	result := Result{
		Action:      action,
		Observation: observation,
		Decision:    decision,
		RecordedAt:  time.Now().UTC(),
	}
	_ = t.publish(ctx, evidenceCategoryForAction(action.Type), result, false)
	return result, nil
}

func (t GovernedTool) Observe(ctx context.Context, sessionID string) (Observation, error) {
	if err := ctx.Err(); err != nil {
		return Observation{}, err
	}
	if t.Runtime == nil {
		return Observation{}, ErrUnsupportedAction
	}
	action := Action{
		Type:      ActionReadVisibleText,
		SessionID: sessionID,
	}
	observation, err := t.Runtime.Execute(ctx, action)
	if err != nil {
		return Observation{}, err
	}
	result := Result{
		Action:      action,
		Observation: observation,
		Decision:    DecisionAllowed,
		RecordedAt:  time.Now().UTC(),
	}
	_ = t.publish(ctx, EvidenceActionExecuted, result, false)
	return observation, nil
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

func (t GovernedTool) isConfirmed(ctx context.Context, action Action) bool {
	if adapter, ok := t.Runtime.(interface {
		Confirmed(context.Context, Action) bool
	}); ok {
		return adapter.Confirmed(ctx, action)
	}
	if t.Confirmation == nil {
		return false
	}
	return t.Confirmation.IsConfirmed(ctx, action.ConfirmationID, action)
}

func (t GovernedTool) publish(ctx context.Context, category EvidenceCategory, result Result, cancelled bool) error {
	if t.EvidenceBus == nil {
		return nil
	}
	return t.EvidenceBus.PublishBrowserEvent(ctx, t.Evidence.Build(category, result.Action.TaskID, result.Action.AgentID, result, cancelled))
}

func evidenceCategoryForAction(actionType ActionType) EvidenceCategory {
	switch actionType {
	case ActionNavigate, ActionOpenTab, ActionBack, ActionForward, ActionReload, ActionClickNavigation:
		return EvidenceNavigationCompleted
	case ActionReadDOM:
		return EvidenceReadDOM
	case ActionReadAccessibility:
		return EvidenceReadAccessibility
	case ActionCaptureScreenshot:
		return EvidenceScreenshotCaptured
	case ActionLocateElement:
		return EvidenceElementLocated
	default:
		return EvidenceActionExecuted
	}
}
