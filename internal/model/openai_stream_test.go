package model

import (
	"agent/internal/privacy"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func streamFixture(t *testing.T, wire string) *OpenAIStreamProvider {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, b := range []byte(wire) {
			w.Write([]byte{b})
			w.(http.Flusher).Flush()
		}
	}))
	t.Cleanup(s.Close)
	return &OpenAIStreamProvider{Client: &OpenAICompatibleClient{BaseURL: s.URL}}
}
func TestStreamTextUTF8Usage(t *testing.T) {
	p := streamFixture(t, "data: {\"choices\":[{\"delta\":{\"content\":\"你好\"},\"finish_reason\":null}]}\r\n\r\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":3}}\n\ndata: [DONE]\n\n")
	var kinds []EventKind
	got, err := p.Stream(context.Background(), TurnRequest{}, func(e ModelEvent) error { kinds = append(kinds, e.Kind); return nil })
	if err != nil || got.Content != "你好" || got.Usage == nil || got.Usage.OutputTokens != 3 || kinds[len(kinds)-1] != EventCompleted {
		t.Fatalf("%+v %v %v", got, err, kinds)
	}
}
func TestStreamInterleavedTools(t *testing.T) {
	wire := "data: " + `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"a","function":{"name":"one","arguments":"{"}},{"index":1,"id":"b","function":{"name":"two","arguments":"{}"}}]}}]}` + "\n\n" +
		"data: " + `{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"}"}}]},"finish_reason":"tool_calls"}]}` + "\n\ndata: [DONE]\n\n"
	got, err := streamFixture(t, wire).Stream(context.Background(), TurnRequest{}, nil)
	if err != nil || len(got.ToolCalls) != 2 || got.ToolCalls[0].ID != "a" || got.ToolCalls[0].Arguments != "{}" {
		t.Fatalf("%+v %v", got, err)
	}
	if got.Usage != nil {
		t.Fatal("missing usage must remain unknown")
	}
}
func TestStreamFailures(t *testing.T) {
	for _, tc := range []struct {
		name, wire string
		req        TurnRequest
	}{
		{"missing_finish", "data: [DONE]\n\n", TurnRequest{}},
		{"provider", "data: {\"error\":{\"message\":\"broken\"}}\n\n", TurnRequest{}},
		{"output_limit", "data: {\"choices\":[{\"delta\":{\"content\":\"long\"}}]}\n\n", TurnRequest{MaxOutputBytes: 2}},
		{"argument_limit", "data: " + `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"a","function":{"arguments":"long"}}]}}]}` + "\n\n", TurnRequest{MaxToolArgumentBytes: 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := streamFixture(t, tc.wire).Stream(context.Background(), tc.req, nil)
			if err == nil {
				t.Fatal("expected error")
			}
		})
	}
}
func TestStreamCallbackAndCancellation(t *testing.T) {
	p := streamFixture(t, "data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n")
	sentinel := errors.New("stop")
	_, err := p.Stream(context.Background(), TurnRequest{}, func(ModelEvent) error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Fatalf("%v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = p.Stream(ctx, TurnRequest{}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
}
func TestStreamPrivacy(t *testing.T) {
	p := &OpenAIStreamProvider{Client: &OpenAICompatibleClient{Provider: ProviderMetadata{TrustLevel: TrustPublicRemote}}}
	_, err := p.Stream(context.Background(), TurnRequest{}, nil)
	if !errors.Is(err, ErrPrivacyGatewayRequired) {
		t.Fatalf("%v", err)
	}
}
func TestStreamCapabilities(t *testing.T) {
	c := Capabilities{}
	if c.Streaming != CapabilityUnknown || c.Tools != CapabilityUnknown {
		t.Fatal(c)
	}
	if c.Restrict(Capabilities{Streaming: CapabilitySupported}).Streaming != CapabilityUnknown {
		t.Fatal("configuration promoted unknown")
	}
	key := CapabilityCacheKey("http://localhost", "model", "configuration")
	if key == CapabilityCacheKey("http://localhost", "other", "configuration") || strings.Contains(key, "configuration") {
		t.Fatal(key)
	}
}
func TestStreamCallbackStopsDelivery(t *testing.T) {
	p := streamFixture(t, "data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n")
	calls := 0
	_, _ = p.Stream(context.Background(), TurnRequest{}, func(ModelEvent) error { calls++; return errors.New("stop") })
	if calls != 1 {
		t.Fatalf("callback called %d times after rejection", calls)
	}
}
func TestStreamFallbackFinalOnly(t *testing.T) {
	p := ChatStreamFallback{Provider: fixedStreamChat{}}
	count := 0
	result, err := p.Stream(context.Background(), TurnRequest{}, func(e ModelEvent) error {
		count++
		if e.Kind != EventCompleted {
			t.Fatal(e)
		}
		return nil
	})
	if result.Usage != nil {
		t.Fatal("fallback cannot attest legacy usage presence")
	}
	if err != nil || !result.Fallback || count != 1 {
		t.Fatalf("%+v %v %d", result, err, count)
	}
}

type fixedStreamChat struct{}

func (fixedStreamChat) Chat(context.Context, ChatRequest) (ChatResponse, error) {
	return ChatResponse{Content: "final"}, nil
}

func TestStreamCancellationDuringRead(t *testing.T) {
	started := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
	}))
	defer s.Close()
	p := &OpenAIStreamProvider{Client: &OpenAICompatibleClient{BaseURL: s.URL}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() { _, err := p.Stream(ctx, TurnRequest{}, nil); finished <- err }()
	<-started
	cancel()
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
}
func TestStreamPrivacyToolDefinitionsBlocked(t *testing.T) {
	p := &OpenAIStreamProvider{Client: &OpenAICompatibleClient{Provider: ProviderMetadata{TrustLevel: TrustPublicRemote}}}
	_, err := p.Stream(context.Background(), TurnRequest{Tools: []ToolDefinition{{Type: "function", Function: ToolFunction{Name: "test"}}}}, nil)
	if !errors.Is(err, ErrPrivacyGatewayRequired) {
		t.Fatalf("%v", err)
	}
}

