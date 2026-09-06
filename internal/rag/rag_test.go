package rag

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"agent/internal/storage"
)

func TestBM25Ranking(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	if _, err := store.Index(ctx, Document{
		ID:        "doc-a",
		SourceURI: "file://a",
		Title:     "Alpha",
		Text:      "alpha alpha alpha retrieval local memory",
	}); err != nil {
		t.Fatalf("Index doc-a error = %v", err)
	}
	if _, err := store.Index(ctx, Document{
		ID:        "doc-b",
		SourceURI: "file://b",
		Title:     "Beta",
		Text:      "browser automation permissions",
	}); err != nil {
		t.Fatalf("Index doc-b error = %v", err)
	}

	results, err := NewBM25(store).Search(ctx, "alpha retrieval", 10)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(results) == 0 {
		t.Fatal("Search() returned no results")
	}
	if results[0].Document.ID != "doc-a" {
		t.Fatalf("top document = %q, want doc-a", results[0].Document.ID)
	}
	if results[0].Rank != 1 {
		t.Fatalf("top rank = %d, want 1", results[0].Rank)
	}
}

func TestFuseRRFDeterministicOrdering(t *testing.T) {
	a := Result{Chunk: Chunk{ID: "a"}, EvidenceID: "a", Rank: 1}
	b := Result{Chunk: Chunk{ID: "b"}, EvidenceID: "b", Rank: 1}
	c := Result{Chunk: Chunk{ID: "c"}, EvidenceID: "c", Rank: 2}

	results := FuseRRF([]RankedList{{b, c}, {a, c}}, 10, 60)
	if len(results) != 3 {
		t.Fatalf("len(results) = %d, want 3", len(results))
	}
	got := []string{results[0].EvidenceID, results[1].EvidenceID, results[2].EvidenceID}
	want := []string{"c", "a", "b"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("result %d = %q, want %q; full=%v", i, got[i], want[i], got)
		}
	}
}

func TestQdrantIntegrationSkipsWhenUnavailable(t *testing.T) {
	baseURL := os.Getenv("QDRANT_URL")
	if baseURL == "" {
		t.Skip("QDRANT_URL is not set")
	}
	client := QdrantClient{
		BaseURL:    baseURL,
		Collection: os.Getenv("QDRANT_COLLECTION"),
		APIKey:     os.Getenv("QDRANT_API_KEY"),
		HTTPClient: &http.Client{Timeout: 2 * time.Second},
	}
	if client.Collection == "" {
		client.Collection = "pachat_test"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Health(ctx); err != nil {
		if errors.Is(err, ErrQdrantUnavailable) {
			t.Skipf("qdrant unavailable: %v", err)
		}
		t.Fatalf("Health() error = %v", err)
	}
}

func TestCompressorPreservesEvidence(t *testing.T) {
	results := []Result{
		{
			Chunk:      Chunk{ID: "chunk-1", Text: "first excerpt", Metadata: map[string]string{"kind": "note"}},
			Document:   Document{SourceURI: "file://one", Title: "One", PrivacyClass: "local_private"},
			EvidenceID: "evidence-1",
			Score:      2,
		},
		{
			Chunk:      Chunk{ID: "chunk-2", Text: "second excerpt"},
			Document:   Document{SourceURI: "file://two", Title: "Two", PrivacyClass: "local_private"},
			EvidenceID: "evidence-2",
			Score:      1,
		},
	}

	compressed := Compressor{MaxChars: 100}.Compress("query", results)
	if len(compressed.Excerpts) != 2 {
		t.Fatalf("len(excerpts) = %d, want 2", len(compressed.Excerpts))
	}
	if compressed.EvidenceIDs[0] != "evidence-1" || compressed.EvidenceIDs[1] != "evidence-2" {
		t.Fatalf("evidence IDs = %v, want [evidence-1 evidence-2]", compressed.EvidenceIDs)
	}
	if compressed.Excerpts[0].SourceURI != "file://one" {
		t.Fatalf("SourceURI = %q, want file://one", compressed.Excerpts[0].SourceURI)
	}
}

func testStore(t *testing.T) SQLiteStore {
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
	return NewSQLiteStore(db.SQL, Chunker{MaxTokens: 20})
}
