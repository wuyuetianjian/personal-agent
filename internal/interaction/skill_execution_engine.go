package interaction

import (
	"agent/internal/model"
	"agent/internal/runtime"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"time"
)

var errSkillNodeFailed = errors.New("skill node failed")

// IsSkillRun routes by immutable run identity, never by checkpoint content.
func (s *Store) IsSkillRun(ctx context.Context, id string) (bool, error) {
	var kind string
	err := s.db.QueryRowContext(ctx, `SELECT execution_kind FROM interaction_runs WHERE id=?`, id).Scan(&kind)
	return kind == skillRunAction, err
}
func skillRunData(ctx context.Context, q contextQuery, id string) (pin SkillRunPin, plan []SkillRunNode, revoked bool, err error) {
	var raw, hash, inputs, planRaw, session, kind string
	err = q.QueryRowContext(ctx, `SELECT s.pin,s.pin_hash,s.inputs,s.plan,s.revoked,r.session_id,r.execution_kind FROM interaction_runs r LEFT JOIN interaction_skill_runs s ON s.run_id=r.id WHERE r.id=?`, id).Scan(&raw, &hash, &inputs, &planRaw, &revoked, &session, &kind)
	if err != nil {
		return pin, nil, false, ErrReconciliation
	}
	if json.Unmarshal([]byte(raw), &pin) != nil || json.Unmarshal([]byte(planRaw), &plan) != nil || kind != skillRunAction || pin.RunID != id || pin.SessionID != session || pin.ExecutionProfile != SkillRunProfile || operatorHash(pin) != hash || operatorHash(plan) != pin.PlanHash || skillBytesHash(inputs) != pin.InputsHash || len(plan) == 0 || len(plan) > 16 {
		return pin, nil, false, ErrReconciliation
	}
	var artifact, contentHash string
	if err = q.QueryRowContext(ctx, `SELECT raw_artifact,content_hash FROM interaction_skill_revisions WHERE id=? AND catalog_id=?`, pin.RevisionID, pin.CatalogID).Scan(&artifact, &contentHash); err != nil {
		return pin, nil, false, ErrReconciliation
	}
	review, canonical, e := compileSkillRun(artifact, pin.ExecutionProfile, json.RawMessage(inputs), pin.ScopeID)
	if e != nil || skillBytesHash(artifact) != pin.ArtifactHash || contentHash != pin.ContentHash || review.Eligibility != "ready" || operatorHash(review) != pin.ReviewHash || review.PlanHash != pin.PlanHash || string(canonical) != inputs || !reflect.DeepEqual(review.Budgets, pin.Budgets) || pin.DeadlineMS-pin.AdmissionTimeMS != 120000 {
		return pin, nil, false, ErrReconciliation
	}
	return
}
func (s *Store) validateSkillCheckpoint(ctx context.Context, id string, cp Checkpoint) error {
	return validateSkillCheckpointAt(ctx, s.db, id, cp)
}
func validateSkillCheckpointAt(ctx context.Context, q contextQuery, id string, cp Checkpoint) error {
	pin, plan, _, err := skillRunData(ctx, q, id)
	if err != nil {
		return err
	}
	expectedBudgets := Budgets{MaxSteps: pin.Budgets.MaxAttempts, MaxDuration: 120 * time.Second, MaxTokens: 1, MaxOutputTokens: 1, MaxOutputBytes: 1048576, MaxToolResultBytes: 65536, MaxEvents: 512, MaxConcurrent: 4}
	if !reflect.DeepEqual(cp.Budgets, expectedBudgets) || !reflect.DeepEqual(cp.Request, model.TurnRequest{}) || cp.Tokens != 0 || cp.OutputBytes != 0 {
		return ErrReconciliation
	}
	if cp.Action != skillRunAction || cp.Deadline.UnixMilli() != pin.DeadlineMS || cp.Budgets.MaxSteps != pin.Budgets.MaxAttempts || cp.Budgets.MaxToolResultBytes != pin.Budgets.MaxResultBytes || cp.Budgets.MaxEvents != 512 || len(cp.Messages) != 0 || len(cp.Pending) > 1 || cp.Steps < 0 || cp.Steps > pin.Budgets.MaxAttempts {
		return ErrReconciliation
	}
	done := 0
	for _, node := range plan {
		var raw string
		if err = q.QueryRowContext(ctx, `SELECT result FROM interaction_skill_node_results WHERE run_id=? AND node_id=?`, id, node.NodeID).Scan(&raw); err != nil {
			return ErrReconciliation
		}
		var n SkillRunNodeResult
		if json.Unmarshal([]byte(raw), &n) != nil || n.NodeID != node.NodeID || n.Capability != node.Capability {
			return ErrReconciliation
		}
		if n.Outcome == "success" || n.Outcome == "failed" {
			if done != nodeIndex(plan, node.NodeID) || n.Result == nil {
				return ErrReconciliation
			}
			var prepared, status, result string
			if err = q.QueryRowContext(ctx, `SELECT p.prepared,j.status,j.result_ref FROM interaction_tool_proposals p JOIN side_effect_records j ON j.idempotency_key=? AND j.workflow_id=p.run_id AND j.node_id=p.id AND j.capability_id=? WHERE p.id=? AND p.run_id=?`, effectKey(id, n.ProposalID), node.Call.Name, n.ProposalID, id).Scan(&prepared, &status, &result); err != nil {
				return ErrReconciliation
			}
			var p preparedTool
			var observed ToolResult
			if json.Unmarshal([]byte(prepared), &p) != nil || json.Unmarshal([]byte(result), &observed) != nil || status != "completed" || !reflect.DeepEqual(p.Proposal.Call, node.Call) || !reflect.DeepEqual(observed, *n.Result) || (n.Outcome == "failed") != observed.IsError {
				return ErrReconciliation
			}
			done++
		} else if n.Outcome != "not_started" && n.Outcome != "unknown" {
			return ErrReconciliation
		}
	}
	next := done
	if len(cp.Pending) == 1 {
		if next >= len(plan) || !reflect.DeepEqual(cp.Pending[0].Call, plan[next].Call) || cp.Pending[0].RequiresApproval {
			return ErrReconciliation
		}
		digest, e := ProposalDigest(id, cp.Pending[0].Call)
		if e != nil || digest != cp.Pending[0].Digest {
			return ErrReconciliation
		}
		var raw string
		if err = q.QueryRowContext(ctx, `SELECT prepared FROM interaction_tool_proposals WHERE id=? AND run_id=?`, cp.Pending[0].ID, id).Scan(&raw); err != nil {
			return ErrReconciliation
		}
		var p preparedTool
		if json.Unmarshal([]byte(raw), &p) != nil || !reflect.DeepEqual(p.Proposal, cp.Pending[0]) || p.RunID != id || p.SessionID != pin.SessionID || p.ScopeID != pin.ScopeID || p.RootIdentity != pin.RootIdentity {
			return ErrReconciliation
		}
		next++
	}
	expected := []ToolCall{}
	for _, node := range plan[next:] {
		expected = append(expected, node.Call)
	}
	if len(expected) != len(cp.Calls) {
		return ErrReconciliation
	}
	for i := range expected {
		if !reflect.DeepEqual(expected[i], cp.Calls[i]) {
			return ErrReconciliation
		}
	}
	if cp.Steps < done || cp.Steps > done+len(cp.Pending) {
		return ErrReconciliation
	}
	return nil
}

