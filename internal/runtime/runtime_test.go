package runtime

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"agent/internal/config"
	"agent/internal/orchestrator"
	"agent/internal/project"
	"agent/internal/rag"
	"agent/internal/storage"
	"agent/internal/workflow"
)

func TestRunUsesLocalRAGWithoutPublicCalls(t *testing.T) {
	ctx := context.Background()
	db := openRuntimeTestDB(t)
	cfg := config.Config{}
	cfg.Agent.Leader.ModelID = "local-planner"
	events := &orchestrator.InMemoryEvidenceBus{}
	rt := NewLocal(cfg, db, events)

	ragStore := rag.NewSQLiteStore(db.SQL, rag.Chunker{MaxTokens: 32})
	if _, err := ragStore.Index(ctx, rag.Document{
		ID:           "doc-nfs",
		SourceURI:    "local://notes/nfs",
		Title:        "NFS performance incident",
		Text:         "NFS latency was resolved by increasing server thread count and checking mount options.",
		PrivacyClass: "local_private",
	}); err != nil {
		t.Fatalf("Index() error = %v", err)
	}
	if err := db.CreateTask(ctx, storage.Task{
		ID:            "task-1",
		Title:         "nfs",
		Input:         "How was NFS latency resolved?",
		Status:        "running",
		LeaderModelID: "local-planner",
		PrivacyClass:  "local_private",
	}); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}

	result, err := rt.Run(ctx, RunRequest{
		TaskID:        "task-1",
		Input:         "How was NFS latency resolved?",
		PrivacyClass:  "local_private",
		LeaderModelID: "local-planner",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Usage.RemoteTokens != 0 || result.RemoteCalls != 0 {
		t.Fatalf("remote usage = tokens %d calls %d, want zero", result.Usage.RemoteTokens, result.RemoteCalls)
	}
	if !strings.Contains(result.Answer, "NFS latency was resolved") {
		t.Fatalf("answer = %q, want local evidence summary", result.Answer)
	}
	if len(result.EvidenceIDs) == 0 {
		t.Fatal("Run() returned no evidence IDs")
	}
	if !result.Verification.PassesPolicy {
		t.Fatalf("verification = %+v, want pass", result.Verification)
	}
	task, err := db.GetTask(ctx, "task-1")
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if task.Status != "completed" || task.FinalAnswer == nil {
		t.Fatalf("task = %+v, want completed with answer", task)
	}
	if len(events.Events()) == 0 {
		t.Fatal("runtime did not publish events")
	}
	for _, event := range events.Events() {
		if event.TaskID != "task-1" {
			t.Fatalf("event task id = %q, want task-1", event.TaskID)
		}
	}
}

func TestWorkflowEngineRunsRecoverableNodesAndCheckpoints(t *testing.T) {
	ctx := context.Background()
	db := openRuntimeTestDB(t)
	cfg := config.Config{}
	cfg.Agent.Leader.ModelID = "local-planner"
	rt := NewLocal(cfg, db, &orchestrator.InMemoryEvidenceBus{})

	ragStore := rag.NewSQLiteStore(db.SQL, rag.Chunker{MaxTokens: 32})
	if _, err := ragStore.Index(ctx, rag.Document{
		ID:           "doc-runtime",
		SourceURI:    "local://notes/runtime",
		Title:        "Runtime workflow",
		Text:         "Persistent workflow nodes should checkpoint evidence after runtime dispatch.",
		PrivacyClass: "local_private",
	}); err != nil {
		t.Fatalf("Index() error = %v", err)
	}
	if err := rt.Workflows.Create(ctx, workflow.Run{
		ID:        "wf-runtime",
		TaskID:    "task-runtime",
		Status:    workflow.StatusPending,
		InputJSON: `{"input":"runtime workflow checkpoint evidence"}`,
	}, []workflow.Node{
		{WorkflowID: "wf-runtime", NodeID: "retrieve", CapabilityID: "rag.search", Status: workflow.NodePending},
		{WorkflowID: "wf-runtime", NodeID: "verify", CapabilityID: "verification.verify", Status: workflow.NodePending},
	}); err != nil {
		t.Fatalf("workflow Create() error = %v", err)
	}

	if err := rt.Workflow.RunWorkflow(ctx, "wf-runtime"); err != nil {
		t.Fatalf("RunWorkflow() error = %v", err)
	}
	run, err := rt.Workflows.Get(ctx, "wf-runtime")
	if err != nil {
		t.Fatalf("workflow Get() error = %v", err)
	}
	if run.Status != workflow.StatusCompleted {
		t.Fatalf("workflow status = %s, want completed", run.Status)
	}
	if got := checkpointCount(t, db.SQL, "wf-runtime", "retrieve"); got != 1 {
		t.Fatalf("retrieve checkpoints = %d, want 1", got)
	}
	if got := checkpointCount(t, db.SQL, "wf-runtime", "verify"); got != 1 {
		t.Fatalf("verify checkpoints = %d, want 1", got)
	}
	evidence, err := rt.Evidence.ListByTask(ctx, "task-runtime")
	if err != nil {
		t.Fatalf("ListByTask() error = %v", err)
	}
	if len(evidence) == 0 {
		t.Fatal("workflow runtime dispatch did not persist evidence")
	}
}

func TestWorkflowEngineRecoverySkipsCompletedNodes(t *testing.T) {
	ctx := context.Background()
	db := openRuntimeTestDB(t)
	cfg := config.Config{}
	cfg.Agent.Leader.ModelID = "local-planner"
	rt := NewLocal(cfg, db, &orchestrator.InMemoryEvidenceBus{})

	if err := rt.Workflows.Create(ctx, workflow.Run{
		ID:        "wf-recover",
		TaskID:    "task-recover",
		Status:    workflow.StatusRunning,
		InputJSON: `{"input":"recover workflow"}`,
	}, []workflow.Node{
		{WorkflowID: "wf-recover", NodeID: "done", CapabilityID: "memory.search", Status: workflow.NodeCompleted},
		{WorkflowID: "wf-recover", NodeID: "left", CapabilityID: "memory.search", Status: workflow.NodePending},
	}); err != nil {
		t.Fatalf("workflow Create() error = %v", err)
	}
	if err := rt.Workflows.SaveCheckpoint(ctx, workflow.Checkpoint{WorkflowID: "wf-recover", NodeID: "done", Status: workflow.NodeCompleted, ResultRef: "already done"}); err != nil {
		t.Fatalf("SaveCheckpoint() error = %v", err)
	}

	if err := rt.Workflow.RunWorkflow(ctx, "wf-recover"); err != nil {
		t.Fatalf("RunWorkflow() error = %v", err)
	}
	if got := checkpointCount(t, db.SQL, "wf-recover", "done"); got != 1 {
		t.Fatalf("completed node checkpoints = %d, want original checkpoint only", got)
	}
	if got := checkpointCount(t, db.SQL, "wf-recover", "left"); got != 1 {
		t.Fatalf("remaining node checkpoints = %d, want 1", got)
	}
}

func TestWorkflowEngineRejectsProjectDeniedCapability(t *testing.T) {
	ctx := context.Background()
	db := openRuntimeTestDB(t)
	cfg := config.Config{}
	cfg.Agent.Leader.ModelID = "local-planner"
	rt := NewLocal(cfg, db, &orchestrator.InMemoryEvidenceBus{})

	if err := rt.Projects.Save(ctx, project.Project{
		ID:                  "proj-locked",
		Name:                "Locked",
		PrivacyClass:        "local_private",
		AllowedCapabilities: []string{"memory.search"},
	}); err != nil {
		t.Fatalf("project Save() error = %v", err)
	}
	if err := rt.Workflows.Create(ctx, workflow.Run{
		ID:        "wf-denied",
		TaskID:    "task-denied",
		ProjectID: "proj-locked",
		Status:    workflow.StatusPending,
		InputJSON: `{"input":"project policy"}`,
	}, []workflow.Node{{WorkflowID: "wf-denied", NodeID: "retrieve", CapabilityID: "rag.search", Status: workflow.NodePending}}); err != nil {
		t.Fatalf("workflow Create() error = %v", err)
	}

	err := rt.Workflow.RunWorkflow(ctx, "wf-denied")
	if !errors.Is(err, ErrWorkflowCapabilityDenied) {
		t.Fatalf("RunWorkflow() error = %v, want ErrWorkflowCapabilityDenied", err)
	}
	run, getErr := rt.Workflows.Get(ctx, "wf-denied")
	if getErr != nil {
		t.Fatalf("workflow Get() error = %v", getErr)
	}
	if run.Status != workflow.StatusFailed {
		t.Fatalf("workflow status = %s, want failed", run.Status)
	}
}

func openRuntimeTestDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.OpenSQLite(context.Background(), filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatalf("OpenSQLite() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := storage.Migrate(context.Background(), db.SQL); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	return db
}

func checkpointCount(t *testing.T, db *sql.DB, workflowID string, nodeID string) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM workflow_checkpoints WHERE workflow_id = ? AND node_id = ?`, workflowID, nodeID).Scan(&count); err != nil {
		t.Fatalf("checkpoint count query error = %v", err)
	}
	return count
}