func TestStreamToolPrivacyOriginalStrings(t *testing.T) {
	for _, location := range []string{"description", "nested_value", "nested_key"} {
		t.Run(location, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				w.Write([]byte("data: [DONE]\n\n"))
			}))
			defer server.Close()
			gateway, err := privacy.NewGateway("fixture-only")
			if err != nil {
				t.Fatal(err)
			}
			tool := ToolDefinition{Type: "function", Function: ToolFunction{Name: "safe", Parameters: map[string]any{"type": "object"}}}
			credential := "AWS_SECRET_ACCESS_KEY=example"
			switch location {
			case "description":
				tool.Function.Description = credential
			case "nested_value":
				tool.Function.Parameters["properties"] = map[string]any{"x": []any{map[string]any{"description": credential}}}
			case "nested_key":
				tool.Function.Parameters["properties"] = map[string]any{credential: map[string]any{"type": "string"}}
			}
			p := &OpenAIStreamProvider{Client: &OpenAICompatibleClient{BaseURL: server.URL, Provider: ProviderMetadata{TrustLevel: TrustPublicRemote}, Privacy: gateway}}
			_, err = p.Stream(context.Background(), TurnRequest{Tools: []ToolDefinition{tool}}, nil)
			if !errors.Is(err, ErrPrivacyBlocked) {
				t.Fatalf("expected block, got %v", err)
			}
			if requests.Load() != 0 {
				t.Fatal("credential payload reached HTTP server")
			}
		})
	}
}