// persistedSkillCheckpoint validates the durable identity at a serialized boundary.
func persistedSkillCheckpoint(ctx context.Context, q contextQuery, id string) (Checkpoint, error) {
	var cp Checkpoint
	var raw string
	var phase Phase
	if err := q.QueryRowContext(ctx, "SELECT phase,checkpoint FROM interaction_engine WHERE run_id=?", id).Scan(&phase, &raw); err != nil {
		return cp, ErrReconciliation
	}
	if json.Unmarshal([]byte(raw), &cp) != nil {
		return cp, ErrReconciliation
	}
	cp.Phase = phase
	return cp, validateSkillCheckpointAt(ctx, q, id, cp)
}

// Call only after checkpoint/journal validation, or after validated observations
// have been published in the same transaction.
func skillOutcomeState(ctx context.Context, q contextQuery, id string) (failed, complete bool, err error) {
	_, plan, _, err := skillRunData(ctx, q, id)
	if err != nil {
		return false, false, err
	}
	complete = true
	for _, node := range plan {
		var raw string
		if err = q.QueryRowContext(ctx, "SELECT result FROM interaction_skill_node_results WHERE run_id=? AND node_id=?", id, node.NodeID).Scan(&raw); err != nil {
			return false, false, err
		}
		var n SkillRunNodeResult
		if json.Unmarshal([]byte(raw), &n) != nil {
			return false, false, ErrReconciliation
		}
		failed = failed || n.Outcome == "failed"
		complete = complete && n.Outcome == "success"
	}
	return
}
func nodeIndex(plan []SkillRunNode, id string) int {
	for i, n := range plan {
		if n.NodeID == id {
			return i
		}
	}
	return -1
}
func NewSkillRunEngine(s *Store, id string) (*Engine, error) {
	b, err := NewSkillRunBroker(s, id)
	if err != nil {
		return nil, err
	}
	return NewEngine(s, actionOnlyProvider{}, b, EngineOptions{})
}

