package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	agentEvent "agent/internal/event"
	"agent/internal/notification"
	"agent/internal/project"
	"agent/internal/skill"
	"agent/internal/storage"
	"agent/internal/trigger"
	"agent/internal/workflow"
)

type listQuery struct {
	Limit        int
	Offset       int
	Status       string
	ProjectID    string
	Type         string
	Enabled      *bool
	PrivacyClass string
	SkillID      string
	Source       string
	Severity     string
	Q            string
}

type paginationMeta struct {
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
	Count  int `json:"count"`
}

type storedEventResponse struct {
	ID            string          `json:"id"`
	Source        string          `json:"source"`
	Type          string          `json:"type"`
	ProjectID     string          `json:"project_id"`
	Payload       json.RawMessage `json:"payload,omitempty"`
	PrivacyClass  string          `json:"privacy_class"`
	TrustLevel    string          `json:"trust_level"`
	OccurredAt    string          `json:"occurred_at,omitempty"`
	ReceivedAt    string          `json:"received_at,omitempty"`
	DedupKey      string          `json:"dedup_key,omitempty"`
	CorrelationID string          `json:"correlation_id,omitempty"`
	CausationID   string          `json:"causation_id,omitempty"`
	EventDepth    int             `json:"event_depth"`
}

func parseListQuery(r *http.Request, defaultLimit int, maxLimit int) (listQuery, error) {
	q := r.URL.Query()
	limit := defaultLimit
	if raw := q.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > maxLimit {
			return listQuery{}, fmt.Errorf("invalid_limit")
		}
		limit = parsed
	}
	offset := 0
	if raw := q.Get("offset"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 || parsed > 100000 {
			return listQuery{}, fmt.Errorf("invalid_offset")
		}
		offset = parsed
	}
	var enabled *bool
	if raw := q.Get("enabled"); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			return listQuery{}, fmt.Errorf("invalid_enabled")
		}
		enabled = &parsed
	}
	return listQuery{
		Limit:        limit,
		Offset:       offset,
		Status:       strings.TrimSpace(q.Get("status")),
		ProjectID:    strings.TrimSpace(q.Get("project_id")),
		Type:         strings.TrimSpace(q.Get("type")),
		Enabled:      enabled,
		PrivacyClass: strings.TrimSpace(q.Get("privacy_class")),
		SkillID:      strings.TrimSpace(q.Get("skill_id")),
		Source:       strings.TrimSpace(q.Get("source")),
		Severity:     strings.TrimSpace(q.Get("severity")),
		Q:            strings.TrimSpace(q.Get("q")),
	}, nil
}

func paginatedResponse(key string, value any, page listQuery) map[string]any {
	count := 0
	switch items := value.(type) {
	case []taskResponse:
		count = len(items)
	case []workflow.Run:
		count = len(items)
	case []storedEventResponse:
		count = len(items)
	case []notification.Notification:
		count = len(items)
	case []project.Project:
		count = len(items)
	case []trigger.Trigger:
		count = len(items)
	case []skill.Record:
		count = len(items)
	}
	return map[string]any{
		key: value,
		"pagination": paginationMeta{
			Limit:  page.Limit,
			Offset: page.Offset,
			Count:  count,
		},
	}
}

func listTaskRows(ctx context.Context, db *sql.DB, page listQuery) ([]storage.Task, error) {
	clauses := []string{}
	args := []any{}
	if page.Status != "" {
		clauses = append(clauses, "status = ?")
		args = append(args, page.Status)
	}
	if page.PrivacyClass != "" {
		clauses = append(clauses, "privacy_class = ?")
		args = append(args, page.PrivacyClass)
	}
	if page.Q != "" {
		clauses = append(clauses, "(title LIKE ? OR final_answer LIKE ?)")
		like := "%" + page.Q + "%"
		args = append(args, like, like)
	}
	query := `
SELECT id, title, input, status, created_at, updated_at, completed_at,
  leader_model_id, privacy_class, final_answer, final_confidence, error_category, error_message
FROM tasks` + whereSQL(clauses) + ` ORDER BY created_at DESC LIMIT ? OFFSET ?`
	args = append(args, page.Limit, page.Offset)
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storage.Task
	for rows.Next() {
		task, err := scanTaskRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, task)
	}
	return out, rows.Err()
}

func scanTaskRow(rows *sql.Rows) (storage.Task, error) {
	var task storage.Task
	err := rows.Scan(&task.ID, &task.Title, &task.Input, &task.Status, &task.CreatedAt, &task.UpdatedAt, &task.CompletedAt, &task.LeaderModelID, &task.PrivacyClass, &task.FinalAnswer, &task.FinalConfidence, &task.ErrorCategory, &task.ErrorMessage)
	return task, err
}

