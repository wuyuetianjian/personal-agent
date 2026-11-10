package interaction

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"time"
)

type SkillRunIntent struct {
	RequestID        string          `json:"request_id"`
	CatalogID        string          `json:"catalog_id"`
	RevisionID       string          `json:"revision_id"`
	ExecutionProfile string          `json:"execution_profile"`
	Inputs           json.RawMessage `json:"inputs"`
}
type SkillRunPin struct {
	SchemaVersion     int             `json:"schema_version"`
	SessionID         string          `json:"session_id"`
	RunID             string          `json:"run_id"`
	CatalogID         string          `json:"catalog_id"`
	RevisionID        string          `json:"revision_id"`
	CatalogGeneration string          `json:"catalog_generation"`
	ScopeID           string          `json:"scope_id"`
	RootIdentity      string          `json:"root_identity"`
	PolicyDigest      string          `json:"policy_digest"`
	ArtifactHash      string          `json:"artifact_sha256"`
	ContentHash       string          `json:"content_sha256"`
	ReviewHash        string          `json:"review_sha256"`
	InputsHash        string          `json:"inputs_sha256"`
	PlanHash          string          `json:"plan_sha256"`
	ExecutionProfile  string          `json:"execution_profile"`
	AdmissionTimeMS   int64           `json:"admission_time_ms"`
	DeadlineMS        int64           `json:"deadline_ms"`
	Budgets           SkillRunBudgets `json:"budgets"`
}
type SkillRunOperation struct {
	SchemaVersion      int            `json:"schema_version"`
	Namespace          string         `json:"namespace"`
	OperationID        string         `json:"operation_id"`
	SessionID          string         `json:"session_id"`
	CatalogID          string         `json:"catalog_id"`
	RevisionID         string         `json:"revision_id"`
	ScopeID            string         `json:"scope_id"`
	RootIdentity       string         `json:"root_identity"`
	PolicyDigest       string         `json:"policy_digest"`
	ExpectedGeneration string         `json:"expected_generation"`
	ExpectedRevision   int64          `json:"expected_revision"`
	Review             SkillRunReview `json:"review"`
	ExpiresAtMS        int64          `json:"expires_at_ms"`
	Digest             string         `json:"digest"`
	State              string         `json:"state"`
	Decision           string         `json:"decision"`
	DecisionRequestID  string         `json:"decision_request_id"`
	RunID              string         `json:"run_id"`
	Reason             string         `json:"reason"`
}
type SkillRunNodeResult struct {
	NodeID     string      `json:"node_id"`
	Capability string      `json:"capability"`
	ProposalID string      `json:"proposal_id"`
	Outcome    string      `json:"outcome"`
	Reason     string      `json:"reason"`
	Result     *ToolResult `json:"result"`
}
type SkillRunRecord struct {
	SchemaVersion      int                  `json:"schema_version"`
	SessionID          string               `json:"session_id"`
	RunID              string               `json:"run_id"`
	Pin                SkillRunPin          `json:"pin"`
	Execution          string               `json:"execution"`
	Evaluation         string               `json:"evaluation"`
	VerificationStatus string               `json:"verification_status"`
	Revoked            bool                 `json:"revoked"`
	Nodes              []SkillRunNodeResult `json:"nodes"`
}
type SkillRunPage struct {
	SchemaVersion int              `json:"schema_version"`
	SessionID     string           `json:"session_id"`
	Runs          []SkillRunRecord `json:"runs"`
	NextCursor    string           `json:"next_cursor"`
}

