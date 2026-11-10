package api

import (
	"agent/internal/interaction"
	"agent/internal/model"
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Operator DTOs intentionally contain metadata only. No config, raw errors,
// manifests, arguments, document bodies, environment or filesystem paths.
type OperatorField struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}
type OperatorRecord struct {
	ID     string          `json:"id"`
	Label  string          `json:"label"`
	State  string          `json:"state"`
	Fields []OperatorField `json:"fields"`
}
type OperatorAction struct {
	Action       string `json:"action"`
	Availability string `json:"availability"`
	Reason       string `json:"reason"`
}
type OperatorPage struct {
	SchemaVersion int              `json:"schema_version"`
	Namespace     string           `json:"namespace"`
	SessionID     string           `json:"session_id"`
	ScopeID       string           `json:"scope_id"`
	Resource      string           `json:"resource"`
	Owner         string           `json:"owner"`
	Filter        string           `json:"filter"`
	Notice        string           `json:"notice"`
	Items         []OperatorRecord `json:"items"`
	Actions       []OperatorAction `json:"actions"`
	NextCursor    string           `json:"next_cursor"`
}
type operatorSource struct {
	table, id, label, state string
	fields                  []string
	names                   []string
	where                   string
	parentTable, parentKey  string
	filters                 []string
	mutations               string
}

