package interaction

import (
	"agent/internal/runtime"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"time"
)

type HookRunSnapshot struct {
	SchemaVersion int        `json:"schema_version"`
	Evaluator     string     `json:"evaluator"`
	Generation    int64      `json:"generation"`
	RevisionID    string     `json:"revision_id"`
	PolicySHA256  string     `json:"policy_sha256"`
	Policy        HookPolicy `json:"policy"`
}
type HookRunPin struct {
	SchemaVersion  int    `json:"schema_version"`
	Evaluator      string `json:"evaluator"`
	Generation     int64  `json:"generation"`
	RevisionID     string `json:"revision_id"`
	PolicySHA256   string `json:"policy_sha256"`
	SnapshotSHA256 string `json:"snapshot_sha256"`
}
type SkillRunOperationV2 struct {
	SchemaVersion      int             `json:"schema_version"`
	Namespace          string          `json:"namespace"`
	OperationID        string          `json:"operation_id"`
	SessionID          string          `json:"session_id"`
	CatalogID          string          `json:"catalog_id"`
	RevisionID         string          `json:"revision_id"`
	ScopeID            string          `json:"scope_id"`
	RootIdentity       string          `json:"root_identity"`
	PolicyDigest       string          `json:"policy_digest"`
	ExpectedGeneration string          `json:"expected_generation"`
	ExpectedRevision   int64           `json:"expected_revision"`
	Review             SkillRunReview  `json:"review"`
	Hooks              HookRunSnapshot `json:"hooks"`
	ExpiresAtMS        int64           `json:"expires_at_ms"`
	Digest             string          `json:"digest"`
	State              string          `json:"state"`
	Decision           string          `json:"decision"`
	DecisionRequestID  string          `json:"decision_request_id"`
	RunID              string          `json:"run_id"`
	Reason             string          `json:"reason"`
}
type SkillRunPinV2 struct {
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
	Hooks             HookRunPin      `json:"hooks"`
}
type SkillRunRecordV2 struct {
	SchemaVersion      int                  `json:"schema_version"`
	SessionID          string               `json:"session_id"`
	RunID              string               `json:"run_id"`
	Pin                SkillRunPinV2        `json:"pin"`
	Execution          string               `json:"execution"`
	Evaluation         string               `json:"evaluation"`
	VerificationStatus string               `json:"verification_status"`
	Revoked            bool                 `json:"revoked"`
	Nodes              []SkillRunNodeResult `json:"nodes"`
}
type SkillRunOperationView struct {
	V1 *SkillRunOperation
	V2 *SkillRunOperationV2
}
type SkillRunView struct {
	V1 *SkillRunRecord
	V2 *SkillRunRecordV2
}
type SkillRunPageV2 struct {
	SchemaVersion int            `json:"schema_version"`
	SessionID     string         `json:"session_id"`
	Runs          []SkillRunView `json:"runs"`
	NextCursor    string         `json:"next_cursor"`
}

func (v SkillRunOperationView) MarshalJSON() ([]byte, error) {
	if v.V1 != nil && v.V2 == nil && v.V1.SchemaVersion == 1 {
		return json.Marshal(v.V1)
	}
	if v.V2 != nil && v.V1 == nil && v.V2.SchemaVersion == 2 {
		return json.Marshal(v.V2)
	}
	return nil, ErrInvalid
}
func (v SkillRunView) MarshalJSON() ([]byte, error) {
	if v.V1 != nil && v.V2 == nil && v.V1.SchemaVersion == 1 && v.V1.Pin.SchemaVersion == 1 {
		return json.Marshal(v.V1)
	}
	if v.V2 != nil && v.V1 == nil && v.V2.SchemaVersion == 2 && v.V2.Pin.SchemaVersion == 2 {
		return json.Marshal(v.V2)
	}
	return nil, ErrInvalid
}

// Internal shared storage state, never reflected into the public v1 protocol.
type skillOperation struct {
	SkillRunOperation
	hooks *HookRunSnapshot
}

