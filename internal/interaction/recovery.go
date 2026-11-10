package interaction

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"agent/internal/runtime"
	"github.com/google/uuid"
)

// Recover is an OFFLINE startup operation: callers must establish that the previous
// process is stopped before calling it. It refuses live owners in this process.
// No time-based lease steals a slow executor, and no uncertain action is retried.
func (e *Engine) Recover(ctx context.Context, id string) error {
	activeOwners.Lock()
	defer activeOwners.Unlock()
	if activeOwners.cancels[id] != nil {
		return ErrOwned
	}
	r, err := e.store.GetRun(ctx, id)
	if err != nil {
		return err
	}
	isSkill, err := e.store.IsSkillRun(ctx, id)
	if err != nil {
		return err
	}
	cp, err := e.Checkpoint(ctx, id)
	if err != nil {
		if isSkill {
			return ErrReconciliation
		}
		return err
	}
	if isSkill {
		if err = e.store.validateSkillCheckpoint(ctx, id, cp); err != nil {
			return err
		}
	} else if cp.Action != "" && cp.Action != "file_undo" {
		return ErrReconciliation
	}
	if r.State != RunRunning {
		return nil
	}
	owner := uuid.NewString()
	if _, err = e.store.db.ExecContext(ctx, "UPDATE interaction_engine SET owner=? WHERE run_id=?", owner, id); err != nil {
		return err
	}
	defer e.store.db.ExecContext(context.Background(), "UPDATE interaction_engine SET owner='' WHERE run_id=? AND owner=?", id, owner)
	if cp.Phase == PhaseReconciliation && !isSkill {
		return ErrReconciliation
	}
	var observations []ToolObservation
	knownFailure := false
	if isSkill {
		knownFailure, _, err = skillOutcomeState(ctx, e.store.db, id)
		if err != nil {
			return err
		}
	}
	if len(cp.Pending) > 0 {
		p := cp.Pending[0]
		journal := runtime.SQLiteSideEffectStore{DB: e.store.db}
		record, err := journal.Get(ctx, effectKey(id, p.ID))
		if err == nil {
			if record.Status != "completed" || (isSkill && (record.WorkflowID != id || record.NodeID != p.ID || record.CapabilityID != p.Call.Name)) {
				return e.reconcile(r, owner, &cp)
			}
			var result ToolResult
			if err = json.Unmarshal([]byte(record.ResultRef), &result); err != nil || len(result.Content) > cp.Budgets.MaxToolResultBytes || (isSkill && len(record.ResultRef) > cp.Budgets.MaxToolResultBytes) {
				return e.reconcile(r, owner, &cp)
			}
			if isSkill {
				var validationErr error
				result, validationErr = validateSkillJournal(ctx, e.store, id, p, record)
				if validationErr != nil {
					return e.reconcile(r, owner, &cp)
				}
			}
			knownFailure = knownFailure || (isSkill && result.IsError)
			observations = append(observations, ToolObservation{ProposalID: p.ID, CallID: p.Call.ID, ToolResult: result})
			if cp.Action == "" {
				cp.Messages = append(cp.Messages, modelToolMessage(p.Call.ID, result.Content))
			}
			cp.Pending = nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		} else if p.RequiresApproval {
			a, err := e.store.GetApproval(ctx, p.ID)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if err == nil && a.ConsumedAt != nil {
				return e.reconcile(r, owner, &cp)
			}
		}
	}
	if isSkill && cp.Phase == PhaseReconciliation && len(observations) == 0 {
		return ErrReconciliation
	}
	var cancelled bool
	if err = e.store.db.QueryRowContext(ctx, "SELECT cancel_requested FROM interaction_engine WHERE run_id=?", id).Scan(&cancelled); err != nil {
		return err
	}
	if isSkill {
		_, _, revoked, e := skillRunData(ctx, e.store.db, id)
		if e != nil {
			return e
		}
		cancelled = cancelled || revoked
	}
	if cancelled {
		cp.Phase = PhaseCancelled
		return e.finish(ctx, r, owner, &cp, RunCancelled, observations...)
	}
	if knownFailure {
		cp.Phase = PhaseFailed
		return e.finish(ctx, r, owner, &cp, RunFailed, observations...)
	}
	if cp.Action != "" && len(cp.Pending) == 0 && len(cp.Calls) == 0 {
		cp.Phase = PhaseCompleted
		return e.finish(ctx, r, owner, &cp, RunCompleted, observations...)
	}
	// A fully checkpointed text answer needs only its terminal commit, not another model call.
	if len(cp.Pending) == 0 && len(cp.Calls) == 0 && len(cp.Messages) > 0 {
		last := cp.Messages[len(cp.Messages)-1]
		if last.Role == "assistant" && len(last.ToolCalls) == 0 {
			cp.Phase = PhaseCompleted
			return e.finish(ctx, r, owner, &cp, RunCompleted, observations...)
		}
	}
	cp.Phase = PhaseRunning
	return e.save(ctx, r, owner, &cp, "run.recovered", nil, observations...)
}
