package interaction

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"time"
)

type HookIntent struct {
	RequestID  string `json:"request_id"`
	Kind       string `json:"kind"`
	RevisionID string `json:"revision_id"`
	PolicyText string `json:"policy_text"`
}
type HookOperation struct {
	SchemaVersion      int         `json:"schema_version"`
	Namespace          string      `json:"namespace"`
	OperationID        string      `json:"operation_id"`
	SessionID          string      `json:"session_id"`
	Kind               string      `json:"kind"`
	RevisionID         string      `json:"revision_id"`
	ExpectedGeneration int64       `json:"expected_generation"`
	BeforeState        string      `json:"before_state"`
	BeforeRevisionID   string      `json:"before_revision_id"`
	Policy             *HookPolicy `json:"policy"`
	PolicySHA256       string      `json:"policy_sha256"`
	ExpiresAtMS        int64       `json:"expires_at_ms"`
	Digest             string      `json:"digest"`
	State              string      `json:"state"`
	Decision           string      `json:"decision"`
	DecisionRequestID  string      `json:"decision_request_id"`
	Reason             string      `json:"reason"`
}
type HookSelection struct {
	SchemaVersion int    `json:"schema_version"`
	SessionID     string `json:"session_id"`
	Generation    int64  `json:"generation"`
	State         string `json:"state"`
	RevisionID    string `json:"revision_id"`
	PolicySHA256  string `json:"policy_sha256"`
}
type HookRevision struct {
	SchemaVersion int        `json:"schema_version"`
	SessionID     string     `json:"session_id"`
	RevisionID    string     `json:"revision_id"`
	PolicySHA256  string     `json:"policy_sha256"`
	Policy        HookPolicy `json:"policy"`
}
type HookRevisionPage struct {
	SchemaVersion int            `json:"schema_version"`
	SessionID     string         `json:"session_id"`
	Revisions     []HookRevision `json:"revisions"`
	NextCursor    string         `json:"next_cursor"`
}