func (o skillOperation) view() SkillRunOperationView {
	if o.hooks == nil {
		v := o.SkillRunOperation
		return SkillRunOperationView{V1: &v}
	}
	v := skillOperationV2(o.SkillRunOperation, *o.hooks)
	return SkillRunOperationView{V2: &v}
}
func (o skillOperation) MarshalJSON() ([]byte, error) { return json.Marshal(o.view()) }
func skillOperationDigest(o skillOperation) string    { o.Digest = ""; return operatorHash(o) }
func skillOperationV2(p SkillRunOperation, h HookRunSnapshot) SkillRunOperationV2 {
	return SkillRunOperationV2{SchemaVersion: p.SchemaVersion, Namespace: p.Namespace, OperationID: p.OperationID, SessionID: p.SessionID, CatalogID: p.CatalogID, RevisionID: p.RevisionID, ScopeID: p.ScopeID, RootIdentity: p.RootIdentity, PolicyDigest: p.PolicyDigest, ExpectedGeneration: p.ExpectedGeneration, ExpectedRevision: p.ExpectedRevision, Review: p.Review, ExpiresAtMS: p.ExpiresAtMS, Digest: p.Digest, State: p.State, Decision: p.Decision, DecisionRequestID: p.DecisionRequestID, RunID: p.RunID, Reason: p.Reason, Hooks: h}
}
func baseSkillRunOperation(p SkillRunOperationV2) SkillRunOperation {
	return SkillRunOperation{SchemaVersion: p.SchemaVersion, Namespace: p.Namespace, OperationID: p.OperationID, SessionID: p.SessionID, CatalogID: p.CatalogID, RevisionID: p.RevisionID, ScopeID: p.ScopeID, RootIdentity: p.RootIdentity, PolicyDigest: p.PolicyDigest, ExpectedGeneration: p.ExpectedGeneration, ExpectedRevision: p.ExpectedRevision, Review: p.Review, ExpiresAtMS: p.ExpiresAtMS, Digest: p.Digest, State: p.State, Decision: p.Decision, DecisionRequestID: p.DecisionRequestID, RunID: p.RunID, Reason: p.Reason}
}
func skillPinV2(p SkillRunPin, h HookRunPin) SkillRunPinV2 {
	return SkillRunPinV2{SchemaVersion: p.SchemaVersion, SessionID: p.SessionID, RunID: p.RunID, CatalogID: p.CatalogID, RevisionID: p.RevisionID, CatalogGeneration: p.CatalogGeneration, ScopeID: p.ScopeID, RootIdentity: p.RootIdentity, PolicyDigest: p.PolicyDigest, ArtifactHash: p.ArtifactHash, ContentHash: p.ContentHash, ReviewHash: p.ReviewHash, InputsHash: p.InputsHash, PlanHash: p.PlanHash, ExecutionProfile: p.ExecutionProfile, AdmissionTimeMS: p.AdmissionTimeMS, DeadlineMS: p.DeadlineMS, Budgets: p.Budgets, Hooks: h}
}
func baseSkillRunPin(p SkillRunPinV2) SkillRunPin {
	return SkillRunPin{SchemaVersion: p.SchemaVersion, SessionID: p.SessionID, RunID: p.RunID, CatalogID: p.CatalogID, RevisionID: p.RevisionID, CatalogGeneration: p.CatalogGeneration, ScopeID: p.ScopeID, RootIdentity: p.RootIdentity, PolicyDigest: p.PolicyDigest, ArtifactHash: p.ArtifactHash, ContentHash: p.ContentHash, ReviewHash: p.ReviewHash, InputsHash: p.InputsHash, PlanHash: p.PlanHash, ExecutionProfile: p.ExecutionProfile, AdmissionTimeMS: p.AdmissionTimeMS, DeadlineMS: p.DeadlineMS, Budgets: p.Budgets}
}

