package runtime

import (
	"context"
	"errors"

	"agent/internal/model"
)

var ErrPublicEscalationUnavailable = errors.New("public escalation unavailable")

type PublicEscalator struct {
	Provider model.ChatProvider
	Model    model.ModelMetadata
}

func (e PublicEscalator) Escalate(ctx context.Context, input string, evidence []Evidence) (string, model.Usage, error) {
	if e.Provider == nil {
		return "", model.Usage{}, ErrPublicEscalationUnavailable
	}
	response, err := e.Provider.Chat(ctx, model.ChatRequest{
		Model: e.Model,
		Messages: []model.ChatMessage{
			{Role: "system", Content: "Answer using only the provided local evidence summary. If insufficient, say so."},
			{Role: "user", Content: minimalEscalationContext(input, evidence)},
		},
	})
	return response.Content, response.Usage, err
}

func minimalEscalationContext(input string, evidence []Evidence) string {
	text := "task: " + input + "\nlocal_evidence:"
	for i, item := range evidence {
		if i >= 3 {
			break
		}
		text += "\n- " + item.Content
	}
	return text
}
