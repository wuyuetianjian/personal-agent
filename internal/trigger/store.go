package trigger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

type Store struct{ DB *sql.DB }

func (s Store) Put(ctx context.Context, t Trigger) error {
	if err := Validate(t); err != nil {
		return err
	}
	now := time.Now().UTC()
	if t.CreatedAt.IsZero() {
		t.CreatedAt = now
	}
	t.UpdatedAt = now
	scheduleJSON, err := json.Marshal(t.Schedule)
	if err != nil {
		return err
	}
	eventJSON, err := json.Marshal(t.Event)
	if err != nil {
		return err
	}
	conditionJSON, err := json.Marshal(t.Condition)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO triggers (id, project_id, type, enabled, workflow_template_id, skill_id, schedule_json, event_json, condition_json, policy_ref, budget_ref, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(id) DO UPDATE SET project_id = excluded.project_id, type = excluded.type, enabled = excluded.enabled, workflow_template_id = excluded.workflow_template_id, skill_id = excluded.skill_id, schedule_json = excluded.schedule_json, event_json = excluded.event_json, condition_json = excluded.condition_json, policy_ref = excluded.policy_ref, budget_ref = excluded.budget_ref, updated_at = excluded.updated_at`, t.ID, t.ProjectID, t.Type, t.Enabled, t.WorkflowTemplateID, t.SkillID, string(scheduleJSON), string(eventJSON), string(conditionJSON), t.PolicyRef, t.BudgetRef, t.CreatedAt, t.UpdatedAt)
	return err
}

func (s Store) Get(ctx context.Context, id string) (Trigger, error) {
	var t Trigger
	var scheduleJSON, eventJSON, conditionJSON string
	err := s.DB.QueryRowContext(ctx, `SELECT id, project_id, type, enabled, workflow_template_id, skill_id, schedule_json, event_json, condition_json, policy_ref, budget_ref, created_at, updated_at FROM triggers WHERE id = ?`, id).Scan(&t.ID, &t.ProjectID, &t.Type, &t.Enabled, &t.WorkflowTemplateID, &t.SkillID, &scheduleJSON, &eventJSON, &conditionJSON, &t.PolicyRef, &t.BudgetRef, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return t, err
	}
	if err := json.Unmarshal([]byte(scheduleJSON), &t.Schedule); err != nil {
		return t, err
	}
	if err := json.Unmarshal([]byte(eventJSON), &t.Event); err != nil {
		return t, err
	}
	if err := json.Unmarshal([]byte(conditionJSON), &t.Condition); err != nil {
		return t, err
	}
	return t, nil
}

func (s Store) ListEnabled(ctx context.Context) ([]Trigger, error) {
	return s.list(ctx, `SELECT id FROM triggers WHERE enabled = true ORDER BY id`)
}

func (s Store) List(ctx context.Context) ([]Trigger, error) {
	return s.list(ctx, `SELECT id FROM triggers ORDER BY updated_at DESC, id`)
}

func (s Store) list(ctx context.Context, query string) ([]Trigger, error) {
	rows, err := s.DB.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]Trigger, 0, len(ids))
	for _, id := range ids {
		t, err := s.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

func (s Store) SetEnabled(ctx context.Context, id string, enabled bool) error {
	result, err := s.DB.ExecContext(ctx, `UPDATE triggers SET enabled = ?, updated_at = ? WHERE id = ?`, enabled, time.Now().UTC(), id)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s Store) PutState(ctx context.Context, st State) error {
	if st.StateJSON == "" {
		st.StateJSON = "{}"
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO trigger_state (trigger_id, last_fired_at, last_success_at, last_failure_at, next_fire_at, last_event_hash, consecutive_failures, cooldown_until, state_json) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(trigger_id) DO UPDATE SET last_fired_at = excluded.last_fired_at, last_success_at = excluded.last_success_at, last_failure_at = excluded.last_failure_at, next_fire_at = excluded.next_fire_at, last_event_hash = excluded.last_event_hash, consecutive_failures = excluded.consecutive_failures, cooldown_until = excluded.cooldown_until, state_json = excluded.state_json`, st.TriggerID, st.LastFiredAt, st.LastSuccessAt, st.LastFailureAt, st.NextFireAt, st.LastEventHash, st.ConsecutiveFailures, st.CooldownUntil, st.StateJSON)
	return err
}

func (s Store) GetState(ctx context.Context, triggerID string) (State, error) {
	var st State
	err := s.DB.QueryRowContext(ctx, `SELECT trigger_id, last_fired_at, last_success_at, last_failure_at, next_fire_at, last_event_hash, consecutive_failures, cooldown_until, state_json FROM trigger_state WHERE trigger_id = ?`, triggerID).Scan(&st.TriggerID, &st.LastFiredAt, &st.LastSuccessAt, &st.LastFailureAt, &st.NextFireAt, &st.LastEventHash, &st.ConsecutiveFailures, &st.CooldownUntil, &st.StateJSON)
	return st, err
}

func (s Store) StateOrDefault(ctx context.Context, triggerID string) (State, error) {
	st, err := s.GetState(ctx, triggerID)
	if errors.Is(err, sql.ErrNoRows) {
		return State{TriggerID: triggerID, StateJSON: "{}"}, nil
	}
	return st, err
}

func (s Store) PutHistory(ctx context.Context, h History) error {
	if h.CreatedAt.IsZero() {
		h.CreatedAt = time.Now().UTC()
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO trigger_history (id, trigger_id, event_id, workflow_id, status, message, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, h.ID, h.TriggerID, h.EventID, h.WorkflowID, h.Status, h.Message, h.CreatedAt)
	return err
}

func (s Store) History(ctx context.Context, triggerID string, limit int) ([]History, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id, trigger_id, event_id, workflow_id, status, message, created_at FROM trigger_history WHERE trigger_id = ? ORDER BY created_at DESC LIMIT ?`, triggerID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []History
	for rows.Next() {
		var h History
		if err := rows.Scan(&h.ID, &h.TriggerID, &h.EventID, &h.WorkflowID, &h.Status, &h.Message, &h.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func (s Store) PutDeadLetter(ctx context.Context, d DeadLetter) error {
	if d.CreatedAt.IsZero() {
		d.CreatedAt = time.Now().UTC()
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO dead_letter_events (id, source_id, source_type, reason, payload, created_at) VALUES (?, ?, ?, ?, ?, ?)`, d.ID, d.SourceID, d.SourceType, d.Reason, d.Payload, d.CreatedAt)
	return err
}
