package runtime

import (
	"context"
	"errors"
	"strings"

	"agent/internal/model"
)

var ErrChatProviderUnavailable = errors.New("configured local chat provider unavailable")

func (r *Runtime) Chat(ctx context.Context, input string) (ChatResult, error) {
	if strings.TrimSpace(input) == "" {
		return ChatResult{}, errors.New("chat input is required")
	}
	if r.ChatProvider == nil {
		answer, _ := synthesizeLocalAnswer(input, nil)
		return ChatResult{Answer: answer}, nil
	}
	response, err := r.ChatProvider.Chat(ctx, model.ChatRequest{
		Model: r.ChatModel,
		Messages: []model.ChatMessage{
			{Role: "system", Content: "You are a local-first personal assistant. Answer the user directly and do not claim to have performed actions you did not perform."},
			{Role: "user", Content: input},
		},
		Temperature:     r.Config.Agent.Leader.Temperature,
		MaxOutputTokens: r.Config.Agent.Leader.MaxOutputTokens,
	})
	if err != nil {
		return ChatResult{}, err
	}
	if strings.TrimSpace(response.Content) == "" {
		return ChatResult{}, ErrChatProviderUnavailable
	}
	return ChatResult{Answer: response.Content, Usage: response.Usage}, nil
}
