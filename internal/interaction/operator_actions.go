package interaction

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"strings"
	"time"
	"unicode/utf8"
)

// OperatorIntent is metadata authority, never tool or workspace authority.
type OperatorIntent struct {
	RequestID string `json:"request_id"`
	Kind      string `json:"kind"`
	TargetID  string `json:"target_id"`
	NewValue  string `json:"new_value"`
}
type OperatorDecision struct {
	RequestID   string `json:"request_id"`
	OperationID string `json:"operation_id"`
	Digest      string `json:"digest"`
	Decision    string `json:"decision"`
}
type OperatorOperation struct {
	ExecutionClass    string `json:"execution_class,omitempty"`
	SchemaVersion     int    `json:"schema_version"`
	Namespace         string `json:"namespace"`
	OperationID       string `json:"operation_id"`
	SessionID         string `json:"session_id"`
	ScopeID           string `json:"scope_id"`
	PolicyDigest      string `json:"policy_digest"`
	Kind              string `json:"kind"`
	TargetID          string `json:"target_id"`
	OldValue          string `json:"old_value"`
	NewValue          string `json:"new_value"`
	Revision          string `json:"revision"`
	ExpiresAtMS       int64  `json:"expires_at_ms"`
	Digest            string `json:"digest"`
	State             string `json:"state"`
	Decision          string `json:"decision"`
	DecisionRequestID string `json:"decision_request_id"`
	Reason            string `json:"reason"`
}

