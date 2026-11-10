package model

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSyntheticToolProbe(t *testing.T) {
	for _, mode := range []string{"valid", "valid_fragments", "text", "wrong_nonce", "malformed", "extra", "duplicate", "wrong_finish", "no_finish", "no_done", "no_done_delimiter", "double_finish", "after_finish", "invalid_utf8", "limit", "wire_limit", "text_limit", "timeout", "protocol", "tool_choice_400", "message_order_400"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Messages  []ChatMessage
					Tools     []ToolDefinition
					Stream    bool
					MaxTokens int `json:"max_tokens"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
					return
				}
				if !req.Stream || len(req.Tools) != 1 || len(req.Messages) != 1 || req.MaxTokens != 256 {
					t.Error("not bounded synthetic request")
					return
				}
				nonce := strings.TrimPrefix(req.Messages[0].Content, "Call synthetic_probe exactly once with nonce: ")
				if len(nonce) != 64 {
					t.Errorf("nonce %q", nonce)
				}
				if mode == "timeout" {
					<-r.Context().Done()
					return
				}
				if mode == "tool_choice_400" || mode == "message_order_400" {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(400)
					message := `"auto" tool choice requires --enable-auto-tool-choice and --tool-call-parser; private-provider-diagnostic`
					if mode == "message_order_400" {
						message = "System message must be at the beginning of the conversation; private-provider-diagnostic"
					}
					json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": message}})
					return
				}
				if mode == "protocol" {
					fmt.Fprint(w, "not SSE")
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				if mode == "wire_limit" {
					fmt.Fprint(w, strings.Repeat(": padding\n", 8000))
				}
				if mode == "invalid_utf8" {
					fmt.Fprintf(w, "data: %s\n\n", []byte{255})
					return
				}
				args := fmt.Sprintf(`{"nonce":%q}`, nonce)
				switch mode {
				case "wrong_nonce":
					args = `{"nonce":"wrong"}`
				case "malformed":
					args = `{"nonce":`
				case "extra":
					args = fmt.Sprintf(`{"nonce":%q,"x":1}`, nonce)
				case "duplicate":
					args = fmt.Sprintf(`{"nonce":%q,"nonce":%q}`, nonce, nonce)
				case "limit":
					args = strings.Repeat(" ", 1500) + args
				}
				send := func(delta any, finish any) {
					raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
					fmt.Fprintf(w, "data: %s\n\n", raw)
				}
				if mode == "text_limit" {
					send(map[string]any{"content": strings.Repeat("x", 5000)}, nil)
				}
				if mode == "text" {
					send(map[string]any{"content": "I support tools"}, "stop")
				} else {
					if mode == "valid_fragments" {
						send(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "call_probe", "function": map[string]any{"name": "synthetic_", "arguments": args[:len(args)/2]}}}}, nil)
						send(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "function": map[string]any{"name": "probe", "arguments": args[len(args)/2:]}}}}, nil)
					} else {
						send(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "call_probe", "function": map[string]any{"name": "synthetic_probe", "arguments": args}}}}, nil)
					}
					if mode != "no_finish" {
						finish := "tool_calls"
						if mode == "wrong_finish" {
							finish = "stop"
						}
						send(map[string]any{}, finish)
					}
					if mode == "double_finish" {
						send(map[string]any{}, "tool_calls")
					}
					if mode == "after_finish" {
						send(map[string]any{"content": "late"}, nil)
					}
				}
				if mode == "no_done_delimiter" {
					fmt.Fprint(w, "data: [DONE]")
				} else if mode != "no_done" {
					fmt.Fprint(w, "data: [DONE]\n\n")
				}
			}))
			defer server.Close()
			p := &OpenAIStreamProvider{Client: &OpenAICompatibleClient{BaseURL: server.URL, Client: server.Client()}}
			timeout := 2 * time.Second
			if mode == "timeout" {
				timeout = 100 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			result := ProbeToolCapability(ctx, p, ModelMetadata{Model: "fixture"})
			if result.Verified != (mode == "valid" || mode == "valid_fragments") {
				t.Fatalf("mode %s: %+v", mode, result)
			}
			expectedReasons := map[string]string{"tool_choice_400": "provider_auto_tool_choice_rejected", "message_order_400": "provider_message_order_rejected", "wrong_nonce": "invalid_arguments", "malformed": "invalid_arguments"}
			if want := expectedReasons[mode]; want != "" && result.Reason != want {
				t.Fatalf("safe diagnosis: got %q want %q", result.Reason, want)
			}
			if strings.Contains(result.Reason, "private-provider-diagnostic") {
				t.Fatal("raw provider error leaked")
			}
			if mode != "timeout" && result.Reason == "timeout_or_cancelled" {
				t.Fatal("fixture unexpectedly timed out")
			}
			if result.Reason == "" {
				t.Fatal("no safe reason")
			}
		})
	}
}
