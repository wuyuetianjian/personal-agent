package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

type WorkingEntry struct {
	ID           string
	TaskID       string
	Key          string
	Value        map[string]string
	PrivacyClass string
	ExpiresAt    time.Time
	CreatedAt    time.Time
}

type WorkingStore struct {
	db *sql.DB
}

func NewWorkingStore(db *sql.DB) WorkingStore {
	return WorkingStore{db: db}
}

func (s WorkingStore) Put(ctx context.Context, entry WorkingEntry) error {
	now := time.Now().UTC()
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = now
	}
	if entry.ExpiresAt.IsZero() {
		entry.ExpiresAt = now.Add(time.Hour)
	}
	if entry.PrivacyClass == "" {
		entry.PrivacyClass = "local_private"
	}
	value, err := json.Marshal(entry.Value)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO memory_working (
  id, task_id, key, value_json, privacy_class, expires_at, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?)
`, entry.ID, entry.TaskID, entry.Key, string(value), entry.PrivacyClass, entry.ExpiresAt, entry.CreatedAt)
	return err
}

func (s WorkingStore) Get(ctx context.Context, taskID string, key string, now time.Time) (WorkingEntry, bool, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	row := s.db.QueryRowContext(ctx, `
SELECT id, task_id, key, value_json, privacy_class, expires_at, created_at
FROM memory_working
WHERE task_id = ? AND key = ? AND expires_at > ?
ORDER BY created_at DESC
LIMIT 1
`, taskID, key, now)
	entry, err := scanWorkingEntry(row)
	if err == sql.ErrNoRows {
		return WorkingEntry{}, false, nil
	}
	if err != nil {
		return WorkingEntry{}, false, err
	}
	return entry, true, nil
}

func (s WorkingStore) CleanupExpired(ctx context.Context, now time.Time) (int64, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM memory_working WHERE expires_at <= ?`, now)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

type workingScanner interface {
	Scan(dest ...any) error
}

func scanWorkingEntry(scanner workingScanner) (WorkingEntry, error) {
	var entry WorkingEntry
	var valueJSON string
	err := scanner.Scan(&entry.ID, &entry.TaskID, &entry.Key, &valueJSON, &entry.PrivacyClass, &entry.ExpiresAt, &entry.CreatedAt)
	if err != nil {
		return WorkingEntry{}, err
	}
	if err := json.Unmarshal([]byte(valueJSON), &entry.Value); err != nil {
		return WorkingEntry{}, err
	}
	return entry, nil
}
