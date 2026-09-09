package model

import (
	"context"
	"errors"

	"agent/internal/privacy"
)

var (
	ErrPrivacyGatewayRequired = errors.New("privacy gateway is required for public remote provider")
	ErrPrivacyBlocked         = errors.New("privacy gateway blocked provider payload")
	ErrUnsupportedOperation   = errors.New("provider operation is unsupported")
)

type Usage struct {
	InputTokens  int
	OutputTokens int
}

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatRequest struct {
	Model           ModelMetadata
	Messages        []ChatMessage
	Temperature     float64
	MaxOutputTokens int
}

type ChatResponse struct {
	Content string
	Usage   Usage
}

type EmbeddingRequest struct {
	Model ModelMetadata
	Input []string
}

type EmbeddingResponse struct {
	Vectors [][]float64
	Usage   Usage
}

type RerankRequest struct {
	Model     ModelMetadata
	Query     string
	Documents []string
}

type RerankResult struct {
	Index int
	Score float64
}

type RerankResponse struct {
	Results []RerankResult
	Usage   Usage
}

type ChatProvider interface {
	Chat(ctx context.Context, request ChatRequest) (ChatResponse, error)
}

type EmbeddingProvider interface {
	Embed(ctx context.Context, request EmbeddingRequest) (EmbeddingResponse, error)
}

type RerankProvider interface {
	Rerank(ctx context.Context, request RerankRequest) (RerankResponse, error)
}

type PrivacyGateway interface {
	TransformText(input string) (privacy.Result, error)
}