func skillRunDigest(p SkillRunOperation) string { p.Digest = ""; return operatorHash(p) }
func skillRunReplay(ctx context.Context, tx *sql.Tx, session, request, fp string) (out SkillRunOperation, found bool, err error) {
	var controlCount int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM interaction_skill_controls WHERE request_id=?`, request).Scan(&controlCount); err != nil {
		return
	}
	if controlCount != 0 {
		return out, true, ErrConflict
	}
	var owner, old, raw string
	err = tx.QueryRowContext(ctx, `SELECT r.session_id,r.fingerprint,CASE WHEN r.result='' THEN o.result ELSE r.result END FROM interaction_skill_run_requests r JOIN interaction_skill_run_operations o ON o.id=r.operation_id WHERE r.request_id=?`, request).Scan(&owner, &old, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return out, false, nil
	}
	if err != nil {
		return
	}
	if owner != session || old != fp {
		return out, true, ErrConflict
	}
	err = json.Unmarshal([]byte(raw), &out)
	return out, true, err
}
func skillRunWorkspace(ctx context.Context, tx *sql.Tx, session string) (w WorkspaceBinding, policy string, err error) {
	var raw, mode string
	w.SessionID = session
	err = tx.QueryRowContext(ctx, `SELECT w.scope_id,w.root_identity,w.policy,a.mode FROM interaction_workspaces w JOIN interaction_api_sessions a ON a.session_id=w.session_id WHERE w.session_id=?`, session).Scan(&w.ScopeID, &w.RootIdentity, &raw, &mode)
	if err != nil {
		return
	}
	if err = json.Unmarshal([]byte(raw), &w.Policy); err != nil {
		return
	}
	if mode == "chat_only" || !skillEnum(string(w.Policy.Mode), "plan", "default", "acceptEdits") || !platformFileCapability().Enabled {
		err = ErrCapabilityUnavailable
		return
	}
	_, policy, err = operatorAuthority(ctx, tx, session)
	return
}
func skillRunAudit(ctx context.Context, tx *sql.Tx, p SkillRunOperation) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO security_audit_log(id,actor,action,target,status,reason,evidence_ids,created_at) VALUES(?,?,?,?,?,?,?,?)`, "skill-run:"+p.OperationID+":"+p.State, "session:"+p.SessionID, "skill.run", p.CatalogID, p.State, p.Reason, "[\""+p.OperationID+"\"]", time.Now().UTC())
	return err
}
func (s *Store) PrepareSkillRun(ctx context.Context, session string, in SkillRunIntent) (out SkillRunOperation, err error) {
	if !operatorRequestID(in.RequestID) || !operatorRequestID(in.CatalogID) || !operatorRequestID(in.RevisionID) || len(in.ExecutionProfile) > 64 || len(in.Inputs) > 32768 {
		return out, ErrInvalid
	}
	if _, e := skillRunJSONObject(in.Inputs); e != nil {
		return out, ErrInvalid
	}
	tx, err := s.queueWrite(ctx, session)
	if err != nil {
		return
	}
	defer tx.Rollback()
	fp := operatorHash([]any{session, "prepare_skill_run", in})
	if old, found, e := skillRunReplay(ctx, tx, session, in.RequestID, fp); found || e != nil {
		return old, e
	}
	cat, err := skillCatalogAt(ctx, tx, in.CatalogID)
	if err != nil {
		return
	}
	rev, err := skillRevisionAt(ctx, tx, in.CatalogID, in.RevisionID)
	if err != nil {
		return
	}
	w, policy, workspaceErr := skillRunWorkspace(ctx, tx, session)
	if workspaceErr != nil && !errors.Is(workspaceErr, sql.ErrNoRows) && !errors.Is(workspaceErr, ErrCapabilityUnavailable) {
		return out, workspaceErr
	}
	review, canonical, err := compileSkillRun(rev.ManifestText, in.ExecutionProfile, in.Inputs, w.ScopeID)
	if err != nil {
		return
	}
	if workspaceErr != nil {
		review.Eligibility, review.Reason = "blocked", "unsupported_workspace"
	}
	if cat.State != "enabled" || cat.SelectedRevision != in.RevisionID {
		review.Eligibility, review.Reason = "blocked", "selection_unavailable"
	}
	if canonical == nil {
		canonical = json.RawMessage(`{}`)
	}
	out = SkillRunOperation{SchemaVersion: 1, Namespace: "runtime-rebuild-v1", OperationID: in.RequestID, SessionID: session, CatalogID: in.CatalogID, RevisionID: in.RevisionID, ScopeID: w.ScopeID, RootIdentity: w.RootIdentity, PolicyDigest: policy, ExpectedGeneration: cat.Generation, ExpectedRevision: cat.Revision, Review: review, ExpiresAtMS: time.Now().Add(10 * time.Minute).UnixMilli(), State: "pending", Reason: review.Reason}
	out.Digest = skillRunDigest(out)
	raw, _ := json.Marshal(out)
	reserve := 4*(len(raw)+4096) + len(canonical)
	var count, total, pending, local, requests int
	err = tx.QueryRowContext(ctx, `SELECT count(*),coalesce(sum(reserved_bytes),0),coalesce(sum(state='pending' AND json_extract(preview,'$.expires_at_ms')>?),0),coalesce(sum(state='pending' AND session_id=? AND json_extract(preview,'$.expires_at_ms')>?),0) FROM interaction_skill_run_operations`, time.Now().UnixMilli(), session, time.Now().UnixMilli()).Scan(&count, &total, &pending, &local)
	if err != nil {
		return
	}
	if err = tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM interaction_skill_run_requests)+(SELECT count(*) FROM interaction_skill_controls)`).Scan(&requests); err != nil {
		return
	}
	var controlBytes int
	if err = tx.QueryRowContext(ctx, `SELECT coalesce(sum(reserved_bytes),0) FROM interaction_skill_controls`).Scan(&controlBytes); err != nil {
		return
	}
	if count >= 1024 || total+controlBytes+reserve > 64<<20 || pending >= 64 || local >= 16 || requests >= 2048 {
		return out, ErrBudget
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO interaction_skill_run_operations VALUES(?,?,?,?,?,'pending',?)`, out.OperationID, session, string(raw), string(canonical), reserve, string(raw)); err != nil {
		return
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO interaction_skill_run_requests VALUES(?,?,?,?,'')`, in.RequestID, session, out.OperationID, fp); err != nil {
		return
	}
	if err = skillRunAudit(ctx, tx, out); err != nil {
		return
	}
	return out, tx.Commit()
}
func (s *Store) DecideSkillRun(ctx context.Context, session string, in OperatorDecision) (out SkillRunOperation, err error) {
	if !operatorRequestID(in.RequestID) || !operatorRequestID(in.OperationID) || len(in.Digest) != 64 || !skillEnum(in.Decision, "confirm", "cancel") {
		return out, ErrInvalid
	}
	tx, err := s.queueWrite(ctx, session)
	if err != nil {
		return
	}
	defer tx.Rollback()
	fp := operatorHash([]any{session, "decide_skill_run", in})
	if old, found, e := skillRunReplay(ctx, tx, session, in.RequestID, fp); found || e != nil {
		return old, e
	}
	var raw, inputs, state string
	if err = tx.QueryRowContext(ctx, `SELECT preview,inputs,state FROM interaction_skill_run_operations WHERE id=? AND session_id=?`, in.OperationID, session).Scan(&raw, &inputs, &state); err != nil {
		return
	}
	if err = json.Unmarshal([]byte(raw), &out); err != nil {
		return
	}
	if state != "pending" || out.Digest != in.Digest || skillRunDigest(out) != in.Digest {
		return out, ErrConflict
	}
	var requests int
	if err = tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM interaction_skill_run_requests)+(SELECT count(*) FROM interaction_skill_controls)`).Scan(&requests); err != nil {
		return
	}
	if requests >= 2048 {
		return out, ErrBudget
	}
	out.Decision, out.DecisionRequestID = in.Decision, in.RequestID
	out.State, out.Reason = "stale", "preview_changed"
	w, policy, authorityErr := skillRunWorkspace(ctx, tx, session)
	cat, catErr := skillCatalogAt(ctx, tx, out.CatalogID)
	switch {
	case in.Decision == "cancel":
		out.State, out.Reason = "cancelled", "user_cancelled"
	case time.Now().UnixMilli() >= out.ExpiresAtMS:
		out.Reason = "preview_expired"
	case out.Review.Eligibility != "ready":
		out.Reason = "eligibility_blocked"
	case authorityErr != nil || w.ScopeID != out.ScopeID || w.RootIdentity != out.RootIdentity || policy != out.PolicyDigest:
		out.Reason = "authority_changed"
	case catErr != nil || cat.Generation != out.ExpectedGeneration || cat.Revision != out.ExpectedRevision || cat.State != "enabled" || cat.SelectedRevision != out.RevisionID:
		out.Reason = "selection_changed"
	default:
		rev, e := skillRevisionAt(ctx, tx, out.CatalogID, out.RevisionID)
		if e != nil {
			return out, e
		}
		review, canonical, e := compileSkillRun(rev.ManifestText, out.Review.ExecutionProfile, json.RawMessage(inputs), w.ScopeID)
		if e != nil || operatorHash(review) != operatorHash(out.Review) || string(canonical) != inputs {
			return out, ErrConflict
		}
		// A preview never resolves a live provider or reads a file. Admission validates
		// already-bound identities; the safe adapter revalidates the actual root at read.
		var active int
		if err = tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM interaction_runs WHERE session_id=? AND state='running')+(SELECT count(*) FROM interaction_queue WHERE session_id=? AND state='pending')`, session, session).Scan(&active); err != nil {
			return
		}
		if active != 0 {
			return out, ErrConflict
		}
		if err = s.admitSkillRun(ctx, tx, &out, rev, inputs, in.RequestID); err != nil {
			return
		}
	}
	result, _ := json.Marshal(out)
	res, e := tx.ExecContext(ctx, `UPDATE interaction_skill_run_operations SET state=?,result=? WHERE id=? AND state='pending'`, out.State, string(result), out.OperationID)
	if err = changed(res, e); err != nil {
		return
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO interaction_skill_run_requests VALUES(?,?,?,?,?)`, in.RequestID, session, out.OperationID, fp, string(result)); err != nil {
		return
	}
	if err = skillRunAudit(ctx, tx, out); err != nil {
		return
	}
	return out, tx.Commit()
}
func (s *Store) admitSkillRun(ctx context.Context, tx *sql.Tx, out *SkillRunOperation, rev SkillRevisionRecord, inputs, request string) error {
	var count, total int
	if err := tx.QueryRowContext(ctx, `SELECT count(*),coalesce(sum(reserved_bytes),0) FROM interaction_skill_runs`).Scan(&count, &total); err != nil {
		return err
	}
	if count >= 256 || total+(8<<20) > 256<<20 {
		return ErrBudget
	}
	now := time.Now()
	id := uuid.NewString()
	b := out.Review.Budgets
	pin := SkillRunPin{SchemaVersion: 1, SessionID: out.SessionID, RunID: id, CatalogID: out.CatalogID, RevisionID: out.RevisionID, CatalogGeneration: out.ExpectedGeneration, ScopeID: out.ScopeID, RootIdentity: out.RootIdentity, PolicyDigest: out.PolicyDigest, ArtifactHash: rev.Review.ArtifactHash, ContentHash: rev.Review.ContentHash, ReviewHash: operatorHash(out.Review), InputsHash: out.Review.InputsHash, PlanHash: out.Review.PlanHash, ExecutionProfile: SkillRunProfile, AdmissionTimeMS: now.UnixMilli(), DeadlineMS: now.Add(120 * time.Second).UnixMilli(), Budgets: b}
	pRaw, _ := json.Marshal(pin)
	plan, _ := json.Marshal(out.Review.Nodes)
	run := Run{ID: id, SessionID: out.SessionID, RequestID: request, Message: Message{Content: "Explicit read-only skill run"}, State: RunRunning, Version: 1}
	msg, _ := json.Marshal(run.Message)
	if _, err := tx.ExecContext(ctx, `INSERT INTO interaction_runs(`+runColumns+`,execution_kind) VALUES(?,?,?,?,?,?,?)`, id, out.SessionID, request, string(msg), RunRunning, 1, skillRunAction); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO interaction_skill_runs(run_id,session_id,catalog_id,revision_id,pin,pin_hash,inputs,plan,retained_bytes) VALUES(?,?,?,?,?,?,?,?,?)`, id, out.SessionID, out.CatalogID, out.RevisionID, string(pRaw), operatorHash(pin), inputs, string(plan), len(pRaw)+len(plan)+len(inputs)+len(msg)+4096); err != nil {
		return err
	}
	cp := Checkpoint{Action: skillRunAction, Phase: PhaseQueued, Deadline: time.UnixMilli(pin.DeadlineMS), Budgets: Budgets{MaxSteps: b.MaxAttempts, MaxDuration: 120 * time.Second, MaxTokens: 1, MaxOutputTokens: 1, MaxOutputBytes: b.MaxOutputBytes, MaxToolResultBytes: b.MaxResultBytes, MaxEvents: b.MaxOrdinaryEvents, MaxConcurrent: 4}, Calls: []ToolCall{}}
	for i, node := range out.Review.Nodes {
		cp.Calls = append(cp.Calls, node.Call)
		n := SkillRunNodeResult{NodeID: node.NodeID, Capability: node.Capability, Outcome: "not_started", Reason: "run_aborted"}
		raw, _ := json.Marshal(n)
		if _, err := tx.ExecContext(ctx, `INSERT INTO interaction_skill_node_results VALUES(?,?,?,?)`, id, node.NodeID, i, string(raw)); err != nil {
			return err
		}
	}
	raw, _ := json.Marshal(cp)
	if _, err := tx.ExecContext(ctx, `INSERT INTO interaction_engine(run_id,phase,checkpoint) VALUES(?,?,?)`, id, cp.Phase, string(raw)); err != nil {
		return err
	}
	if err := appendEngineEvent(ctx, tx, run, "run.created", run); err != nil {
		return err
	}
	out.RunID, out.State, out.Reason = id, "admitted", "admitted"
	return nil
}
func (s *Store) SkillRunOperation(ctx context.Context, session, request string) (out SkillRunOperation, err error) {
	var raw string
	err = s.db.QueryRowContext(ctx, `SELECT CASE WHEN r.result='' THEN o.result ELSE r.result END FROM interaction_skill_run_requests r JOIN interaction_skill_run_operations o ON o.id=r.operation_id WHERE r.request_id=? AND r.session_id=?`, request, session).Scan(&raw)
	if err == nil {
		err = json.Unmarshal([]byte(raw), &out)
	}
	return
}
func (s *Store) SkillRun(ctx context.Context, session, id string) (out SkillRunRecord, err error) {
	var raw, phase string
	var terminal bool
	err = s.db.QueryRowContext(ctx, `SELECT s.pin,s.revoked,s.terminal,coalesce(e.phase,'needs_reconciliation') FROM interaction_skill_runs s LEFT JOIN interaction_engine e ON e.run_id=s.run_id WHERE s.run_id=? AND s.session_id=?`, id, session).Scan(&raw, &out.Revoked, &terminal, &phase)
	if err != nil {
		return
	}
	if err = json.Unmarshal([]byte(raw), &out.Pin); err != nil {
		return
	}
	out.SchemaVersion, out.SessionID, out.RunID, out.Execution, out.Evaluation, out.VerificationStatus = 1, session, id, phase, "not_run", "not_evaluated"
	if terminal && out.Revoked {
		out.Execution = "revoked"
	} else if !skillEnum(phase, "queued", "failed", "cancelled", "needs_reconciliation", "completed") {
		out.Execution = "running"
	}
	out.Nodes = []SkillRunNodeResult{}
	rows, e := s.db.QueryContext(ctx, `SELECT result FROM interaction_skill_node_results WHERE run_id=? ORDER BY ordinal`, id)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		var n SkillRunNodeResult
		if err = rows.Scan(&raw); err != nil {
			return
		}
		if err = json.Unmarshal([]byte(raw), &n); err != nil {
			return
		}
		out.Nodes = append(out.Nodes, n)
	}
	return out, rows.Err()
}
func (s *Store) ListSkillRuns(ctx context.Context, session, cursor string, limit int) (out SkillRunPage, err error) {
	out = SkillRunPage{SchemaVersion: 1, SessionID: session, Runs: []SkillRunRecord{}}
	if limit < 1 || limit > 100 {
		return out, ErrInvalid
	}
	var after int64
	if cursor != "" {
		if err = s.db.QueryRowContext(ctx, `SELECT rowid FROM interaction_skill_runs WHERE run_id=? AND session_id=?`, cursor, session).Scan(&after); err != nil {
			return
		}
	}
	rows, e := s.db.QueryContext(ctx, `SELECT run_id FROM interaction_skill_runs WHERE session_id=? AND rowid>? ORDER BY rowid LIMIT ?`, session, after, limit+1)
	if e != nil {
		return out, e
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return
	}
	if len(ids) > limit {
		ids = ids[:limit]
		out.NextCursor = ids[len(ids)-1]
	}
	for _, id := range ids {
		r, e := s.SkillRun(ctx, session, id)
		if e != nil {
			return out, e
		}
		out.Runs = append(out.Runs, r)
	}
	return
}
