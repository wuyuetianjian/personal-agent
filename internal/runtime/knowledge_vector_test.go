package runtime

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"agent/internal/rag"
	"agent/internal/storage"
)

// Only the external search boundary is replaced: every match passes through
// the actual production adapter before the real LocalPipeline consumes it.
type ownedVectorMatches struct {
	adapter embeddingVectorRetriever
	matches []rag.QdrantSearchResult
}

func (v ownedVectorMatches) Search(ctx context.Context, _ string, _ int) ([]rag.Result, error) {
	var out []rag.Result
	for _, match := range v.matches {
		result, e := v.adapter.resultFromMatch(ctx, match)
		if e == nil {
			out = append(out, result)
		}
	}
	return out, nil
}
func TestKnowledgeVectorAdapterRefusesManagedAndMissingEvidence(t *testing.T) {
	for _, state := range []string{"indexed", "deactivated", "stale", "foreign-grant"} {
		t.Run(state, func(t *testing.T) {
			ctx := t.Context()
			db, e := storage.OpenSQLite(ctx, filepath.Join(t.TempDir(), "owned.db"))
			if e != nil {
				t.Fatal(e)
			}
			t.Cleanup(func() { db.Close() })
			if e = storage.Migrate(ctx, db.SQL); e != nil {
				t.Fatal(e)
			}
			store := rag.NewSQLiteStore(db.SQL, rag.Chunker{})
			content := "private managed seahorse evidence"
			indexed, e := store.IndexRevision(ctx, rag.Document{ID: "knowledge-owned", SourceURI: "knowledge:knowledge-owned", Text: content})
			if e != nil {
				t.Fatal(e)
			}
			dbState := "indexed"
			if state == "deactivated" {
				dbState = state
			}
			if _, e = db.SQL.ExecContext(ctx, `INSERT INTO interaction_knowledge_documents VALUES(?,?,?,?,?,?,?,?,?)`, indexed.DocumentID, "scope-A", "text", "owned", content, rag.HashText(content), "lifecycle", indexed.Revision, dbState); e != nil {
				t.Fatal(e)
			}
			if state == "stale" {
				if _, e = db.SQL.ExecContext(ctx, `UPDATE document_chunks SET text='new private managed seahorse evidence' WHERE document_id=?`, indexed.DocumentID); e != nil {
					t.Fatal(e)
				}
			}
			scope := "scope-A"
			if state == "foreign-grant" {
				scope = "scope-B"
			}
			if _, e = db.SQL.ExecContext(ctx, `INSERT INTO project_document_scopes SELECT id,?,content_hash,? FROM documents WHERE id=?`, scope, indexed.Revision, indexed.DocumentID); e != nil {
				t.Fatal(e)
			}
			if _, e = store.ResultByChunkID(ctx, indexed.Chunks[0].ID); !errors.Is(e, sql.ErrNoRows) {
				t.Fatal("direct store must refuse managed evidence", e)
			}
			legacy, e := store.IndexRevision(ctx, rag.Document{ID: "legacy-owned", SourceURI: "local://owned", Text: "harmless legacy evidence"})
			if e != nil {
				t.Fatal(e)
			}
			adapter := embeddingVectorRetriever{Store: store}
			cases := []struct {
				name  string
				match rag.QdrantSearchResult
			}{
				{"current-chunk", rag.QdrantSearchResult{ID: indexed.Chunks[0].ID, Payload: map[string]any{"chunk_id": indexed.Chunks[0].ID, "document_id": indexed.DocumentID, "text": content}}},
				{"old-chunk", rag.QdrantSearchResult{ID: "old-vector", Payload: map[string]any{"chunk_id": "old-managed-chunk", "document_id": indexed.DocumentID, "text": content}}},
				{"missing-chunk", rag.QdrantSearchResult{ID: "missing", Payload: map[string]any{"document_id": indexed.DocumentID, "text": content}}},
				{"payload-only", rag.QdrantSearchResult{Payload: map[string]any{"document_id": indexed.DocumentID, "text": content}}},
				{"source-only", rag.QdrantSearchResult{Payload: map[string]any{"source_uri": "knowledge:knowledge-owned", "text": content}}},
				{"text-only", rag.QdrantSearchResult{Payload: map[string]any{"text": content}}},
				{"managed-doc-with-legacy-chunk", rag.QdrantSearchResult{ID: "vector", Payload: map[string]any{"chunk_id": legacy.Chunks[0].ID, "document_id": indexed.DocumentID, "text": content}}},
				{"managed-source-with-legacy-chunk", rag.QdrantSearchResult{ID: "vector", Payload: map[string]any{"chunk_id": legacy.Chunks[0].ID, "source_uri": "knowledge:knowledge-owned", "text": content}}},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					if result, e := adapter.resultFromMatch(ctx, tc.match); e == nil {
						t.Fatalf("denied identity became vector evidence: %+v", result)
					}
					pipeline := LocalPipeline{BM25: rag.NewBM25(store), Vector: ownedVectorMatches{adapter: adapter, matches: []rag.QdrantSearchResult{tc.match}}, Compressor: rag.Compressor{MaxChars: 4000}}
					if got, e := pipeline.Retrieve(ctx, "owned-task", "seahorse", RetrieveOptions{TopK: 8}); e != nil || len(got) != 0 {
						t.Fatal("denied content reached pipeline", got, e)
					}
				})
			}
		})
	}
}
func TestKnowledgeVectorAdapterUsesOnlyAllowedLocalContent(t *testing.T) {
	ctx := t.Context()
	db, e := storage.OpenSQLite(ctx, filepath.Join(t.TempDir(), "owned.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if e = storage.Migrate(ctx, db.SQL); e != nil {
		t.Fatal(e)
	}
	store := rag.NewSQLiteStore(db.SQL, rag.Chunker{})
	first, e := store.IndexRevision(ctx, rag.Document{ID: "legacy-owned", SourceURI: "local://owned", Text: "original legacy text"})
	if e != nil {
		t.Fatal(e)
	}
	adapter := embeddingVectorRetriever{Store: store}
	valid := rag.QdrantSearchResult{ID: "vector-id", Payload: map[string]any{"chunk_id": first.Chunks[0].ID, "document_id": "legacy-owned", "source_uri": "local://owned", "text": "untrusted payload text", "title": "untrusted title", "privacy_class": "public"}}
	if got, e := adapter.resultFromMatch(ctx, valid); e != nil || got.Chunk.Text != "original legacy text" || got.Document.PrivacyClass == "public" {
		t.Fatal(got, e)
	}
	if got, e := adapter.resultFromMatch(ctx, rag.QdrantSearchResult{ID: first.Chunks[0].ID}); e != nil || got.Chunk.Text != "original legacy text" {
		t.Fatal(got, e)
	}
	if _, e = store.IndexRevision(ctx, rag.Document{ID: "legacy-owned", SourceURI: "local://owned", Text: "new current legacy text"}); e != nil {
		t.Fatal(e)
	}
	if got, e := adapter.resultFromMatch(ctx, valid); e == nil {
		t.Fatal("old missing local chunk revived payload", got)
	}
	if e = db.Close(); e != nil {
		t.Fatal(e)
	}
	if got, e := adapter.resultFromMatch(ctx, valid); e == nil {
		t.Fatal("storage failure became payload evidence", got)
	}
}
