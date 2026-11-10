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
	// A vector hit ranks an existing admitted local chunk; remote payload is
	// never evidence authority. ErrNoRows includes an intentional managed-scope
	// denial as well as an old/missing chunk. Neither may revive cached text.
	if chunkID == "" {
		return rag.Result{}, fmt.Errorf("vector hit has no local chunk identity")
	}
	result, err := r.Store.ResultByChunkID(ctx, chunkID)
	if err != nil {
		return rag.Result{}, err
	}
	// Do not let a managed/foreign document claim borrow an unrelated allowed
	// chunk. Payload identities, when supplied, must match the local provenance.
	if documentID := payloadString(match.Payload, "document_id"); documentID != "" && documentID != result.Document.ID {
		return rag.Result{}, fmt.Errorf("vector document identity does not match local evidence")
	}
	if source := payloadString(match.Payload, "source_uri"); source != "" && source != result.Document.SourceURI {
		return rag.Result{}, fmt.Errorf("vector source identity does not match local evidence")
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

// AssignDocumentProject is trusted ingestion CAS, not source/model metadata.
// expectedRevision MUST be the token returned alongside the indexed content by
// IndexRevision. Looking up the latest generation after verifying other content
// would lose the binding. An empty token cannot authorize an unversioned grant.
func AssignDocumentProject(ctx context.Context, db *sql.DB, documentID, projectID, expectedRevision string) error {
	if documentID == "" || projectID == "" || expectedRevision == "" {
		return rag.ErrDocumentRevisionChanged
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// The conditional write acquires the SQLite writer before validation. The
	// insert and idempotence check cannot observe different document generations.
	result, err := tx.ExecContext(ctx, `INSERT INTO project_document_scopes(document_id,project_id,content_hash,revision)
 SELECT d.id,?,d.content_hash,v.revision FROM documents d JOIN document_revisions v ON v.document_id=d.id
 WHERE d.id=? AND v.revision=? ON CONFLICT(document_id) DO NOTHING`, projectID, documentID, expectedRevision)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		var owner, assignedRevision, currentRevision string
		err = tx.QueryRowContext(ctx, `SELECT s.project_id,s.revision,v.revision FROM project_document_scopes s
  JOIN document_revisions v ON v.document_id=s.document_id WHERE s.document_id=?`, documentID).Scan(&owner, &assignedRevision, &currentRevision)
		if err == sql.ErrNoRows {
			return rag.ErrDocumentRevisionChanged
		}
		if err != nil {
			return err
		}
		if currentRevision != expectedRevision || assignedRevision != expectedRevision {
			return rag.ErrDocumentRevisionChanged
		}
		if owner != projectID {
			return fmt.Errorf("immutable document project mismatch")
		}
	}
	return tx.Commit()
}

type projectCorpus struct {
	store     rag.SQLiteStore
	projectID string
}

func (p projectCorpus) ListChunks(ctx context.Context) ([]rag.Result, error) {
	return p.store.ListProjectChunks(ctx, p.projectID)
}

// NewProjectPipeline reuses BM25/fusion/compression, restricting the corpus BEFORE
// ranking. Legacy vector payloads and remote rerankers have no verified project
// provenance, so are deliberately not enabled in the scoped adapter.
func NewProjectPipeline(db *sql.DB, projectID string) LocalPipeline {
	return LocalPipeline{BM25: rag.NewBM25(projectCorpus{store: rag.NewSQLiteStore(db, rag.Chunker{MaxTokens: 160}), projectID: projectID}), Compressor: rag.Compressor{MaxChars: 4000}, TopK: 8}
}
