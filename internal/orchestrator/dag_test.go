package orchestrator

import (
	"errors"
	"testing"

	"agent/internal/agent"
)

func TestDAGRejectsCycle(t *testing.T) {
	_, err := NewDAG([]agent.TaskNode{
		{ID: "a", Role: agent.RoleResearch, Dependencies: []string{"b"}},
		{ID: "b", Role: agent.RoleResearch, Dependencies: []string{"a"}},
	})
	if !errors.Is(err, ErrCycleDetected) {
		t.Fatalf("NewDAG() error = %v, want ErrCycleDetected", err)
	}
}

func TestDAGReadyReturnsDependencySatisfiedPendingNodes(t *testing.T) {
	dag, err := NewDAG([]agent.TaskNode{
		{ID: "a", Role: agent.RoleResearch},
		{ID: "b", Role: agent.RoleResearch, Dependencies: []string{"a"}},
		{ID: "c", Role: agent.RoleResearch, Dependencies: []string{"a"}},
		{ID: "d", Role: agent.RoleResearch, Dependencies: []string{"b"}},
	})
	if err != nil {
		t.Fatalf("NewDAG() error = %v", err)
	}

	ready := dag.Ready(map[string]bool{"a": true}, map[string]bool{"a": true})
	got := ids(ready)
	want := []string{"b", "c"}
	if len(got) != len(want) {
		t.Fatalf("ready = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ready = %v, want %v", got, want)
		}
	}
}

func ids(nodes []agent.TaskNode) []string {
	out := make([]string, len(nodes))
	for i, node := range nodes {
		out[i] = node.ID
	}
	return out
}
