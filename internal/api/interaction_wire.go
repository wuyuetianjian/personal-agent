package api

import (
	"agent/internal/interaction"
	"agent/internal/model"
	"context"
	"encoding/json"
)

// SchemaVersion is independent of aggregate run.Version.
const InteractionSchemaVersion = 1
const snapshotRunLimit = 50

type SessionView struct {
	ProjectLabel   *interaction.ProjectLabel      `json:"project_label,omitempty"`
	Provenance     *interaction.SessionProvenance `json:"provenance,omitempty"`
	MCP            *interaction.MCPStatus         `json:"mcp,omitempty"`
	ToolCapability string                         `json:"tool_capability,omitempty"`
	NonSandboxExec bool                           `json:"non_sandbox_exec,omitempty"`
	ExecWarning    string                         `json:"exec_warning,omitempty"`
	ID             string                         `json:"id"`
	ModelID        string                         `json:"model_id"`
	Mode           string                         `json:"mode"`
	Files          bool                           `json:"files"`
	Tools          bool                           `json:"tools"`
	Reason         string                         `json:"reason"`
}
type ApprovalView struct {
	ID        string `json:"id"`
	Digest    string `json:"digest"`
	ExpiresAt string `json:"expires_at"`
	Decision  string `json:"decision"`
}
type ToolView struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	State     string `json:"state"`
	Result    string `json:"result,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
}
type RunView struct {
	ID        string        `json:"id"`
	RequestID string        `json:"request_id"`
	Input     string        `json:"input"`
	State     string        `json:"state"`
	Phase     string        `json:"phase"`
	Text      string        `json:"text"`
	Draft     string        `json:"draft"`
	Tools     []ToolView    `json:"tools"`
	Approval  *ApprovalView `json:"approval,omitempty"`
}
type Snapshot struct {
	SchemaVersion int         `json:"schema_version"`
	Session       SessionView `json:"session"`
	Cursor        int64       `json:"cursor"`
	Runs          []RunView   `json:"runs"`
	NextBefore    *int64      `json:"next_before,omitempty"`
	HasOlder      bool        `json:"has_older"`
}
type WireEvent struct {
	SchemaVersion int    `json:"schema_version"`
	SessionID     string `json:"session_id"`
	RunID         string `json:"run_id"`
	Sequence      int64  `json:"sequence"`
	Type          string `json:"type"`
	Data          any    `json:"data"`
}
type sessionBinding struct {
	ModelID     string `json:"model_id"`
	Fingerprint string `json:"fingerprint"`
	Tools       bool   `json:"tools"`
}

func (s *InteractionService) session(ctx context.Context, id string) (SessionView, sessionBinding, error) {
	var mode, raw string
	err := s.runtime.DB.SQL.QueryRowContext(ctx, "SELECT mode,binding FROM interaction_api_sessions WHERE session_id=?", id).Scan(&mode, &raw)
	var b sessionBinding
	if err == nil {
		err = json.Unmarshal([]byte(raw), &b)
	}
	v := SessionView{ID: id, ModelID: b.ModelID, Mode: mode, Files: mode != "chat_only", Tools: b.Tools && mode != "chat_only"}
	if err == nil {
		v.Provenance, err = s.store.Provenance(ctx, id)
	}
	if err == nil {
		v.ProjectLabel, err = s.store.ProjectLabel(ctx, id)
	}
	mcpStatus := s.broker.MCPStatus(ctx, id)
	v.MCP = &mcpStatus
	v.ToolCapability = "unverified"
	if err == nil && mode != "chat_only" {
		current, e := s.options.Resolve(ctx, b.ModelID)
		if e != nil || current.validate() != nil {
			v.Tools = false
			v.ToolCapability = "unavailable"
			v.Reason = "Provider unavailable; no tool authority."
		} else if current.Binding() != b.Fingerprint {
			v.Tools = false
			v.ToolCapability = "provider_changed"
			v.Reason = "Provider policy or tool evidence changed; create a new session. Existing approvals cannot be used."
		} else {
			v.ToolCapability = current.toolCapability()
		}
	}
	if mode == "chat_only" {
		v.ToolCapability = "not_applicable"
		v.Reason = "Explicit chat-only: no workspace authorization, file tools or scoped retrieval."
	} else if !b.Tools && v.Reason == "" {
		v.Reason = "Model tool support is not verified; tools are not advertised."
	}
	if err == nil && mode != "chat_only" && mode != "plan" && v.Tools && s.broker.Capabilities().Exec.Enabled {
		w, e := s.store.Workspace(ctx, id)
		if e != nil {
			return v, b, e
		}
		v.NonSandboxExec = w.Policy.NonSandboxExec
		if v.NonSandboxExec {
			v.ExecWarning = interaction.NonSandboxExecWarning
		}
	}
	return v, b, err
}
func projectEvent(ev interaction.Event) WireEvent {
	w := WireEvent{SchemaVersion: 1, SessionID: ev.SessionID, RunID: ev.RunID, Sequence: ev.Sequence, Type: ev.Type, Data: map[string]any{}}
	switch ev.Type {
	case "run.created", "run.completed", "run.failed", "run.cancelled":
		var r interaction.Run
		if json.Unmarshal(ev.Payload, &r) == nil {
			w.Data = map[string]any{"request_id": r.RequestID, "input": r.Message.Content, "state": r.State}
		}
	case "approval.decided":
		var p struct {
			ID       string `json:"id"`
			Digest   string `json:"digest"`
			Decision string `json:"decision"`
		}
		if json.Unmarshal(ev.Payload, &p) == nil {
			w.Data = p
		}
	case "model.text_delta":
		var p struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(ev.Payload, &p) == nil {
			w.Data = map[string]string{"text": p.Text}
		}
	case "model.completed":
		var p model.TurnResult
		if json.Unmarshal(ev.Payload, &p) == nil {
			w.Data = map[string]any{"text": p.Content, "usage": p.Usage}
		}
	case "tool.proposed", "approval.waiting", "tool.started":
		var p interaction.ToolProposal
		if json.Unmarshal(ev.Payload, &p) == nil {
			w.Data = map[string]any{"id": p.ID, "name": p.Call.Name, "arguments": p.Call.Arguments, "requires_approval": p.RequiresApproval, "digest": p.Digest, "expires_at": p.ExpiresAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")}
		}
	case "tool.recovered", "tool.uncertain":
		var p interaction.ToolObservation
		if json.Unmarshal(ev.Payload, &p) == nil {
			w.Data = map[string]any{"id": p.ProposalID, "call_id": p.CallID, "content": p.Content, "is_error": p.IsError}
		}
	case "tool.completed":
		var p interaction.ToolResult
		if json.Unmarshal(ev.Payload, &p) == nil {
			w.Data = map[string]any{"content": p.Content, "is_error": p.IsError}
		}
	}
	return w
}
func applyWire(r *RunView, w WireEvent) {
	wasCancelling := r.Phase == "cancelling"
	defer func() {
		if wasCancelling && r.State == "running" && r.Phase != "needs_reconciliation" {
			r.Phase = "cancelling"
		}
	}()
	raw, _ := json.Marshal(w.Data)
	var d struct {
		RequestID string `json:"request_id"`
		Input     string `json:"input"`
		State     string `json:"state"`
		Text      string `json:"text"`
		ID        string `json:"id"`
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
		Digest    string `json:"digest"`
		ExpiresAt string `json:"expires_at"`
		Content   string `json:"content"`
		Decision  string `json:"decision"`
		IsError   bool   `json:"is_error"`
	}
	_ = json.Unmarshal(raw, &d)
	switch w.Type {
	case "run.created":
		r.ID = w.RunID
		r.RequestID = d.RequestID
		r.Input = d.Input
		r.State = "running"
		r.Phase = "queued"
		r.Tools = []ToolView{}
	case "model.started":
		r.Phase = "model"
		r.Draft = ""
	case "model.text_delta":
		r.Draft += d.Text
	case "model.completed":
		r.Text += d.Text
		r.Draft = ""
		r.Phase = "running"
	case "tool.proposed":
		r.Tools = append(r.Tools, ToolView{ID: d.ID, Name: d.Name, Arguments: d.Arguments, State: "proposed"})
		r.Phase = "running"
	case "approval.waiting":
		r.Phase = "waiting_approval"
		r.Approval = &ApprovalView{ID: d.ID, Digest: d.Digest, ExpiresAt: d.ExpiresAt, Decision: "pending"}
	case "approval.decided":
		if r.Approval != nil && r.Approval.ID == d.ID {
			r.Approval.Decision = d.Decision
		}
	case "tool.started":
		r.Phase = "executing"
		r.Approval = nil
		if len(r.Tools) > 0 {
			r.Tools[len(r.Tools)-1].State = "executing"
		}
	case "tool.recovered", "tool.uncertain":
		for i := range r.Tools {
			t := &r.Tools[i]
			if t.ID == d.ID {
				t.State = "completed"
				if w.Type == "tool.uncertain" {
					t.State = "unknown"
					r.Phase = "needs_reconciliation"
				}
				t.Result = d.Content
				t.IsError = d.IsError
				break
			}
		}
	case "tool.completed":
		r.Phase = "running"
		if len(r.Tools) > 0 {
			t := &r.Tools[len(r.Tools)-1]
			t.State = "completed"
			t.Result = d.Content
			t.IsError = d.IsError
		}
	case "run.waiting_capacity":
		r.Phase = "waiting_capacity"
	case "run.cancelling":
		r.Phase = "cancelling"
	case "run.needs_reconciliation":
		r.Phase = "needs_reconciliation"
	case "run.recovered":
		r.Phase = "running"
		r.Draft = ""
	case "run.completed", "run.failed", "run.cancelled":
		r.State = d.State
		r.Phase = d.State
		r.Approval = nil
	}
}

// snapshot uses an immutable event prefix. It never exports a checkpoint/request.
// Run count and per-run engine event budgets bound projection work.
func (s *InteractionService) snapshot(ctx context.Context, id string) (Snapshot, error) {
	return s.snapshotBefore(ctx, id, 0)
}
func (s *InteractionService) snapshotBefore(ctx context.Context, id string, before int64) (Snapshot, error) {
	v, _, err := s.session(ctx, id)
	if err != nil {
		return Snapshot{}, err
	}
	out := Snapshot{SchemaVersion: 1, Session: v, Runs: []RunView{}}
	tx, err := s.runtime.DB.SQL.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = tx.QueryRowContext(ctx, "SELECT sequence FROM interaction_sessions WHERE id=?", id).Scan(&out.Cursor); err != nil {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, "SELECT run_id,sequence FROM interaction_events WHERE session_id=? AND type='run.created' AND sequence<=? AND (?=0 OR sequence<?) ORDER BY sequence DESC LIMIT ?", id, out.Cursor, before, before, snapshotRunLimit+1)
	if err != nil {
		return out, err
	}
	var ids []string
	var sequences []int64
	for rows.Next() {
		var rid string
		var seq int64
		if err = rows.Scan(&rid, &seq); err != nil {
			rows.Close()
			return out, err
		}
		ids = append(ids, rid)
		sequences = append(sequences, seq)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if len(ids) > snapshotRunLimit {
		out.HasOlder = true
		ids = ids[:snapshotRunLimit]
		cursor := sequences[snapshotRunLimit-1]
		out.NextBefore = &cursor
	}
	for i := len(ids) - 1; i >= 0; i-- {
		rows, err = tx.QueryContext(ctx, "SELECT version,session_id,run_id,sequence,type,payload FROM interaction_events WHERE run_id=? AND sequence<=? ORDER BY sequence", ids[i], out.Cursor)
		if err != nil {
			return out, err
		}
		r := RunView{Tools: []ToolView{}}
		for rows.Next() {
			var ev interaction.Event
			var raw string
			if err = rows.Scan(&ev.Version, &ev.SessionID, &ev.RunID, &ev.Sequence, &ev.Type, &raw); err != nil {
				rows.Close()
				return out, err
			}
			ev.Payload = json.RawMessage(raw)
			applyWire(&r, projectEvent(ev))
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return out, err
		}
		out.Runs = append(out.Runs, r)
	}
	return out, tx.Commit()
}