// Exact canonical decoding rejects missing/unknown/duplicate fields including
// authority-bearing snapshots; it never treats schema 2 as an optional v1 tail.
func hookExactJSON(raw []byte, out any, limit int) error {
	if _, e := skillJSONObjectLimit(raw, limit); e != nil {
		return ErrReconciliation
	}
	if json.Unmarshal(raw, out) != nil {
		return ErrReconciliation
	}
	canonical, e := json.Marshal(out)
	if e != nil || string(canonical) != string(raw) {
		return ErrReconciliation
	}
	return nil
}
func decodeSkillOperation(raw []byte) (out skillOperation, err error) {
	var v struct {
		SchemaVersion int `json:"schema_version"`
	}
	if json.Unmarshal(raw, &v) != nil {
		return out, ErrReconciliation
	}
	switch v.SchemaVersion {
	case 1:
		err = hookExactJSON(raw, &out.SkillRunOperation, 131072)
	case 2:
		var p SkillRunOperationV2
		err = hookExactJSON(raw, &p, 131072)
		out.SkillRunOperation = baseSkillRunOperation(p)
		out.hooks = &p.Hooks
		if err == nil {
			err = validateHookSnapshot(p.Hooks)
		}
	default:
		err = ErrReconciliation
	}
	return
}
func decodeSkillPin(raw []byte) (pin SkillRunPin, h *HookRunPin, err error) {
	var v struct {
		SchemaVersion int `json:"schema_version"`
	}
	if json.Unmarshal(raw, &v) != nil {
		return pin, nil, ErrReconciliation
	}
	switch v.SchemaVersion {
	case 1:
		err = hookExactJSON(raw, &pin, 8192)
	case 2:
		var p SkillRunPinV2
		err = hookExactJSON(raw, &p, 8192)
		pin = baseSkillRunPin(p)
		h = &p.Hooks
		if h.SchemaVersion != 1 || h.Evaluator != HookEvaluator || h.Generation < 0 || len(h.PolicySHA256) != 64 || len(h.SnapshotSHA256) != 64 {
			err = ErrReconciliation
		}
	default:
		err = ErrReconciliation
	}
	return
}
func hookSnapshotAt(ctx context.Context, q contextQuery, session string) (h HookRunSnapshot, err error) {
	sel, err := hookSelectionAt(ctx, q, session)
	if err != nil {
		return
	}
	h = HookRunSnapshot{SchemaVersion: 1, Evaluator: HookEvaluator, Generation: sel.Generation, RevisionID: sel.RevisionID, Policy: HookPolicy{SchemaVersion: 1, Evaluator: HookEvaluator, Rules: []HookRule{}}}
	if sel.State == "enabled" {
		r, e := hookRevisionAt(ctx, q, session, sel.RevisionID)
		if e != nil {
			return h, e
		}
		h.Policy = r.Policy
	}
	h.PolicySHA256 = operatorHash(h.Policy)
	return
}
func hookPin(h HookRunSnapshot) HookRunPin {
	return HookRunPin{SchemaVersion: 1, Evaluator: HookEvaluator, Generation: h.Generation, RevisionID: h.RevisionID, PolicySHA256: h.PolicySHA256, SnapshotSHA256: operatorHash(h)}
}
func validateHookSnapshot(h HookRunSnapshot) error {
	if h.SchemaVersion != 1 || h.Evaluator != HookEvaluator || h.Generation < 0 || validateHookPolicy(h.Policy, true) != nil || operatorHash(h.Policy) != h.PolicySHA256 || (h.RevisionID == "") != (len(h.Policy.Rules) == 0) {
		return ErrReconciliation
	}
	if h.RevisionID != "" {
		raw, _ := json.Marshal(h.Policy)
		_, canonical, e := ParseHookPolicy(raw)
		if e != nil || string(raw) != string(canonical) {
			return ErrReconciliation
		}
	}
	return nil
}
func validateHookSnapshotAt(ctx context.Context, q contextQuery, session string, h HookRunSnapshot) error {
	if e := validateHookSnapshot(h); e != nil {
		return e
	}
	if h.RevisionID != "" {
		r, e := hookRevisionAt(ctx, q, session, h.RevisionID)
		if e != nil || r.PolicySHA256 != h.PolicySHA256 || !reflect.DeepEqual(r.Policy, h.Policy) {
			return ErrReconciliation
		}
	}
	return nil
}
func hookSnapshotMatches(h HookRunSnapshot, sel HookSelection) bool {
	return h.Generation == sel.Generation && h.RevisionID == sel.RevisionID && ((sel.State == "disabled" && len(h.Policy.Rules) == 0) || (sel.State == "enabled" && h.PolicySHA256 == sel.PolicySHA256))
}
func insertHookSnapshot(ctx context.Context, tx *sql.Tx, id, session string, h HookRunSnapshot, pinHash string) error {
	raw, _ := json.Marshal(h)
	_, e := tx.ExecContext(ctx, `INSERT INTO interaction_skill_run_hooks VALUES(?,?,?,?,?,163840)`, id, session, string(raw), operatorHash(h), pinHash)
	return e
}
func hookRunSnapshotAt(ctx context.Context, q contextQuery, id string) (h HookRunSnapshot, has bool, err error) {
	var pinRaw, session, pinHash string
	if err = q.QueryRowContext(ctx, `SELECT pin,session_id,pin_hash FROM interaction_skill_runs WHERE run_id=?`, id).Scan(&pinRaw, &session, &pinHash); err != nil {
		return
	}
	pin, hp, e := decodeSkillPin([]byte(pinRaw))
	if e != nil {
		return h, false, e
	}
	if hp == nil {
		var count int
		if err = q.QueryRowContext(ctx, `SELECT count(*) FROM interaction_skill_run_hooks WHERE run_id=?`, id).Scan(&count); err != nil {
			return
		}
		if count != 0 {
			return h, false, ErrReconciliation
		}
		return h, false, nil
	}
	var raw, storedSession, hash, binding string
	err = q.QueryRowContext(ctx, `SELECT snapshot,session_id,snapshot_hash,pin_hash FROM interaction_skill_run_hooks WHERE run_id=?`, id).Scan(&raw, &storedSession, &hash, &binding)
	if err != nil {
		return h, true, ErrReconciliation
	}
	if hookExactJSON([]byte(raw), &h, 12288) != nil || storedSession != session || pin.SessionID != session || hash != operatorHash(h) || binding != pinHash || !reflect.DeepEqual(*hp, hookPin(h)) || validateHookSnapshotAt(ctx, q, session, h) != nil {
		return h, true, ErrReconciliation
	}
	return h, true, nil
}
func (s *Store) PrepareSkillRun(ctx context.Context, session string, in SkillRunIntent) (SkillRunOperation, error) {
	p, e := s.prepareSkillRun(ctx, session, in, false)
	return p.SkillRunOperation, e
}
func (s *Store) PrepareSkillRunWithHooks(ctx context.Context, session string, in SkillRunIntent) (SkillRunOperationV2, error) {
	p, e := s.prepareSkillRun(ctx, session, in, true)
	if e != nil {
		return SkillRunOperationV2{}, e
	}
	if p.hooks == nil {
		return SkillRunOperationV2{}, ErrConflict
	}
	return skillOperationV2(p.SkillRunOperation, *p.hooks), nil
}
func (s *Store) DecideSkillRun(ctx context.Context, session string, in OperatorDecision) (SkillRunOperation, error) {
	p, e := s.decideSkillRun(ctx, session, in, false)
	return p.SkillRunOperation, e
}
func (s *Store) DecideSkillRunWithHooks(ctx context.Context, session string, in OperatorDecision) (SkillRunOperationView, error) {
	p, e := s.decideSkillRun(ctx, session, in, true)
	if e != nil {
		return SkillRunOperationView{}, e
	}
	return p.view(), nil
}
func (s *Store) SkillRunOperationWithHooks(ctx context.Context, session, request string) (SkillRunOperationView, error) {
	var raw string
	e := s.db.QueryRowContext(ctx, `SELECT CASE WHEN r.result='' THEN o.result ELSE r.result END FROM interaction_skill_run_requests r JOIN interaction_skill_run_operations o ON o.id=r.operation_id WHERE r.request_id=? AND r.session_id=?`, request, session).Scan(&raw)
	if e != nil {
		return SkillRunOperationView{}, e
	}
	p, e := decodeSkillOperation([]byte(raw))
	return p.view(), e
}
func (s *Store) SkillRun(ctx context.Context, session, id string) (SkillRunRecord, error) {
	p, h, e := s.skillRunRecord(ctx, session, id)
	if e == nil && h != nil {
		return SkillRunRecord{}, ErrConflict
	}
	return p, e
}
func (s *Store) SkillRunWithHooks(ctx context.Context, session, id string) (SkillRunView, error) {
	r, h, e := s.skillRunRecord(ctx, session, id)
	if e != nil {
		return SkillRunView{}, e
	}
	if h == nil {
		return SkillRunView{V1: &r}, nil
	}
	if _, _, _, e = skillRunData(ctx, s.db, id); e != nil {
		return SkillRunView{}, e
	}
	v := SkillRunRecordV2{SchemaVersion: 2, SessionID: r.SessionID, RunID: r.RunID, Pin: skillPinV2(r.Pin, *h), Execution: r.Execution, Evaluation: r.Evaluation, VerificationStatus: r.VerificationStatus, Revoked: r.Revoked, Nodes: r.Nodes}
	return SkillRunView{V2: &v}, nil
}
func (s *Store) ListSkillRunsWithHooks(ctx context.Context, session, cursor string, limit int) (out SkillRunPageV2, err error) {
	out = SkillRunPageV2{SchemaVersion: 2, SessionID: session, Runs: []SkillRunView{}}
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
		r, e := s.SkillRunWithHooks(ctx, session, id)
		if e != nil {
			return out, e
		}
		out.Runs = append(out.Runs, r)
	}
	return
}