type SkillRunBroker struct {
	store  *Store
	runID  string
	native *WorkspaceBroker
}

func NewSkillRunBroker(s *Store, id string) (*SkillRunBroker, error) {
	if s == nil {
		return nil, ErrInvalid
	}
	cp, err := s.EngineCheckpoint(context.Background(), id)
	if err != nil {
		return nil, ErrReconciliation
	}
	if err = s.validateSkillCheckpoint(context.Background(), id, cp); err != nil {
		return nil, err
	}
	return &SkillRunBroker{store: s, runID: id, native: &WorkspaceBroker{store: s, skillRunID: id}}, nil
}
func (b *SkillRunBroker) Propose(ctx context.Context, id string, call ToolCall) (ToolProposal, error) {
	if id != b.runID {
		return ToolProposal{}, ErrConflict
	}
	_, plan, revoked, err := skillRunData(ctx, b.store.db, id)
	if err != nil {
		return ToolProposal{}, err
	}
	if revoked {
		return ToolProposal{}, context.Canceled
	}
	found := false
	for _, node := range plan {
		if reflect.DeepEqual(call, node.Call) {
			found = true
		}
	}
	if !found {
		return ToolProposal{}, ErrConflict
	}
	pin, _, _, err := skillRunData(ctx, b.store.db, id)
	if err != nil {
		return ToolProposal{}, err
	}
	w, err := b.store.Workspace(ctx, pin.SessionID)
	valid := err == nil && w.ScopeID == pin.ScopeID && w.RootIdentity == pin.RootIdentity
	if valid {
		identity, e := workspaceIdentity(w.Policy.Root)
		valid = e == nil && identity == pin.RootIdentity
	}
	tx, e := b.store.queueWrite(ctx, pin.SessionID)
	if e != nil {
		return ToolProposal{}, e
	}
	defer tx.Rollback()
	_, policy, e := operatorAuthority(ctx, tx, pin.SessionID)
	valid = valid && e == nil && policy == pin.PolicyDigest
	if !valid {
		if _, e = tx.ExecContext(ctx, `UPDATE interaction_skill_runs SET revoked=1 WHERE run_id=?`, id); e != nil {
			return ToolProposal{}, e
		}
		if e = tx.Commit(); e != nil {
			return ToolProposal{}, e
		}
		return ToolProposal{}, context.Canceled
	}
	if e = tx.Commit(); e != nil {
		return ToolProposal{}, e
	}
	return b.native.Propose(ctx, id, call)
}
func (b *SkillRunBroker) Execute(ctx context.Context, proposal string) (ToolResult, error) {
	p, err := b.native.loadPrepared(ctx, proposal)
	if err != nil {
		return ToolResult{}, err
	}
	if p.RunID != b.runID {
		return ToolResult{}, ErrConflict
	}
	return b.native.Execute(ctx, proposal)
}

