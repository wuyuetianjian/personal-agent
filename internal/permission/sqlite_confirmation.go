package permission

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

type SQLiteConfirmationStore struct {
	DB *sql.DB
}

func (s SQLiteConfirmationStore) Request(ctx context.Context, request Request) (string, error) {
	if request.ID == "" {
		request.ID = "confirm_" + request.TaskID + "_" + string(request.Action)
	}
	if request.RequestedAt.IsZero() {
		request.RequestedAt = time.Now().UTC()
	}
	ids, err := json.Marshal(request.EvidenceIDs)
	if err != nil {
		return "", err
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO confirmation_requests (id, task_id, node_id, action, target, risk, evidence_ids_json, proposed_effect, status, requested_at, decided_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'pending', ?, NULL) ON CONFLICT(id) DO UPDATE SET task_id = excluded.task_id, node_id = excluded.node_id, action = excluded.action, target = excluded.target, risk = excluded.risk, evidence_ids_json = excluded.evidence_ids_json, proposed_effect = excluded.proposed_effect, requested_at = excluded.requested_at`, request.ID, request.TaskID, request.NodeID, request.Action, request.Target, request.Risk, string(ids), request.ProposedEffect, request.RequestedAt)
	return request.ID, err
}

func (s SQLiteConfirmationStore) IsApproved(ctx context.Context, confirmationID string) (bool, error) {
	status, err := s.Status(ctx, confirmationID)
	return status == "approved", err
}

func (s SQLiteConfirmationStore) Approve(ctx context.Context, confirmationID string) error {
	return s.setStatus(ctx, confirmationID, "approved")
}

func (s SQLiteConfirmationStore) Deny(ctx context.Context, confirmationID string) error {
	return s.setStatus(ctx, confirmationID, "denied")
}

func (s SQLiteConfirmationStore) Status(ctx context.Context, confirmationID string) (string, error) {
	var status string
	err := s.DB.QueryRowContext(ctx, `SELECT status FROM confirmation_requests WHERE id = ?`, confirmationID).Scan(&status)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return status, err
}

func (s SQLiteConfirmationStore) RequestByID(id string) (Request, bool) {
	request, err := s.get(context.Background(), id)
	return request, err == nil
}

func (s SQLiteConfirmationStore) List(ctx context.Context, status string, limit int) ([]Request, error) {
	if limit <= 0 {
		limit = 20
	}
	query := `SELECT id, task_id, node_id, action, target, risk, evidence_ids_json, proposed_effect, requested_at FROM confirmation_requests ORDER BY requested_at DESC LIMIT ?`
	args := []any{limit}
	if status != "" {
		query = `SELECT id, task_id, node_id, action, target, risk, evidence_ids_json, proposed_effect, requested_at FROM confirmation_requests WHERE status = ? ORDER BY requested_at DESC LIMIT ?`
		args = []any{status, limit}
	}
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Request
	for rows.Next() {
		request, err := scanConfirmation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, request)
	}
	return out, rows.Err()
}

func (s SQLiteConfirmationStore) get(ctx context.Context, id string) (Request, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT id, task_id, node_id, action, target, risk, evidence_ids_json, proposed_effect, requested_at FROM confirmation_requests WHERE id = ?`, id)
	return scanConfirmation(row)
}

func (s SQLiteConfirmationStore) setStatus(ctx context.Context, confirmationID string, status string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE confirmation_requests SET status = ?, decided_at = ? WHERE id = ?`, status, time.Now().UTC(), confirmationID)
	return err
}

type confirmationScanner interface {
	Scan(dest ...any) error
}

func scanConfirmation(scanner confirmationScanner) (Request, error) {
	var request Request
	var action, risk, evidence string
	err := scanner.Scan(&request.ID, &request.TaskID, &request.NodeID, &action, &request.Target, &risk, &evidence, &request.ProposedEffect, &request.RequestedAt)
	if err != nil {
		return request, err
	}
	request.Action = Action(action)
	request.Risk = RiskLevel(risk)
	if evidence != "" {
		if err := json.Unmarshal([]byte(evidence), &request.EvidenceIDs); err != nil {
			return request, err
		}
	}
	return request, nil
}