var ErrHookDenied = errors.New("skill node stopped by Hook policy")

type HookReceipt struct {
	SchemaVersion  int         `json:"schema_version"`
	ReceiptID      string      `json:"receipt_id"`
	SessionID      string      `json:"session_id"`
	RunID          string      `json:"run_id"`
	Event          string      `json:"event"`
	NodeID         string      `json:"node_id"`
	ProposalID     string      `json:"proposal_id"`
	ProposalDigest string      `json:"proposal_digest"`
	SnapshotSHA256 string      `json:"snapshot_sha256"`
	RevisionID     string      `json:"revision_id"`
	Capability     string      `json:"capability"`
	Status         string      `json:"status"`
	Decision       string      `json:"decision"`
	Matches        []HookMatch `json:"matches"`
	Reason         string      `json:"reason"`
}
type HookReceiptList struct {
	SchemaVersion int           `json:"schema_version"`
	SessionID     string        `json:"session_id"`
	RunID         string        `json:"run_id"`
	Receipts      []HookReceipt `json:"receipts"`
}

func hookReceipt(h HookRunSnapshot, session, id string, m HookMetadata, p ToolProposal) HookReceipt {
	r := HookReceipt{SchemaVersion: 1, SessionID: session, RunID: id, Event: m.Event, NodeID: m.NodeID, ProposalID: p.ID, ProposalDigest: p.Digest, SnapshotSHA256: operatorHash(h), RevisionID: h.RevisionID, Capability: m.Capability, Status: m.Status, Matches: []HookMatch{}}
	sentinel := m.NodeID
	if m.Event == "run_terminal" {
		sentinel = "terminal"
	}
	r.ReceiptID = operatorHash([]string{"declarative-hooks-v1:receipt", session, id, r.SnapshotSHA256, m.Event, sentinel, p.ID, p.Digest})
	result, e := EvaluateHookPolicy(h.Policy, m)
	if e != nil {
		r.Decision, r.Reason = "evaluation_error", "hook_evaluation_failed"
	} else {
		r.Decision, r.Reason, r.Matches = result.Decision, result.Reason, result.Matches
	}
	return r
}
func hookReceiptBytes(r HookReceipt, raw []byte) int {
	return len(r.RunID) + len(r.SessionID) + len(r.Event) + len(r.NodeID) + len(r.ReceiptID) + len(r.ProposalID) + len(r.ProposalDigest) + len(r.SnapshotSHA256) + len(raw) + 128
}
func hookReceiptAt(ctx context.Context, q contextQuery, id, event, node string) (r HookReceipt, err error) {
	var raw string
	err = q.QueryRowContext(ctx, `SELECT payload FROM interaction_skill_hook_receipts WHERE run_id=? AND event=? AND node_id=?`, id, event, node).Scan(&raw)
	if err != nil {
		return
	}
	err = hookExactJSON([]byte(raw), &r, 4096)
	return
}
func writeHookReceipt(ctx context.Context, tx *sql.Tx, r HookReceipt, ordinal int) error {
	raw, _ := json.Marshal(r)
	if hookReceiptBytes(r, raw) > 4096 || len(r.Matches) > 8 {
		return ErrBudget
	}
	old, e := hookReceiptAt(ctx, tx, r.RunID, r.Event, r.NodeID)
	if e == nil {
		if !reflect.DeepEqual(old, r) {
			return ErrReconciliation
		}
		return nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO interaction_skill_hook_receipts VALUES(?,?,?,?,?,?,?,?,?,?,?)`, r.RunID, r.SessionID, r.Event, r.NodeID, ordinal, r.ReceiptID, r.ProposalID, r.ProposalDigest, r.SnapshotSHA256, string(raw), hookReceiptBytes(r, raw))
	return e
}
func (s *Store) SkillHookReceipts(ctx context.Context, session, id string) (out HookReceiptList, err error) {
	out = HookReceiptList{SchemaVersion: 1, SessionID: session, RunID: id, Receipts: []HookReceipt{}}
	var owner string
	if err = s.db.QueryRowContext(ctx, `SELECT session_id FROM interaction_skill_runs WHERE run_id=? AND session_id=?`, id, session).Scan(&owner); err != nil {
		return
	}
	if _, _, err = hookRunSnapshotAt(ctx, s.db, id); err != nil {
		return
	}
	rows, e := s.db.QueryContext(ctx, `SELECT payload FROM interaction_skill_hook_receipts WHERE run_id=? ORDER BY ordinal,CASE event WHEN 'before_node' THEN 0 WHEN 'after_node' THEN 1 ELSE 2 END LIMIT 34`, id)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		var r HookReceipt
		if err = rows.Scan(&raw); err != nil {
			return
		}
		if err = hookExactJSON([]byte(raw), &r, 4096); err != nil {
			return
		}
		if r.RunID != id || r.SessionID != session || len(out.Receipts) >= 33 {
			return out, ErrReconciliation
		}
		out.Receipts = append(out.Receipts, r)
	}
	return out, rows.Err()
}

// Hook authority only restricts independently valid Skill authority.
func hookCurrentAuthority(ctx context.Context, q contextQuery, id string, pin SkillRunPin) error {
	h, has, e := hookRunSnapshotAt(ctx, q, id)
	if e != nil {
		return e
	}
	sel, e := hookSelectionAt(ctx, q, pin.SessionID)
	if e != nil {
		return e
	}
	if !has {
		if sel.Generation != 0 {
			return context.Canceled
		}
	} else if !hookSnapshotMatches(h, sel) {
		return context.Canceled
	}
	return nil
}
func hookBeforeEvidence(ctx context.Context, q contextQuery, id string, p ToolProposal, requireJournal bool) error {
	h, has, e := hookRunSnapshotAt(ctx, q, id)
	if e != nil || !has {
		return e
	}
	var session string
	if e = q.QueryRowContext(ctx, `SELECT session_id FROM interaction_skill_runs WHERE run_id=?`, id).Scan(&session); e != nil {
		return e
	}
	capability := "workspace.read"
	if p.Call.Name == "search_files" {
		capability = "workspace.search"
	}
	var n SkillRunNodeResult
	var raw string
	if e = q.QueryRowContext(ctx, `SELECT result FROM interaction_skill_node_results WHERE run_id=? AND node_id=?`, id, p.Call.ID).Scan(&raw); e != nil || json.Unmarshal([]byte(raw), &n) != nil {
		return ErrReconciliation
	}
	capability = n.Capability
	expected := hookReceipt(h, session, id, HookMetadata{"before_node", p.Call.ID, capability, "ready"}, p)
	actual, e := hookReceiptAt(ctx, q, id, "before_node", p.Call.ID)
	if e != nil || expected.Decision != "no_restriction" || !reflect.DeepEqual(actual, expected) {
		return ErrReconciliation
	}
	if requireJournal {
		var workflow, node, cap string
		if e = q.QueryRowContext(ctx, `SELECT workflow_id,node_id,capability_id FROM side_effect_records WHERE idempotency_key=?`, effectKey(id, p.ID)).Scan(&workflow, &node, &cap); e != nil || workflow != id || node != p.ID || cap != p.Call.Name {
			return ErrReconciliation
		}
	}
	return nil
}
func (e *Engine) startSkillNode(ctx context.Context, r Run, owner string, cp *Checkpoint) error {
	tx, err := e.store.queueWrite(ctx, r.SessionID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	durable, err := persistedSkillCheckpoint(ctx, tx, r.ID)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(durable, *cp) || len(cp.Pending) != 1 || cp.Phase == PhaseExecuting || cp.Phase == PhaseReconciliation {
		return ErrReconciliation
	}
	var storedOwner string
	var cancelled bool
	if err = tx.QueryRowContext(ctx, `SELECT owner,cancel_requested FROM interaction_engine WHERE run_id=?`, r.ID).Scan(&storedOwner, &cancelled); err != nil {
		return err
	}
	if storedOwner != owner {
		return ErrOwned
	}
	pin, plan, revoked, err := skillRunData(ctx, tx, r.ID)
	if err != nil {
		return err
	}
	if revoked || cancelled {
		return context.Canceled
	}
	if err = hookCurrentAuthority(ctx, tx, r.ID, pin); err != nil {
		return err
	}
	if time.Now().UnixMilli() >= pin.DeadlineMS || cp.Steps >= pin.Budgets.MaxAttempts || cp.Events >= cp.Budgets.MaxEvents {
		return ErrBudget
	}
	w, policy, err := skillRunWorkspace(ctx, tx, r.SessionID)
	if err != nil {
		return err
	}
	if w.ScopeID != pin.ScopeID || w.RootIdentity != pin.RootIdentity || policy != pin.PolicyDigest {
		return ErrConflict
	}
	cat, err := skillCatalogAt(ctx, tx, pin.CatalogID)
	if err != nil || cat.State != "enabled" || cat.SelectedRevision != pin.RevisionID || cat.Generation != pin.CatalogGeneration {
		return ErrConflict
	}
	var controls int
	if err = tx.QueryRowContext(ctx, `SELECT control_events FROM interaction_skill_runs WHERE run_id=?`, r.ID).Scan(&controls); err != nil {
		return err
	}
	if controls >= 60 {
		return ErrBudget
	}
	p := cp.Pending[0]
	index := nodeIndex(plan, p.Call.ID)
	if index < 0 || !reflect.DeepEqual(p.Call, plan[index].Call) {
		return ErrReconciliation
	}
	h, has, err := hookRunSnapshotAt(ctx, tx, r.ID)
	if err != nil {
		return err
	}
	if !has {
		return ErrReconciliation
	}
	// Existing claims, even without a receipt, are never interpreted as retryable.
	var claims, executed int
	if err = tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM side_effect_records WHERE idempotency_key=? OR (workflow_id=? AND node_id=?)),executed FROM interaction_tool_proposals WHERE id=? AND run_id=?`, effectKey(r.ID, p.ID), r.ID, p.ID, p.ID, r.ID).Scan(&claims, &executed); err != nil {
		return err
	}
	if claims != 0 || executed != 0 {
		return ErrReconciliation
	}
	var existingReceipt int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM interaction_skill_hook_receipts WHERE run_id=? AND event='before_node' AND node_id=?`, r.ID, p.Call.ID).Scan(&existingReceipt); err != nil {
		return err
	}
	if existingReceipt != 0 {
		return ErrReconciliation
	}
	receipt := hookReceipt(h, r.SessionID, r.ID, HookMetadata{"before_node", p.Call.ID, plan[index].Capability, "ready"}, p)
	if err = writeHookReceipt(ctx, tx, receipt, index); err != nil {
		return err
	}
	if receipt.Decision != "no_restriction" {
		reason := "hook_denied"
		if receipt.Decision == "evaluation_error" {
			reason = "hook_evaluation_failed"
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO interaction_skill_hook_stops VALUES(?,?,?,?,?,?)`, r.ID, p.Call.ID, p.ID, p.Digest, receipt.ReceiptID, reason); err != nil {
			return err
		}
		n := SkillRunNodeResult{NodeID: p.Call.ID, Capability: plan[index].Capability, Outcome: "not_started", Reason: reason}
		raw, _ := json.Marshal(n)
		if _, err = tx.ExecContext(ctx, `UPDATE interaction_skill_node_results SET result=? WHERE run_id=? AND node_id=?`, string(raw), r.ID, p.Call.ID); err != nil {
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		return ErrHookDenied
	}
	next := *cp
	next.Steps++
	next.Events++
	next.Phase = PhaseExecuting
	raw, _ := json.Marshal(next)
	res, err := tx.ExecContext(ctx, `UPDATE interaction_engine SET phase=?,checkpoint=? WHERE run_id=? AND owner=? AND cancel_requested=0`, next.Phase, string(raw), r.ID, owner)
	if err = changed(res, err); err != nil {
		return err
	}
	if err = appendEngineEvent(ctx, tx, r, "tool.started", p); err != nil {
		return err
	}
	_, claimed, err := runtime.ClaimSideEffectTx(ctx, tx, runtime.SideEffectRecord{IdempotencyKey: effectKey(r.ID, p.ID), WorkflowID: r.ID, NodeID: p.ID, CapabilityID: p.Call.Name})
	if err != nil {
		return err
	}
	if !claimed {
		return ErrReconciliation
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	*cp = next
	return nil
}

