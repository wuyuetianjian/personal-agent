package model

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"agent/internal/privacy"
)

type OpenAICompatibleClient struct {
	Provider ProviderMetadata
	BaseURL  string
	APIKey   string
	Client   *http.Client
	Privacy  PrivacyGateway
}

func (c *OpenAICompatibleClient) Chat(ctx context.Context, request ChatRequest) (ChatResponse, error) {
	messages := make([]ChatMessage, len(request.Messages))
	copy(messages, request.Messages)
	if err := c.enforcePrivacy(messages); err != nil {
		return ChatResponse{}, err
	}

	payload := openAIChatRequest{
		Model:       request.Model.Model,
		Messages:    messages,
		Temperature: request.Temperature,
		MaxTokens:   request.MaxOutputTokens,
	}
	var response openAIChatResponse
	if err := c.post(ctx, "/chat/completions", payload, &response); err != nil {
		return ChatResponse{}, err
	}
	content := ""
	if len(response.Choices) > 0 {
		content = response.Choices[0].Message.Content
	}
	return ChatResponse{
		Content: content,
		Usage: Usage{
			InputTokens:  response.Usage.PromptTokens,
			OutputTokens: response.Usage.CompletionTokens,
		},
	}, nil
}

func (c *OpenAICompatibleClient) Embed(ctx context.Context, request EmbeddingRequest) (EmbeddingResponse, error) {
	input := make([]string, len(request.Input))
	copy(input, request.Input)
	if err := c.enforcePrivacyStrings(input); err != nil {
		return EmbeddingResponse{}, err
	}
	payload := openAIEmbeddingRequest{Model: request.Model.Model, Input: input}
	var response openAIEmbeddingResponse
	if err := c.post(ctx, "/embeddings", payload, &response); err != nil {
		return EmbeddingResponse{}, err
	}
	vectors := make([][]float64, len(response.Data))
	for _, item := range response.Data {
		if item.Index >= 0 && item.Index < len(vectors) {
			vectors[item.Index] = item.Embedding
		}
	}
	return EmbeddingResponse{Vectors: vectors, Usage: Usage{InputTokens: response.Usage.PromptTokens}}, nil
}

func (c *OpenAICompatibleClient) Rerank(ctx context.Context, request RerankRequest) (RerankResponse, error) {
	return RerankResponse{}, ErrUnsupportedOperation
}

func (c *OpenAICompatibleClient) enforcePrivacy(messages []ChatMessage) error {
	if !c.requiresPrivacyGateway() {
		return nil
	}
	if c.Privacy == nil {
		return ErrPrivacyGatewayRequired
	}
	for i := range messages {
		result, err := c.Privacy.TransformText(messages[i].Content)
		if err != nil {
			if err == privacy.ErrPayloadBlocked {
				return ErrPrivacyBlocked
			}
			return err
		}
		messages[i].Content = result.Text
	}
	return nil
}

func (c *OpenAICompatibleClient) enforcePrivacyStrings(values []string) error {
	if !c.requiresPrivacyGateway() {
		return nil
	}
	if c.Privacy == nil {
		return ErrPrivacyGatewayRequired
	}
	for i := range values {
		result, err := c.Privacy.TransformText(values[i])
		if err != nil {
			if err == privacy.ErrPayloadBlocked {
				return ErrPrivacyBlocked
			}
			return err
		}
		values[i] = result.Text
	}
	return nil
}

func (c *OpenAICompatibleClient) requiresPrivacyGateway() bool {
	return c.Provider.TrustLevel == TrustPublicRemote || c.Provider.RequirePrivacyGateway
}

func (c *OpenAICompatibleClient) post(ctx context.Context, path string, payload any, output any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	baseURL := strings.TrimRight(c.BaseURL, "/")
	if baseURL == "" {
		baseURL = strings.TrimRight(c.Provider.BaseURL, "/")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	client := c.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		limited, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("openai-compatible provider status %d: %s", resp.StatusCode, strings.TrimSpace(string(limited)))
	}
	return json.NewDecoder(resp.Body).Decode(output)
}

type openAIChatRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	Temperature float64       `json:"temperature,omitempty"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
}

type openAIChatResponse struct {
	Choices []struct {
		Message ChatMessage `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

type openAIEmbeddingRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type openAIEmbeddingResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float64 `json:"embedding"`
	} `json:"data"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
	} `json:"usage"`
}
