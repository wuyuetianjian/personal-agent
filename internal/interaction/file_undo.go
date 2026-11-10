package interaction

import (
	"agent/internal/model"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"time"
)

const FileUndoWarning = "Verified successful native existing-file replacements only; pending, denied, refused and unknown source effects are excluded; exec, MCP and external effects are non-reversible. Fresh approval always required. Current bytes must match the recorded postimage. No creation/deletion, Git rollback or conversation rewind. External writers can race the last check and atomic rename; no filesystem compare-and-swap or OS sandbox guarantee. A reserved checkpoint cannot be requested again, even after denial/refusal; uncertain effects require reconciliation, never replay."

type EditCheckpoint struct {
	ID           string     `json:"id"`
	SessionID    string     `json:"session_id"`
	SourceRunID  string     `json:"source_run_id"`
	Scope        string     `json:"scope"`
	Root         string     `json:"root"`
	RootIdentity string     `json:"root_identity"`
	Review       EditReview `json:"review"`
	Disposition  string     `json:"disposition"`
	UndoRunID    string     `json:"undo_run_id,omitempty"`
}
type EditCheckpointInventory struct {
	SchemaVersion int              `json:"schema_version"`
	SessionID     string           `json:"session_id"`
	Supported     bool             `json:"supported"`
	Reason        string           `json:"reason"`
	Checkpoints   []EditCheckpoint `json:"checkpoints"`
}

