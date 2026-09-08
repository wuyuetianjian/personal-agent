package notification

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"time"
)

type Notification struct {
	ID            string
	ProjectID     string
	TriggerID     string
	Title         string
	Body          string
	DedupKey      string
	Severity      string
	Status        string
	DeliveryState string
	DeliveryAfter *time.Time
	CreatedAt     time.Time
	ReadAt        *time.Time
}

type Policy struct {
	QuietHoursStart string
	QuietHoursEnd   string
}

type Store struct {
	DB *sql.DB
}

func (s Store) Put(ctx context.Context, n Notification) error {
	_, err := s.PutWithPolicy(ctx, n, Policy{})
	return err
}

func (s Store) PutWithPolicy(ctx context.Context, n Notification, policy Policy) (bool, error) {
	if n.Status == "" {
		n.Status = "unread"
	}
	if n.Severity == "" {
		n.Severity = "info"
	}
	if n.CreatedAt.IsZero() {
		n.CreatedAt = time.Now().UTC()
	}
	n = applyPolicy(n, policy)
	result, err := s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO notification_inbox (id, project_id, trigger_id, title, body, dedup_key, severity, status, delivery_state, delivery_after, created_at, read_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, n.ID, n.ProjectID, n.TriggerID, n.Title, n.Body, n.DedupKey, n.Severity, n.Status, n.DeliveryState, n.DeliveryAfter, n.CreatedAt, n.ReadAt)
	if err != nil {
		return false, err
	}
	rows, _ := result.RowsAffected()
	return rows == 1, nil
}

func (s Store) List(ctx context.Context, limit int) ([]Notification, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id, project_id, trigger_id, title, body, dedup_key, severity, status, delivery_state, delivery_after, created_at, read_at FROM notification_inbox ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Notification
	for rows.Next() {
		var n Notification
		if err := rows.Scan(&n.ID, &n.ProjectID, &n.TriggerID, &n.Title, &n.Body, &n.DedupKey, &n.Severity, &n.Status, &n.DeliveryState, &n.DeliveryAfter, &n.CreatedAt, &n.ReadAt); err != nil {
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

func applyPolicy(n Notification, policy Policy) Notification {
	if n.DeliveryState == "" {
		n.DeliveryState = "inbox"
	}
	start, okStart := parseClock(policy.QuietHoursStart)
	end, okEnd := parseClock(policy.QuietHoursEnd)
	if !okStart || !okEnd || start == end {
		return n
	}
	current := n.CreatedAt.UTC().Hour()*60 + n.CreatedAt.UTC().Minute()
	if !withinQuietHours(current, start, end) {
		return n
	}
	n.DeliveryState = "quiet_hours"
	after := n.CreatedAt.UTC()
	endHour := end / 60
	endMinute := end % 60
	after = time.Date(after.Year(), after.Month(), after.Day(), endHour, endMinute, 0, 0, time.UTC)
	if !after.After(n.CreatedAt.UTC()) {
		after = after.Add(24 * time.Hour)
	}
	n.DeliveryAfter = &after
	return n
}

func withinQuietHours(current, start, end int) bool {
	if start < end {
		return current >= start && current < end
	}
	return current >= start || current < end
}

func parseClock(value string) (int, bool) {
	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return 0, false
	}
	hour, err := strconv.Atoi(parts[0])
	if err != nil || hour < 0 || hour > 23 {
		return 0, false
	}
	minute, err := strconv.Atoi(parts[1])
	if err != nil || minute < 0 || minute > 59 {
		return 0, false
	}
	return hour*60 + minute, true
}
