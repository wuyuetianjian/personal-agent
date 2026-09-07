package runtime

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"agent/internal/agent"
	"agent/internal/browser"
	"agent/internal/config"
	"agent/internal/model"
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
	workflows, err := rt.Workflows.List(ctx, 10)
	if err != nil {
		t.Fatalf("workflow List() error = %v", err)
	}
	if len(workflows) != 1 || workflows[0].TaskID != "task-1" || workflows[0].Status != workflow.StatusCompleted {
		t.Fatalf("workflows = %#v, want completed workflow for task-1", workflows)
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

func TestRunUsesConfiguredPublicEscalatorUsage(t *testing.T) {
	ctx := context.Background()
	db := openRuntimeTestDB(t)
	cfg := configForRuntimeTest()
	rt := NewLocal(cfg, db, &orchestrator.InMemoryEvidenceBus{})
	rt.Escalator = &PublicEscalator{Provider: staticChatProvider("public summary")}
	if err := db.CreateTask(ctx, storage.Task{ID: "task-public", Title: "public", Input: "unknown", Status: "running", LeaderModelID: "local-planner", PrivacyClass: "local_private"}); err != nil {
		t.Fatal(err)
	}
	result, err := rt.Run(ctx, RunRequest{TaskID: "task-public", Input: "unknown", LeaderModelID: "local-planner"})
	if err != nil {
		t.Fatal(err)
	}
	if result.RemoteCalls != 1 || result.Usage.RemoteTokens != 8 {
		t.Fatalf("result=%#v, want one remote call with provider usage", result)
	}
}

func TestRunUsesModelBackedReasoningAndSynthesisCheckpoint(t *testing.T) {
	ctx := context.Background()
	db := openRuntimeTestDB(t)
	cfg := configForRuntimeTest()
	cfg.Agent.Leader.Temperature = 0.2
	cfg.Agent.Leader.MaxOutputTokens = 2048
	cfg.Agent.SubAgents = map[string]config.RoleModelConfig{
		"reasoning": {Temperature: 0.1, MaxOutputTokens: 512},
		"synthesis": {Temperature: 0.3, MaxOutputTokens: 768},
	}
	rt := NewLocal(cfg, db, &orchestrator.InMemoryEvidenceBus{})
	rt.ChatModel = model.ModelMetadata{ID: "local-planner", Model: "qwen3.8:27b-mlx"}
	rt.ChatProvider = &queuedChatProvider{responses: []model.ChatResponse{
		{Content: `{"claims":[{"text":"The local note supports the answer.","confidence":0.88,"evidence_ids":[]}],"decision_summary":"model reasoning summary","confidence":0.88,"evidence_ids":[]}`, Usage: model.Usage{InputTokens: 7, OutputTokens: 11}},
		{Content: "model-backed final answer", Usage: model.Usage{InputTokens: 13, OutputTokens: 17}},
	}}

	ragStore := rag.NewSQLiteStore(db.SQL, rag.Chunker{MaxTokens: 32})
	if _, err := ragStore.Index(ctx, rag.Document{
		ID:           "doc-model-synthesis",
		SourceURI:    "local://notes/model-synthesis",
		Title:        "Model synthesis",
		Text:         "The local note supports the model-backed synthesis path.",
		PrivacyClass: "local_private",
	}); err != nil {
		t.Fatalf("Index() error = %v", err)
	}
	if err := db.CreateTask(ctx, storage.Task{ID: "task-model", Title: "model", Input: "Use the local note", Status: "running", LeaderModelID: "local-planner", PrivacyClass: "local_private"}); err != nil {
		t.Fatal(err)
	}

	result, err := rt.Run(ctx, RunRequest{TaskID: "task-model", Input: "Use the local note", LeaderModelID: "local-planner"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Answer != "model-backed final answer" {
		t.Fatalf("answer = %q, want model synthesis checkpoint", result.Answer)
	}
	if result.Usage.InputTokens != 13 || result.Usage.OutputTokens != 17 || result.Usage.RemoteTokens != 0 {
		t.Fatalf("usage = %#v, want synthesis provider usage without remote tokens", result.Usage)
	}
	evidence, err := rt.Evidence.ListByTask(ctx, "task-model")
	if err != nil {
		t.Fatalf("ListByTask() error = %v", err)
	}
	foundReasoningEvidence := false
	for _, item := range evidence {
		if item.NodeID == "reasoning" && item.SourceType == SourceModel && strings.Contains(item.Content, "model reasoning summary") {
			foundReasoningEvidence = true
		}
	}
	if !foundReasoningEvidence {
		t.Fatalf("evidence = %#v, want model reasoning evidence", evidence)
	}
	workflows, err := rt.Workflows.List(ctx, 1)
	if err != nil {
		t.Fatalf("workflow List() error = %v", err)
	}
	checkpoints, err := rt.Workflows.ListCheckpoints(ctx, workflows[0].ID)
	if err != nil {
		t.Fatalf("ListCheckpoints() error = %v", err)
	}
	var sawReasoning, sawSynthesis bool
	for _, cp := range checkpoints {
		if cp.NodeID == "reasoning" {
			sawReasoning = true
		}
		if cp.NodeID == "synthesis" && cp.ResultRef == "model-backed final answer" {
			sawSynthesis = true
		}
	}
	if !sawReasoning || !sawSynthesis {
		t.Fatalf("checkpoints = %#v, want reasoning and synthesis checkpoints", checkpoints)
	}
}

func TestWorkflowEngineExecutesRegisteredBrowserReadExecutor(t *testing.T) {
	ctx := context.Background()
	db := openRuntimeTestDB(t)
	cfg := configForRuntimeTest()
	cfg.Browser.Enabled = true
	rt := NewLocal(cfg, db, &orchestrator.InMemoryEvidenceBus{})
	rt.Executors.Register("browser.read", BrowserExecutor{
		Tool:     fakeBrowserTool{observation: browser.Observation{URL: "http://127.0.0.1:8787", Title: "Local", VisibleText: "browser executor evidence"}},
		Config:   cfg.Browser,
		Evidence: rt.Evidence,
	})
	if err := rt.Workflows.Create(ctx, workflow.Run{
		ID:        "wf-browser",
		TaskID:    "task-browser",
		Status:    workflow.StatusPending,
		InputJSON: `{"input":"read browser"}`,
	}, []workflow.Node{{WorkflowID: "wf-browser", NodeID: "browser-read", CapabilityID: "browser.read", Role: string(agent.RoleBrowser), Status: workflow.NodePending}}); err != nil {
		t.Fatalf("workflow Create() error = %v", err)
	}

	if err := rt.Workflow.RunWorkflow(ctx, "wf-browser"); err != nil {
		t.Fatalf("RunWorkflow() error = %v", err)
	}
	evidence, err := rt.Evidence.ListByTask(ctx, "task-browser")
	if err != nil {
		t.Fatalf("ListByTask() error = %v", err)
	}
	if len(evidence) != 1 || evidence[0].SourceType != SourceBrowser || !strings.Contains(evidence[0].Content, "browser executor evidence") {
		t.Fatalf("evidence = %#v, want browser evidence", evidence)
	}
}

type queuedChatProvider struct {
	responses []model.ChatResponse
	calls     int
}

func (p *queuedChatProvider) Chat(ctx context.Context, request model.ChatRequest) (model.ChatResponse, error) {
	if err := ctx.Err(); err != nil {
		return model.ChatResponse{}, err
	}
	if p.calls >= len(p.responses) {
		return model.ChatResponse{}, errors.New("unexpected chat call")
	}
	response := p.responses[p.calls]
	p.calls++
	return response, nil
}

type fakeBrowserTool struct {
	observation browser.Observation
}

func (t fakeBrowserTool) Execute(ctx context.Context, action browser.Action) (browser.Result, error) {
	return browser.Result{Action: action, Observation: t.observation, Decision: browser.DecisionAllowed}, ctx.Err()
}

func (t fakeBrowserTool) Observe(ctx context.Context, sessionID string) (browser.Observation, error) {
	return t.observation, ctx.Err()
}

func configForRuntimeTest() config.Config {
	var cfg config.Config
	cfg.Agent.Leader.ModelID = "local-planner"
	return cfg
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