// A checkpoint is derived only from an immutable prepared native edit plus its
// completed success journal, never from text, an approval or a model's claim.
func editCheckpoint(ctx context.Context, q contextQuery, session, id string) (EditCheckpoint, error) {
	var out EditCheckpoint
	var raw, status, result string
	var executed bool
	err := q.QueryRowContext(ctx, `SELECT p.prepared,p.executed,j.status,j.result_ref FROM interaction_tool_proposals p JOIN interaction_runs r ON r.id=p.run_id JOIN side_effect_records j ON j.workflow_id=r.id AND j.node_id=p.id AND j.capability_id='apply_patch' WHERE p.id=? AND r.session_id=? AND j.idempotency_key=('interaction:' || length(r.id) || ':' || r.id || ':' || p.id)`, id, session).Scan(&raw, &executed, &status, &result)
	if err != nil {
		return out, err
	}
	var p preparedTool
	var res ToolResult
	if json.Unmarshal([]byte(raw), &p) != nil || json.Unmarshal([]byte(result), &res) != nil || !executed || status != "completed" || res.IsError || p.Review == nil || p.Review.UndoOf != "" || p.Proposal.Call.Name != "apply_patch" || p.SessionID != session || p.Proposal.ID != id {
		return out, ErrInvalid
	}
	var a PatchArguments
	if decodeTool(p.Proposal.Call, &a) != nil || a.Scope != p.ScopeID || p.Review.Scope != p.ScopeID || a.Path != p.Review.Path || a.Preimage != p.Review.OldSHA256 || a.Content != p.Review.NewContent || contentHash([]byte(p.Review.OldContent)) != a.Preimage || contentHash([]byte(a.Content)) != p.Review.NewSHA256 || p.Review.OldSHA256 == p.Review.NewSHA256 || res.Content != "Applied exact replacement; SHA256 "+p.Review.NewSHA256 {
		return out, ErrInvalid
	}
	var policy string
	var rootID, scope string
	if err = q.QueryRowContext(ctx, "SELECT policy,root_identity,scope_id FROM interaction_workspaces WHERE session_id=?", session).Scan(&policy, &rootID, &scope); err != nil {
		return out, err
	}
	var w WorkspacePolicy
	if err = json.Unmarshal([]byte(policy), &w); err != nil {
		return out, err
	}
	if rootID != p.RootIdentity || scope != p.ScopeID {
		return out, ErrConflict
	}
	out = EditCheckpoint{ID: id, SessionID: session, SourceRunID: p.RunID, Scope: p.ScopeID, Root: w.Root, RootIdentity: p.RootIdentity, Review: *p.Review, Disposition: "available"}
	var state RunState
	var cpRaw sql.NullString
	err = q.QueryRowContext(ctx, `SELECT u.run_id,r.state,e.checkpoint FROM interaction_edit_undos u JOIN interaction_runs r ON r.id=u.run_id LEFT JOIN interaction_engine e ON e.run_id=r.id WHERE u.checkpoint_id=?`, id).Scan(&out.UndoRunID, &state, &cpRaw)
	if err == sql.ErrNoRows {
		if err = checkFileHead(ctx, q, p.ScopeID, p.Review.Path, p.Proposal.ID, ""); err == ErrConflict {
			out.Disposition = "superseded"
			return out, nil
		}
		return out, err
	}
	if err != nil {
		return out, err
	}
	out.Disposition = "reserved"
	if state != RunRunning {
		out.Disposition = "closed_without_undo"
	}
	var cp Checkpoint
	if cpRaw.Valid && json.Unmarshal([]byte(cpRaw.String), &cp) == nil && cp.Phase == PhaseReconciliation {
		out.Disposition = "unknown"
	}
	var jr, js string
	err = q.QueryRowContext(ctx, `SELECT j.status,j.result_ref FROM side_effect_records j JOIN interaction_tool_proposals p ON p.id=j.node_id AND p.run_id=j.workflow_id WHERE j.workflow_id=? AND j.capability_id='apply_patch'`, out.UndoRunID).Scan(&js, &jr)
	if err == nil {
		var r ToolResult
		if js != "completed" || json.Unmarshal([]byte(jr), &r) != nil {
			out.Disposition = "unknown"
		} else if r.IsError {
			out.Disposition = "refused"
		} else if r.Content != "Applied exact replacement; SHA256 "+p.Review.OldSHA256 {
			out.Disposition = "unknown"
		} else {
			out.Disposition = "undone"
		}
	} else if err != sql.ErrNoRows {
		return out, err
	}
	return out, nil
}
func (s *Store) EditCheckpoints(ctx context.Context, session string) (EditCheckpointInventory, error) {
	out := EditCheckpointInventory{SchemaVersion: 1, SessionID: session, Reason: FileUndoWarning, Checkpoints: []EditCheckpoint{}}
	var exists int
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM interaction_sessions WHERE id=?", session).Scan(&exists); err != nil {
		return out, err
	}
	if exists != 1 {
		return out, sql.ErrNoRows
	}
	w, err := s.Workspace(ctx, session)
	if err == sql.ErrNoRows {
		out.Reason = "No workspace binding. " + out.Reason
		return out, nil
	}
	if err != nil {
		return out, err
	}
	out.Supported = platformFileCapability().Enabled && w.Policy.Mode != ModePlan
	if w.Policy.Mode == ModePlan {
		out.Reason = "Unsupported in plan mode. " + out.Reason
	}
	if !platformFileCapability().Enabled {
		out.Reason = platformFileCapability().Reason + ". " + out.Reason
	}
	if identity, e := workspaceIdentity(w.Policy.Root); e != nil || identity != w.RootIdentity {
		out.Supported = false
		out.Reason = "Workspace root unavailable or changed. " + out.Reason
	}
	rows, err := s.db.QueryContext(ctx, `SELECT p.id FROM interaction_tool_proposals p JOIN interaction_runs r ON r.id=p.run_id WHERE r.session_id=? ORDER BY p.rowid DESC LIMIT 1001`, session)
	if err != nil {
		return out, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return out, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if len(ids) > 1000 {
		return out, ErrBudget
	}
	size := 0
	for _, id := range ids {
		p, e := editCheckpoint(ctx, s.db, session, id)
		if e == sql.ErrNoRows || e == ErrInvalid {
			continue
		}
		if e != nil {
			return out, e
		}
		size += len(p.Review.OldContent) + len(p.Review.NewContent)
		if size > 4<<20 {
			return out, ErrBudget
		}
		out.Checkpoints = append(out.Checkpoints, p)
	}
	return out, nil
}

