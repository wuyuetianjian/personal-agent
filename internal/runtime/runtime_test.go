package runtime

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"agent/internal/config"
	"agent/internal/orchestrator"
	"agent/internal/rag"
	"agent/internal/storage"
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
