package runtime

import (
	"context"
	"strings"

	"agent/internal/memory"
	"agent/internal/rag"
)

type RetrieveOptions struct {
	TopK int
}

type Pipeline interface {
	Retrieve(ctx context.Context, taskID string, query string, opts RetrieveOptions) ([]Evidence, error)
}

type LocalPipeline struct {
	BM25       rag.BM25
	Vector     VectorRetriever
	Reranker   rag.Reranker
	Compressor rag.Compressor
	TopK       int
}

type VectorRetriever interface {
	Search(ctx context.Context, query string, limit int) ([]rag.Result, error)
}

func (p LocalPipeline) Retrieve(ctx context.Context, taskID string, query string, opts RetrieveOptions) ([]Evidence, error) {
	topK := opts.TopK
	if topK <= 0 {
		topK = p.TopK
	}
	if topK <= 0 {
		topK = 8
	}
	bm25Results, err := p.BM25.Search(ctx, query, topK)
	if err != nil {
		return nil, err
	}
	lists := []rag.RankedList{bm25Results}
	if p.Vector != nil {
		vectorResults, vectorErr := p.Vector.Search(ctx, query, topK)
		if vectorErr == nil && len(vectorResults) > 0 {
			lists = append(lists, vectorResults)
		}
	}
	results := rag.FuseRRF(lists, topK, 60)
	if p.Reranker != nil {
		reranked, rerankErr := p.Reranker.Rerank(query, results)
		if rerankErr == nil && len(reranked) > 0 {
			results = reranked
		}
	}
	compressed := p.Compressor.Compress(query, results)
	evidence := make([]Evidence, 0, len(compressed.Excerpts))
	for _, excerpt := range compressed.Excerpts {
		evidence = append(evidence, Evidence{
			ID:           stableEvidenceID(taskID, string(SourceRAG), excerpt.EvidenceID),
			TaskID:       taskID,
			NodeID:       "retrieval",
			SourceType:   SourceRAG,
			SourceID:     excerpt.EvidenceID,
			Content:      excerpt.Text,
			Score:        excerpt.Score,
			Trust:        0.85,
			PrivacyClass: defaultPrivacyClass(excerpt.PrivacyClass),
		})
	}
	return evidence, nil
}

type MemoryService interface {
	Search(ctx context.Context, taskID string, query string, limit int) ([]Evidence, error)
}

type SQLiteMemoryService struct {
	Episodic memory.Store
	Semantic memory.SemanticStore
}

func (s SQLiteMemoryService) Search(ctx context.Context, taskID string, query string, limit int) ([]Evidence, error) {
	if limit <= 0 {
		limit = 8
	}
	query = strings.ToLower(strings.TrimSpace(query))
	var out []Evidence
	facts, err := s.Semantic.FindByScope(ctx, "project", limit)
	if err != nil {
		return nil, err
	}
	for _, fact := range facts {
		text := strings.TrimSpace(fact.Subject + " " + fact.Predicate + " " + fact.Object)
		if text == "" || (query != "" && !looseMatch(query, text)) {
			continue
		}
		out = append(out, Evidence{
			ID:           stableEvidenceID(taskID, string(SourceMemory), fact.ID),
			TaskID:       taskID,
			NodeID:       "memory",
			SourceType:   SourceMemory,
			SourceID:     fact.ID,
			Content:      text,
			Score:        fact.Confidence,
			Trust:        0.9,
			PrivacyClass: defaultPrivacyClass(fact.PrivacyClass),
		})
		if len(out) >= limit {
			return out, nil
		}
	}
	events, err := s.Episodic.Recent(ctx, limit)
	if err != nil {
		return nil, err
	}
	for _, event := range events {
		text := event.Summary
		if payloadText := event.Payload["text"]; payloadText != "" {
			text = payloadText
		}
		if strings.TrimSpace(text) == "" || (query != "" && !looseMatch(query, text)) {
			continue
		}
		out = append(out, Evidence{
			ID:           stableEvidenceID(taskID, string(SourceMemory), event.ID),
			TaskID:       taskID,
			NodeID:       "memory",
			SourceType:   SourceMemory,
			SourceID:     event.ID,
			Content:      text,
			Score:        event.Confidence,
			Trust:        0.8,
			PrivacyClass: defaultPrivacyClass(event.PrivacyClass),
		})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func looseMatch(query string, text string) bool {
	text = strings.ToLower(text)
	for _, term := range strings.Fields(query) {
		if len(term) >= 3 && strings.Contains(text, term) {
			return true
		}
	}
	return false
}