// UndoEdit admits a deterministic action and its one-way reservation in the same
// writer transaction as the recoverable request ledger. No effect happens here.
func (s *Store) UndoEdit(ctx context.Context, session string, in SessionOperation) (Run, error) {
	var out Run
	if in.Kind != "file_undo" || in.BoundaryRunID == "" {
		return out, ErrInvalid
	}
	tx, fp, replay, err := s.sessionOperation(ctx, session, in)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if replay != "" {
		err = json.Unmarshal([]byte(replay), &out)
		return out, err
	}
	p, err := editCheckpoint(ctx, tx, session, in.BoundaryRunID)
	if err != nil {
		return out, err
	}
	if p.Disposition != "available" {
		return out, ErrConflict
	}
	var policy string
	if err = tx.QueryRowContext(ctx, "SELECT policy FROM interaction_workspaces WHERE session_id=?", session).Scan(&policy); err != nil {
		return out, err
	}
	var wp WorkspacePolicy
	if err = json.Unmarshal([]byte(policy), &wp); err != nil {
		return out, err
	}
	if wp.Mode == ModePlan || !platformFileCapability().Enabled {
		return out, ErrCapabilityUnavailable
	}
	w := WorkspaceBinding{SessionID: session, ScopeID: p.Scope, RootIdentity: p.RootIdentity, Policy: wp}
	current, err := readWorkspaceFile(ctx, w, p.Review.Path)
	if err != nil {
		return out, err
	}
	if contentHash(current) != p.Review.NewSHA256 {
		return out, fmt.Errorf("%w: checkpoint postimage no longer matches", ErrConflict)
	}
	out = Run{ID: uuid.NewString(), SessionID: session, RequestID: in.RequestID, Message: Message{Content: "Explicit native file undo: " + p.ID}, State: RunRunning, Version: 1}
	msg, _ := json.Marshal(out.Message)
	if _, err = tx.ExecContext(ctx, "INSERT INTO interaction_runs("+runColumns+") VALUES(?,?,?,?,?,?)", out.ID, session, in.RequestID, string(msg), out.State, out.Version); err != nil {
		return Run{}, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO interaction_edit_undos(checkpoint_id,run_id) VALUES(?,?)", p.ID, out.ID); err != nil {
		return Run{}, err
	}
	args, _ := json.Marshal(PatchArguments{Scope: p.Scope, Path: p.Review.Path, Preimage: p.Review.NewSHA256, Content: p.Review.OldContent})
	// Engine defaults supply the same conservative duration/events/effect budgets.
	engine, _ := NewEngine(s, actionOnlyProvider{}, nil, EngineOptions{})
	cp := engine.initialCheckpoint(out)
	cp.Action = "file_undo"
	cp.Calls = []ToolCall{{ID: "undo-" + p.ID, Name: "apply_patch", Arguments: string(args)}}
	cp.Deadline = time.Now().Add(5 * time.Minute)
	raw, _ := json.Marshal(cp)
	if _, err = tx.ExecContext(ctx, "INSERT INTO interaction_engine(run_id,phase,checkpoint) VALUES(?,?,?)", out.ID, cp.Phase, string(raw)); err != nil {
		return Run{}, err
	}
	if err = appendEvent(ctx, tx, out, "run.created"); err != nil {
		return Run{}, err
	}
	return out, finishSessionOperation(ctx, tx, session, in, fp, out)
}

type actionOnlyProvider struct{}

func (actionOnlyProvider) Stream(context.Context, model.TurnRequest, func(model.ModelEvent) error) (model.TurnResult, error) {
	return model.TurnResult{}, ErrInvalid
}

// For legacy successful journals with no head, refuse any other invoked native
// replacement: historical preparation order cannot attest effect order. New invocations always advance a durable head before rename.
func checkFileHead(ctx context.Context, q contextQuery, scope, path, id, ownUndo string) error {
	var head string
	err := q.QueryRowContext(ctx, "SELECT proposal_id FROM interaction_file_heads WHERE scope_id=? AND path=?", scope, path).Scan(&head)
	if err == nil {
		if head != id {
			return ErrConflict
		}
		return nil
	}
	if err != sql.ErrNoRows {
		return err
	}
	var n int
	err = q.QueryRowContext(ctx, `SELECT count(*) FROM interaction_tool_proposals WHERE executed=1 AND id<>? AND id<>? AND json_extract(prepared,'$.ScopeID')=? AND json_extract(prepared,'$.Review.path')=?`, id, ownUndo, scope, path).Scan(&n)
	if err != nil {
		return err
	}
	if n != 0 {
		return ErrConflict
	}
	return nil
}
func (b *WorkspaceBroker) reserveFileHead(ctx context.Context, w WorkspaceBinding, p preparedTool, path string) error {
	tx, err := b.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Reserve writer before reading the old head.
	if _, err = tx.ExecContext(ctx, "UPDATE interaction_tool_proposals SET executed=executed WHERE id=?", p.Proposal.ID); err != nil {
		return err
	}
	if p.Review != nil && p.Review.UndoOf != "" {
		if err = checkFileHead(ctx, tx, w.ScopeID, path, p.Review.UndoOf, p.Proposal.ID); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO interaction_file_heads(scope_id,path,proposal_id) VALUES(?,?,?) ON CONFLICT(scope_id,path) DO UPDATE SET proposal_id=excluded.proposal_id", w.ScopeID, path, p.Proposal.ID)
	if err != nil {
		return err
	}
	return tx.Commit()
}