// These are literal, reviewed SQL expressions, never supplied by callers. Rows
// and each selected field are bounded in SQLite before allocation in Go.
var operatorSources = map[string]operatorSource{
	"tasks":           {table: "tasks t", id: "t.id", label: "t.title", state: "t.status", fields: []string{"t.leader_model_id", "t.privacy_class"}, names: []string{"model_id", "privacy"}, filters: []string{"pending", "running", "completed", "failed", "cancelled"}, mutations: "create,run,resume,cancel"},
	"workflows":       {table: "workflow_runs t", id: "t.id", label: "t.skill_id", state: "t.status", fields: []string{"t.task_id", "t.project_id", "t.skill_version"}, names: []string{"task_id", "project_id", "skill_version"}, filters: []string{"pending", "running", "completed", "failed", "cancelled"}, mutations: "admit,run,resume,cancel"},
	"nodes":           {table: "workflow_nodes t", id: "t.node_id", label: "t.capability_id", state: "t.status", fields: []string{"t.workflow_id", "t.attempt"}, names: []string{"workflow_id", "attempt"}, where: "t.workflow_id=?", parentTable: "workflow_runs", parentKey: "id"},
	"checkpoints":     {table: "workflow_checkpoints t", id: "t.id", label: "t.node_id", state: "t.status", fields: []string{"t.workflow_id", "t.created_at"}, names: []string{"workflow_id", "created_at"}, where: "t.workflow_id=?", parentTable: "workflow_runs", parentKey: "id"},
	"evidence":        {table: "evidence t", id: "t.id", label: "t.type", state: "t.privacy_class", fields: []string{"t.task_id", "t.node_id", "t.content_hash"}, names: []string{"task_id", "node_id", "content_hash"}, where: "t.task_id=?", parentTable: "tasks", parentKey: "id"},
	"projects":        {table: "projects t JOIN project_catalog_lifecycle l ON l.project_id=t.id JOIN project_execution_classification c ON c.project_id=t.id", id: "t.id", label: "t.name", state: "l.state", fields: []string{"t.privacy_class", "'catalog only; not permission revocation'", "c.execution_class", "CASE WHEN c.execution_class='metadata_only' THEN 'no tools/models/workflows/knowledge isolation enabled' ELSE 'existing legacy policy; catalog state does not revoke execution' END"}, names: []string{"privacy", "binding", "execution_class", "execution_notice"}, filters: []string{"active", "archived"}, mutations: "rename,archive,restore,create,update,use"},
	"knowledge":       {table: "documents t JOIN project_document_scopes o ON o.document_id=t.id JOIN document_revisions v ON v.document_id=t.id AND v.revision=o.revision", id: "t.id", label: "t.title", state: "t.privacy_class", fields: []string{"o.project_id", "v.revision", "t.content_hash"}, names: []string{"owner_scope", "revision", "content_hash"}, where: "o.project_id=? AND o.content_hash=t.content_hash", mutations: "ingest,deactivate,reindex,search,assign"},
	"skills":          {table: "skills t", id: "t.id", label: "t.name", state: "t.status", fields: []string{"t.active_version"}, names: []string{"active_version"}, filters: []string{"active", "disabled", "draft"}, mutations: "import,validate,enable,disable,run"},
	"versions":        {table: "skill_versions t", id: "t.version", label: "t.skill_id", state: "'stored; not evaluation proof'", fields: []string{"t.checksum", "t.created_at"}, names: []string{"checksum", "created_at"}, where: "t.skill_id=?", parentTable: "skills", parentKey: "id"},
	"triggers":        {table: "triggers t", id: "t.id", label: "t.type", state: "CASE WHEN t.enabled THEN 'enabled_record' ELSE 'disabled_record' END", fields: []string{"t.project_id", "t.skill_id"}, names: []string{"project_id", "skill_id"}, mutations: "create,update,enable,disable,run"},
	"trigger-state":   {table: "trigger_state t", id: "t.trigger_id", label: "'stored schedule state'", state: "'not scheduler health'", fields: []string{"t.next_fire_at", "t.last_fired_at", "t.consecutive_failures"}, names: []string{"next_fire_at", "last_fired_at", "consecutive_failures"}, where: "t.trigger_id=?", parentTable: "triggers", parentKey: "id"},
	"trigger-history": {table: "trigger_history t", id: "t.id", label: "t.event_id", state: "t.status", fields: []string{"t.trigger_id", "t.workflow_id", "t.created_at"}, names: []string{"trigger_id", "workflow_id", "created_at"}, where: "t.trigger_id=?", parentTable: "triggers", parentKey: "id"},
	"deadletters":     {table: "dead_letter_events t", id: "t.id", label: "t.source_type", state: "'stored; payload withheld'", fields: []string{"t.source_id", "t.created_at"}, names: []string{"source_id", "created_at"}, mutations: "retry,delete"},
	"notifications":   {table: "notification_inbox t", id: "t.id", label: "t.title", state: "t.status", fields: []string{"t.project_id", "t.trigger_id", "t.created_at"}, names: []string{"project_id", "trigger_id", "created_at"}, filters: []string{"unread", "read"}, mutations: "mark-read,delete"},
	"approvals":       {table: "interaction_approvals t JOIN interaction_runs r ON r.id=t.run_id", id: "t.id", label: "t.run_id", state: "t.decision", fields: []string{"t.action_digest", "t.expires_at"}, names: []string{"digest", "expires_at_unix_nano"}, where: "r.session_id=? AND t.decision='pending' AND t.consumed_at IS NULL"},
	"audit":           {table: "security_audit_log t", id: "t.id", label: "t.action", state: "t.status", fields: []string{"t.created_at"}, names: []string{"created_at"}},
}

func operatorRoute(p []string) bool { return len(p) == 4 && p[0] == "sessions" && p[2] == "operator" }
func operatorQuery(q url.Values) bool {
	for k, v := range q {
		if (k != "limit" && k != "cursor" && k != "filter" && k != "owner") || len(v) != 1 || v[0] == "" || len(v[0]) > 2048 {
			return false
		}
	}
	return true
}

var errOperatorCursor = errors.New("invalid operator cursor")

type operatorCursor struct {
	Session, Resource, Owner, Filter string
	After                            int64
}

