package api

import (
	"agent/internal/interaction"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"unicode/utf8"

	"github.com/google/uuid"
)

type SessionAdmissionView struct {
	MCPOptIn     *bool                     `json:"mcp_opt_in,omitempty"`
	RequestID    string                    `json:"request_id"`
	SessionID    string                    `json:"session_id"`
	State        string                    `json:"state"`
	Reason       string                    `json:"reason"`
	ProjectLabel *interaction.ProjectLabel `json:"project_label,omitempty"`
}

type mcpAdmission struct {
	SessionAdmissionView
	fingerprint string
	kind        string
}

func (s *InteractionService) admission(ctx context.Context, request string) (mcpAdmission, error) {
	a := mcpAdmission{SessionAdmissionView: SessionAdmissionView{RequestID: request}}
	e := s.runtime.DB.SQL.QueryRowContext(ctx, "SELECT fingerprint,session_id,state,kind FROM interaction_mcp_admissions WHERE request_id=?", request).Scan(&a.fingerprint, &a.SessionID, &a.State, &a.kind)
	if e == nil {
		optIn := a.kind == "mcp"
		a.MCPOptIn = &optIn
	}
	a.Reason = "Admission is single-use; inspection never launches. For unresolved/revoked admission, inspect the session if bound or create a new request explicitly. Startup effects may already have occurred."
	if e == nil && a.State == "complete" {
		a.ProjectLabel, e = s.store.ProjectLabel(ctx, a.SessionID)
	}
	return a, e
}

