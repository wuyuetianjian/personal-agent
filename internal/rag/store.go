package rag

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type SQLiteStore struct {
	db      *sql.DB
	chunker Chunker
}

func NewSQLiteStore(db *sql.DB, chunker Chunker) SQLiteStore {
	return SQLiteStore{db: db, chunker: chunker}
}

var ErrDocumentRevisionChanged = errors.New("document revision changed or missing")

// IndexedDocument couples the exact indexed chunks with a storage-generated
// generation from the same write transaction. Source metadata cannot set it.
type IndexedDocument struct {
	DocumentID string
	Revision   string
	Chunks     []Chunk
}

func (s SQLiteStore) Index(ctx context.Context, document Document) ([]Chunk, error) {
	indexed, err := s.IndexRevision(ctx, document)
	return indexed.Chunks, err
}

// IndexRevision is the trusted ingestion entry point for later CAS assignment.
// Do not replace its returned token by reading the newest revision after a race.
func (s SQLiteStore) IndexRevision(ctx context.Context, document Document) (IndexedDocument, error) {
	now := time.Now().UTC()
	if document.CreatedAt.IsZero() {
		document.CreatedAt = now
	}
	document.UpdatedAt = now
	if document.ContentHash == "" {
		document.ContentHash = HashText(document.Text)
	}
	if document.PrivacyClass == "" {
		document.PrivacyClass = DefaultPrivacyClass
	}
	metadataJSON, err := json.Marshal(cloneMap(document.Metadata))
	if err != nil {
		return IndexedDocument{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return IndexedDocument{}, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
INSERT INTO documents (
  id, source_uri, title, content_hash, metadata_json, privacy_class, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  source_uri = excluded.source_uri,
  title = excluded.title,
  content_hash = excluded.content_hash,
  metadata_json = excluded.metadata_json,
  privacy_class = excluded.privacy_class,
  updated_at = excluded.updated_at
`, document.ID, document.SourceURI, document.Title, document.ContentHash, string(metadataJSON),
		document.PrivacyClass, document.CreatedAt, document.UpdatedAt); err != nil {
		return IndexedDocument{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM document_chunks WHERE document_id = ?`, document.ID); err != nil {
		return IndexedDocument{}, err
	}

	chunks := s.chunker.Chunk(document)
	for i := range chunks {
		if chunks[i].CreatedAt.IsZero() {
			chunks[i].CreatedAt = now
		}
		chunkMetadataJSON, err := json.Marshal(cloneMap(chunks[i].Metadata))
		if err != nil {
			return IndexedDocument{}, err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO document_chunks (
  id, document_id, chunk_index, text, token_count, content_hash, metadata_json, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
`, chunks[i].ID, chunks[i].DocumentID, chunks[i].ChunkIndex, chunks[i].Text, chunks[i].TokenCount,
			chunks[i].ContentHash, string(chunkMetadataJSON), chunks[i].CreatedAt); err != nil {
			return IndexedDocument{}, err
		}
	}

	var revision string
	if err := tx.QueryRowContext(ctx, "SELECT revision FROM document_revisions WHERE document_id=?", document.ID).Scan(&revision); err != nil {
		return IndexedDocument{}, err
	}
	if err := tx.Commit(); err != nil {
		return IndexedDocument{}, err
	}
	return IndexedDocument{DocumentID: document.ID, Revision: revision, Chunks: chunks}, nil
}

func (s SQLiteStore) ListChunks(ctx context.Context) ([]Result, error) {
	return s.listChunks(ctx, "")
}

// ListProjectChunks uses trusted ownership rows, never document metadata. A
// changed document must be explicitly re-assigned by trusted ingestion.
func (s SQLiteStore) ListProjectChunks(ctx context.Context, projectID string) ([]Result, error) {
	if projectID == "" {
		return nil, fmt.Errorf("project scope required")
	}
	return s.listChunks(ctx, projectID)
}
func (s SQLiteStore) listChunks(ctx context.Context, projectID string) ([]Result, error) {
	// Managed knowledge is workspace-only. Legacy/global readers and vector-ID
	// lookup cannot bypass its scope, deactivation or current-generation checks.
	filter := " WHERE NOT EXISTS (SELECT 1 FROM interaction_knowledge_documents k WHERE k.id=d.id) "
	var args []any
	if projectID != "" {
		filter = " WHERE EXISTS (SELECT 1 FROM project_document_scopes ps WHERE ps.document_id=d.id AND ps.project_id=? AND ps.content_hash=d.content_hash AND ps.revision=(SELECT revision FROM document_revisions WHERE document_id=d.id)) AND NOT EXISTS (SELECT 1 FROM interaction_knowledge_documents k WHERE k.id=d.id AND (k.scope_id<>? OR k.state<>'indexed' OR k.content_hash<>d.content_hash OR k.index_revision<>(SELECT revision FROM document_revisions WHERE document_id=d.id))) "
		args = append(args, projectID, projectID)
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT
  c.id, c.document_id, c.chunk_index, c.text, c.token_count, c.content_hash, c.metadata_json, c.created_at,
  d.id, d.source_uri, d.title, d.content_hash, d.metadata_json, d.privacy_class, d.created_at, d.updated_at
FROM document_chunks c
JOIN documents d ON d.id = c.document_id
`+filter+` ORDER BY d.id ASC, c.chunk_index ASC, c.id ASC
`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []Result
	for rows.Next() {
		var result Result
		var chunkMetadata string
		var documentMetadata string
		if err := rows.Scan(
			&result.Chunk.ID,
			&result.Chunk.DocumentID,
			&result.Chunk.ChunkIndex,
			&result.Chunk.Text,
			&result.Chunk.TokenCount,
			&result.Chunk.ContentHash,
			&chunkMetadata,
			&result.Chunk.CreatedAt,
			&result.Document.ID,
			&result.Document.SourceURI,
			&result.Document.Title,
			&result.Document.ContentHash,
			&documentMetadata,
			&result.Document.PrivacyClass,
			&result.Document.CreatedAt,
			&result.Document.UpdatedAt,
		); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(chunkMetadata), &result.Chunk.Metadata); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(documentMetadata), &result.Document.Metadata); err != nil {
			return nil, err
		}
		result.EvidenceID = result.Chunk.ID
		results = append(results, result)
	}
	return results, rows.Err()
}

func (s SQLiteStore) ResultByChunkID(ctx context.Context, chunkID string) (Result, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT
  c.id, c.document_id, c.chunk_index, c.text, c.token_count, c.content_hash, c.metadata_json, c.created_at,
  d.id, d.source_uri, d.title, d.content_hash, d.metadata_json, d.privacy_class, d.created_at, d.updated_at
FROM document_chunks c
JOIN documents d ON d.id = c.document_id
WHERE c.id = ? AND NOT EXISTS (SELECT 1 FROM interaction_knowledge_documents k WHERE k.id=d.id)
`, chunkID)
	var result Result
	var chunkMetadata string
	var documentMetadata string
	if err := row.Scan(
		&result.Chunk.ID,
		&result.Chunk.DocumentID,
		&result.Chunk.ChunkIndex,
		&result.Chunk.Text,
		&result.Chunk.TokenCount,
		&result.Chunk.ContentHash,
		&chunkMetadata,
		&result.Chunk.CreatedAt,
		&result.Document.ID,
		&result.Document.SourceURI,
		&result.Document.Title,
		&result.Document.ContentHash,
		&documentMetadata,
		&result.Document.PrivacyClass,
		&result.Document.CreatedAt,
		&result.Document.UpdatedAt,
	); err != nil {
		return Result{}, err
	}
	if err := json.Unmarshal([]byte(chunkMetadata), &result.Chunk.Metadata); err != nil {
		return Result{}, err
	}
	if err := json.Unmarshal([]byte(documentMetadata), &result.Document.Metadata); err != nil {
		return Result{}, err
	}
	result.EvidenceID = result.Chunk.ID
	return result, nil
}
