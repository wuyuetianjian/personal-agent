package runtime

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"time"
)

type EvidenceStore interface {
	Put(ctx context.Context, evidence Evidence) error
	ListByTask(ctx context.Context, taskID string) ([]Evidence, error)
	Get(ctx context.Context, evidenceID string) (Evidence, error)
}

type SQLiteEvidenceStore struct {
	DB *sql.DB
}

func (s SQLiteEvidenceStore) Put(ctx context.Context, evidence Evidence) error {
	if evidence.CreatedAt.IsZero() {
		evidence.CreatedAt = time.Now().UTC()
	}
	if evidence.PrivacyClass == "" {
		evidence.PrivacyClass = "local_private"
	}
	metadata, err := json.Marshal(map[string]any{
		"claim":     evidence.Claim,
		"source_id": evidence.SourceID,
		"score":     evidence.Score,
		"trust":     evidence.Trust,
	})
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `
INSERT INTO evidence (
  id, task_id, node_id, type, source, uri, content_text, content_hash,
  metadata_json, privacy_class, created_at
) VALUES (?, ?, ?, ?, ?, NULL, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  content_text = excluded.content_text,
  metadata_json = excluded.metadata_json,
  privacy_class = excluded.privacy_class
`, evidence.ID, evidence.TaskID, nullableString(evidence.NodeID), string(evidence.SourceType),
		evidence.SourceID, evidence.Content, hashText(evidence.Content), string(metadata),
		evidence.PrivacyClass, evidence.CreatedAt)
	return err
}

func (s SQLiteEvidenceStore) ListByTask(ctx context.Context, taskID string) ([]Evidence, error) {
	rows, err := s.DB.QueryContext(ctx, `
SELECT id, task_id, COALESCE(node_id, ''), type, source, COALESCE(content_text, ''),
  metadata_json, privacy_class, created_at
FROM evidence
WHERE task_id = ?
ORDER BY created_at ASC, id ASC
`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Evidence
	for rows.Next() {
		item, err := scanEvidence(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s SQLiteEvidenceStore) Get(ctx context.Context, evidenceID string) (Evidence, error) {
	row := s.DB.QueryRowContext(ctx, `
SELECT id, task_id, COALESCE(node_id, ''), type, source, COALESCE(content_text, ''),
  metadata_json, privacy_class, created_at
FROM evidence
WHERE id = ?
`, evidenceID)
	return scanEvidence(row)
}

type evidenceScanner interface {
	Scan(dest ...any) error
}

func scanEvidence(scanner evidenceScanner) (Evidence, error) {
	var item Evidence
	var sourceType string
	var metadataJSON string
	if err := scanner.Scan(&item.ID, &item.TaskID, &item.NodeID, &sourceType, &item.SourceID,
		&item.Content, &metadataJSON, &item.PrivacyClass, &item.CreatedAt); err != nil {
		return Evidence{}, err
	}
	item.SourceType = SourceType(sourceType)
	var metadata struct {
		Claim string  `json:"claim"`
		Score float64 `json:"score"`
		Trust float64 `json:"trust"`
	}
	if metadataJSON != "" {
		if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil {
			return Evidence{}, err
		}
	}
	item.Claim = metadata.Claim
	item.Score = metadata.Score
	item.Trust = metadata.Trust
	return item, nil
}

func stableEvidenceID(taskID string, sourceType string, sourceID string) string {
	sum := sha256.Sum256([]byte(taskID + "\x00" + sourceType + "\x00" + sourceID))
	return "ev_" + hex.EncodeToString(sum[:8])
}

func hashText(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func defaultPrivacyClass(value string) string {
	if value == "" {
		return "local_private"
	}
	return value
}