func operatorHash(v any) string { b, _ := json.Marshal(v); return fmt.Sprintf("%x", sha256.Sum256(b)) }
func operatorRequestID(s string) bool {
	if len(s) == 0 || len(s) > 128 || s == "." || s == ".." {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}
func operatorDigest(p OperatorOperation) string { p.Digest = ""; return operatorHash(p) }
func operatorTarget(ctx context.Context, tx *sql.Tx, kind, id string) (value, revision string, err error) {
	var row int64
	var extra string
	switch kind {
	case "project.rename", "project.archive", "project.restore":
		var name, generation, state string
		var revision int64
		err = tx.QueryRowContext(ctx, `SELECT p.name,l.generation,l.revision,l.state FROM projects p JOIN project_catalog_lifecycle l ON l.project_id=p.id WHERE p.id=? AND length(CAST(p.name AS BLOB))<=2048`, id).Scan(&name, &generation, &revision, &state)
		if err != nil {
			return "", "", err
		}
		if !utf8.ValidString(name) || generation == "" || revision < 1 || (state != "active" && state != "archived") {
			return "", "", ErrInvalid
		}
		value = state
		if kind == "project.rename" {
			value = name
		}
		return value, operatorHash([]any{"project_catalog", id, generation, revision, name, state}), nil
	case "notification.mark-read":
		err = tx.QueryRowContext(ctx, `SELECT rowid,status,coalesce(CAST(read_at AS TEXT),'') || ':' || CAST(created_at AS TEXT) FROM notification_inbox WHERE id=?`, id).Scan(&row, &value, &extra)
		if err == nil && value != "unread" && value != "read" {
			err = ErrInvalid
		}
	default:
		err = ErrInvalid
	}
	if err != nil {
		return "", "", err
	}
	return value, operatorHash([]any{kind, id, row, value, extra}), nil
}
func operatorAuthority(ctx context.Context, tx *sql.Tx, session string) (scope, policy string, err error) {
	raw, e := queuePolicy(ctx, tx, session)
	if e != nil {
		return "", "", e
	}
	e = tx.QueryRowContext(ctx, `SELECT coalesce((SELECT scope_id FROM interaction_workspaces WHERE session_id=?),'')`, session).Scan(&scope)
	return scope, operatorHash(raw), e
}
func operatorAudit(ctx context.Context, tx *sql.Tx, p OperatorOperation) error {
	// Exact old/new values live in the protected operation, not copied into audit labels.
	_, e := tx.ExecContext(ctx, `INSERT INTO security_audit_log(id,actor,action,target,status,reason,evidence_ids,created_at) VALUES(?,?,?,?,?,?,?,?)`, "operator:"+p.OperationID+":"+p.State, "session:"+p.SessionID, p.Kind, p.TargetID, p.State, p.Reason, "[\""+p.OperationID+"\"]", time.Now().UTC())
	return e
}
func (s *Store) PrepareOperator(ctx context.Context, session string, in OperatorIntent) (OperatorOperation, error) {
	return s.prepareOperator(ctx, session, in, time.Now())
}
func (s *Store) prepareOperator(ctx context.Context, session string, in OperatorIntent, now time.Time) (out OperatorOperation, err error) {
	if !operatorRequestID(in.RequestID) || (in.Kind == "project.create" && in.TargetID != "") || (in.Kind != "project.create" && in.TargetID == "") || len(in.TargetID) > 512 || !utf8.ValidString(in.TargetID) || strings.ContainsRune(in.TargetID, 0) || !utf8.ValidString(in.NewValue) || len(in.NewValue) > 2048 {
		return out, ErrInvalid
	}
	if in.Kind == "project.archive" || in.Kind == "project.restore" {
		if in.Kind == "project.archive" && in.NewValue != "archived" || in.Kind == "project.restore" && in.NewValue != "active" {
			return out, ErrInvalid
		}
	} else if in.Kind == "notification.mark-read" {
		if in.NewValue != "read" {
			return out, ErrInvalid
		}
	} else if (in.Kind != "project.rename" && in.Kind != "project.create") || strings.TrimSpace(in.NewValue) == "" {
		return out, ErrInvalid
	}
	tx, e := s.queueWrite(ctx, session)
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	fp := operatorHash([]any{session, "prepare", in})
	if old, found, e := operatorReplay(ctx, tx, session, in.RequestID, fp); e != nil || found {
		return old, e
	}
	scope, policy, e := operatorAuthority(ctx, tx, session)
	if e != nil {
		return out, e
	}
	target := in.TargetID
	var value, revision string
	if in.Kind == "project.create" {
		if e = operatorCreateCapacity(ctx, tx, now, true); e != nil {
			return out, e
		}
		target = "project-" + uuid.NewString()
		if e = operatorCreateVacant(ctx, tx, target); e != nil {
			return out, e
		}
		revision = operatorHash([]string{"project.create", target, "metadata_only", "local_private", "[]", "", "{}"})
	} else {
		value, revision, e = operatorTarget(ctx, tx, in.Kind, target)
		if e != nil {
			return out, e
		}
	}
	out = OperatorOperation{SchemaVersion: 1, Namespace: "runtime-rebuild-v1", OperationID: in.RequestID, SessionID: session, ScopeID: scope, PolicyDigest: policy, Kind: in.Kind, TargetID: target, OldValue: value, NewValue: in.NewValue, Revision: revision, ExpiresAtMS: now.Add(10 * time.Minute).UnixMilli(), State: "pending", Reason: "Prepared only; no target change. Exact explicit confirmation required."}
	if in.Kind == "project.create" {
		out.ExecutionClass = "metadata_only"
		out.Reason = "Prepared metadata_only project only; no project exists before confirmation. " + operatorCreateNotice
	}
	out.Digest = operatorDigest(out)
	raw, _ := json.Marshal(out)
	if _, e = tx.ExecContext(ctx, `INSERT INTO interaction_operator_operations VALUES(?,?,?,'pending',?)`, out.OperationID, session, string(raw), string(raw)); e != nil {
		return out, e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO interaction_operator_requests VALUES(?,?,?,?, '')`, in.RequestID, session, out.OperationID, fp); e != nil {
		return out, e
	}
	if e = operatorAudit(ctx, tx, out); e != nil {
		return out, e
	}
	return out, tx.Commit()
}
func operatorReplay(ctx context.Context, tx *sql.Tx, session, request, fp string) (out OperatorOperation, found bool, err error) {
	var owner, old, raw string
	e := tx.QueryRowContext(ctx, `SELECT r.session_id,r.fingerprint,CASE WHEN r.result='' THEN o.result ELSE r.result END FROM interaction_operator_requests r JOIN interaction_operator_operations o ON o.id=r.operation_id WHERE r.request_id=?`, request).Scan(&owner, &old, &raw)
	if errors.Is(e, sql.ErrNoRows) {
		return out, false, nil
	}
	if e != nil {
		return out, false, e
	}
	if owner != session || old != fp {
		return out, true, ErrConflict
	}
	e = json.Unmarshal([]byte(raw), &out)
	return out, true, e
}
func (s *Store) OperatorOperation(ctx context.Context, session, request string) (out OperatorOperation, err error) {
	if !operatorRequestID(request) {
		return out, ErrInvalid
	}
	var raw string
	e := s.db.QueryRowContext(ctx, `SELECT CASE WHEN r.result='' THEN o.result ELSE r.result END FROM interaction_operator_requests r JOIN interaction_operator_operations o ON o.id=r.operation_id WHERE r.request_id=? AND r.session_id=?`, request, session).Scan(&raw)
	if e != nil {
		return out, e
	}
	e = json.Unmarshal([]byte(raw), &out)
	// Expiry is an effective read state, not a domain write or consumption.
	if e == nil && out.State == "pending" && time.Now().UnixMilli() >= out.ExpiresAtMS {
		out.State = "expired"
		out.Reason = "Preview expired; no target change."
	}
	return out, e
}
func (s *Store) DecideOperator(ctx context.Context, session string, in OperatorDecision) (OperatorOperation, error) {
	return s.decideOperator(ctx, session, in, time.Now())
}
func (s *Store) decideOperator(ctx context.Context, session string, in OperatorDecision, now time.Time) (out OperatorOperation, err error) {
	if !operatorRequestID(in.RequestID) || !operatorRequestID(in.OperationID) || len(in.Digest) != 64 || (in.Decision != "confirm" && in.Decision != "cancel") {
		return out, ErrInvalid
	}
	tx, e := s.queueWrite(ctx, session)
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	fp := operatorHash([]any{session, "decision", in})
	if old, found, e := operatorReplay(ctx, tx, session, in.RequestID, fp); e != nil || found {
		return old, e
	}
	var raw, state string
	if e = tx.QueryRowContext(ctx, `SELECT preview,state FROM interaction_operator_operations WHERE id=? AND session_id=?`, in.OperationID, session).Scan(&raw, &state); errors.Is(e, sql.ErrNoRows) {
		return out, ErrConflict
	} else if e != nil {
		return out, e
	}
	if e = json.Unmarshal([]byte(raw), &out); e != nil {
		return out, e
	}
	if state != "pending" || out.Digest != in.Digest || operatorDigest(out) != in.Digest {
		return out, ErrConflict
	}
	out.Decision = in.Decision
	out.DecisionRequestID = in.RequestID
	scope, policy, e := operatorAuthority(ctx, tx, session)
	if e != nil {
		return out, e
	}
	switch {
	case now.UnixMilli() >= out.ExpiresAtMS:
		out.State = "expired"
		out.Reason = "Preview expired; no target change."
	case scope != out.ScopeID || policy != out.PolicyDigest:
		out.State = "rejected"
		out.Reason = "Original authority changed; no target change."
	case in.Decision == "cancel":
		out.State = "rejected"
		out.Reason = "User cancelled preview; no target change."
	case out.Kind == "project.create":
		if out.ExecutionClass != "metadata_only" {
			return out, ErrInvalid
		}
		e = operatorCreateVacant(ctx, tx, out.TargetID)
		if e == nil {
			e = operatorCreateCapacity(ctx, tx, now, false)
		}
		if errors.Is(e, ErrConflict) {
			out.State = "rejected"
			out.Reason = "Project identity already exists (including retained tombstones) or catalog capacity reached; no project created."
		} else if e != nil {
			return out, e
		} else {
			// Classification MUST precede the INSERT-only project write. The existing
			// project trigger supplies lifecycle generation in this same transaction.
			if _, e = tx.ExecContext(ctx, `INSERT INTO project_execution_classification(project_id,execution_class) VALUES(?,'metadata_only')`, out.TargetID); e != nil {
				return out, e
			}
			if _, e = tx.ExecContext(ctx, `INSERT INTO projects(id,name,privacy_class,repository_refs_json,knowledge_scopes_json,memory_scope,allowed_skills_json,allowed_capabilities_json,allowed_coding_agents_json,budget_policy,allowed_models_json) VALUES(?,?,'local_private','[]','[]','','[]','[]','[]','{}','[]')`, out.TargetID, out.NewValue); e != nil {
				return out, e
			}
			out.State = "committed"
			out.Reason = "metadata_only project committed atomically with classification, lifecycle, result and audit. " + operatorCreateNotice
		}
	default:
		value, revision, e := operatorTarget(ctx, tx, out.Kind, out.TargetID)
		if e != nil && !errors.Is(e, sql.ErrNoRows) && !errors.Is(e, ErrInvalid) {
			return out, e
		}
		if e != nil || value != out.OldValue || revision != out.Revision {
			out.State = "rejected"
			out.Reason = "Target changed or disappeared; prepare a new exact preview."
		} else if value == out.NewValue {
			out.State = "no_op"
			out.Reason = "Already at requested value; no target change."
		} else {
			var res sql.Result
			if out.Kind == "project.rename" {
				res, e = tx.ExecContext(ctx, `UPDATE projects SET name=? WHERE id=? AND name=?`, out.NewValue, out.TargetID, out.OldValue)
			} else if out.Kind == "project.archive" || out.Kind == "project.restore" {
				res, e = tx.ExecContext(ctx, `UPDATE project_catalog_lifecycle SET state=?,revision=revision+1 WHERE project_id=? AND state=?`, out.NewValue, out.TargetID, out.OldValue)
			} else {
				res, e = tx.ExecContext(ctx, `UPDATE notification_inbox SET status='read',read_at=? WHERE id=? AND status='unread'`, now.UTC(), out.TargetID)
			}
			if e = changed(res, e); e != nil {
				return out, e
			}
			out.State = "committed"
			out.Reason = "Local metadata change committed atomically with operation and audit. No external delivery or execution."
		}
	}
	result, _ := json.Marshal(out)
	if _, e = tx.ExecContext(ctx, `UPDATE interaction_operator_operations SET state=?,result=? WHERE id=? AND state='pending'`, out.State, string(result), out.OperationID); e != nil {
		return out, e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO interaction_operator_requests VALUES(?,?,?,?,?)`, in.RequestID, session, out.OperationID, fp, string(result)); e != nil {
		return out, e
	}
	if e = operatorAudit(ctx, tx, out); e != nil {
		return out, e
	}
	return out, tx.Commit()
}

// Fixed inert storage defaults are descriptive, never execution authority.
const operatorCreateNotice = "no tools/models/workflows/knowledge isolation enabled. Fixed defaults: privacy=local_private; repository_refs/knowledge_scopes/allowed_skills/allowed_capabilities/allowed_coding_agents/allowed_models=[]; memory_scope=empty; budget_policy={}. No policy, root, scope, grant or session association."
const operatorCreateCatalogLimit = 10000
const operatorCreateRetainedLimit = 10000
const operatorCreatePendingLimit = 64

func operatorCreateVacant(ctx context.Context, tx *sql.Tx, id string) error {
	var count int
	if e := tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM projects WHERE id=?)+(SELECT count(*) FROM project_execution_classification WHERE project_id=?)+(SELECT count(*) FROM project_catalog_lifecycle WHERE project_id=?)`, id, id, id).Scan(&count); e != nil {
		return e
	}
	if count != 0 {
		return ErrConflict
	}
	return nil
}
func operatorCreateCapacity(ctx context.Context, tx *sql.Tx, now time.Time, preview bool) error {
	var projects int
	if e := tx.QueryRowContext(ctx, `SELECT count(*) FROM (SELECT id FROM projects UNION SELECT project_id FROM project_execution_classification UNION SELECT project_id FROM project_catalog_lifecycle)`).Scan(&projects); e != nil {
		return e
	}
	if projects >= operatorCreateCatalogLimit {
		return ErrConflict
	}
	if !preview {
		return nil
	}
	var retained, pending int
	if e := tx.QueryRowContext(ctx, `SELECT count(*),coalesce(sum(CASE WHEN state='pending' AND json_extract(preview,'$.expires_at_ms')>? THEN 1 ELSE 0 END),0) FROM interaction_operator_operations WHERE json_extract(preview,'$.kind')='project.create'`, now.UnixMilli()).Scan(&retained, &pending); e != nil {
		return e
	}
	if retained >= operatorCreateRetainedLimit || pending >= operatorCreatePendingLimit {
		return ErrConflict
	}
	return nil
}
