package model

import "context"

type StreamProvider interface {
	Stream(context.Context, TurnRequest, func(ModelEvent) error) (TurnResult, error)
}
type EventKind string

const (
	EventTextDelta EventKind = "text_delta"
	EventToolDelta EventKind = "tool_delta"
	EventUsage     EventKind = "usage"
	EventError     EventKind = "error"
	EventCompleted EventKind = "completed"
)

type ToolDefinition struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}
type ToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}
type ToolCall struct{ ID, Name, Arguments string }
type TurnRequest struct {
	ChatRequest
	Tools                []ToolDefinition
	MaxOutputBytes       int
	MaxToolArgumentBytes int
	// Probe-only strict transport completion and total wire-byte limits.
	RequireStreamDone bool
	MaxStreamBytes    int
}
type TurnResult struct {
	Content      string
	ToolCalls    []ToolCall
	Usage        *Usage
	FinishReason string
	// Fallback is true only for an explicitly selected non-streaming adapter.
	Fallback bool
}
type ModelEvent struct {
	Kind     EventKind
	Text     string
	ToolCall ToolCall
	Usage    *Usage
	Error    error
	Result   *TurnResult
}

// ChatStreamFallback explicitly adapts Chat without manufacturing incremental events.
type ChatStreamFallback struct{ Provider ChatProvider }

func (p ChatStreamFallback) Stream(ctx context.Context, r TurnRequest, emit func(ModelEvent) error) (TurnResult, error) {
	if len(r.Tools) > 0 {
		return TurnResult{}, ErrUnsupportedOperation
	}
	v, err := p.Provider.Chat(ctx, r.ChatRequest)
	if err != nil {
		return TurnResult{}, err
	}
	// Legacy ChatResponse does not attest usage presence, including nonzero values.
	// Keep usage unknown rather than inventing a presence guarantee.
	result := TurnResult{Content: v.Content, Fallback: true}
	if emit != nil {
		err = emit(ModelEvent{Kind: EventCompleted, Result: &result})
	}
	return result, err
}

// ResponsesStreamProvider reserves the Responses adapter contract without enabling it.
type ResponsesStreamProvider interface{ StreamProvider }