// A committed known stop is validated independently of a tool result. The
// pending proposal stays intact and no observation is fabricated during recovery.
func hookStopAt(ctx context.Context, q contextQuery, id string) (stopped bool, err error) {
	var node, proposal, digest, receiptID, reason string
	err = q.QueryRowContext(ctx, `SELECT node_id,proposal_id,proposal_digest,receipt_id,reason FROM interaction_skill_hook_stops WHERE run_id=?`, id).Scan(&node, &proposal, &digest, &receiptID, &reason)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	h, has, e := hookRunSnapshotAt(ctx, q, id)
	if e != nil || !has {
		return false, ErrReconciliation
	}
	var cp Checkpoint
	var raw, session string
	var executed, claims int
	if e = q.QueryRowContext(ctx, `SELECT e.checkpoint,s.session_id,p.executed,(SELECT count(*) FROM side_effect_records WHERE idempotency_key=? OR (workflow_id=? AND node_id=?)) FROM interaction_engine e JOIN interaction_skill_runs s ON s.run_id=e.run_id JOIN interaction_tool_proposals p ON p.id=? AND p.run_id=e.run_id WHERE e.run_id=?`, effectKey(id, proposal), id, proposal, proposal, id).Scan(&raw, &session, &executed, &claims); e != nil || json.Unmarshal([]byte(raw), &cp) != nil || len(cp.Pending) != 1 || executed != 0 || claims != 0 {
		return false, ErrReconciliation
	}
	p := cp.Pending[0]
	if p.ID != proposal || p.Digest != digest || p.Call.ID != node {
		return false, ErrReconciliation
	}
	var n SkillRunNodeResult
	if e = q.QueryRowContext(ctx, `SELECT result FROM interaction_skill_node_results WHERE run_id=? AND node_id=?`, id, node).Scan(&raw); e != nil || json.Unmarshal([]byte(raw), &n) != nil || n.Outcome != "not_started" || n.Result != nil || n.ProposalID != "" || n.Reason != reason {
		return false, ErrReconciliation
	}
	expected := hookReceipt(h, session, id, HookMetadata{"before_node", node, n.Capability, "ready"}, p)
	got, e := hookReceiptAt(ctx, q, id, "before_node", node)
	if e != nil || !reflect.DeepEqual(expected, got) || got.ReceiptID != receiptID || !skillEnum(got.Decision, "deny", "evaluation_error") || (got.Decision == "deny") != (reason == "hook_denied") {
		return false, ErrReconciliation
	}
	var ordinal int
	if e = q.QueryRowContext(ctx, `SELECT ordinal FROM interaction_skill_node_results WHERE run_id=? AND node_id=?`, id, node).Scan(&ordinal); e != nil || cp.Steps != ordinal {
		return false, ErrReconciliation
	}
	return true, nil
}