func hookOperationDigest(p HookOperation) string {
	// The review projection deliberately excludes every mutable decision field.
	return operatorHash(struct {
		SchemaVersion      int         `json:"schema_version"`
		Namespace          string      `json:"namespace"`
		OperationID        string      `json:"operation_id"`
		SessionID          string      `json:"session_id"`
		Kind               string      `json:"kind"`
		RevisionID         string      `json:"revision_id"`
		ExpectedGeneration int64       `json:"expected_generation"`
		BeforeState        string      `json:"before_state"`
		BeforeRevisionID   string      `json:"before_revision_id"`
		Policy             *HookPolicy `json:"policy"`
		PolicySHA256       string      `json:"policy_sha256"`
		ExpiresAtMS        int64       `json:"expires_at_ms"`
	}{p.SchemaVersion, p.Namespace, p.OperationID, p.SessionID, p.Kind, p.RevisionID, p.ExpectedGeneration, p.BeforeState, p.BeforeRevisionID, p.Policy, p.PolicySHA256, p.ExpiresAtMS})
}
func hookSelectionAt(ctx context.Context, q contextQuery, session string) (out HookSelection, err error) {
	out = HookSelection{SchemaVersion: 1, SessionID: session, State: "disabled"}
	var present int
	if err = q.QueryRowContext(ctx, `SELECT count(*) FROM interaction_sessions WHERE id=?`, session).Scan(&present); err != nil {
		return
	}
	if present != 1 {
		return out, sql.ErrNoRows
	}
	err = q.QueryRowContext(ctx, `SELECT generation,state,revision_id FROM interaction_hook_sessions WHERE session_id=?`, session).Scan(&out.Generation, &out.State, &out.RevisionID)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	if err == nil && out.State == "enabled" {
		var r HookRevision
		r, err = hookRevisionAt(ctx, q, session, out.RevisionID)
		out.PolicySHA256 = r.PolicySHA256
	}
	return
}
func hookRevisionAt(ctx context.Context, q contextQuery, session, id string) (out HookRevision, err error) {
	out = HookRevision{SchemaVersion: 1, SessionID: session, RevisionID: id}
	var raw, evaluator string
	err = q.QueryRowContext(ctx, `SELECT policy,policy_hash,evaluator FROM interaction_hook_revisions WHERE id=? AND session_id=?`, id, session).Scan(&raw, &out.PolicySHA256, &evaluator)
	if err != nil {
		return
	}
	var canonical []byte
	out.Policy, canonical, err = ParseHookPolicy([]byte(raw))
	if err != nil || string(canonical) != raw || skillBytesHash(raw) != out.PolicySHA256 || evaluator != HookEvaluator {
		return out, ErrReconciliation
	}
	return
}
func (s *Store) HookSelection(ctx context.Context, session string) (HookSelection, error) {
	return hookSelectionAt(ctx, s.db, session)
}
func (s *Store) HookRevision(ctx context.Context, session, id string) (HookRevision, error) {
	return hookRevisionAt(ctx, s.db, session, id)
}
func hookReadOperation(raw string) (out HookOperation, err error) {
	if hookExactJSON([]byte(raw), &out, 16384) != nil || out.SchemaVersion != 1 || out.Namespace != "runtime-rebuild-v1" || hookOperationDigest(out) != out.Digest {
		return out, ErrReconciliation
	}
	return
}
func hookReplay(ctx context.Context, tx *sql.Tx, session, request, fp string) (out HookOperation, found bool, err error) {
	var owner, old, raw string
	err = tx.QueryRowContext(ctx, `SELECT r.session_id,r.fingerprint,CASE WHEN r.result='' THEN o.result ELSE r.result END FROM interaction_hook_requests r JOIN interaction_hook_operations o ON o.id=r.operation_id WHERE r.request_id=?`, request).Scan(&owner, &old, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return out, false, nil
	}
	if err != nil {
		return
	}
	if owner != session || old != fp {
		return out, true, ErrConflict
	}
	out, err = hookReadOperation(raw)
	return out, true, err
}
func (s *Store) PrepareHooks(ctx context.Context, session string, in HookIntent) (out HookOperation, err error) {
	if err = validateHookIntent(in); err != nil {
		return
	}
	tx, err := s.queueWrite(ctx, session)
	if err != nil {
		return
	}
	defer tx.Rollback()
	fp := operatorHash([]any{session, "prepare_hooks", in})
	if old, found, e := hookReplay(ctx, tx, session, in.RequestID, fp); found || e != nil {
		return old, e
	}
	sel, err := hookSelectionAt(ctx, tx, session)
	if err != nil {
		return
	}
	if err = hookManagementCapacity(ctx, tx, session, in.Kind); err != nil {
		return
	}
	out = HookOperation{SchemaVersion: 1, Namespace: "runtime-rebuild-v1", OperationID: in.RequestID, SessionID: session, Kind: in.Kind, RevisionID: in.RevisionID, ExpectedGeneration: sel.Generation, BeforeState: sel.State, BeforeRevisionID: sel.RevisionID, ExpiresAtMS: time.Now().Add(10 * time.Minute).UnixMilli(), State: "pending", Reason: "ready"}
	switch in.Kind {
	case "import":
		p, b, e := ParseHookPolicy([]byte(in.PolicyText))
		if e != nil {
			return out, e
		}
		out.Policy = &p
		out.PolicySHA256 = skillBytesHash(string(b))
		out.RevisionID = uuid.NewString()
	case "enable":
		r, e := hookRevisionAt(ctx, tx, session, in.RevisionID)
		if e != nil {
			return out, e
		}
		out.Policy = &r.Policy
		out.PolicySHA256 = r.PolicySHA256
	}
	out.Digest = hookOperationDigest(out)
	raw, _ := json.Marshal(out)
	if len(raw) > 16384 {
		return out, ErrBudget
	}
	var exists, n int
	if err = tx.QueryRowContext(ctx, `SELECT count(*),coalesce(sum(session_id=?),0) FROM interaction_hook_sessions`, session).Scan(&n, &exists); err != nil {
		return
	}
	if exists == 0 {
		if n >= 128 {
			return out, ErrBudget
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO interaction_hook_sessions VALUES(?,0,'disabled','')`, session); err != nil {
			return
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO interaction_hook_operations VALUES(?,?,?,?,?,?,'pending',?,65536)`, out.OperationID, session, out.Kind, out.RevisionID, out.ExpiresAtMS, string(raw), string(raw)); err != nil {
		return
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO interaction_hook_requests VALUES(?,?,?,0,?,'')`, in.RequestID, session, out.OperationID, fp); err != nil {
		return
	}
	return out, tx.Commit()
}
func (s *Store) DecideHooks(ctx context.Context, session string, in OperatorDecision) (out HookOperation, err error) {
	if !operatorRequestID(in.RequestID) || !operatorRequestID(in.OperationID) || in.RequestID == in.OperationID || len(in.Digest) != 64 || !skillEnum(in.Decision, "confirm", "cancel") {
		return out, ErrInvalid
	}
	tx, err := s.queueWrite(ctx, session)
	if err != nil {
		return
	}
	defer tx.Rollback()
	fp := operatorHash([]any{session, "decide_hooks", in})
	if old, found, e := hookReplay(ctx, tx, session, in.RequestID, fp); found || e != nil {
		return old, e
	}
	var raw, state string
	if err = tx.QueryRowContext(ctx, `SELECT preview,state FROM interaction_hook_operations WHERE id=? AND session_id=?`, in.OperationID, session).Scan(&raw, &state); err != nil {
		return
	}
	out, err = hookReadOperation(raw)
	if err != nil {
		return
	}
	if state != "pending" || out.Digest != in.Digest {
		return out, ErrConflict
	}
	sel, err := hookSelectionAt(ctx, tx, session)
	if err != nil {
		return
	}
	out.Decision, out.DecisionRequestID = in.Decision, in.RequestID
	switch {
	case in.Decision == "cancel":
		out.State, out.Reason = "cancelled", "user_cancelled"
	case time.Now().UnixMilli() >= out.ExpiresAtMS:
		out.State, out.Reason = "stale", "preview_expired"
	case sel.Generation != out.ExpectedGeneration || sel.State != out.BeforeState || sel.RevisionID != out.BeforeRevisionID:
		out.State, out.Reason = "stale", "selection_changed"
	default:
		out.State = "applied"
		switch out.Kind {
		case "import":
			if out.Policy == nil || validateHookPolicy(*out.Policy, false) != nil || operatorHash(out.Policy) != out.PolicySHA256 {
				return out, ErrReconciliation
			}
			b, _ := json.Marshal(out.Policy)
			_, err = tx.ExecContext(ctx, `INSERT INTO interaction_hook_revisions VALUES(?,?,?,?,?,?)`, out.RevisionID, session, string(b), out.PolicySHA256, HookEvaluator, out.OperationID)
			out.Reason = "imported"
		case "enable", "disable":
			next, revision := "disabled", ""
			if out.Kind == "enable" {
				r, e := hookRevisionAt(ctx, tx, session, out.RevisionID)
				if e != nil || r.PolicySHA256 != out.PolicySHA256 || operatorHash(r.Policy) != operatorHash(out.Policy) {
					return out, ErrReconciliation
				}
				next, revision = "enabled", out.RevisionID
			}
			if sel.State == next && sel.RevisionID == revision {
				out.Reason = "unchanged"
			} else {
				if sel.Generation == int64(^uint64(0)>>1) {
					return out, ErrBudget
				}
				_, err = tx.ExecContext(ctx, `UPDATE interaction_hook_sessions SET generation=generation+1,state=?,revision_id=? WHERE session_id=? AND generation=?`, next, revision, session, sel.Generation)
				out.Reason = next
			}
		default:
			return out, ErrReconciliation
		}
		if err != nil {
			return
		}
	}
	result, _ := json.Marshal(out)
	if len(result) > 16384 {
		return out, ErrBudget
	}
	res, e := tx.ExecContext(ctx, `UPDATE interaction_hook_operations SET state=?,result=? WHERE id=? AND state='pending'`, out.State, string(result), out.OperationID)
	if err = changed(res, e); err != nil {
		return
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO interaction_hook_requests VALUES(?,?,?,1,?,?)`, in.RequestID, session, out.OperationID, fp, string(result)); err != nil {
		return
	}
	audit, _ := json.Marshal(struct {
		Kind       string `json:"kind"`
		State      string `json:"state"`
		Reason     string `json:"reason"`
		RevisionID string `json:"revision_id"`
		Generation int64  `json:"expected_generation"`
	}{out.Kind, out.State, out.Reason, out.RevisionID, out.ExpectedGeneration})
	if _, err = tx.ExecContext(ctx, `INSERT INTO interaction_hook_audits VALUES(?,?,?)`, out.OperationID, session, string(audit)); err != nil {
		return
	}
	return out, tx.Commit()
}
func (s *Store) HookOperation(ctx context.Context, session, request string) (out HookOperation, err error) {
	var raw string
	err = s.db.QueryRowContext(ctx, `SELECT CASE WHEN r.result='' THEN o.result ELSE r.result END FROM interaction_hook_requests r JOIN interaction_hook_operations o ON o.id=r.operation_id WHERE r.request_id=? AND r.session_id=?`, request, session).Scan(&raw)
	if err != nil {
		return
	}
	return hookReadOperation(raw)
}
func (s *Store) ListHookRevisions(ctx context.Context, session, cursor string, limit int) (out HookRevisionPage, err error) {
	out = HookRevisionPage{SchemaVersion: 1, SessionID: session, Revisions: []HookRevision{}}
	if limit < 1 || limit > 32 {
		return out, ErrInvalid
	}
	if _, err = hookSelectionAt(ctx, s.db, session); err != nil {
		return
	}
	var after int64
	if cursor != "" {
		b, e := base64.RawURLEncoding.DecodeString(cursor)
		var pair []string
		if e != nil || json.Unmarshal(b, &pair) != nil || len(pair) != 2 || pair[0] != session || !operatorRequestID(pair[1]) {
			return out, ErrInvalid
		}
		if err = s.db.QueryRowContext(ctx, `SELECT rowid FROM interaction_hook_revisions WHERE id=? AND session_id=?`, pair[1], session).Scan(&after); err != nil {
			return
		}
	}
	rows, e := s.db.QueryContext(ctx, `SELECT id FROM interaction_hook_revisions WHERE session_id=? AND rowid>? ORDER BY rowid LIMIT ?`, session, after, limit+1)
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
		b, _ := json.Marshal([2]string{session, ids[len(ids)-1]})
		out.NextCursor = base64.RawURLEncoding.EncodeToString(b)
	}
	for _, id := range ids {
		r, e := hookRevisionAt(ctx, s.db, session, id)
		if e != nil {
			return out, e
		}
		out.Revisions = append(out.Revisions, r)
	}
	return
}

// SignalRevokedSessionSkillRuns is an optional post-commit latency hint. It
// neither changes durable intent nor creates an execution owner. Lost signals
// cannot restore authority: the existing native claim and finish recheck it.
func (s *Store) SignalRevokedSessionSkillRuns(ctx context.Context, session string) {
	rows, err := s.db.QueryContext(ctx, `SELECT run_id FROM interaction_skill_runs WHERE session_id=? AND revoked=1 AND terminal=0`, session)
	if err != nil {
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return
	}
	activeOwners.Lock()
	defer activeOwners.Unlock()
	for _, id := range ids {
		if cancel := activeOwners.cancels[id]; cancel != nil {
			cancel()
		}
	}
}
