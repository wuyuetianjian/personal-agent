package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"

	"agent/internal/model"
)

func TestBoundedModelPlannerRejectsUnknownCapability(t *testing.T) {
	db := openRuntimeTestDB(t)
	rt := NewLocal(configForRuntimeTest(), db, nil)
	planner := BoundedModelPlanner{
		Provider:     staticChatProvider(`{"nodes":[{"id":"bad","capability_id":"missing.tool","role":"tool","dependencies":[]}]}`),
		Capabilities: rt.Capabilities,
	}
	_, _, err := planner.Plan(context.Background(), RunRequest{TaskID: "task-1", Input: "do work"})
	if !errors.Is(err, ErrWorkflowCapabilityUnknown) {
		t.Fatalf("Plan() error = %v, want ErrWorkflowCapabilityUnknown", err)
	}
}

func TestBoundedModelPlannerRejectsDisabledCapability(t *testing.T) {
	db := openRuntimeTestDB(t)
	rt := NewLocal(configForRuntimeTest(), db, nil)
	planner := BoundedModelPlanner{
		Provider:     staticChatProvider(`{"nodes":[{"id":"browser","capability_id":"browser.read","role":"browser","dependencies":[]}]}`),
		Capabilities: rt.Capabilities,
	}
	_, _, err := planner.Plan(context.Background(), RunRequest{TaskID: "task-1", Input: "read browser"})
	if !errors.Is(err, ErrWorkflowCapabilityDenied) {
		t.Fatalf("Plan() error = %v, want ErrWorkflowCapabilityDenied", err)
	}
}

func TestBoundedModelPlannerAcceptsBoundedDAG(t *testing.T) {
	db := openRuntimeTestDB(t)
	rt := NewLocal(configForRuntimeTest(), db, nil)
	planner := BoundedModelPlanner{
		Provider:     staticChatProvider(`{"nodes":[{"id":"memory","capability_id":"memory.search","role":"memory","dependencies":[]},{"id":"verify","capability_id":"verification.verify","role":"verification","dependencies":["memory"]}]}`),
		Capabilities: rt.Capabilities,
	}
	nodes, usage, err := planner.Plan(context.Background(), RunRequest{TaskID: "task-1", Input: "do work"})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if len(nodes) != 2 || nodes[1].Dependencies[0] != "memory" {
		t.Fatalf("nodes=%#v", nodes)
	}
	if usage.InputTokens != 3 {
		t.Fatalf("usage=%#v", usage)
	}
}

func TestBoundedModelPlannerRejectsCycle(t *testing.T) {
	db := openRuntimeTestDB(t)
	rt := NewLocal(configForRuntimeTest(), db, nil)
	planner := BoundedModelPlanner{
		Provider:     staticChatProvider(`{"nodes":[{"id":"a","capability_id":"memory.search","role":"memory","dependencies":["b"]},{"id":"b","capability_id":"rag.search","role":"retrieval","dependencies":["a"]}]}`),
		Capabilities: rt.Capabilities,
	}
	_, _, err := planner.Plan(context.Background(), RunRequest{TaskID: "task-1", Input: "do work"})
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("Plan() error = %v, want cycle rejection", err)
	}
}

func TestBoundedModelPlannerRejectsNodeCountOverLimit(t *testing.T) {
	db := openRuntimeTestDB(t)
	rt := NewLocal(configForRuntimeTest(), db, nil)
	planner := BoundedModelPlanner{
		Provider:     staticChatProvider(`{"nodes":[{"id":"one","capability_id":"memory.search","role":"memory","dependencies":[]},{"id":"two","capability_id":"rag.search","role":"retrieval","dependencies":[]}]}`),
		Capabilities: rt.Capabilities,
		MaxNodes:     1,
	}
	_, _, err := planner.Plan(context.Background(), RunRequest{TaskID: "task-1", Input: "do work"})
	if err == nil || !strings.Contains(err.Error(), "outside limit 1") {
		t.Fatalf("Plan() error = %v, want node limit rejection", err)
	}
}

func TestBoundedModelPlannerReturnsUnsupportedWhenProviderUnavailable(t *testing.T) {
	planner := BoundedModelPlanner{}
	_, _, err := planner.Plan(context.Background(), RunRequest{TaskID: "task-1", Input: "do work"})
	if !errors.Is(err, model.ErrUnsupportedOperation) {
		t.Fatalf("Plan() error = %v, want ErrUnsupportedOperation", err)
	}
}

func TestBoundedModelPlannerUsesConfiguredOutputLimitAndBudgetPrompt(t *testing.T) {
	db := openRuntimeTestDB(t)
	rt := NewLocal(configForRuntimeTest(), db, nil)
	provider := &captureChatProvider{content: `{"nodes":[{"id":"memory","capability_id":"memory.search","role":"memory","dependencies":[]}]}`}
	planner := BoundedModelPlanner{
		Provider:        provider,
		Capabilities:    rt.Capabilities,
		MaxOutputTokens: 256,
	}
	_, _, err := planner.Plan(context.Background(), RunRequest{
		TaskID:       "task-1",
		Input:        "do work",
		PrivacyClass: "confidential",
		MaxCostUSD:   0.25,
	})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if provider.request.MaxOutputTokens != 256 {
		t.Fatalf("MaxOutputTokens = %d, want configured fallback", provider.request.MaxOutputTokens)
	}
	if len(provider.request.Messages) != 2 ||
		!strings.Contains(provider.request.Messages[0].Content, "memory.search") ||
		!strings.Contains(provider.request.Messages[1].Content, "max_cost_usd: 0.250000") ||
		!strings.Contains(provider.request.Messages[1].Content, "privacy_class: confidential") {
		t.Fatalf("planner messages = %#v", provider.request.Messages)
	}
}

type staticChatProvider string

func (p staticChatProvider) Chat(ctx context.Context, request model.ChatRequest) (model.ChatResponse, error) {
	return model.ChatResponse{Content: string(p), Usage: model.Usage{InputTokens: 3, OutputTokens: 5}}, ctx.Err()
}

type captureChatProvider struct {
	content string
	request model.ChatRequest
}

func (p *captureChatProvider) Chat(ctx context.Context, request model.ChatRequest) (model.ChatResponse, error) {
	p.request = request
	return model.ChatResponse{Content: p.content, Usage: model.Usage{InputTokens: 3, OutputTokens: 5}}, ctx.Err()
}
