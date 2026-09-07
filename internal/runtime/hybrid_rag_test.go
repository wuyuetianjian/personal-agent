package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"agent/internal/config"
	"agent/internal/model"
	"agent/internal/rag"
)

func TestBuildWiresHybridRAGVectorRetriever(t *testing.T) {
	ctx := context.Background()
	embeddingServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings" {
			t.Fatalf("embedding path = %s, want /embeddings", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":  []map[string]any{{"index": 0, "embedding": []float64{0.1, 0.2}}},
			"usage": map[string]any{"prompt_tokens": 3},
		})
	}))
	defer embeddingServer.Close()

	qdrantServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/collections/pachat/points/search" {
			t.Fatalf("qdrant path = %s, want search endpoint", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": []map[string]any{{
				"id":    "vector-match-1",
				"score": 0.99,
				"payload": map[string]any{
					"chunk_id":      "vector-match-1",
					"document_id":   "doc-vector",
					"text":          "vector retrieval returns this indexed chunk",
					"source_uri":    "local://vector",
					"title":         "Vector",
					"privacy_class": "local_private",
				},
			}},
		})
	}))
	defer qdrantServer.Close()

	cfg := configForHybridRAGTest(t, embeddingServer.URL, qdrantServer.URL)
	rt, err := Build(ctx, cfg)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	pipeline, ok := rt.RAG.(LocalPipeline)
	if !ok {
		t.Fatalf("RAG type = %T, want LocalPipeline", rt.RAG)
	}
	if pipeline.Vector == nil {
		t.Fatal("Build() did not wire Vector retriever")
	}

	store := rag.NewSQLiteStore(rt.Storage.SQL, rag.Chunker{MaxTokens: 32})
	if _, err := store.Index(ctx, rag.Document{
		ID:           "doc-vector",
		SourceURI:    "local://vector",
		Title:        "Vector",
		Text:         "vector retrieval returns this indexed chunk",
		PrivacyClass: "local_private",
	}); err != nil {
		t.Fatalf("Index() error = %v", err)
	}
	evidence, err := rt.RAG.Retrieve(ctx, "task-vector", "vector retrieval", RetrieveOptions{TopK: 4})
	if err != nil {
		t.Fatalf("Retrieve() error = %v", err)
	}
	if len(evidence) == 0 || !strings.Contains(evidence[0].Content, "vector retrieval") {
		t.Fatalf("evidence = %#v, want vector-backed indexed evidence", evidence)
	}
}

func TestHybridRAGFallsBackToBM25WhenQdrantFails(t *testing.T) {
	ctx := context.Background()
	embeddingServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"index": 0, "embedding": []float64{0.1, 0.2}}},
		})
	}))
	defer embeddingServer.Close()
	qdrantServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	defer qdrantServer.Close()

	rt, err := Build(ctx, configForHybridRAGTest(t, embeddingServer.URL, qdrantServer.URL))
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	store := rag.NewSQLiteStore(rt.Storage.SQL, rag.Chunker{MaxTokens: 32})
	if _, err := store.Index(ctx, rag.Document{
		ID:           "doc-bm25",
		SourceURI:    "local://bm25",
		Title:        "BM25",
		Text:         "bm25 fallback survives vector outage",
		PrivacyClass: "local_private",
	}); err != nil {
		t.Fatalf("Index() error = %v", err)
	}

	evidence, err := rt.RAG.Retrieve(ctx, "task-bm25", "bm25 fallback", RetrieveOptions{TopK: 4})
	if err != nil {
		t.Fatalf("Retrieve() error = %v", err)
	}
	if len(evidence) == 0 || !strings.Contains(evidence[0].Content, "bm25 fallback") {
		t.Fatalf("evidence = %#v, want BM25 fallback evidence", evidence)
	}
}

func configForHybridRAGTest(t *testing.T, embeddingBaseURL string, qdrantBaseURL string) config.Config {
	t.Helper()
	cfg := configForBuilderPlannerTest(t)
	cfg.Storage.SQLite.Path = filepath.Join(t.TempDir(), "hybrid-rag.db")
	cfg.Models.Providers["local-ollama"] = config.ProviderConfig{
		Type:       "openai_compatible",
		BaseURL:    embeddingBaseURL,
		TrustLevel: "local_private",
	}
	cfg.Models.Registry = append(cfg.Models.Registry, config.ModelConfig{
		ID:           "local-embedding",
		Provider:     "local-ollama",
		Model:        "local-embedding-model",
		TrustLevel:   "local_private",
		Capabilities: []string{string(model.CapabilityEmbedding)},
	})
	cfg.RAG.Vector = config.VectorRAGConfig{
		Enabled:          true,
		EmbeddingModelID: "local-embedding",
		QdrantBaseURL:    qdrantBaseURL,
		QdrantCollection: "pachat",
	}
	return cfg
}
