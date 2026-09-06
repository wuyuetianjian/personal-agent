package runtime

import (
	"context"
	"errors"
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

type staticChatProvider string

func (p staticChatProvider) Chat(ctx context.Context, request model.ChatRequest) (model.ChatResponse, error) {
	return model.ChatResponse{Content: string(p), Usage: model.Usage{InputTokens: 3, OutputTokens: 5}}, ctx.Err()
}
