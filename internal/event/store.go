package event

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

var ErrDuplicate = errors.New("duplicate event")

type Store struct{ DB *sql.DB }

func (s Store) Put(ctx context.Context, e Event) error {
	e = Normalize(e)
	if err := e.Validate(); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO events (id, source, type, project_id, payload, privacy_class, trust_level, occurred_at, received_at, dedup_key, correlation_id, causation_id, event_depth) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, e.ID, e.Source, e.Type, e.ProjectID, e.Payload, e.PrivacyClass, e.TrustLevel, e.OccurredAt, e.ReceivedAt, e.DedupKey, e.CorrelationID, e.CausationID, e.EventDepth)
	if err != nil && isConstraintError(err) {
		return fmt.Errorf("%w: %v", ErrDuplicate, err)
	}
	return err
}

func (s Store) Get(ctx context.Context, id string) (Event, error) {
	var e Event
	err := s.DB.QueryRowContext(ctx, `SELECT id, source, type, project_id, payload, privacy_class, trust_level, occurred_at, received_at, dedup_key, correlation_id, causation_id, event_depth FROM events WHERE id = ?`, id).Scan(&e.ID, &e.Source, &e.Type, &e.ProjectID, &e.Payload, &e.PrivacyClass, &e.TrustLevel, &e.OccurredAt, &e.ReceivedAt, &e.DedupKey, &e.CorrelationID, &e.CausationID, &e.EventDepth)
	return e, err
}

func (s Store) ListByProject(ctx context.Context, projectID string, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id, source, type, project_id, payload, privacy_class, trust_level, occurred_at, received_at, dedup_key, correlation_id, causation_id, event_depth FROM events WHERE project_id = ? ORDER BY received_at DESC LIMIT ?`, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.Source, &e.Type, &e.ProjectID, &e.Payload, &e.PrivacyClass, &e.TrustLevel, &e.OccurredAt, &e.ReceivedAt, &e.DedupKey, &e.CorrelationID, &e.CausationID, &e.EventDepth); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func isConstraintError(err error) bool {
	return err != nil && strings.Contains(strings.ToUpper(err.Error()), "UNIQUE")
}
