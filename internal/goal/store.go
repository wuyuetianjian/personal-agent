package goal

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

type Store struct {
	DB *sql.DB
}

func (s Store) SaveGoal(ctx context.Context, g Goal) error {
	now := time.Now().UTC()
	if g.Status == "" {
		g.Status = "active"
	}
	if g.StateJSON == "" {
		g.StateJSON = "{}"
	}
	if g.CreatedAt.IsZero() {
		g.CreatedAt = now
	}
	g.UpdatedAt = now
	_, err := s.DB.ExecContext(ctx, `INSERT INTO goals (id, project_id, title, status, max_iterations, state_json, completion_criteria, budget_policy, workflow_id, last_evaluated_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(id) DO UPDATE SET project_id = excluded.project_id, title = excluded.title, status = excluded.status, max_iterations = excluded.max_iterations, state_json = excluded.state_json, completion_criteria = excluded.completion_criteria, budget_policy = excluded.budget_policy, workflow_id = excluded.workflow_id, last_evaluated_at = excluded.last_evaluated_at, updated_at = excluded.updated_at`,
		g.ID, g.ProjectID, g.Title, g.Status, g.MaxIterations, g.StateJSON, g.CompletionCriteria, g.BudgetPolicy, g.WorkflowID, g.LastEvaluatedAt, g.CreatedAt, g.UpdatedAt)
	return err
}

func (s Store) GetGoal(ctx context.Context, id string) (Goal, error) {
	var g Goal
	err := s.DB.QueryRowContext(ctx, `SELECT id, project_id, title, status, max_iterations, state_json, completion_criteria, budget_policy, workflow_id, last_evaluated_at, created_at, updated_at FROM goals WHERE id = ?`, id).Scan(
		&g.ID, &g.ProjectID, &g.Title, &g.Status, &g.MaxIterations, &g.StateJSON, &g.CompletionCriteria, &g.BudgetPolicy, &g.WorkflowID, &g.LastEvaluatedAt, &g.CreatedAt, &g.UpdatedAt)
	return g, err
}

func (s Store) SaveMilestones(ctx context.Context, milestones []Milestone) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	for _, m := range milestones {
		if m.Status == "" {
			m.Status = "pending"
		}
		if m.CreatedAt.IsZero() {
			m.CreatedAt = now
		}
		m.UpdatedAt = now
		dependencies, err := json.Marshal(m.Dependencies)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO goal_milestones (id, goal_id, title, status, dependencies_json, workflow_id, completion_criteria, order_index, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(id) DO UPDATE SET title = excluded.title, status = excluded.status, dependencies_json = excluded.dependencies_json, workflow_id = excluded.workflow_id, completion_criteria = excluded.completion_criteria, order_index = excluded.order_index, updated_at = excluded.updated_at`,
			m.ID, m.GoalID, m.Title, m.Status, string(dependencies), m.WorkflowID, m.CompletionCriteria, m.Order, m.CreatedAt, m.UpdatedAt)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s Store) ListMilestones(ctx context.Context, goalID string) ([]Milestone, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, goal_id, title, status, dependencies_json, workflow_id, completion_criteria, order_index, created_at, updated_at FROM goal_milestones WHERE goal_id = ? ORDER BY order_index ASC, id ASC`, goalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Milestone
	for rows.Next() {
		var m Milestone
		var dependencies string
		if err := rows.Scan(&m.ID, &m.GoalID, &m.Title, &m.Status, &dependencies, &m.WorkflowID, &m.CompletionCriteria, &m.Order, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(dependencies), &m.Dependencies); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s Store) SetGoalStatus(ctx context.Context, id string, status string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE goals SET status = ?, updated_at = ? WHERE id = ?`, status, time.Now().UTC(), id)
	return err
}

func (s Store) Pause(ctx context.Context, id string) error {
	return s.SetGoalStatus(ctx, id, "paused")
}

func (s Store) Resume(ctx context.Context, id string) error {
	return s.SetGoalStatus(ctx, id, "active")
}

func (s Store) Reevaluate(ctx context.Context, planner Planner, id string) ([]Milestone, error) {
	g, err := s.GetGoal(ctx, id)
	if err != nil {
		return nil, err
	}
	milestones, err := planner.Plan(ctx, g)
	if err != nil {
		return nil, err
	}
	if err := s.SaveMilestones(ctx, milestones); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	g.LastEvaluatedAt = &now
	if err := s.SaveGoal(ctx, g); err != nil {
		return nil, err
	}
	return milestones, nil
}
