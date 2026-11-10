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
)

const JobOriginLimit = 8
const JobGlobalLimit = 32

var ErrJobRevoked = errors.New("job origin authority revoked")
var ErrJobFull = errors.New("job capacity exhausted")

type Job struct {
	ID              string `json:"id"`
	OriginSessionID string `json:"origin_session_id"`
	OriginRunID     string `json:"origin_run_id"`
	SessionID       string `json:"session_id"`
	RunID           string `json:"run_id"`
	RequestID       string `json:"request_id"`
	Content         string `json:"content"`
	Effects         bool   `json:"effects"`
	State           string `json:"state"`
	Phase           string `json:"phase"`
	BlockedReason   string `json:"blocked_reason"`
	PolicyDigest    string `json:"policy_digest"`
}
type JobView struct {
	SchemaVersion   int    `json:"schema_version"`
	OriginSessionID string `json:"origin_session_id"`
	Limit           int    `json:"limit"`
	Items           []Job  `json:"items"`
}
type JobOperationRecord struct {
	SchemaVersion int    `json:"schema_version"`
	SessionID     string `json:"session_id"`
	RequestID     string `json:"request_id"`
	Kind          string `json:"kind"`
	Payload       string `json:"payload"`
	Job           Job    `json:"job"`
}

const jobColumns = `j.id,j.origin_session_id,j.origin_run_id,j.session_id,j.run_id,j.request_id,j.content,j.effects,j.state,j.stop_requested,j.blocked_reason,j.policy,j.execution_policy,COALESCE(r.state,''),COALESCE(e.phase,''),COALESCE(e.owner,''),EXISTS(SELECT 1 FROM interaction_approvals a WHERE a.run_id=j.run_id AND a.decision='pending' AND a.consumed_at IS NULL),CASE WHEN json_valid(e.checkpoint) THEN COALESCE(json_extract(e.checkpoint,'$.Phase'),'') ELSE '' END`

func scanJob(row scanner) (Job, error) {
	var j Job
	var state, policy, executionPolicy, rs, owner, checkpointPhase string
	var stop, pending bool
	e := row.Scan(&j.ID, &j.OriginSessionID, &j.OriginRunID, &j.SessionID, &j.RunID, &j.RequestID, &j.Content, &j.Effects, &state, &stop, &j.BlockedReason, &policy, &executionPolicy, &rs, &j.Phase, &owner, &pending, &checkpointPhase)
	j.PolicyDigest = jobPolicyDigest(policy, executionPolicy)
	j.State = state
	if state == "admitted" {
		switch {
		case rs == "completed":
			j.State = "completed"
		case rs == "cancelled":
			j.State = "stopped"
		case rs == "failed":
			j.State = "failed"
		case j.Phase == "" || j.Phase == "needs_reconciliation" || owner == "" && (checkpointPhase == "executing" || checkpointPhase == "needs_reconciliation"):
			j.State = "unknown"
		case stop:
			j.State = "cancelling"
		case j.Phase == "waiting_approval" && pending:
			j.State = "waiting_approval"
		case j.Phase == "queued":
			j.State = "admitted"
		case owner == "":
			j.State = "unknown"
		default:
			j.State = "running"
		}
	}
	return j, e
}

const jobSelect = "SELECT " + jobColumns + " FROM interaction_jobs j LEFT JOIN interaction_runs r ON r.id=j.run_id LEFT JOIN interaction_engine e ON e.run_id=j.run_id "

func (s *Store) Job(ctx context.Context, id string) (Job, error) {
	return scanJob(s.db.QueryRowContext(ctx, jobSelect+"WHERE j.id=?", id))
}
func (s *Store) JobForRun(ctx context.Context, id string) (Job, error) {
	return scanJob(s.db.QueryRowContext(ctx, jobSelect+"WHERE j.run_id=?", id))
}
func (s *Store) IsJobSession(ctx context.Context, id string) (bool, error) {
	var n int
	e := s.db.QueryRowContext(ctx, "SELECT count(*) FROM interaction_jobs WHERE session_id=?", id).Scan(&n)
	return n != 0, e
}
func (s *Store) Jobs(ctx context.Context, origin string) (JobView, error) {
	v := JobView{SchemaVersion: 1, OriginSessionID: origin, Limit: JobOriginLimit, Items: []Job{}}
	rows, e := s.db.QueryContext(ctx, jobSelect+`WHERE j.origin_session_id=? AND (j.state='registered' OR r.state='running' OR j.sequence IN (SELECT sequence FROM interaction_jobs WHERE origin_session_id=? ORDER BY sequence DESC LIMIT 50)) ORDER BY j.sequence`, origin, origin)
	if e != nil {
		return v, e
	}
	defer rows.Close()
	for rows.Next() {
		j, e := scanJob(rows)
		if e != nil {
			return v, e
		}
		v.Items = append(v.Items, j)
	}
	return v, rows.Err()
}

