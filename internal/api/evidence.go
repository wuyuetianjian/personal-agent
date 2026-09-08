package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type evidenceResponse struct {
	ID                 string  `json:"id"`
	TaskID             string  `json:"task_id"`
	NodeID             string  `json:"node_id,omitempty"`
	Claim              string  `json:"claim"`
	Evidence           string  `json:"evidence"`
	SourceType         string  `json:"source_type"`
	SourceID           string  `json:"source_id"`
	Trust              float64 `json:"trust"`
	Privacy            string  `json:"privacy"`
	Timestamp          string  `json:"timestamp"`
	VerificationStatus string  `json:"verification_status"`
	Redacted           bool    `json:"redacted"`
}

func (s Server) listEvidence(w http.ResponseWriter, r *http.Request) {
	db := s.sqlDB()
	if db == nil {
		writeError(w, http.StatusServiceUnavailable, "evidence_store_unavailable")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	items, err := listEvidenceRows(r.Context(), db, r.URL.Query().Get("task_id"), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "evidence_list_failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"evidence": items})
}

func (s Server) getEvidence(w http.ResponseWriter, r *http.Request) {
	db := s.sqlDB()
	if db == nil {
		writeError(w, http.StatusServiceUnavailable, "evidence_store_unavailable")
		return
	}
	item, err := getEvidenceRow(r.Context(), db, r.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "evidence_not_found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "evidence_get_failed")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func listEvidenceRows(ctx context.Context, db *sql.DB, taskID string, limit int) ([]evidenceResponse, error) {
	query := evidenceSelectSQL + ` ORDER BY e.created_at DESC, e.id ASC LIMIT ?`
	args := []any{limit}
	if strings.TrimSpace(taskID) != "" {
		query = evidenceSelectSQL + ` WHERE e.task_id = ? ORDER BY e.created_at DESC, e.id ASC LIMIT ?`
		args = []any{taskID, limit}
	}
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []evidenceResponse
	for rows.Next() {
		item, err := scanEvidenceResponse(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func getEvidenceRow(ctx context.Context, db *sql.DB, id string) (evidenceResponse, error) {
	row := db.QueryRowContext(ctx, evidenceSelectSQL+` WHERE e.id = ?`, id)
	return scanEvidenceResponse(row)
}

const evidenceSelectSQL = `SELECT e.id, e.task_id, COALESCE(e.node_id, ''), e.type, e.source, COALESCE(e.content_text, ''), e.metadata_json, e.privacy_class, e.created_at,
COALESCE((SELECT c.claim_text FROM claim_evidence ce JOIN claims c ON c.id = ce.claim_id WHERE ce.evidence_id = e.id ORDER BY c.created_at DESC, c.id ASC LIMIT 1), ''),
COALESCE((SELECT c.status FROM claim_evidence ce JOIN claims c ON c.id = ce.claim_id WHERE ce.evidence_id = e.id ORDER BY c.created_at DESC, c.id ASC LIMIT 1), 'unverified')
FROM evidence e`

type evidenceRowScanner interface {
	Scan(dest ...any) error
}

func scanEvidenceResponse(scanner evidenceRowScanner) (evidenceResponse, error) {
	var item evidenceResponse
	var content string
	var metadataJSON string
	var createdAt time.Time
	var verifiedClaim string
	if err := scanner.Scan(&item.ID, &item.TaskID, &item.NodeID, &item.SourceType, &item.SourceID, &content, &metadataJSON, &item.Privacy, &createdAt, &verifiedClaim, &item.VerificationStatus); err != nil {
		return evidenceResponse{}, err
	}
	var metadata struct {
		Claim string  `json:"claim"`
		Trust float64 `json:"trust"`
	}
	if metadataJSON != "" {
		if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil {
			return evidenceResponse{}, err
		}
	}
	item.Claim = metadata.Claim
	item.Trust = metadata.Trust
	item.Timestamp = formatTime(createdAt)
	item.Evidence, item.Redacted = evidencePreview(content, item.Privacy)
	if verifiedClaim != "" {
		item.Claim = verifiedClaim
	}
	return item, nil
}

func evidencePreview(content string, privacy string) (string, bool) {
	sanitized := sanitizeForAPI(content)
	redacted := sanitized != content || privacy == "confidential"
	if privacy == "confidential" {
		return "[redacted confidential evidence]", true
	}
	if len(sanitized) > 240 {
		sanitized = sanitized[:240] + "..."
		redacted = true
	}
	return sanitized, redacted
}