// Called with s.mu held. Claim ownership durably before crossing the startup
// effect boundary, then release the service lock for all external operations.
// The worker lifetime is joined by Close; request/service cancellation aborts
// preparation, but never removes the durable claim or allows its reuse.
func (s *InteractionService) createAdmittedSession(w http.ResponseWriter, r *http.Request, in writeInput, fingerprint, modelID string) {
	ctx := r.Context()
	if s.preparations >= maxInteractionWorkers {
		wireError(w, 503, "service_capacity")
		return
	}
	id := uuid.NewString()
	kind := "session"
	if in.MCP {
		kind = "mcp"
	}
	if _, e := s.runtime.DB.SQL.ExecContext(ctx, "INSERT INTO interaction_mcp_admissions(request_id,fingerprint,session_id,state,kind) VALUES(?,?,?,'starting',?)", in.RequestID, fingerprint, id, kind); e != nil {
		wireError(w, 500, "storage_error")
		return
	}
	s.preparations++
	s.wg.Add(1)
	s.mu.Unlock()
	defer s.wg.Done()
	defer func() { s.mu.Lock(); s.preparations-- }() // Restore caller's lock contract.
	admissionCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(s.ctx, cancel)
	defer func() { stop(); cancel() }()
	committed := false
	defer func() {
		if !committed {
			s.broker.ForgetMCPSession(id)
			// starting remains non-replayable if this best-effort status write fails.
			_, _ = s.runtime.DB.SQL.ExecContext(context.Background(), "UPDATE interaction_mcp_admissions SET state='unresolved' WHERE request_id=? AND state='starting'", in.RequestID)
		}
	}()
	fail := func() {
		code := "session_admission_unresolved"
		if in.MCP {
			code = "mcp_admission_unresolved"
		}
		wireJSON(w, 409, map[string]any{"schema_version": 1, "error": map[string]string{"code": code, "message": "Preparation may have occurred. Inspect admission; a new explicit request is required."}, "session_id": id, "admission_path": "/api/v1/admissions/" + url.PathEscape(in.RequestID)})
	}
	// Resolve once, outside service/database locks, before publishing anything.
	sel, e := s.options.Resolve(admissionCtx, modelID)
	if e != nil || sel.validate() != nil || sel.Model.ID != modelID {
		wireError(w, 409, "provider_unavailable")
		return
	}
	if in.MCP && (in.Mode == "chat_only" || !sel.ToolsSupported || !s.options.MCP.Enabled) {
		wireError(w, 409, "mcp_unavailable")
		return
	}
	var workspace interaction.WorkspaceBinding
	if in.Mode != "chat_only" {
		policy := interaction.WorkspacePolicy{Root: s.options.Workspace, Mode: interaction.PermissionMode(in.Mode), NonSandboxExec: s.options.NonSandboxExec}
		// Validate the workspace (including DB exclusion) before process startup.
		workspace, e = s.store.PrepareWorkspace(admissionCtx, id, policy)
		if e != nil {
			wireError(w, 409, "workspace_unavailable_choose_chat_only")
			return
		}
		if in.MCP {
			policy, e = s.broker.PrepareMCPPolicy(admissionCtx, id, workspace.Policy)
			if e != nil {
				fail()
				return
			}
			workspace.Policy = policy
		}
	}
	sel = sel.forMode(in.Mode)
	binding, _ := json.Marshal(sessionBinding{ModelID: modelID, Fingerprint: sel.Binding(), Tools: sel.ToolsSupported})
	// The response is staged from the exact resolved binding, not a second
	// provider resolution or a read of an incomplete public session.
	body := SessionView{ID: id, ModelID: modelID, Mode: in.Mode, Files: in.Mode != "chat_only", Tools: sel.ToolsSupported && in.Mode != "chat_only", ToolCapability: sel.toolCapability()}
	mcpStatus := s.broker.StagedMCPStatus(id, workspace.Policy.MCP)
	body.MCP = &mcpStatus
	if in.Mode == "chat_only" {
		body.ToolCapability = "not_applicable"
		body.Reason = "Explicit chat-only: no workspace authorization, file tools or scoped retrieval."
	} else if !body.Tools {
		body.Reason = "Model tool support is not verified; tools are not advertised."
	}
	if in.Mode != "chat_only" && in.Mode != "plan" && body.Tools && s.broker.Capabilities().Exec.Enabled {
		body.NonSandboxExec = workspace.Policy.NonSandboxExec
		if body.NonSandboxExec {
			body.ExecWarning = interaction.NonSandboxExecWarning
		}
	}
	tx, e := s.runtime.DB.SQL.BeginTx(admissionCtx, nil)
	if e != nil {
		fail()
		return
	}
	defer tx.Rollback()
	// First write reserves SQLite's writer before validating the live catalog.
	// Archive-before-publication refuses. Archive-after cannot change this label.
	if _, e = tx.ExecContext(admissionCtx, "INSERT INTO interaction_sessions(id) VALUES(?)", id); e != nil {
		fail()
		return
	}
	body.ProjectLabel, e = interaction.ActiveProjectLabel(admissionCtx, tx, in.CatalogProjectID)
	if e != nil {
		fail()
		return
	}
	if in.Mode == "chat_only" {
		policy, _ := json.Marshal(interaction.ChatOnlyPolicy{})
		_, e = tx.ExecContext(admissionCtx, "INSERT INTO interaction_chat_policies(session_id,policy) VALUES(?,?)", id, string(policy))
	} else {
		policy, _ := json.Marshal(workspace.Policy)
		_, e = tx.ExecContext(admissionCtx, "INSERT INTO interaction_workspaces(session_id,scope_id,policy,root_identity) VALUES(?,?,?,?)", id, workspace.ScopeID, string(policy), workspace.RootIdentity)
	}
	if e != nil {
		fail()
		return
	}
	if _, e = tx.ExecContext(admissionCtx, "INSERT INTO interaction_api_sessions(session_id,request_id,binding,mode) VALUES(?,?,?,?)", id, in.RequestID, string(binding), in.Mode); e != nil {
		fail()
		return
	}
	if e = interaction.InsertProjectLabel(admissionCtx, tx, id, body.ProjectLabel); e != nil {
		fail()
		return
	}
	raw, _ := json.Marshal(body)
	record, _ := json.Marshal(actionResult{Status: 201, Body: raw})
	if _, e = tx.ExecContext(admissionCtx, "INSERT INTO interaction_api_actions(request_id,fingerprint,result) VALUES(?,?,?)", in.RequestID, fingerprint, string(record)); e == nil {
		var result sql.Result
		result, e = tx.ExecContext(admissionCtx, "UPDATE interaction_mcp_admissions SET state='complete' WHERE request_id=? AND state='starting'", in.RequestID)
		if e == nil {
			var n int64
			n, e = result.RowsAffected()
			if e == nil && n != 1 {
				e = interaction.ErrConflict
			}
		}
	}
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		fail()
		return
	}
	committed = true
	wireJSON(w, 201, body)
}

