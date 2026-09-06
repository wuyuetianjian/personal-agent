package runtime

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"agent/internal/rag"
	"agent/internal/storage"
)

func TestLocalPipelineFallsBackWhenVectorUnavailable(t *testing.T) {
	ctx := context.Background()
	db, err := storage.OpenSQLite(ctx, filepath.Join(t.TempDir(), "rag.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.Migrate(ctx, db.SQL); err != nil {
		t.Fatal(err)
	}
	store := rag.NewSQLiteStore(db.SQL, rag.Chunker{MaxTokens: 32})
	if _, err := store.Index(ctx, rag.Document{ID: "doc-1", SourceURI: "local://doc", Title: "Doc", Text: "hybrid retrieval keeps bm25 fallback", PrivacyClass: "local_private"}); err != nil {
		t.Fatal(err)
	}
	pipeline := LocalPipeline{
		BM25:       rag.NewBM25(store),
		Vector:     failingVector{},
		Compressor: rag.Compressor{MaxChars: 4000},
		TopK:       4,
	}
	evidence, err := pipeline.Retrieve(ctx, "task-1", "hybrid retrieval", RetrieveOptions{TopK: 4})
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence) == 0 {
		t.Fatal("expected BM25 fallback evidence")
	}
}

type failingVector struct{}

func (failingVector) Search(ctx context.Context, query string, limit int) ([]rag.Result, error) {
	return nil, errors.New("vector unavailable")
}