func operatorText(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			return -1
		}
		return r
	}, s)
	r := []rune(s)
	if len(r) > 512 {
		return string(r[:512]) + "…"
	}
	return s
}
func opRecord(id, label, state string, fields ...OperatorField) OperatorRecord {
	if fields == nil {
		fields = []OperatorField{}
	}
	if len(id) > 512 {
		id = "[identifier exceeds bound]"
	}
	// Keep opaque IDs exact. Display sanitization must never create an alias for
	// another record; clients reject unsafe IDs for owner drilldowns.
	return OperatorRecord{id, operatorText(label), operatorText(state), fields}
}
func opField(name, value string) OperatorField { return OperatorField{name, operatorText(value)} }
func (s *InteractionService) readOperator(w http.ResponseWriter, r *http.Request, p []string) bool {
	if !operatorRoute(p) {
		return false
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	q := r.URL.Query()
	resource, session := p[3], p[1]
	def, stored := operatorSources[resource]
	if !stored && resource != "permissions" && resource != "models" && resource != "mcp" && resource != "diagnostics" {
		wireError(w, 404, "operator_resource_not_found")
		return true
	}
	limit := 25
	if q.Has("limit") {
		n, e := strconv.Atoi(q.Get("limit"))
		if e != nil || n < 1 || n > 50 {
			wireError(w, 400, "invalid_query")
			return true
		}
		limit = n
	}
	owner, filter := q.Get("owner"), q.Get("filter")
	if len(owner) > 256 || strings.ContainsAny(owner, "\x00/\\") || (def.parentTable == "" && owner != "") || (def.parentTable != "" && owner == "") {
		wireError(w, 400, "invalid_owner")
		return true
	}
	if filter != "" {
		ok := false
		for _, f := range def.filters {
			if f == filter {
				ok = true
			}
		}
		if !ok {
			wireError(w, 400, "invalid_filter")
			return true
		}
	}
	cursor := operatorCursor{session, resource, owner, filter, 0}
	if q.Has("cursor") {
		raw, e := base64.RawURLEncoding.DecodeString(q.Get("cursor"))
		var c operatorCursor
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if e != nil || !json.Valid(raw) || decoder.Decode(&c) != nil || c.Session != session || c.Resource != resource || c.Owner != owner || c.Filter != filter || c.After < 1 {
			wireError(w, 400, "invalid_cursor")
			return true
		}
		cursor = c
	}
	var mode, raw string
	err := s.runtime.DB.SQL.QueryRowContext(ctx, "SELECT mode,binding FROM interaction_api_sessions WHERE session_id=?", session).Scan(&mode, &raw)
	if err != nil {
		wireError(w, 404, "session_not_found")
		return true
	}
	var binding sessionBinding
	if json.Unmarshal([]byte(raw), &binding) != nil {
		wireError(w, 500, "storage_error")
		return true
	}
	scope := ""
	err = s.runtime.DB.SQL.QueryRowContext(ctx, "SELECT scope_id FROM interaction_workspaces WHERE session_id=?", session).Scan(&scope)
	if err != nil && err != sql.ErrNoRows {
		wireError(w, 500, "storage_error")
		return true
	}
	page := OperatorPage{1, "runtime-rebuild-v1", session, scope, resource, owner, filter, "Read-only rebuilt runtime. Empty business tables: legacy data not imported. Metadata is not execution evidence. Text fields capped at 512 characters.", []OperatorRecord{}, []OperatorAction{{"read", "available", "Bounded stored metadata only; no probes or execution."}}, ""}
	for _, action := range strings.Split(def.mutations, ",") {
		if action != "" {
			availability, reason := "not_implemented", "Requires rebuilt deterministic management admission, approval and reconciliation."
			if resource == "projects" && (action == "rename" || action == "archive" || action == "restore" || action == "create") || resource == "notifications" && action == "mark-read" {
				availability, reason = "available", "Exact preview and explicit confirmation required; local metadata only, no execution or authority change."
			}

			if resource == "projects" && action == "create" {
				reason = "Exact preview and confirmation create metadata_only only: no tools/models/workflows/knowledge isolation enabled; no session association."
			}
			if resource == "projects" && availability == "not_implemented" {
				switch action {
				case "use":
					reason = "No current-session relabeling; select an optional descriptive label only when creating a new session. Existing workspace/scope bindings are immutable."
				case "update":
					reason = "No project policy editing is implemented; catalog state never grants or revokes execution or knowledge authority."
				}
			}
			if resource == "knowledge" && action != "assign" {
				availability, reason = "unsupported", interaction.ErrKnowledgePolicy.Error()
				if scope != "" && (mode == "default" || mode == "acceptEdits" || action == "search" && mode == "plan") {
					availability, reason = "available", "Explicit local knowledge API; exact staged preview and confirmation for writes. Deactivation retains evidence. Search invokes local BM25, never a provider."
					if !s.broker.Capabilities().Files.Enabled {
						availability, reason = "unsupported", s.broker.Capabilities().Files.Reason
					}
				}
			}
			page.Actions = append(page.Actions, OperatorAction{action, availability, reason})
		}
	}
	switch resource {
	case "tasks", "workflows":
		page.Notice += " Business task/workflow records are distinct from interaction runs, input queues and Jobs."
	case "projects":
		page.Notice += " Project IDs are metadata, not workspace ScopeIDs; current immutable scope shown separately. Catalog only; not permission revocation. Archive/restore does not stop jobs/runs, revoke tools or remove knowledge. Project labels preserve exact text up to 2048 UTF-8 bytes instead of the generic character cap; larger labels are unavailable."
	case "knowledge":
		page.Notice += " Only current immutable workspace ownership and matching stored revision; chat-only has no scope. No document contents or source paths."
	case "notifications":
		page.Notice += " In-app record is not delivery to email, Slack or push."
	case "triggers", "trigger-history":
		page.Notice += " Stored enabled/completed status does not attest a running scheduler or execution."
	case "audit":
		page.Notice += " Source: security_audit_log; incomplete coverage of rebuilt interactions. Rebuilt events/approvals/journal remain separate session records."
	}
	var after int64
	if stored {
		if resource == "projects" {
			var missing int
			if e := s.runtime.DB.SQL.QueryRowContext(ctx, `SELECT count(*) FROM projects p LEFT JOIN project_catalog_lifecycle l ON l.project_id=p.id LEFT JOIN project_execution_classification c ON c.project_id=p.id WHERE l.project_id IS NULL OR c.execution_class IS NULL OR c.execution_class NOT IN ('legacy_policy','metadata_only')`).Scan(&missing); e != nil || missing != 0 {
				wireError(w, 500, "storage_error")
				return true
			}
		}
		page.Items, after, err = s.operatorRows(ctx, def, scope, session, owner, filter, cursor.After, limit)
	} else {
		page.Items, err = s.operatorStatic(ctx, resource, session, mode, scope, binding)
		if err == nil {
			start := cursor.After
			if start > int64(len(page.Items)) {
				err = errOperatorCursor
			} else {
				end := start + int64(limit)
				if end < int64(len(page.Items)) {
					after = end
				} else {
					end = int64(len(page.Items))
				}
				page.Items = page.Items[start:end]
			}
		}
		page.Actions = append(page.Actions, operatorStaticActions(resource)...)
	}
	if err != nil {
		if err == errOperatorCursor {
			wireError(w, 400, "invalid_cursor")
		} else if err == sql.ErrNoRows {
			wireError(w, 404, "owner_not_found")
		} else {
			wireError(w, 500, "storage_error")
		}
		return true
	}
	if after > 0 {
		cursor.After = after
		b, _ := json.Marshal(cursor)
		page.NextCursor = base64.RawURLEncoding.EncodeToString(b)
	}
	wireJSON(w, 200, page)
	return true
}
func (s *InteractionService) operatorRows(ctx context.Context, d operatorSource, scope, session, owner, filter string, after int64, limit int) ([]OperatorRecord, int64, error) {
	if d.parentTable != "" {
		var found int
		if e := s.runtime.DB.SQL.QueryRowContext(ctx, "SELECT 1 FROM "+d.parentTable+" WHERE "+d.parentKey+"=?", owner).Scan(&found); e != nil {
			return nil, 0, e
		}
	}
	cols := []string{d.id, d.label, d.state}
	cols = append(cols, d.fields...)
	for i, c := range cols {
		cols[i] = "substr(COALESCE(CAST(" + c + " AS TEXT),''),1,512)"
		if i == 1 && strings.HasPrefix(d.table, "projects ") {
			// SQLite TEXT substr truncates at embedded NUL. Select only bounded
			// complete labels so visible escaping never hides a control suffix.
			cols[i] = "CASE WHEN length(CAST(" + c + " AS BLOB))<=2048 THEN CAST(" + c + " AS TEXT) ELSE '[name exceeds metadata bound]' END"
		}
		if i == 0 {
			unavailable := "'[identifier exceeds bound]'"
			if strings.HasPrefix(d.table, "projects ") {
				// Project IDs feed mutation controls. A display placeholder could
				// alias a different real ID, so NULL makes Scan refuse this page.
				// Keep the bound and exact ID in one query, with no preflight race.
				unavailable = "NULL"
			}
			cols[i] = "CASE WHEN length(CAST(" + c + " AS BLOB))<=512 THEN CAST(" + c + " AS TEXT) ELSE " + unavailable + " END"
		}
	}
	query := "SELECT t.rowid," + strings.Join(cols, ",") + " FROM " + d.table + " WHERE t.rowid>?"
	args := []any{after}
	if d.where != "" {
		query += " AND " + d.where
		switch {
		case d.parentTable != "":
			args = append(args, owner)
		case strings.HasPrefix(d.table, "documents"):
			args = append(args, scope)
		default:
			args = append(args, session)
		}
	}
	if filter != "" {
		query += " AND " + d.state + "=?"
		args = append(args, filter)
	}
	query += " ORDER BY t.rowid LIMIT ?"
	args = append(args, limit+1)
	rows, e := s.runtime.DB.SQL.QueryContext(ctx, query, args...)
	if e != nil {
		return nil, 0, e
	}
	defer rows.Close()
	out := []OperatorRecord{}
	var last int64
	for rows.Next() {
		var rowid int64
		values := make([]string, len(cols))
		dest := []any{&rowid}
		for i := range values {
			dest = append(dest, &values[i])
		}
		if e = rows.Scan(dest...); e != nil {
			return nil, 0, e
		}
		if len(out) == limit {
			return out, last, nil
		}
		rec := opRecord(values[0], values[1], values[2])
		if strings.HasPrefix(d.table, "projects ") {
			rec.Label = values[1]
		}
		for i, name := range d.names {
			rec.Fields = append(rec.Fields, opField(name, values[i+3]))
		}
		out = append(out, rec)
		last = rowid
	}
	return out, 0, rows.Err()
}
func operatorStaticActions(resource string) []OperatorAction {
	out := []OperatorAction{}
	actions := map[string]string{"permissions": "change-policy", "models": "configure,probe", "mcp": "configure,reconnect", "diagnostics": "integrity-check,backup,export,import,restore,retention,vacuum"}
	for _, a := range strings.Split(actions[resource], ",") {
		out = append(out, OperatorAction{a, "not_implemented", "No management write or probe on inventory views."})
	}
	if resource == "diagnostics" {
		for _, a := range []string{"browser-adapter", "coding-adapter", "external-notification", "native-macos-windows-files"} {
			out = append(out, OperatorAction{a, "unsupported", "No validated rebuilt external adapter/native platform; Linux-only file adapter remains unchanged."})
		}
	}
	return out
}
func (s *InteractionService) operatorStatic(ctx context.Context, resource, session, mode, scope string, b sessionBinding) ([]OperatorRecord, error) {
	out := []OperatorRecord{}
	switch resource {
	case "permissions":
		out = append(out, opRecord(session, "Immutable session policy", mode, opField("scope_id", scope), opField("model_id", b.ModelID), opField("admitted_model_tools", strconv.FormatBool(b.Tools)), opField("authority", "Immutable mode and exact tool proposals; empty restrictive sets grant nothing. Project Allowed* metadata does not grant session authority.")))
		if scope != "" {
			p, e := s.store.Workspace(ctx, session)
			if e != nil {
				return nil, e
			}
			out = append(out, opRecord(scope, "Workspace identity (path withheld)", "immutable", opField("root_identity", p.RootIdentity), opField("non_sandbox_exec", strconv.FormatBool(p.Policy.NonSandboxExec)), opField("system_constraint_count", strconv.Itoa(len(p.Policy.SystemConstraints))), opField("user_constraint_count", strconv.Itoa(len(p.Policy.UserConstraints)))))
		}
	case "models":
		out = append(out, s.options.OperatorModels...)
		if len(out) == 0 {
			out = append(out, opRecord(s.options.DefaultModel, "Host configured default", "configured; not re-resolved on view"))
		}
		out = append(out, opRecord(b.ModelID, "Current immutable admission", "resolved_at_session_creation", opField("tools_admitted", strconv.FormatBool(b.Tools)), opField("verification", "Admission is not current provider health or a new probe. See startup inventory evidence; changed configuration requires new admission.")))
	case "mcp":
		status := s.broker.MCPStatus(ctx, session)
		for _, server := range status.Servers {
			out = append(out, opRecord(server.ID, server.ID, server.State, opField("reason", server.Reason), opField("tool_count", strconv.Itoa(server.Tools)), opField("protocol_version", server.ProtocolVersion), opField("config_digest", server.ConfigFingerprint)))
		}
	case "diagnostics":
		cap := s.broker.Capabilities()
		out = append(out, opRecord("runtime", "Locked rebuilt runtime", "open", opField("os", runtime.GOOS), opField("schema", "Recognized catalog/ledger checked before writable startup; not an integrity check."), opField("file_adapter", strconv.FormatBool(cap.Files.Enabled)), opField("file_reason", cap.Files.Reason), opField("exec_adapter", strconv.FormatBool(cap.Exec.Enabled)), opField("exec_reason", cap.Exec.Reason)))
		for _, table := range []string{"schema_migrations", "tasks", "workflow_runs", "projects", "documents", "skills", "triggers", "notification_inbox", "interaction_sessions", "interaction_runs", "interaction_queue", "interaction_jobs"} {
			var n int
			e := s.runtime.DB.SQL.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&n)
			if e != nil {
				return nil, e
			}
			out = append(out, opRecord(table, "Storage row count", "metadata", opField("count", strconv.Itoa(n))))
		}
	}
	return out, nil
}

// OperatorModelSnapshot is called at daemon startup, not on a GET. Resolution
// constructs provider metadata but does not stream or perform synthetic probes.
func OperatorModelSnapshot(id string, p ProviderSelection, err error) OperatorRecord {
	if err != nil || p.validate() != nil {
		return opRecord(id, id, "configured_unresolved", opField("reason", "Provider configuration unavailable at startup; no probe performed."))
	}
	return opRecord(id, id, "configured_resolved", opField("provider_id", p.Model.ProviderID), opField("configured_chat", strconv.FormatBool(p.Model.Capabilities[model.CapabilityChat])), opField("configured_tool_calling", strconv.FormatBool(p.Model.Capabilities[model.CapabilityToolCalling])), opField("tool_capability", p.toolCapability()), opField("synthetic_evidence_id", p.ToolEvidenceID), opField("reason", safeToolEvidenceReason(p.ToolCapabilityReason)), opField("snapshot", "Startup metadata only, not live health. Synthetic verification is not real-platform validation."))
}
