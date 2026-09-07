package runtime

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"

	"agent/internal/config"
	"agent/internal/model"
	"agent/internal/rag"
)

type embeddingVectorRetriever struct {
	Provider model.EmbeddingProvider
	Model    model.ModelMetadata
	Qdrant   rag.QdrantClient
	Store    rag.SQLiteStore
}

func (r embeddingVectorRetriever) Search(ctx context.Context, query string, limit int) ([]rag.Result, error) {
	embedding, err := r.Provider.Embed(ctx, model.EmbeddingRequest{Model: r.Model, Input: []string{query}})
	if err != nil {
		return nil, err
	}
	if len(embedding.Vectors) == 0 {
		return nil, rag.ErrQdrantUnavailable
	}
	matches, err := r.Qdrant.Search(ctx, embedding.Vectors[0], limit)
	if err != nil {
		return nil, err
	}
	results := make([]rag.Result, 0, len(matches))
	for index, match := range matches {
		result, err := r.resultFromMatch(ctx, match)
		if err != nil {
			continue
		}
		result.Score = match.Score
		result.Rank = index + 1
		results = append(results, result)
	}
	return results, nil
}

func (r embeddingVectorRetriever) resultFromMatch(ctx context.Context, match rag.QdrantSearchResult) (rag.Result, error) {
	chunkID := payloadString(match.Payload, "chunk_id")
	if chunkID == "" {
		chunkID = match.ID
	}
	if chunkID != "" {
		if result, err := r.Store.ResultByChunkID(ctx, chunkID); err == nil {
			return result, nil
		} else if err != sql.ErrNoRows {
			return rag.Result{}, err
		}
	}
	text := payloadString(match.Payload, "text")
	if text == "" {
		return rag.Result{}, fmt.Errorf("qdrant match %s has no chunk payload", match.ID)
	}
	documentID := payloadString(match.Payload, "document_id")
	if documentID == "" {
		documentID = payloadString(match.Payload, "source_uri")
	}
	result := rag.Result{
		Chunk: rag.Chunk{
			ID:         chunkID,
			DocumentID: documentID,
			Text:       text,
		},
		Document: rag.Document{
			ID:           documentID,
			SourceURI:    payloadString(match.Payload, "source_uri"),
			Title:        payloadString(match.Payload, "title"),
			PrivacyClass: payloadString(match.Payload, "privacy_class"),
		},
		EvidenceID: chunkID,
	}
	if result.EvidenceID == "" {
		result.EvidenceID = match.ID
	}
	return result, nil
}

type modelReranker struct {
	Provider model.RerankProvider
	Model    model.ModelMetadata
}

func (r modelReranker) Rerank(query string, results []rag.Result) ([]rag.Result, error) {
	documents := make([]string, len(results))
	for i, result := range results {
		documents[i] = result.Chunk.Text
	}
	response, err := r.Provider.Rerank(context.Background(), model.RerankRequest{Model: r.Model, Query: query, Documents: documents})
	if err != nil {
		return nil, err
	}
	out := make([]rag.Result, 0, len(response.Results))
	for rank, item := range response.Results {
		if item.Index < 0 || item.Index >= len(results) {
			continue
		}
		result := results[item.Index]
		result.Score = item.Score
		result.Rank = rank + 1
		out = append(out, result)
	}
	return out, nil
}

func configureHybridRAG(cfg config.Config, db *sql.DB, rt *Runtime) error {
	store := rag.NewSQLiteStore(db, rag.Chunker{MaxTokens: 160})
	pipeline := LocalPipeline{
		BM25:       rag.NewBM25(store),
		Compressor: rag.Compressor{MaxChars: 4000},
		TopK:       8,
	}
	registry, err := model.NewRegistry(cfg.Models)
	if err != nil {
		return err
	}
	if cfg.RAG.Vector.Enabled {
		vector, err := buildVectorRetriever(cfg, registry, store)
		if err != nil {
			return err
		}
		pipeline.Vector = vector
	}
	if cfg.RAG.Reranker.Enabled {
		reranker, err := buildReranker(cfg, registry)
		if err != nil {
			return err
		}
		pipeline.Reranker = reranker
	}
	rt.RAG = pipeline
	return nil
}

func buildVectorRetriever(cfg config.Config, registry *model.Registry, store rag.SQLiteStore) (VectorRetriever, error) {
	metadata, err := registry.Model(cfg.RAG.Vector.EmbeddingModelID)
	if err != nil {
		return nil, err
	}
	if !metadata.Capabilities[model.CapabilityEmbedding] {
		return nil, fmt.Errorf("%w: model %s lacks capability %s", model.ErrNoMatchingModel, metadata.ID, model.CapabilityEmbedding)
	}
	provider, err := buildOpenAICompatibleClient(metadata)
	if err != nil {
		return nil, err
	}
	baseURL := cfg.RAG.Vector.QdrantBaseURL
	if baseURL == "" && cfg.RAG.Vector.QdrantBaseURLEnv != "" {
		baseURL, _ = config.EnvValue(cfg.RAG.Vector.QdrantBaseURLEnv)
	}
	apiKey, _ := config.EnvValue(cfg.RAG.Vector.QdrantAPIKeyEnv)
	return embeddingVectorRetriever{
		Provider: provider,
		Model:    metadata,
		Qdrant: rag.QdrantClient{
			BaseURL:    baseURL,
			Collection: cfg.RAG.Vector.QdrantCollection,
			APIKey:     apiKey,
		},
		Store: store,
	}, nil
}

func buildReranker(cfg config.Config, registry *model.Registry) (rag.Reranker, error) {
	metadata, err := registry.Model(cfg.RAG.Reranker.ModelID)
	if err != nil {
		return nil, err
	}
	if !metadata.Capabilities[model.CapabilityRerank] {
		return nil, fmt.Errorf("%w: model %s lacks capability %s", model.ErrNoMatchingModel, metadata.ID, model.CapabilityRerank)
	}
	provider, err := buildOpenAICompatibleClient(metadata)
	if err != nil {
		return nil, err
	}
	rerankProvider, ok := any(provider).(model.RerankProvider)
	if !ok {
		return nil, model.ErrUnsupportedOperation
	}
	return modelReranker{Provider: rerankProvider, Model: metadata}, nil
}

func buildOpenAICompatibleClient(metadata model.ModelMetadata) (*model.OpenAICompatibleClient, error) {
	if !metadata.Provider.Enabled {
		return nil, model.ErrNoMatchingModel
	}
	if metadata.Provider.Type != "openai_compatible" {
		return nil, fmt.Errorf("provider %q has unsupported type %q", metadata.Provider.ID, metadata.Provider.Type)
	}
	baseURL := metadata.Provider.BaseURL
	if baseURL == "" && metadata.Provider.BaseURLEnv != "" {
		baseURL, _ = config.EnvValue(metadata.Provider.BaseURLEnv)
	}
	if baseURL == "" {
		return nil, fmt.Errorf("provider %q has no base URL", metadata.Provider.ID)
	}
	apiKey, _ := config.EnvValue(metadata.Provider.APIKeyEnv)
	return &model.OpenAICompatibleClient{
		Provider: metadata.Provider,
		BaseURL:  baseURL,
		APIKey:   apiKey,
	}, nil
}

func payloadString(payload map[string]any, key string) string {
	value, ok := payload[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case int:
		return strconv.Itoa(typed)
	default:
		return fmt.Sprint(typed)
	}
}