func TestStreamToolPrivacyPreservesStructureAndInput(t *testing.T) {
	gateway, _ := privacy.NewGateway("fixture-only")
	schema := map[string]any{"properties": map[string]any{"person@example.com": map[string]any{"description": "contact person@example.com", "enum": []any{"safe", float64(7), true, nil}}}}
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
	}))
	defer server.Close()
	p := &OpenAIStreamProvider{Client: &OpenAICompatibleClient{BaseURL: server.URL, Provider: ProviderMetadata{TrustLevel: TrustPublicRemote}, Privacy: gateway}}
	_, err := p.Stream(context.Background(), TurnRequest{Tools: []ToolDefinition{{Type: "function", Function: ToolFunction{Name: "safe", Parameters: schema}}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(captured)
	if strings.Contains(string(raw), "person@example.com") {
		t.Fatal("original string leaked")
	}
	properties := captured["tools"].([]any)[0].(map[string]any)["function"].(map[string]any)["parameters"].(map[string]any)["properties"].(map[string]any)
	if len(properties) != 1 {
		t.Fatal(properties)
	}
	for _, value := range properties {
		values := value.(map[string]any)["enum"].([]any)
		if len(values) != 4 || values[1] != float64(7) || values[2] != true || values[3] != nil {
			t.Fatal(values)
		}
	}
	if _, exists := schema["properties"].(map[string]any)["person@example.com"]; !exists {
		t.Fatal("input mutated")
	}
}

func TestStreamTranscriptToolPrivacy(t *testing.T) {
	for _, field := range []string{"arguments", "result", "id"} {
		t.Run(field, func(t *testing.T) {
			var requests atomic.Int32
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
			defer s.Close()
			gateway, _ := privacy.NewGateway("fixture-only")
			p := &OpenAIStreamProvider{Client: &OpenAICompatibleClient{BaseURL: s.URL, Provider: ProviderMetadata{TrustLevel: TrustPublicRemote}, Privacy: gateway}}
			msg := ChatMessage{Role: "assistant", ToolCalls: []ChatToolCall{{ID: "c1", Type: "function", Function: ChatToolFunction{Name: "read", Arguments: `{}`}}}}
			secret := "-----BEGIN PRIVATE KEY-----\nabc\n-----END PRIVATE KEY-----"
			switch field {
			case "arguments":
				raw, _ := json.Marshal(map[string]string{"key": secret})
				msg.ToolCalls[0].Function.Arguments = string(raw)
			case "result":
				msg = ChatMessage{Role: "tool", Content: secret, ToolCallID: "c1"}
			case "id":
				msg.ToolCalls[0].ID = secret
			}
			_, err := p.Stream(context.Background(), TurnRequest{ChatRequest: ChatRequest{Messages: []ChatMessage{msg}}}, nil)
			if !errors.Is(err, ErrPrivacyBlocked) || requests.Load() != 0 {
				t.Fatalf("error %v requests %d", err, requests.Load())
			}
		})
	}
}

func TestStreamToolTranscriptWirePreservation(t *testing.T) {
	gateway, _ := privacy.NewGateway("fixture-only")
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		decoder := json.NewDecoder(r.Body)
		decoder.UseNumber()
		if err := decoder.Decode(&body); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
	}))
	defer server.Close()
	args := `{"n":9007199254740993,"email":"alice@example.com"}`
	messages := []ChatMessage{{Role: "assistant", ToolCalls: []ChatToolCall{{ID: "call-1", Type: "function", Function: ChatToolFunction{Name: "read", Arguments: args}}}}, {Role: "tool", ToolCallID: "call-1", Content: "safe result"}}
	p := &OpenAIStreamProvider{Client: &OpenAICompatibleClient{BaseURL: server.URL, Provider: ProviderMetadata{TrustLevel: TrustPublicRemote}, Privacy: gateway}}
	if _, err := p.Stream(context.Background(), TurnRequest{ChatRequest: ChatRequest{Messages: messages}}, nil); err != nil {
		t.Fatal(err)
	}
	if messages[0].ToolCalls[0].Function.Arguments != args {
		t.Fatal("mutated caller")
	}
	got := body["messages"].([]any)
	call := got[0].(map[string]any)["tool_calls"].([]any)[0].(map[string]any)
	f := call["function"].(map[string]any)
	if call["id"] != "call-1" || got[1].(map[string]any)["tool_call_id"] != "call-1" || !strings.Contains(f["arguments"].(string), "9007199254740993") {
		t.Fatalf("protocol or number changed: %+v", body)
	}
	if strings.Contains(f["arguments"].(string), "alice@example.com") {
		t.Fatal("argument privacy not applied")
	}
}
