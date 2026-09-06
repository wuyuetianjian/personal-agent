package workflow

import (
	"agent/internal/storage"
	"context"
	"path/filepath"
	"testing"
)

func TestCheckpointAndRecoveryDoNotReplayCompletedNodes(t *testing.T) {
	db, err := storage.OpenSQLite(context.Background(), filepath.Join(t.TempDir(), "workflow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.Migrate(context.Background(), db.SQL); err != nil {
		t.Fatal(err)
	}
	s := Store{DB: db.SQL}
	run := Run{ID: "wf-1", TaskID: "task-1", Status: StatusRunning}
	nodes := []Node{{WorkflowID: "wf-1", NodeID: "one", Status: NodeRunning}, {WorkflowID: "wf-1", NodeID: "two", Status: NodeRunning, SideEffect: true}}
	if err := s.Create(context.Background(), run, nodes); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveCheckpoint(context.Background(), Checkpoint{WorkflowID: "wf-1", NodeID: "one", Status: NodeCompleted, ResultRef: "result-1"}); err != nil {
		t.Fatal(err)
	}
	remaining, err := s.Recover(context.Background(), "wf-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || remaining[0].NodeID != "two" {
		t.Fatalf("remaining=%#v", remaining)
	}
}

func TestWorkflowPauseResumeCancel(t *testing.T) {
	db, _ := storage.OpenSQLite(context.Background(), filepath.Join(t.TempDir(), "workflow.db"))
	defer db.Close()
	if err := storage.Migrate(context.Background(), db.SQL); err != nil {
		t.Fatal(err)
	}
	s := Store{DB: db.SQL}
	if err := s.Create(context.Background(), Run{ID: "wf", Status: StatusPending}, nil); err != nil {
		t.Fatal(err)
	}
	for _, status := range []Status{StatusRunning, StatusPaused, StatusRunning, StatusCancelled} {
		if err := s.UpdateStatus(context.Background(), "wf", status); err != nil {
			t.Fatalf("status %s: %v", status, err)
		}
	}
}

func TestWorkflowNodeDependenciesPersistForRecovery(t *testing.T) {
	db, err := storage.OpenSQLite(context.Background(), filepath.Join(t.TempDir(), "workflow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.Migrate(context.Background(), db.SQL); err != nil {
		t.Fatal(err)
	}
	s := Store{DB: db.SQL}
	if err := s.Create(context.Background(), Run{ID: "wf-deps", TaskID: "task-deps", Status: StatusPending}, []Node{
		{WorkflowID: "wf-deps", NodeID: "memory", CapabilityID: "memory.search"},
		{WorkflowID: "wf-deps", NodeID: "verify", CapabilityID: "verification.verify", Dependencies: []string{"memory"}},
	}); err != nil {
		t.Fatal(err)
	}
	nodes, err := s.Recover(context.Background(), "wf-deps")
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 {
		t.Fatalf("nodes=%#v", nodes)
	}
	var verify Node
	for _, node := range nodes {
		if node.NodeID == "verify" {
			verify = node
		}
	}
	if len(verify.Dependencies) != 1 || verify.Dependencies[0] != "memory" || verify.DAGVersion != "v1" {
		t.Fatalf("verify node=%#v, want persisted dependency and dag version", verify)
	}
}
