package workflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrInvalidTransition  = errors.New("invalid workflow state transition")
	ErrCheckpointRequired = errors.New("checkpoint is required before node completion")
)

type Store struct{ DB *sql.DB }

func (s Store) Create(ctx context.Context, run Run, nodes []Node) error {
	if run.Status == "" {
		run.Status = StatusPending
	}
	now := time.Now().UTC()
	if run.StartedAt.IsZero() {
		run.StartedAt = now
	}
	run.UpdatedAt = now
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO workflow_runs (id, task_id, project_id, skill_id, skill_version, status, input_json, result_json, started_at, updated_at, completed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, run.ID, run.TaskID, run.ProjectID, run.SkillID, run.SkillVersion, run.Status, run.InputJSON, run.ResultJSON, run.StartedAt, run.UpdatedAt, run.CompletedAt)
	if err != nil {
		return err
	}
	for _, node := range nodes {
		if node.Status == "" {
			node.Status = NodePending
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO workflow_nodes (workflow_id, node_id, capability_id, role, status, attempt, idempotency_key, side_effect, started_at, completed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, run.ID, node.NodeID, node.CapabilityID, node.Role, node.Status, node.Attempt, node.IdempotencyKey, node.SideEffect, node.StartedAt, node.CompletedAt)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s Store) Get(ctx context.Context, id string) (Run, error) {
	var run Run
	err := s.DB.QueryRowContext(ctx, `SELECT id, task_id, project_id, skill_id, skill_version, status, input_json, result_json, started_at, updated_at, completed_at FROM workflow_runs WHERE id = ?`, id).Scan(&run.ID, &run.TaskID, &run.ProjectID, &run.SkillID, &run.SkillVersion, &run.Status, &run.InputJSON, &run.ResultJSON, &run.StartedAt, &run.UpdatedAt, &run.CompletedAt)
	return run, err
}

func (s Store) List(ctx context.Context, limit int) ([]Run, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id, task_id, project_id, skill_id, skill_version, status, input_json, result_json, started_at, updated_at, completed_at FROM workflow_runs ORDER BY updated_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		var run Run
		if err := rows.Scan(&run.ID, &run.TaskID, &run.ProjectID, &run.SkillID, &run.SkillVersion, &run.Status, &run.InputJSON, &run.ResultJSON, &run.StartedAt, &run.UpdatedAt, &run.CompletedAt); err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

func (s Store) UpdateStatus(ctx context.Context, id string, status Status) error {
	if status == StatusCompleted || status == StatusFailed || status == StatusCancelled {
		return s.finish(ctx, id, status)
	}
	if status != StatusRunning && status != StatusPaused && status != StatusWaitingApproval && status != StatusRetrying && status != StatusPending {
		return ErrInvalidTransition
	}
	result, err := s.DB.ExecContext(ctx, `UPDATE workflow_runs SET status = ?, updated_at = ? WHERE id = ? AND status NOT IN (?, ?, ?)`, status, time.Now().UTC(), id, StatusCompleted, StatusFailed, StatusCancelled)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrInvalidTransition
	}
	return nil
}

func (s Store) finish(ctx context.Context, id string, status Status) error {
	now := time.Now().UTC()
	result, err := s.DB.ExecContext(ctx, `UPDATE workflow_runs SET status = ?, updated_at = ?, completed_at = ? WHERE id = ? AND status NOT IN (?, ?, ?)`, status, now, now, id, StatusCompleted, StatusFailed, StatusCancelled)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrInvalidTransition
	}
	return nil
}

func (s Store) SaveCheckpoint(ctx context.Context, cp Checkpoint) error {
	ids, err := json.Marshal(cp.EvidenceIDs)
	if err != nil {
		return err
	}
	if cp.CreatedAt.IsZero() {
		cp.CreatedAt = time.Now().UTC()
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO workflow_checkpoints (workflow_id, node_id, status, evidence_ids_json, result_ref, usage_json, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, cp.WorkflowID, cp.NodeID, cp.Status, string(ids), cp.ResultRef, cp.UsageJSON, cp.CreatedAt)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE workflow_nodes SET status = ?, completed_at = ? WHERE workflow_id = ? AND node_id = ? AND status NOT IN (?, ?, ?)`, cp.Status, cp.CreatedAt, cp.WorkflowID, cp.NodeID, NodeCompleted, NodeFailed, NodeCancelled)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s Store) Recover(ctx context.Context, workflowID string) ([]Node, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT workflow_id, node_id, capability_id, role, status, attempt, idempotency_key, side_effect, started_at, completed_at FROM workflow_nodes WHERE workflow_id = ? AND status NOT IN (?, ?, ?, ?) ORDER BY node_id`, workflowID, NodeCompleted, NodeFailed, NodeSkipped, NodeCancelled)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Node
	for rows.Next() {
		var n Node
		if err := rows.Scan(&n.WorkflowID, &n.NodeID, &n.CapabilityID, &n.Role, &n.Status, &n.Attempt, &n.IdempotencyKey, &n.SideEffect, &n.StartedAt, &n.CompletedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