// Check all POST routes, not just MCP sessions: changing route or removing mcp
// from a request ID cannot bypass a pre-existing startup reservation.
func (s *InteractionService) rejectAdmissionReplay(w http.ResponseWriter, ctx context.Context, request, fingerprint string) bool {
	a, e := s.admission(ctx, request)
	if errors.Is(e, sql.ErrNoRows) {
		return false
	}
	if e != nil {
		wireError(w, 500, "storage_error")
		return true
	}
	if a.fingerprint != fingerprint {
		wireError(w, 409, "request_id_conflict")
		return true
	}
	code := "session_admission_unresolved"
	if a.kind == "mcp" {
		code = "mcp_admission_unresolved"
	}
	wireJSON(w, 409, map[string]any{"schema_version": 1, "error": map[string]string{"code": code, "message": a.Reason}, "admission": a})
	return true
}

// encoding/json replaces malformed Unicode with U+FFFD. Identity-bearing input
// must refuse that lossy conversion while accepting valid UTF-16 surrogate pairs.
func exactJSONStringUnicode(raw []byte) bool { return utf8.Valid(raw) && knowledgeUnicodeJSON(raw) }

func (s *InteractionService) validateSessionReplay(ctx context.Context, in writeInput, raw json.RawMessage) error {
	var body struct {
		ID           string          `json:"id"`
		ProjectLabel json.RawMessage `json:"project_label"`
	}
	if e := json.Unmarshal(raw, &body); e != nil {
		return e
	}
	var id string
	if e := s.runtime.DB.SQL.QueryRowContext(ctx, "SELECT session_id FROM interaction_api_sessions WHERE request_id=?", in.RequestID).Scan(&id); e != nil {
		return e
	}
	if id != body.ID {
		return interaction.ErrInvalid
	}
	var label *interaction.ProjectLabel
	if len(body.ProjectLabel) != 0 {
		var e error
		label, e = decodeStoredProjectLabel(body.ProjectLabel)
		if e != nil {
			return e
		}
	}
	if label == nil && in.CatalogProjectID != "" || label != nil && label.ProjectID != in.CatalogProjectID {
		return interaction.ErrInvalid
	}
	stored, e := s.store.ProjectLabel(ctx, id)
	if e != nil {
		return e
	}
	if !reflect.DeepEqual(label, stored) {
		return interaction.ErrInvalid
	}
	return nil
}

// Stored wire data must be exact before encoding/json can normalize it. In
// particular an absent/null/duplicate name is not the valid historical "" name.
func decodeStoredProjectLabel(raw json.RawMessage) (*interaction.ProjectLabel, error) {
	if !exactJSONStringUnicode(raw) {
		return nil, interaction.ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	token, e := d.Token()
	if e != nil || token != json.Delim('{') {
		return nil, interaction.ErrInvalid
	}
	p := &interaction.ProjectLabel{}
	seen := map[string]bool{}
	for d.More() {
		token, e = d.Token()
		key, ok := token.(string)
		if e != nil || !ok || seen[key] {
			return nil, interaction.ErrInvalid
		}
		seen[key] = true
		token, e = d.Token()
		if e != nil {
			return nil, interaction.ErrInvalid
		}
		switch key {
		case "project_id", "generation", "name":
			value, ok := token.(string)
			if !ok {
				return nil, interaction.ErrInvalid
			}
			switch key {
			case "project_id":
				p.ProjectID = value
			case "generation":
				p.Generation = value
			case "name":
				p.Name = value
			}
		case "revision":
			number, ok := token.(json.Number)
			if !ok {
				return nil, interaction.ErrInvalid
			}
			p.Revision, e = number.Int64()
			if e != nil {
				return nil, interaction.ErrInvalid
			}
		default:
			return nil, interaction.ErrInvalid
		}
	}
	token, e = d.Token()
	if e != nil || token != json.Delim('}') || len(seen) != 4 || d.Decode(new(any)) != io.EOF || !p.Valid() {
		return nil, interaction.ErrInvalid
	}
	return p, nil
}
