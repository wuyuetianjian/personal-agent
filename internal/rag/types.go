package rag

import "time"

const DefaultPrivacyClass = "local_private"

type Document struct {
	ID           string
	SourceURI    string
	Title        string
	Text         string
	ContentHash  string
	Metadata     map[string]string
	PrivacyClass string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type Chunk struct {
	ID          string
	DocumentID  string
	ChunkIndex  int
	Text        string
	TokenCount  int
	ContentHash string
	Metadata    map[string]string
	CreatedAt   time.Time
}

type Result struct {
	Chunk      Chunk
	Document   Document
	Score      float64
	Rank       int
	EvidenceID string
}

type RankedList []Result

type Reranker interface {
	Rerank(query string, results []Result) ([]Result, error)
}

type Indexer interface {
	Index(document Document) ([]Chunk, error)
}
