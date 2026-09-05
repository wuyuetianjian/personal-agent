package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

type EpisodicEvent struct {
	ID           string
	TaskID       string
	EventType    string
	Summary      string
	Payload      map[string]string
	EvidenceIDs  []string
	Confidence   float64
	PrivacyClass string
	CreatedAt    time.Time
}

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) Store {
	return Store{db: db}
}

func (s Store) Append(ctx context.Context, event EpisodicEvent) error {
	now := time.Now().UTC()
	if event.CreatedAt.IsZero() {
		event.CreatedAt = now
	}
	if event.Confidence == 0 {
		event.Confidence = 1
	}
	if event.PrivacyClass == "" {
		event.PrivacyClass = "local_private"
	}
	payload, err := json.Marshal(event.Payload)
	if err != nil {
		return err
	}
	evidenceIDs, err := json.Marshal(event.EvidenceIDs)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO memory_episodic (
  id, task_id, event_type, summary, payload_json, evidence_ids_json,
  confidence, privacy_class, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
`, event.ID, event.TaskID, event.EventType, event.Summary, string(payload), string(evidenceIDs),
		event.Confidence, event.PrivacyClass, event.CreatedAt)
	return err
}

func (s Store) Recent(ctx context.Context, limit int) ([]EpisodicEvent, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, task_id, event_type, summary, payload_json, evidence_ids_json,
  confidence, privacy_class, created_at
FROM memory_episodic
ORDER BY created_at DESC
LIMIT ?
`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []EpisodicEvent
	for rows.Next() {
		var event EpisodicEvent
		var payloadJSON string
		var evidenceJSON string
		if err := rows.Scan(&event.ID, &event.TaskID, &event.EventType, &event.Summary, &payloadJSON,
			&evidenceJSON, &event.Confidence, &event.PrivacyClass, &event.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(payloadJSON), &event.Payload); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(evidenceJSON), &event.EvidenceIDs); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}
