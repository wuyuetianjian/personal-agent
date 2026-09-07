package rag

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

type SQLiteStore struct {
	db      *sql.DB
	chunker Chunker
}

func NewSQLiteStore(db *sql.DB, chunker Chunker) SQLiteStore {
	return SQLiteStore{db: db, chunker: chunker}
}

func (s SQLiteStore) Index(ctx context.Context, document Document) ([]Chunk, error) {
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
		return nil, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
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
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM document_chunks WHERE document_id = ?`, document.ID); err != nil {
		return nil, err
	}

	chunks := s.chunker.Chunk(document)
	for i := range chunks {
		if chunks[i].CreatedAt.IsZero() {
			chunks[i].CreatedAt = now
		}
		chunkMetadataJSON, err := json.Marshal(cloneMap(chunks[i].Metadata))
		if err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO document_chunks (
  id, document_id, chunk_index, text, token_count, content_hash, metadata_json, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
`, chunks[i].ID, chunks[i].DocumentID, chunks[i].ChunkIndex, chunks[i].Text, chunks[i].TokenCount,
			chunks[i].ContentHash, string(chunkMetadataJSON), chunks[i].CreatedAt); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return chunks, nil
}

func (s SQLiteStore) ListChunks(ctx context.Context) ([]Result, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT
  c.id, c.document_id, c.chunk_index, c.text, c.token_count, c.content_hash, c.metadata_json, c.created_at,
  d.id, d.source_uri, d.title, d.content_hash, d.metadata_json, d.privacy_class, d.created_at, d.updated_at
FROM document_chunks c
JOIN documents d ON d.id = c.document_id
ORDER BY d.id ASC, c.chunk_index ASC, c.id ASC
`)
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
WHERE c.id = ?
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