// These exact limits/adapters are persisted before admission. A changed runtime
// policy revokes old registered intent rather than silently reinterpreting it.
func JobBudgets() Budgets {
	return Budgets{MaxSteps: 16, MaxDuration: 2 * time.Minute, MaxTokens: 32000, MaxOutputTokens: 2048, MaxOutputBytes: 128 << 10, MaxToolResultBytes: 32 << 10, MaxEvents: 512, MaxConcurrent: 4}
}
func jobExecutionPolicy(effects bool) string {
	raw, _ := json.Marshal(struct {
		Version  int
		Effects  bool
		Adapters []string
		Budgets  Budgets
	}{1, effects, []string{"read", "search", "apply_patch"}, JobBudgets()})
	return string(raw)
}
func jobPolicyDigest(policy, execution string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(policy+"\x00"+execution)))
}
func jobPayload(content, originRun string, effects bool) string {
	b, _ := json.Marshal([]any{content, originRun, effects})
	return string(b)
}
func (s *Store) CreateJob(ctx context.Context, origin, request, content, originRun string, effects bool) (Job, error) {
	if request == "" || len(request) > 128 || strings.TrimSpace(content) == "" || len(content) > 64<<10 {
		return Job{}, ErrInvalid
	}
	tx, e := s.queueWrite(ctx, origin)
	if e != nil {
		return Job{}, e
	}
	defer tx.Rollback()
	payload := jobPayload(content, originRun, effects)
	var oldID, oldOrigin, kind, oldPayload string
	e = tx.QueryRowContext(ctx, "SELECT job_id,session_id,kind,payload FROM interaction_job_operations WHERE request_id=?", request).Scan(&oldID, &oldOrigin, &kind, &oldPayload)
	if e == nil {
		if oldOrigin != origin || kind != "create" || oldPayload != payload {
			return Job{}, ErrConflict
		}
		j, e := scanJob(tx.QueryRowContext(ctx, jobSelect+"WHERE j.id=?", oldID))
		if e != nil {
			return j, e
		}
		return j, tx.Commit()
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return Job{}, e
	}
	var count, local int
	e = tx.QueryRowContext(ctx, `SELECT count(*),COALESCE(sum(j.origin_session_id=?),0) FROM interaction_jobs j LEFT JOIN interaction_runs r ON r.id=j.run_id WHERE j.state='registered' OR r.state='running'`, origin).Scan(&count, &local)
	if e != nil {
		return Job{}, e
	}
	if count >= JobGlobalLimit || local >= JobOriginLimit {
		return Job{}, ErrJobFull
	}
	if originRun != "" {
		var owner string
		if e = tx.QueryRowContext(ctx, "SELECT session_id FROM interaction_runs WHERE id=?", originRun).Scan(&owner); e != nil {
			if errors.Is(e, sql.ErrNoRows) {
				return Job{}, ErrInvalid
			}
			return Job{}, e
		}
		if owner != origin {
			return Job{}, ErrInvalid
		}
	}
	var nested int
	if e = tx.QueryRowContext(ctx, "SELECT count(*) FROM interaction_jobs WHERE session_id=?", origin).Scan(&nested); e != nil {
		return Job{}, e
	}
	if nested != 0 {
		return Job{}, ErrInvalid
	}
	executionPolicy := jobExecutionPolicy(effects)
	policy, e := queuePolicy(ctx, tx, origin)
	if e != nil {
		return Job{}, e
	}
	var parts []string
	if json.Unmarshal([]byte(policy), &parts) != nil || len(parts) != 5 {
		return Job{}, ErrInvalid
	}
	mode := parts[1]
	workspace := parts[2]
	if workspace != "" {
		var p WorkspacePolicy
		if json.Unmarshal([]byte(workspace), &p) != nil {
			return Job{}, ErrInvalid
		}
		if effects && p.Mode == ModePlan {
			return Job{}, ErrInvalid
		}
		p.Mode = ModePlan
		if effects {
			p.Mode = ModeDefault
		}
		p.MCP = nil
		p.NonSandboxExec = false
		raw, _ := json.Marshal(p)
		workspace = string(raw)
		mode = string(p.Mode)
	} else if effects {
		return Job{}, ErrInvalid
	}
	j := Job{ID: uuid.NewString(), OriginSessionID: origin, OriginRunID: originRun, SessionID: uuid.NewString(), RunID: uuid.NewString(), RequestID: request, Content: content, Effects: effects, State: "registered"}
	if _, e = tx.ExecContext(ctx, "INSERT INTO interaction_sessions(id) VALUES(?)", j.SessionID); e != nil {
		return Job{}, e
	}
	if e = copyProjectLabel(ctx, tx, origin, j.SessionID); e != nil {
		return Job{}, e
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO interaction_api_sessions(session_id,request_id,binding,mode) VALUES(?,?,?,?)", j.SessionID, "job-session:"+j.ID, parts[0], mode); e != nil {
		return Job{}, e
	}
	if workspace != "" {
		var scope string
		if e = tx.QueryRowContext(ctx, "SELECT scope_id FROM interaction_workspaces WHERE session_id=?", origin).Scan(&scope); e != nil {
			return Job{}, e
		}
		_, e = tx.ExecContext(ctx, "INSERT INTO interaction_workspaces(session_id,scope_id,policy,root_identity) VALUES(?,?,?,?)", j.SessionID, scope, workspace, parts[4])
	} else {
		_, e = tx.ExecContext(ctx, "INSERT INTO interaction_chat_policies(session_id,policy) VALUES(?,?)", j.SessionID, parts[3])
	}
	if e != nil {
		return Job{}, e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO interaction_jobs(id,origin_session_id,origin_run_id,session_id,run_id,request_id,content,effects,policy,execution_policy,state) VALUES(?,?,?,?,?,?,?,?,?,?,'registered')`, j.ID, origin, originRun, j.SessionID, j.RunID, request, content, effects, policy, executionPolicy); e != nil {
		return Job{}, e
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO interaction_job_operations VALUES(?,?,?,'create',?)", request, origin, j.ID, payload); e != nil {
		return Job{}, e
	}
	j.PolicyDigest = jobPolicyDigest(policy, executionPolicy)
	return j, tx.Commit()
}
func validateJobTx(ctx context.Context, tx *sql.Tx, id string) error {
	var origin, policy, executionPolicy, blocked string
	var effects bool
	e := tx.QueryRowContext(ctx, "SELECT origin_session_id,policy,execution_policy,effects,blocked_reason FROM interaction_jobs WHERE id=?", id).Scan(&origin, &policy, &executionPolicy, &effects, &blocked)
	if e != nil {
		return e
	}
	if blocked != "" || executionPolicy != jobExecutionPolicy(effects) {
		return ErrJobRevoked
	}
	current, e := queuePolicy(ctx, tx, origin)
	if e != nil {
		return e
	}
	if current != policy {
		return ErrJobRevoked
	}
	var parts []string
	if json.Unmarshal([]byte(policy), &parts) != nil || len(parts) != 5 {
		return ErrJobRevoked
	}
	if parts[2] != "" {
		var p WorkspacePolicy
		if json.Unmarshal([]byte(parts[2]), &p) != nil {
			return ErrJobRevoked
		}
		identity, e := workspaceIdentity(p.Root)
		if e != nil || identity != parts[4] {
			return ErrJobRevoked
		}
	}
	return nil
}
func (s *Store) ValidateJob(ctx context.Context, id string) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	return validateJobTx(ctx, tx, id)
}
func (s *Store) BlockJob(ctx context.Context, id, reason string) error {
	_, e := s.db.ExecContext(ctx, "UPDATE interaction_jobs SET blocked_reason=? WHERE id=? AND blocked_reason=''", reason, id)
	return e
}
func (s *Store) AdmitJob(ctx context.Context, id string) (Run, error) {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return Run{}, e
	}
	defer tx.Rollback()
	res, e := tx.ExecContext(ctx, "UPDATE interaction_jobs SET state='admitted' WHERE id=? AND state='registered' AND stop_requested=0 AND blocked_reason=''", id)
	if e = changed(res, e); e != nil {
		return Run{}, e
	}
	if e = validateJobTx(ctx, tx, id); e != nil {
		return Run{}, e
	}
	var r Run
	r.State = RunRunning
	r.Version = 1
	if e = tx.QueryRowContext(ctx, "SELECT run_id,session_id,content FROM interaction_jobs WHERE id=?", id).Scan(&r.ID, &r.SessionID, &r.Message.Content); e != nil {
		return r, e
	}
	r.RequestID = "job:" + id
	raw, _ := json.Marshal(r.Message)
	if _, e = tx.ExecContext(ctx, "INSERT INTO interaction_runs("+runColumns+") VALUES(?,?,?,?,?,?)", r.ID, r.SessionID, r.RequestID, string(raw), r.State, r.Version); e != nil {
		return r, e
	}
	if e = appendEvent(ctx, tx, r, "run.created"); e != nil {
		return r, e
	}
	return r, tx.Commit()
}

// FailNewJobPreparation may only be called by the exclusive service immediately
// after successful admission, before start has ever been called for this Run.
// Absence of a checkpoint alone is NOT that proof: recovery must never use this.
// Persist the reason and terminal event atomically; an uncertain write stays
// conservative, with no worker start or later attempt to infer non-execution.
func (s *Store) FailNewJobPreparation(ctx context.Context, r Run) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	res, e := tx.ExecContext(ctx, `UPDATE interaction_runs SET state='failed',version=version+1 WHERE id=? AND version=? AND state='running' AND NOT EXISTS(SELECT 1 FROM interaction_engine WHERE run_id=?)`, r.ID, r.Version, r.ID)
	if e = changed(res, e); e != nil {
		return e
	}
	res, e = tx.ExecContext(ctx, `UPDATE interaction_jobs SET blocked_reason='context_budget_exceeded_shorten_job_input' WHERE run_id=? AND state='admitted' AND blocked_reason=''`, r.ID)
	if e = changed(res, e); e != nil {
		return e
	}
	r.State = RunFailed
	r.Version++
	if e = appendEvent(ctx, tx, r, "run.failed"); e != nil {
		return e
	}
	return tx.Commit()
}

func (s *Store) RegisteredJobs(ctx context.Context) ([]Job, error) {
	rows, e := s.db.QueryContext(ctx, jobSelect+"WHERE j.state='registered' AND j.stop_requested=0 AND j.blocked_reason='' ORDER BY j.sequence LIMIT ?", JobGlobalLimit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	list := []Job{}
	for rows.Next() {
		j, e := scanJob(rows)
		if e != nil {
			return nil, e
		}
		list = append(list, j)
	}
	return list, rows.Err()
}
func (s *Store) JobOperation(ctx context.Context, origin, request string) (JobOperationRecord, error) {
	v := JobOperationRecord{SchemaVersion: 1, SessionID: origin, RequestID: request}
	var id string
	e := s.db.QueryRowContext(ctx, "SELECT job_id,kind,payload FROM interaction_job_operations WHERE session_id=? AND request_id=?", origin, request).Scan(&id, &v.Kind, &v.Payload)
	if e != nil {
		return v, e
	}
	v.Job, e = s.Job(ctx, id)
	return v, e
}

// Intent and cancellation flag are durable together; signalling/cleanup remains Engine's job.
func (s *Store) StopJob(ctx context.Context, origin, id, request string) error {
	_, e := s.mutateJob(ctx, origin, id, request, "stop", "", "", "", "")
	return e
}
func (s *Store) DecideJob(ctx context.Context, origin, id, request, approval, digest, decision string) (bool, error) {
	raw, _ := json.Marshal([]string{approval, digest, decision})
	return s.mutateJob(ctx, origin, id, request, "decision", string(raw), approval, digest, decision)
}
func (s *Store) mutateJob(ctx context.Context, origin, id, request, kind, payload, approval, digest, decision string) (bool, error) {
	if request == "" || len(request) > 128 {
		return false, ErrInvalid
	}
	tx, e := s.queueWrite(ctx, origin)
	if e != nil {
		return false, e
	}
	defer tx.Rollback()
	var oi, oo, ok, op string
	e = tx.QueryRowContext(ctx, "SELECT job_id,session_id,kind,payload FROM interaction_job_operations WHERE request_id=?", request).Scan(&oi, &oo, &ok, &op)
	if e == nil {
		if oi != id || oo != origin || ok != kind || op != payload {
			return false, ErrConflict
		}
		return false, tx.Commit()
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return false, e
	}
	var run, state string
	if e = tx.QueryRowContext(ctx, "SELECT run_id,state FROM interaction_jobs WHERE id=? AND origin_session_id=?", id, origin).Scan(&run, &state); e != nil {
		return false, e
	}
	if kind == "stop" {
		if _, e = tx.ExecContext(ctx, "UPDATE interaction_jobs SET stop_requested=1,state=CASE WHEN state='registered' THEN 'stopped' ELSE state END WHERE id=?", id); e != nil {
			return false, e
		}
		if _, e = tx.ExecContext(ctx, "UPDATE interaction_engine SET cancel_requested=1,phase='cancelling' WHERE run_id=? AND phase NOT IN ('completed','failed','cancelled','needs_reconciliation')", run); e != nil {
			return false, e
		}
	} else {
		if decision != "approved" && decision != "denied" {
			return false, ErrInvalid
		}
		if e = validateJobTx(ctx, tx, id); e != nil {
			return false, e
		}
		res, err := tx.ExecContext(ctx, `UPDATE interaction_approvals SET decision=? WHERE id=? AND run_id=? AND action_digest=? AND decision='pending' AND expires_at>? AND EXISTS(SELECT 1 FROM interaction_runs WHERE id=run_id AND state='running') AND EXISTS(SELECT 1 FROM interaction_engine WHERE run_id=interaction_approvals.run_id AND phase='waiting_approval' AND cancel_requested=0) AND EXISTS(SELECT 1 FROM interaction_jobs WHERE id=? AND stop_requested=0)`, decision, approval, run, digest, time.Now().UnixNano(), id)
		if e = changed(res, err); e != nil {
			return false, e
		}
		var r Run
		if e = tx.QueryRowContext(ctx, "SELECT id,session_id,version FROM interaction_runs WHERE id=?", run).Scan(&r.ID, &r.SessionID, &r.Version); e != nil {
			return false, e
		}
		if _, e = tx.ExecContext(ctx, "UPDATE interaction_sessions SET sequence=sequence+1 WHERE id=?", r.SessionID); e != nil {
			return false, e
		}
		data, _ := json.Marshal(map[string]string{"id": approval, "digest": digest, "decision": decision})
		if _, e = tx.ExecContext(ctx, "INSERT INTO interaction_events(session_id,run_id,sequence,version,type,payload) SELECT id,?,sequence,?,'approval.decided',? FROM interaction_sessions WHERE id=?", run, r.Version, string(data), r.SessionID); e != nil {
			return false, e
		}
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO interaction_job_operations VALUES(?,?,?,?,?)", request, origin, id, kind, payload); e != nil {
		return false, e
	}
	return true, tx.Commit()
}

// Preserve the broker's exact prepared-action digest interface inside its package.
type JobBroker struct {
	broker   *WorkspaceBroker
	run      string
	validate func(context.Context) error
}

func NewJobBroker(b *WorkspaceBroker, run string, validate func(context.Context) error) *JobBroker {
	return &JobBroker{b, run, validate}
}
func (b *JobBroker) Propose(ctx context.Context, run string, call ToolCall) (ToolProposal, error) {
	if run != b.run {
		return ToolProposal{}, ErrInvalid
	}
	if e := b.validate(ctx); e != nil {
		return ToolProposal{}, e
	}
	switch call.Name {
	case "read", "search", "apply_patch":
	default:
		return ToolProposal{}, ErrCapabilityUnavailable
	}
	return b.broker.Propose(ctx, run, call)
}
func (b *JobBroker) Execute(ctx context.Context, id string) (ToolResult, error) {
	if e := b.validate(ctx); e != nil {
		return ToolResult{}, e
	}
	return b.broker.Execute(ctx, id)
}
func (b *JobBroker) preparedActionDigest(ctx context.Context, run string, p ToolProposal) (string, error) {
	if run != b.run {
		return "", ErrInvalid
	}
	return b.broker.preparedActionDigest(ctx, run, p)
}
