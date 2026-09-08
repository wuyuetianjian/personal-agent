package runtime

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"agent/internal/agent"
)

var ErrSideEffectAlreadyClaimed = errors.New("side effect idempotency key already claimed")

type SideEffectRecord struct {
	IdempotencyKey string
	WorkflowID     string
	NodeID         string
	CapabilityID   string
	Status         string
	ResultRef      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type SideEffectStore interface {
	Claim(ctx context.Context, record SideEffectRecord) (SideEffectRecord, bool, error)
	Complete(ctx context.Context, key string, resultRef string) error
}

type SQLiteSideEffectStore struct {
	DB *sql.DB
}

func (s SQLiteSideEffectStore) Claim(ctx context.Context, record SideEffectRecord) (SideEffectRecord, bool, error) {
	if record.IdempotencyKey == "" {
		return SideEffectRecord{}, false, errors.New("side effect idempotency key is required")
	}
	now := time.Now().UTC()
	record.Status = "started"
	record.CreatedAt = now
	record.UpdatedAt = now
	result, err := s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO side_effect_records (idempotency_key, workflow_id, node_id, capability_id, status, result_ref, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		record.IdempotencyKey, record.WorkflowID, record.NodeID, record.CapabilityID, record.Status, record.ResultRef, record.CreatedAt, record.UpdatedAt)
	if err != nil {
		return SideEffectRecord{}, false, err
	}
	rows, _ := result.RowsAffected()
	if rows == 1 {
		return record, true, nil
	}
	existing, err := s.Get(ctx, record.IdempotencyKey)
	return existing, false, err
}

func (s SQLiteSideEffectStore) Get(ctx context.Context, key string) (SideEffectRecord, error) {
	var record SideEffectRecord
	err := s.DB.QueryRowContext(ctx, `SELECT idempotency_key, workflow_id, node_id, capability_id, status, result_ref, created_at, updated_at FROM side_effect_records WHERE idempotency_key = ?`, key).Scan(
		&record.IdempotencyKey, &record.WorkflowID, &record.NodeID, &record.CapabilityID, &record.Status, &record.ResultRef, &record.CreatedAt, &record.UpdatedAt)
	return record, err
}

func (s SQLiteSideEffectStore) Complete(ctx context.Context, key string, resultRef string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE side_effect_records SET status = ?, result_ref = ?, updated_at = ? WHERE idempotency_key = ?`, "completed", resultRef, time.Now().UTC(), key)
	return err
}

func duplicateSideEffectResult(node agent.TaskNode, existing SideEffectRecord) agent.Result {
	message := "side effect already claimed for idempotency key " + existing.IdempotencyKey
	category := agent.ErrorBlockedMissingInput
	errorMessage := ErrSideEffectAlreadyClaimed.Error()
	if existing.Status == "completed" && existing.ResultRef != "" {
		message = existing.ResultRef
		category = ""
		errorMessage = ""
	}
	return agent.Result{
		TaskID:        node.TaskID,
		NodeID:        node.ID,
		Role:          node.Role,
		Text:          message,
		ErrorCategory: category,
		ErrorMessage:  errorMessage,
		Usage:         agentUsage(node.Input, message),
	}
}
