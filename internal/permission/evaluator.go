package permission

import (
	"context"
	"errors"
	"time"
)

var ErrConfirmationStoreRequired = errors.New("confirmation store required")

type Evaluator struct {
	Policy        Policy
	Confirmations ConfirmationStore
	Now           func() time.Time
}

func (e Evaluator) Evaluate(ctx context.Context, request Request) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	now := e.now()
	if request.RequestedAt.IsZero() {
		request.RequestedAt = now
	}
	decision := e.Policy.DecisionFor(request.Action)
	result := Result{
		Request:   request,
		Decision:  decision,
		Reason:    "policy decision",
		DecidedAt: now,
	}
	if decision != DecisionConfirm {
		return result, nil
	}
	if request.ID != "" && e.Confirmations != nil {
		approved, err := e.Confirmations.IsApproved(ctx, request.ID)
		if err != nil {
			return Result{}, err
		}
		if approved {
			result.Decision = DecisionAllow
			result.Reason = "confirmed"
			result.ConfirmationID = request.ID
			return result, nil
		}
	}
	if e.Confirmations == nil {
		return Result{}, ErrConfirmationStoreRequired
	}
	confirmationID, err := e.Confirmations.Request(ctx, request)
	if err != nil {
		return Result{}, err
	}
	result.ConfirmationID = confirmationID
	result.Reason = "confirmation required"
	return result, nil
}

func (e Evaluator) Audit(result Result, id string) AuditRecord {
	now := e.now()
	if !result.DecidedAt.IsZero() {
		now = result.DecidedAt
	}
	return AuditRecord{
		ID:             id,
		TaskID:         result.Request.TaskID,
		NodeID:         result.Request.NodeID,
		Action:         result.Request.Action,
		Target:         result.Request.Target,
		Decision:       result.Decision,
		Risk:           result.Request.Risk,
		Reason:         result.Reason,
		ConfirmationID: result.ConfirmationID,
		EvidenceIDs:    append([]string(nil), result.Request.EvidenceIDs...),
		ProposedEffect: result.Request.ProposedEffect,
		CreatedAt:      now,
	}
}

func (e Evaluator) now() time.Time {
	if e.Now != nil {
		return e.Now().UTC()
	}
	return time.Now().UTC()
}