// skillDispatchClaim is the only native content-read admission boundary. Catalog
// lifecycle writes serialize with this transaction, including crash-before-signal.
func (b *WorkspaceBroker) skillDispatchClaim(ctx context.Context, p preparedTool) error {
	tx, err := b.store.queueWrite(ctx, p.SessionID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	cp, err := persistedSkillCheckpoint(ctx, tx, p.RunID)
	if err != nil {
		return err
	}
	if cp.Phase != PhaseExecuting || len(cp.Pending) != 1 || !reflect.DeepEqual(cp.Pending[0], p.Proposal) {
		return ErrReconciliation
	}
	pin, plan, revoked, err := skillRunData(ctx, tx, p.RunID)
	if err != nil {
		return err
	}
	if p.RunID != b.skillRunID || revoked {
		return context.Canceled
	}
	if time.Now().UnixMilli() >= pin.DeadlineMS {
		return ErrBudget
	}
	w, policy, err := skillRunWorkspace(ctx, tx, p.SessionID)
	if err != nil {
		return ErrConflict
	}
	if w.ScopeID != pin.ScopeID || w.RootIdentity != pin.RootIdentity || policy != pin.PolicyDigest {
		return ErrConflict
	}
	cat, err := skillCatalogAt(ctx, tx, pin.CatalogID)
	if err != nil || cat.State != "enabled" || cat.SelectedRevision != pin.RevisionID || cat.Generation != pin.CatalogGeneration {
		return ErrConflict
	}
	index := nodeIndex(plan, p.Proposal.Call.ID)
	if index < 0 || !reflect.DeepEqual(plan[index].Call, p.Proposal.Call) {
		return ErrConflict
	}
	for _, node := range plan[:index] {
		var raw string
		if err = tx.QueryRowContext(ctx, `SELECT result FROM interaction_skill_node_results WHERE run_id=? AND node_id=?`, p.RunID, node.NodeID).Scan(&raw); err != nil {
			return err
		}
		var n SkillRunNodeResult
		if json.Unmarshal([]byte(raw), &n) != nil || n.Outcome != "success" {
			return ErrConflict
		}
	}
	var controls int
	var cancelled bool
	if err = tx.QueryRowContext(ctx, `SELECT s.control_events,e.cancel_requested FROM interaction_skill_runs s JOIN interaction_engine e ON e.run_id=s.run_id WHERE s.run_id=?`, p.RunID).Scan(&controls, &cancelled); err != nil {
		return err
	}
	if cancelled {
		return context.Canceled
	}
	if controls >= 60 {
		return ErrBudget
	}
	res, err := tx.ExecContext(ctx, `UPDATE interaction_tool_proposals SET executed=1 WHERE id=? AND run_id=? AND executed=0`, p.Proposal.ID, p.RunID)
	if err = changed(res, err); err != nil {
		return err
	}
	return tx.Commit()
}
func skillNodeObservation(ctx context.Context, tx *sql.Tx, id string, o ToolObservation) error {
	_, plan, _, err := skillRunData(ctx, tx, id)
	if err != nil {
		return err
	}
	index := nodeIndex(plan, o.CallID)
	if index < 0 {
		return ErrReconciliation
	}
	var old string
	if err = tx.QueryRowContext(ctx, `SELECT result FROM interaction_skill_node_results WHERE run_id=? AND node_id=?`, id, o.CallID).Scan(&old); err != nil {
		return err
	}
	n := SkillRunNodeResult{NodeID: o.CallID, Capability: plan[index].Capability, ProposalID: o.ProposalID, Outcome: "success", Result: &o.ToolResult}
	if o.IsError {
		n.Outcome, n.Reason = "failed", "tool_refused"
	}
	raw, _ := json.Marshal(n)
	if old == string(raw) {
		return nil
	}
	var prior SkillRunNodeResult
	if json.Unmarshal([]byte(old), &prior) != nil || !skillEnum(prior.Outcome, "not_started", "unknown") {
		return ErrReconciliation
	}
	_, err = tx.ExecContext(ctx, `UPDATE interaction_skill_node_results SET result=? WHERE run_id=? AND node_id=?`, string(raw), id, o.CallID)
	return err
}

func validateSkillJournal(ctx context.Context, s *Store, id string, p ToolProposal, record runtime.SideEffectRecord) (ToolResult, error) {
	var result ToolResult
	if record.WorkflowID != id || record.NodeID != p.ID || record.CapabilityID != p.Call.Name || record.IdempotencyKey != effectKey(id, p.ID) || record.Status != "completed" || len(record.ResultRef) > 65536 {
		return result, ErrReconciliation
	}
	d := json.NewDecoder(strings.NewReader(record.ResultRef))
	d.DisallowUnknownFields()
	if d.Decode(&result) != nil || d.Decode(new(any)) != io.EOF {
		return result, ErrReconciliation
	}
	raw, _ := json.Marshal(result)
	if string(raw) != record.ResultRef {
		return result, ErrReconciliation
	}
	return result, nil
}