func listWorkflowRows(ctx context.Context, db *sql.DB, page listQuery) ([]workflow.Run, error) {
	clauses := []string{}
	args := []any{}
	if page.Status != "" {
		clauses = append(clauses, "status = ?")
		args = append(args, page.Status)
	}
	if page.ProjectID != "" {
		clauses = append(clauses, "project_id = ?")
		args = append(args, page.ProjectID)
	}
	if page.SkillID != "" {
		clauses = append(clauses, "skill_id = ?")
		args = append(args, page.SkillID)
	}
	query := `SELECT id, task_id, project_id, skill_id, skill_version, status, input_json, result_json, started_at, updated_at, completed_at FROM workflow_runs` + whereSQL(clauses) + ` ORDER BY updated_at DESC LIMIT ? OFFSET ?`
	args = append(args, page.Limit, page.Offset)
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []workflow.Run
	for rows.Next() {
		var run workflow.Run
		if err := rows.Scan(&run.ID, &run.TaskID, &run.ProjectID, &run.SkillID, &run.SkillVersion, &run.Status, &run.InputJSON, &run.ResultJSON, &run.StartedAt, &run.UpdatedAt, &run.CompletedAt); err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

func listEventRows(ctx context.Context, db *sql.DB, page listQuery) ([]storedEventResponse, error) {
	clauses := []string{}
	args := []any{}
	if page.ProjectID != "" {
		clauses = append(clauses, "project_id = ?")
		args = append(args, page.ProjectID)
	}
	if page.Type != "" {
		clauses = append(clauses, "type = ?")
		args = append(args, page.Type)
	}
	if page.Source != "" {
		clauses = append(clauses, "source = ?")
		args = append(args, page.Source)
	}
	if page.PrivacyClass != "" {
		clauses = append(clauses, "privacy_class = ?")
		args = append(args, page.PrivacyClass)
	}
	query := `SELECT id, source, type, project_id, payload, privacy_class, trust_level, occurred_at, received_at, dedup_key, correlation_id, causation_id, event_depth FROM events` + whereSQL(clauses) + ` ORDER BY received_at DESC LIMIT ? OFFSET ?`
	args = append(args, page.Limit, page.Offset)
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storedEventResponse
	for rows.Next() {
		var event agentEvent.Event
		if err := rows.Scan(&event.ID, &event.Source, &event.Type, &event.ProjectID, &event.Payload, &event.PrivacyClass, &event.TrustLevel, &event.OccurredAt, &event.ReceivedAt, &event.DedupKey, &event.CorrelationID, &event.CausationID, &event.EventDepth); err != nil {
			return nil, err
		}
		out = append(out, storedEventToResponse(event))
	}
	return out, rows.Err()
}

func listNotificationRows(ctx context.Context, db *sql.DB, page listQuery) ([]notification.Notification, error) {
	clauses := []string{}
	args := []any{}
	if page.Status != "" {
		clauses = append(clauses, "status = ?")
		args = append(args, page.Status)
	}
	if page.ProjectID != "" {
		clauses = append(clauses, "project_id = ?")
		args = append(args, page.ProjectID)
	}
	if page.Severity != "" {
		clauses = append(clauses, "severity = ?")
		args = append(args, page.Severity)
	}
	if page.Q != "" {
		clauses = append(clauses, "(title LIKE ? OR body LIKE ?)")
		like := "%" + page.Q + "%"
		args = append(args, like, like)
	}
	query := `SELECT id, project_id, trigger_id, title, body, dedup_key, severity, status, delivery_state, delivery_after, created_at, read_at FROM notification_inbox` + whereSQL(clauses) + ` ORDER BY created_at DESC LIMIT ? OFFSET ?`
	args = append(args, page.Limit, page.Offset)
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []notification.Notification
	for rows.Next() {
		var n notification.Notification
		if err := rows.Scan(&n.ID, &n.ProjectID, &n.TriggerID, &n.Title, &n.Body, &n.DedupKey, &n.Severity, &n.Status, &n.DeliveryState, &n.DeliveryAfter, &n.CreatedAt, &n.ReadAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func listProjectRows(ctx context.Context, db *sql.DB, page listQuery) ([]project.Project, error) {
	clauses := []string{}
	args := []any{}
	if page.PrivacyClass != "" {
		clauses = append(clauses, "privacy_class = ?")
		args = append(args, page.PrivacyClass)
	}
	if page.Q != "" {
		clauses = append(clauses, "name LIKE ?")
		args = append(args, "%"+page.Q+"%")
	}
	query := `SELECT id,name,privacy_class,repository_refs_json,knowledge_scopes_json,memory_scope,allowed_skills_json,allowed_capabilities_json,allowed_coding_agents_json,allowed_models_json,budget_policy FROM projects` + whereSQL(clauses) + ` ORDER BY name ASC LIMIT ? OFFSET ?`
	args = append(args, page.Limit, page.Offset)
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []project.Project
	for rows.Next() {
		item, err := scanProjectRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func scanProjectRow(rows *sql.Rows) (project.Project, error) {
	var p project.Project
	var repos, scopes, skills, caps, agents, models string
	if err := rows.Scan(&p.ID, &p.Name, &p.PrivacyClass, &repos, &scopes, &p.MemoryScope, &skills, &caps, &agents, &models, &p.BudgetPolicy); err != nil {
		return project.Project{}, err
	}
	for _, item := range []struct {
		raw string
		dst any
	}{{repos, &p.RepositoryRefs}, {scopes, &p.KnowledgeScopes}, {skills, &p.AllowedSkills}, {caps, &p.AllowedCapabilities}, {agents, &p.AllowedCodingAgents}, {models, &p.AllowedModels}} {
		if err := json.Unmarshal([]byte(item.raw), item.dst); err != nil {
			return project.Project{}, err
		}
	}
	return p, nil
}

func listTriggerRows(ctx context.Context, db *sql.DB, page listQuery) ([]trigger.Trigger, error) {
	clauses := []string{}
	args := []any{}
	if page.ProjectID != "" {
		clauses = append(clauses, "project_id = ?")
		args = append(args, page.ProjectID)
	}
	if page.Type != "" {
		clauses = append(clauses, "type = ?")
		args = append(args, page.Type)
	}
	if page.SkillID != "" {
		clauses = append(clauses, "skill_id = ?")
		args = append(args, page.SkillID)
	}
	if page.Enabled != nil {
		clauses = append(clauses, "enabled = ?")
		args = append(args, *page.Enabled)
	}
	query := `SELECT id, project_id, type, enabled, workflow_template_id, skill_id, schedule_json, event_json, condition_json, policy_ref, budget_ref, created_at, updated_at FROM triggers` + whereSQL(clauses) + ` ORDER BY updated_at DESC, id ASC LIMIT ? OFFSET ?`
	args = append(args, page.Limit, page.Offset)
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []trigger.Trigger
	for rows.Next() {
		var item trigger.Trigger
		var scheduleJSON, eventJSON, conditionJSON string
		if err := rows.Scan(&item.ID, &item.ProjectID, &item.Type, &item.Enabled, &item.WorkflowTemplateID, &item.SkillID, &scheduleJSON, &eventJSON, &conditionJSON, &item.PolicyRef, &item.BudgetRef, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(scheduleJSON), &item.Schedule); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(eventJSON), &item.Event); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(conditionJSON), &item.Condition); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func listSkillRows(ctx context.Context, db *sql.DB, page listQuery) ([]skill.Record, error) {
	clauses := []string{}
	args := []any{}
	if page.Status != "" {
		clauses = append(clauses, "status = ?")
		args = append(args, page.Status)
	}
	if page.Q != "" {
		clauses = append(clauses, "(name LIKE ? OR description LIKE ? OR id LIKE ?)")
		like := "%" + page.Q + "%"
		args = append(args, like, like, like)
	}
	query := `SELECT id,name,description,active_version,status,source,created_at,updated_at FROM skills` + whereSQL(clauses) + ` ORDER BY id ASC LIMIT ? OFFSET ?`
	args = append(args, page.Limit, page.Offset)
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []skill.Record
	for rows.Next() {
		var item skill.Record
		if err := rows.Scan(&item.ID, &item.Name, &item.Description, &item.ActiveVersion, &item.Status, &item.Source, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func storedEventToResponse(event agentEvent.Event) storedEventResponse {
	payload := json.RawMessage("{}")
	if len(event.Payload) > 0 {
		redactedPayload := []byte(sanitizeForAPI(string(event.Payload)))
		if json.Valid(redactedPayload) {
			payload = json.RawMessage(redactedPayload)
		}
	}
	return storedEventResponse{
		ID:            sanitizeForAPI(event.ID),
		Source:        sanitizeForAPI(event.Source),
		Type:          sanitizeForAPI(event.Type),
		ProjectID:     sanitizeForAPI(event.ProjectID),
		Payload:       payload,
		PrivacyClass:  sanitizeForAPI(event.PrivacyClass),
		TrustLevel:    sanitizeForAPI(event.TrustLevel),
		OccurredAt:    formatTime(event.OccurredAt),
		ReceivedAt:    formatTime(event.ReceivedAt),
		DedupKey:      sanitizeForAPI(event.DedupKey),
		CorrelationID: sanitizeForAPI(event.CorrelationID),
		CausationID:   sanitizeForAPI(event.CausationID),
		EventDepth:    event.EventDepth,
	}
}

func whereSQL(clauses []string) string {
	if len(clauses) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(clauses, " AND ")
}
