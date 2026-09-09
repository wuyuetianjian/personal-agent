package model

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"agent/internal/privacy"
)

func TestPublicProviderRequiresPrivacyGateway(t *testing.T) {
	client := &OpenAICompatibleClient{
		Provider: ProviderMetadata{TrustLevel: TrustPublicRemote, RequirePrivacyGateway: true},
		BaseURL:  "https://example.invalid/v1",
		Client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			t.Fatal("transport was called before privacy enforcement")
			return nil, nil
		})},
	}
	_, err := client.Chat(context.Background(), ChatRequest{
		Model:    ModelMetadata{Model: "remote-model"},
		Messages: []ChatMessage{{Role: "user", Content: "hello"}},
	})
	if !errors.Is(err, ErrPrivacyGatewayRequired) {
		t.Fatalf("Chat() error = %v, want %v", err, ErrPrivacyGatewayRequired)
	}
}

func TestPublicProviderBlocksCredentialPayload(t *testing.T) {
	gateway, err := privacy.NewGateway("test-secret")
	if err != nil {
		t.Fatalf("NewGateway() error = %v", err)
	}
	client := &OpenAICompatibleClient{
		Provider: ProviderMetadata{TrustLevel: TrustPublicRemote, RequirePrivacyGateway: true},
		BaseURL:  "https://example.invalid/v1",
		Privacy:  gateway,
		Client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			t.Fatal("transport was called with blocked payload")
			return nil, nil
		})},
	}
	_, err = client.Chat(context.Background(), ChatRequest{
		Model:    ModelMetadata{Model: "remote-model"},
		Messages: []ChatMessage{{Role: "user", Content: "-----BEGIN PRIVATE KEY-----\nabc\n-----END PRIVATE KEY-----"}},
	})
	if !errors.Is(err, ErrPrivacyBlocked) {
		t.Fatalf("Chat() error = %v, want %v", err, ErrPrivacyBlocked)
	}
}

func TestPublicProviderSendsSanitizedChatPayload(t *testing.T) {
	gateway, err := privacy.NewGateway("test-secret")
	if err != nil {
		t.Fatalf("NewGateway() error = %v", err)
	}
	var body struct {
		Messages []ChatMessage `json:"messages"`
	}
	client := &OpenAICompatibleClient{
		Provider: ProviderMetadata{TrustLevel: TrustPublicRemote, RequirePrivacyGateway: true},
		BaseURL:  "https://example.invalid/v1",
		Privacy:  gateway,
		Client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/v1/chat/completions" {
				t.Fatalf("path = %s, want /v1/chat/completions", req.URL.Path)
			}
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatalf("Decode request body error = %v", err)
			}
			return jsonResponse(`{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":7,"completion_tokens":3}}`), nil
		})},
	}
	response, err := client.Chat(context.Background(), ChatRequest{
		Model:           ModelMetadata{Model: "remote-model"},
		Messages:        []ChatMessage{{Role: "user", Content: "email alice@example.com token=secret123"}},
		Temperature:     0.1,
		MaxOutputTokens: 12,
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if response.Content != "ok" || response.Usage.InputTokens != 7 || response.Usage.OutputTokens != 3 {
		t.Fatalf("Chat() response = %+v", response)
	}
	if len(body.Messages) != 1 {
		t.Fatalf("request messages = %+v", body.Messages)
	}
	if body.Messages[0].Role != "user" {
		t.Fatalf("request message role = %q, want user", body.Messages[0].Role)
	}
	content := body.Messages[0].Content
	for _, leaked := range []string{"alice@example.com", "secret123"} {
		if strings.Contains(content, leaked) {
			t.Fatalf("sanitized content leaked %q in %q", leaked, content)
		}
	}
}

func TestEmbeddingProviderUsesMockableTransport(t *testing.T) {
	client := &OpenAICompatibleClient{
		Provider: ProviderMetadata{TrustLevel: TrustLocalPrivate},
		BaseURL:  "https://example.invalid/v1",
		Client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/v1/embeddings" {
				t.Fatalf("path = %s, want /v1/embeddings", req.URL.Path)
			}
			return jsonResponse(`{"data":[{"index":0,"embedding":[0.1,0.2]}],"usage":{"prompt_tokens":2}}`), nil
		})},
	}
	response, err := client.Embed(context.Background(), EmbeddingRequest{
		Model: ModelMetadata{Model: "embedding-model"},
		Input: []string{"hello"},
	})
	if err != nil {
		t.Fatalf("Embed() error = %v", err)
	}
	if len(response.Vectors) != 1 || len(response.Vectors[0]) != 2 {
		t.Fatalf("Embed() response = %+v", response)
	}
}

func TestChatProviderHonorsContextCancellation(t *testing.T) {
	started := make(chan struct{})
	client := &OpenAICompatibleClient{
		Provider: ProviderMetadata{TrustLevel: TrustLocalPrivate},
		BaseURL:  "https://example.invalid/v1",
		Client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			close(started)
			<-req.Context().Done()
			return nil, req.Context().Err()
		})},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := client.Chat(ctx, ChatRequest{Model: ModelMetadata{Model: "local"}, Messages: []ChatMessage{{Role: "user", Content: "wait"}}})
		done <- err
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Chat() error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Chat() did not return after cancellation")
	}
}

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func jsonResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewBufferString(body)),
	}
}
