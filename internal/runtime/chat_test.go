package runtime

import (
	"context"
	"testing"

	"agent/internal/config"
	"agent/internal/model"
)

func TestChatUsesConfiguredLocalProvider(t *testing.T) {
	rt := &Runtime{
		Config:       config.Config{Agent: config.AgentConfig{Leader: config.RoleModelConfig{Temperature: 0.2, MaxOutputTokens: 128}}},
		ChatProvider: staticChatResponse("本地模型回答"),
		ChatModel:    model.ModelMetadata{Model: "qwen3.8:27b-mlx"},
	}

	result, err := rt.Chat(context.Background(), "你好")
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if result.Answer != "本地模型回答" {
		t.Fatalf("answer = %q", result.Answer)
	}
}

type staticChatResponse string

func (p staticChatResponse) Chat(context.Context, model.ChatRequest) (model.ChatResponse, error) {
	return model.ChatResponse{Content: string(p), Usage: model.Usage{InputTokens: 3, OutputTokens: 4}}, nil
}
