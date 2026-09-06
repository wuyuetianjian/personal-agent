package rag

import (
	"context"
	"math"
	"sort"
)

const (
	bm25K1 = 1.5
	bm25B  = 0.75
)

type CorpusProvider interface {
	ListChunks(ctx context.Context) ([]Result, error)
}

type BM25 struct {
	corpus CorpusProvider
}

func NewBM25(corpus CorpusProvider) BM25 {
	return BM25{corpus: corpus}
}

func (b BM25) Search(ctx context.Context, query string, limit int) ([]Result, error) {
	if limit <= 0 {
		limit = 10
	}
	corpus, err := b.corpus.ListChunks(ctx)
	if err != nil {
		return nil, err
	}
	queryTerms := uniqueTerms(tokenize(query))
	if len(queryTerms) == 0 || len(corpus) == 0 {
		return nil, nil
	}

	docFreq := make(map[string]int)
	termFreqs := make([]map[string]int, len(corpus))
	var totalLength float64
	for i, result := range corpus {
		terms := tokenize(result.Chunk.Text)
		totalLength += float64(len(terms))
		termFreqs[i] = frequencies(terms)
		seen := map[string]bool{}
		for term := range termFreqs[i] {
			if !seen[term] {
				docFreq[term]++
				seen[term] = true
			}
		}
	}
	avgLength := totalLength / float64(len(corpus))
	if avgLength == 0 {
		avgLength = 1
	}

	results := make([]Result, 0, len(corpus))
	for i, result := range corpus {
		length := float64(len(tokenize(result.Chunk.Text)))
		score := 0.0
		for _, term := range queryTerms {
			tf := float64(termFreqs[i][term])
			if tf == 0 {
				continue
			}
			idf := math.Log(1 + (float64(len(corpus))-float64(docFreq[term])+0.5)/(float64(docFreq[term])+0.5))
			score += idf * (tf * (bm25K1 + 1)) / (tf + bm25K1*(1-bm25B+bm25B*(length/avgLength)))
		}
		if score > 0 {
			result.Score = score
			results = append(results, result)
		}
	}

	sortResults(results)
	if len(results) > limit {
		results = results[:limit]
	}
	for i := range results {
		results[i].Rank = i + 1
	}
	return results, nil
}

func frequencies(terms []string) map[string]int {
	out := make(map[string]int)
	for _, term := range terms {
		if term != "" {
			out[term]++
		}
	}
	return out
}

func uniqueTerms(terms []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, term := range terms {
		if term == "" || seen[term] {
			continue
		}
		seen[term] = true
		out = append(out, term)
	}
	return out
}

func sortResults(results []Result) {
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		if results[i].Document.ID != results[j].Document.ID {
			return results[i].Document.ID < results[j].Document.ID
		}
		if results[i].Chunk.ChunkIndex != results[j].Chunk.ChunkIndex {
			return results[i].Chunk.ChunkIndex < results[j].Chunk.ChunkIndex
		}
		return results[i].Chunk.ID < results[j].Chunk.ID
	})
}
