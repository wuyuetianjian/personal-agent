package audit

import (
	"context"
	"database/sql"
	"time"

	"agent/internal/observability"
)

type Record struct {
	ID          string
	Actor       string
	Action      string
	Target      string
	Status      string
	Reason      string
	EvidenceIDs string
	CreatedAt   time.Time
}

type Store struct {
	DB *sql.DB
}

func (s Store) Append(ctx context.Context, record Record) error {
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now().UTC()
	}
	_, err := s.DB.ExecContext(ctx, `
INSERT INTO security_audit_log (id, actor, action, target, status, reason, evidence_ids, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
`, observability.Redact(record.ID), observability.Redact(record.Actor), observability.Redact(record.Action),
		observability.Redact(record.Target), observability.Redact(record.Status), observability.Redact(record.Reason),
		observability.Redact(record.EvidenceIDs), record.CreatedAt)
	return err
}

func (s Store) List(ctx context.Context, limit int) ([]Record, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.DB.QueryContext(ctx, `
SELECT id, actor, action, target, status, reason, evidence_ids, created_at
FROM security_audit_log
ORDER BY created_at DESC
LIMIT ?
`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var records []Record
	for rows.Next() {
		var record Record
		if err := rows.Scan(&record.ID, &record.Actor, &record.Action, &record.Target, &record.Status, &record.Reason, &record.EvidenceIDs, &record.CreatedAt); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}
