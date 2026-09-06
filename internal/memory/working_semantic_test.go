package memory

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"agent/internal/rag"
	"agent/internal/storage"
)

func TestWorkingStoreLifecycle(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	store := NewWorkingStore(db.SQL)
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

	if err := store.Put(ctx, WorkingEntry{
		ID:        "work-1",
		TaskID:    "task-1",
		Key:       "draft",
		Value:     map[string]string{"text": "hello"},
		ExpiresAt: now.Add(time.Minute),
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	entry, ok, err := store.Get(ctx, "task-1", "draft", now)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !ok {
		t.Fatal("Get() ok = false, want true")
	}
	if entry.Value["text"] != "hello" {
		t.Fatalf("text = %q, want hello", entry.Value["text"])
	}

	deleted, err := store.CleanupExpired(ctx, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("CleanupExpired() error = %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}
	_, ok, err = store.Get(ctx, "task-1", "draft", now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("Get() after cleanup error = %v", err)
	}
	if ok {
		t.Fatal("Get() ok = true after cleanup, want false")
	}
}

func TestSemanticStoreUpsertAndIndexesFact(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	indexer := &recordingIndexer{}
	store := NewSemanticStore(db.SQL, indexer)

	if err := store.Upsert(ctx, SemanticFact{
		ID:          "fact-1",
		Scope:       "user",
		Subject:     "Sunny",
		Predicate:   "prefers",
		Object:      "local-first agents",
		Source:      "chat",
		EvidenceIDs: []string{"ev-1"},
	}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	facts, err := store.FindByScope(ctx, "user", 10)
	if err != nil {
		t.Fatalf("FindByScope() error = %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("len(facts) = %d, want 1", len(facts))
	}
	if facts[0].EvidenceIDs[0] != "ev-1" {
		t.Fatalf("EvidenceIDs = %v, want [ev-1]", facts[0].EvidenceIDs)
	}
	if len(indexer.documents) != 1 {
		t.Fatalf("indexed docs = %d, want 1", len(indexer.documents))
	}
	if indexer.documents[0].SourceURI != "memory://semantic/fact-1" {
		t.Fatalf("SourceURI = %q, want memory://semantic/fact-1", indexer.documents[0].SourceURI)
	}
	if indexer.documents[0].Metadata["evidence_ids"] != "ev-1" {
		t.Fatalf("metadata evidence_ids = %q, want ev-1", indexer.documents[0].Metadata["evidence_ids"])
	}
}

type recordingIndexer struct {
	documents []rag.Document
}

func (r *recordingIndexer) Index(_ context.Context, document rag.Document) ([]rag.Chunk, error) {
	r.documents = append(r.documents, document)
	return []rag.Chunk{{ID: document.ID + "_chunk_1"}}, nil
}

func testDB(t *testing.T) *storage.DB {
	t.Helper()
	ctx := context.Background()
	db, err := storage.OpenSQLite(ctx, filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatalf("OpenSQLite() error = %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := storage.Migrate(ctx, db.SQL); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	return db
}
