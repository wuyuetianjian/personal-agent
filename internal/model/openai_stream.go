package model

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"unicode/utf8"
)

var ErrIncompleteStream = errors.New("model stream ended without finish")
var ErrInvalidToolArguments = errors.New("invalid tool arguments")
var ErrStreamLimit = errors.New("model stream size limit exceeded")

// OpenAIStreamProvider is independent of the legacy Chat entry point. No automatic retries.
type OpenAIStreamProvider struct{ Client *OpenAICompatibleClient }

func (p *OpenAIStreamProvider) Stream(ctx context.Context, r TurnRequest, emit func(ModelEvent) error) (result TurnResult, err error) {
	callbackStopped := false
	send := func(e ModelEvent) error {
		if emit != nil && !callbackStopped {
			e := emit(e)
			if e != nil {
				callbackStopped = true
			}
			return e
		}
		return nil
	}
	defer func() {
		if err != nil {
			_ = send(ModelEvent{Kind: EventError, Error: err})
		}
	}()
	if p.Client == nil {
		return result, errors.New("missing model client")
	}
	c := p.Client
	messages := append([]ChatMessage(nil), r.Messages...)
	if err = c.enforcePrivacy(messages); err != nil {
		return
	}
	// Decode only to normalize JSON-compatible schema containers; the gateway sees
	// decoded original strings (including real line breaks), never escaped JSON.
	tools := r.Tools
	if c.requiresPrivacyGateway() && len(tools) > 0 {
		raw, e := json.Marshal(tools)
		if e != nil {
			return result, e
		}
		var original any
		if e = json.Unmarshal(raw, &original); e != nil {
			return result, e
		}
		transformed, e := transformToolStrings(c, original)
		if e != nil {
			return result, e
		}
		raw, e = json.Marshal(transformed)
		if e != nil {
			return result, e
		}
		var sanitized []ToolDefinition
		if e = json.Unmarshal(raw, &sanitized); e != nil {
			return result, e
		}
		tools = sanitized
	}
	payload := struct {
		openAIChatRequest
		Stream        bool             `json:"stream"`
		StreamOptions map[string]bool  `json:"stream_options"`
		Tools         []ToolDefinition `json:"tools,omitempty"`
	}{openAIChatRequest{r.Model.Model, messages, r.Temperature, r.MaxOutputTokens}, true, map[string]bool{"include_usage": true}, tools}
	body, e := json.Marshal(payload)
	if e != nil {
		return result, e
	}
	base := c.BaseURL
	if base == "" {
		base = c.Provider.BaseURL
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+"/chat/completions", bytes.NewReader(body))
	if e != nil {
		return result, e
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	client := c.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, e := client.Do(req)
	if e != nil {
		return result, e
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if r.RequireStreamDone {
			// Synthetic probe diagnostics only: cap the error body and never expose
			// arbitrary upstream content. The request context still bounds this read.
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 8193))
			reason := "provider_http_rejected"
			if len(body) <= 8192 {
				reason = probeRejectionReason(resp.StatusCode, body)
			}
			return result, &StreamRejectionError{Status: resp.StatusCode, Reason: reason}
		}
		return result, fmt.Errorf("stream provider status %d", resp.StatusCode)
	}
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		return result, errors.New("provider did not return SSE")
	}
	outputLimit := r.MaxOutputBytes
	if outputLimit <= 0 {
		outputLimit = 4 << 20
	}
	argumentLimit := r.MaxToolArgumentBytes
	if argumentLimit <= 0 {
		argumentLimit = 1 << 20
	}
	var reader io.Reader = resp.Body
	var bounded *io.LimitedReader
	if r.MaxStreamBytes > 0 {
		bounded = &io.LimitedReader{R: resp.Body, N: int64(r.MaxStreamBytes) + 1}
		reader = bounded
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 8<<20)
	calls := map[int]*ToolCall{}
	ids := map[string]int{}
	argumentBytes := 0
	finished := false
	done := false
	var data []string
	dataBytes := 0
	dispatch := func() error {
		if len(data) == 0 {
			return nil
		}
		raw := strings.Join(data, "\n")
		data = nil
		dataBytes = 0
		if raw == "[DONE]" {
			done = true
			return nil
		}
		if !utf8.ValidString(raw) {
			return errors.New("invalid UTF-8 stream")
		}
		var chunk struct {
			Error   json.RawMessage `json:"error"`
			Choices []struct {
				Index int `json:"index"`
				Delta struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct {
				Prompt     int `json:"prompt_tokens"`
				Completion int `json:"completion_tokens"`
			} `json:"usage"`
		}
		if e := json.Unmarshal([]byte(raw), &chunk); e != nil {
			return e
		}
		if len(chunk.Error) > 0 && string(chunk.Error) != "null" {
			return errors.New("provider reported stream error")
		}
		for _, choice := range chunk.Choices {
			if choice.Index != 0 {
				return errors.New("multiple stream choices unsupported")
			}
			if finished && (choice.Delta.Content != "" || len(choice.Delta.ToolCalls) > 0) {
				return errors.New("delta after finish")
			}
			if text := choice.Delta.Content; text != "" {
				if len(result.Content)+len(text) > outputLimit {
					return ErrStreamLimit
				}
				result.Content += text
				if e := send(ModelEvent{Kind: EventTextDelta, Text: text}); e != nil {
					return e
				}
			}
			for _, delta := range choice.Delta.ToolCalls {
				call := calls[delta.Index]
				if call == nil {
					call = &ToolCall{}
					calls[delta.Index] = call
				}
				if delta.ID != "" {
					if call.ID != "" && call.ID != delta.ID {
						return errors.New("tool call ID changed")
					}
					if index, ok := ids[delta.ID]; ok && index != delta.Index {
						return errors.New("duplicate tool call ID")
					}
					call.ID = delta.ID
					ids[delta.ID] = delta.Index
				}
				argumentBytes += len(delta.Function.Arguments)
				if argumentBytes > argumentLimit || len(calls) > 128 {
					return ErrStreamLimit
				}
				call.Name += delta.Function.Name
				call.Arguments += delta.Function.Arguments
				if len(call.Name) > 4096 || len(call.ID) > 4096 {
					return ErrStreamLimit
				}
				if e := send(ModelEvent{Kind: EventToolDelta, ToolCall: ToolCall{call.ID, delta.Function.Name, delta.Function.Arguments}}); e != nil {
					return e
				}
			}
			if choice.FinishReason != nil && *choice.FinishReason != "" {
				if r.RequireStreamDone && finished {
					return errors.New("duplicate finish")
				}
				finished = true
				result.FinishReason = *choice.FinishReason
			}
		}
		if chunk.Usage != nil {
			result.Usage = &Usage{InputTokens: chunk.Usage.Prompt, OutputTokens: chunk.Usage.Completion}
			if e := send(ModelEvent{Kind: EventUsage, Usage: result.Usage}); e != nil {
				return e
			}
		}
		return nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if e = dispatch(); e != nil {
				return result, e
			}
			if done {
				break
			}
		} else if strings.HasPrefix(line, "data:") {
			dataBytes += len(line)
			if dataBytes > 8<<20 {
				return result, ErrStreamLimit
			}
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if e = scanner.Err(); e != nil {
		return result, e
	}
	if e = ctx.Err(); e != nil {
		return result, e
	}
	if len(data) > 0 {
		if r.RequireStreamDone {
			return result, ErrIncompleteStream
		}
		if e = dispatch(); e != nil {
			return result, e
		}
	}
	if bounded != nil && bounded.N <= 0 {
		return result, ErrStreamLimit
	}
	if !finished || r.RequireStreamDone && !done {
		return result, ErrIncompleteStream
	}
	indexes := make([]int, 0, len(calls))
	for index := range calls {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	for _, index := range indexes {
		call := calls[index]
		if call.ID == "" || call.Name == "" {
			return result, errors.New("incomplete tool call")
		}
		if !json.Valid([]byte(call.Arguments)) {
			return result, ErrInvalidToolArguments
		}
		result.ToolCalls = append(result.ToolCalls, *call)
	}
	err = send(ModelEvent{Kind: EventCompleted, Result: &result})
	return
}

var _ StreamProvider = (*OpenAIStreamProvider)(nil)

// transformToolStrings preserves JSON container shape and checks both string
// values and object keys before any remote transmission. Colliding transformed
// keys fail closed rather than silently dropping schema entries.
func transformToolStrings(c *OpenAICompatibleClient, value any) (any, error) {
	switch v := value.(type) {
	case string:
		values := []string{v}
		if err := c.enforcePrivacyStrings(values); err != nil {
			return nil, err
		}
		return values[0], nil
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			transformed, err := transformToolStrings(c, item)
			if err != nil {
				return nil, err
			}
			out[i] = transformed
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			transformedKey, err := transformToolStrings(c, key)
			if err != nil {
				return nil, err
			}
			k := transformedKey.(string)
			if _, exists := out[k]; exists {
				return nil, errors.New("privacy transformation collides tool schema keys")
			}
			transformed, err := transformToolStrings(c, item)
			if err != nil {
				return nil, err
			}
			out[k] = transformed
		}
		return out, nil
	default:
		return value, nil
	}
}
