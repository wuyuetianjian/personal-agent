package notification

import (
	"context"
	"database/sql"
	"time"
)

type Notification struct {
	ID        string
	ProjectID string
	TriggerID string
	Title     string
	Body      string
	DedupKey  string
	Status    string
	CreatedAt time.Time
	ReadAt    *time.Time
}

type Store struct {
	DB *sql.DB
}

func (s Store) Put(ctx context.Context, n Notification) error {
	if n.Status == "" {
		n.Status = "unread"
	}
	if n.CreatedAt.IsZero() {
		n.CreatedAt = time.Now().UTC()
	}
	_, err := s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO notification_inbox (id, project_id, trigger_id, title, body, dedup_key, status, created_at, read_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, n.ID, n.ProjectID, n.TriggerID, n.Title, n.Body, n.DedupKey, n.Status, n.CreatedAt, n.ReadAt)
	return err
}

func (s Store) List(ctx context.Context, limit int) ([]Notification, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id, project_id, trigger_id, title, body, dedup_key, status, created_at, read_at FROM notification_inbox ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Notification
	for rows.Next() {
		var n Notification
		if err := rows.Scan(&n.ID, &n.ProjectID, &n.TriggerID, &n.Title, &n.Body, &n.DedupKey, &n.Status, &n.CreatedAt, &n.ReadAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s Store) MarkRead(ctx context.Context, id string) error {
	now := time.Now().UTC()
	_, err := s.DB.ExecContext(ctx, `UPDATE notification_inbox SET status = 'read', read_at = ? WHERE id = ?`, now, id)
	return err
}
