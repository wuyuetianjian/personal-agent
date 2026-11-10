package model

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

// ToolProbeResult attests only one bounded synthetic tool-call exchange, not
// general compatibility, text streaming, usage reporting or tool execution.
type ToolProbeResult struct {
	Verified bool
	Reason   string
}

// ProbeToolCapability must only be called by an explicit user-invoked operation.
// It sends no caller messages/files, performs no tool execution and never retries.
func ProbeToolCapability(ctx context.Context, p StreamProvider, m ModelMetadata) ToolProbeResult {
	fail := func(reason string) ToolProbeResult { return ToolProbeResult{Reason: reason} }
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return fail("nonce_unavailable")
	}
	nonce := hex.EncodeToString(random[:])
	req := TurnRequest{
		ChatRequest: ChatRequest{
			Model:           m,
			Messages:        []ChatMessage{{Role: "user", Content: "Call synthetic_probe exactly once with nonce: " + nonce}},
			MaxOutputTokens: 256,
		},
		Tools: []ToolDefinition{{Type: "function", Function: ToolFunction{
			Name: "synthetic_probe", Description: "Harmless synthetic echo; never executed.",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{"nonce": map[string]any{"type": "string", "enum": []string{nonce}}},
				"required":   []string{"nonce"}, "additionalProperties": false,
			},
		}}},
		MaxOutputBytes: 4096, MaxToolArgumentBytes: 1024,
		MaxStreamBytes: 64 << 10, RequireStreamDone: true,
	}

	completed := 0
	result, err := p.Stream(ctx, req, func(e ModelEvent) error {
		if e.Kind == EventCompleted {
			completed++
		}
		return nil
	})
	if ctx.Err() != nil {
		return fail("timeout_or_cancelled")
	}
	if err != nil {
		var rejected *StreamRejectionError
		if errors.As(err, &rejected) {
			return fail(rejected.Reason)
		}
		if errors.Is(err, ErrInvalidToolArguments) {
			return fail("invalid_arguments")
		}
		return fail("invalid_or_incomplete_stream")
	}
	if completed != 1 || result.Fallback || result.FinishReason != "tool_calls" || len(result.ToolCalls) != 1 {
		return fail("expected_tool_call_missing")
	}
	call := result.ToolCalls[0]
	if call.ID == "" || call.Name != "synthetic_probe" || !utf8.ValidString(call.Arguments) || len(call.Arguments) > 1024 {
		return fail("invalid_tool_call")
	}
	// Strict single-property object rejects duplicate keys, extra properties,
	// wrong types, trailing JSON and non-exact nonces without storing any output.
	d := json.NewDecoder(strings.NewReader(call.Arguments))
	tok, e := d.Token()
	if e != nil || tok != json.Delim('{') {
		return fail("invalid_arguments")
	}
	tok, e = d.Token()
	if e != nil || tok != "nonce" {
		return fail("invalid_arguments")
	}
	var got string
	if d.Decode(&got) != nil || got != nonce || d.More() {
		return fail("invalid_arguments")
	}
	tok, e = d.Token()
	if e != nil || tok != json.Delim('}') || d.Decode(new(any)) != io.EOF {
		return fail("invalid_arguments")
	}
	return ToolProbeResult{Verified: true, Reason: "synthetic_tool_call_verified"}
}

// StreamRejectionError deliberately contains only allowlisted diagnostics. The
// original provider body, credentials and arbitrary exception text are discarded.
type StreamRejectionError struct {
	Status int
	Reason string
}

func (e *StreamRejectionError) Error() string { return e.Reason }

func probeRejectionReason(status int, body []byte) string {
	reason := "provider_http_rejected"
	if status != 400 || !utf8.Valid(body) {
		return reason
	}
	var envelope struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return reason
	}
	message := strings.ToLower(envelope.Error.Message)
	if strings.Contains(message, "enable-auto-tool-choice") || strings.Contains(message, "auto") && (strings.Contains(message, "tool choice") || strings.Contains(message, "tool_choice")) {
		return "provider_auto_tool_choice_rejected"
	}
	if strings.Contains(message, "system") && strings.Contains(message, "message") && (strings.Contains(message, "beginning") || strings.Contains(message, "first") || strings.Contains(message, "start")) {
		return "provider_message_order_rejected"
	}
	return reason
}