func hookNodeObservation(ctx context.Context, tx *sql.Tx, id string, o ToolObservation) error {
	h, has, err := hookRunSnapshotAt(ctx, tx, id)
	if err != nil || !has {
		return err
	}
	var raw, session, status, result string
	var ordinal int
	if err = tx.QueryRowContext(ctx, `SELECT p.prepared,s.session_id,n.ordinal,j.status,j.result_ref FROM interaction_tool_proposals p JOIN interaction_skill_runs s ON s.run_id=p.run_id JOIN interaction_skill_node_results n ON n.run_id=p.run_id AND n.node_id=? JOIN side_effect_records j ON j.idempotency_key=? AND j.workflow_id=p.run_id AND j.node_id=p.id AND j.capability_id=json_extract(p.prepared,'$.Proposal.Call.Name') WHERE p.id=? AND p.run_id=?`, o.CallID, effectKey(id, o.ProposalID), o.ProposalID, id).Scan(&raw, &session, &ordinal, &status, &result); err != nil {
		return ErrReconciliation
	}
	var p preparedTool
	var actual ToolResult
	if json.Unmarshal([]byte(raw), &p) != nil || json.Unmarshal([]byte(result), &actual) != nil || status != "completed" || !reflect.DeepEqual(actual, o.ToolResult) || p.Proposal.Call.ID != o.CallID {
		return ErrReconciliation
	}
	if err = hookBeforeEvidence(ctx, tx, id, p.Proposal, true); err != nil {
		return err
	}
	var n SkillRunNodeResult
	if err = tx.QueryRowContext(ctx, `SELECT result FROM interaction_skill_node_results WHERE run_id=? AND node_id=?`, id, o.CallID).Scan(&raw); err != nil || json.Unmarshal([]byte(raw), &n) != nil {
		return ErrReconciliation
	}
	observed := "success"
	if o.IsError {
		observed = "failed"
	}
	receipt := hookReceipt(h, session, id, HookMetadata{"after_node", o.CallID, n.Capability, observed}, p.Proposal)
	return writeHookReceipt(ctx, tx, receipt, ordinal)
}
func hookRunTerminal(ctx context.Context, tx *sql.Tx, id, finalStatus string) error {
	h, has, err := hookRunSnapshotAt(ctx, tx, id)
	if err != nil || !has {
		return err
	}
	if !skillEnum(finalStatus, "completed", "failed", "cancelled", "revoked") {
		return ErrReconciliation
	}
	var session string
	if err = tx.QueryRowContext(ctx, `SELECT session_id FROM interaction_skill_runs WHERE run_id=?`, id).Scan(&session); err != nil {
		return err
	}
	return writeHookReceipt(ctx, tx, hookReceipt(h, session, id, HookMetadata{Event: "run_terminal", Status: finalStatus}, ToolProposal{}), 16)
}
func validateHookCheckpointAt(ctx context.Context, q contextQuery, id string, cp Checkpoint, plan []SkillRunNode) error {
	h, has, err := hookRunSnapshotAt(ctx, q, id)
	if err != nil || !has {
		return err
	}
	stopped, err := hookStopAt(ctx, q, id)
	if err != nil {
		return err
	}
	var session string
	if err = q.QueryRowContext(ctx, `SELECT session_id FROM interaction_skill_runs WHERE run_id=?`, id).Scan(&session); err != nil {
		return err
	}
	for _, node := range plan {
		var raw string
		var n SkillRunNodeResult
		if err = q.QueryRowContext(ctx, `SELECT result FROM interaction_skill_node_results WHERE run_id=? AND node_id=?`, id, node.NodeID).Scan(&raw); err != nil || json.Unmarshal([]byte(raw), &n) != nil {
			return ErrReconciliation
		}
		if skillEnum(n.Outcome, "success", "failed") {
			var prepared string
			if err = q.QueryRowContext(ctx, `SELECT prepared FROM interaction_tool_proposals WHERE id=? AND run_id=?`, n.ProposalID, id).Scan(&prepared); err != nil {
				return ErrReconciliation
			}
			var p preparedTool
			if json.Unmarshal([]byte(prepared), &p) != nil {
				return ErrReconciliation
			}
			if err = hookBeforeEvidence(ctx, q, id, p.Proposal, true); err != nil {
				return err
			}
			expected := hookReceipt(h, session, id, HookMetadata{"after_node", node.NodeID, node.Capability, n.Outcome}, p.Proposal)
			actual, e := hookReceiptAt(ctx, q, id, "after_node", node.NodeID)
			if e != nil || !reflect.DeepEqual(actual, expected) {
				return ErrReconciliation
			}
		} else {
			if n.Outcome == "not_started" && (n.Result != nil || n.ProposalID != "") {
				return ErrReconciliation
			}
			if skillEnum(n.Reason, "hook_denied", "hook_evaluation_failed") && !stopped {
				return ErrReconciliation
			}
			var after int
			if err = q.QueryRowContext(ctx, `SELECT count(*) FROM interaction_skill_hook_receipts WHERE run_id=? AND event='after_node' AND node_id=?`, id, node.NodeID).Scan(&after); err != nil {
				return err
			}
			if after != 0 {
				return ErrReconciliation
			}
		}
	}
	if len(cp.Pending) == 1 && !stopped {
		p := cp.Pending[0]
		var claims int
		if err = q.QueryRowContext(ctx, `SELECT count(*) FROM side_effect_records WHERE idempotency_key=?`, effectKey(id, p.ID)).Scan(&claims); err != nil {
			return err
		}
		if claims != 0 || skillEnum(string(cp.Phase), "executing", "needs_reconciliation") {
			if err = hookBeforeEvidence(ctx, q, id, p, true); err != nil {
				return err
			}
		}
	}
	return nil
}
