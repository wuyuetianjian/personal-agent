package runtime

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"agent/internal/agent"
	"agent/internal/browser"
	"agent/internal/codingagent"
	"agent/internal/config"
	"agent/internal/model"
	"agent/internal/orchestrator"
	"agent/internal/project"
	"agent/internal/rag"
	"agent/internal/skill"
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

func TestWorkflowEngineExecutesRegisteredCodingExecutor(t *testing.T) {
	ctx := context.Background()
	db := openRuntimeTestDB(t)
	cfg := configForRuntimeTest()
	enabled := true
	cfg.CodingAgents.Backends = map[string]config.CodingAgentBackendConfig{
		"codex": {
			Enabled:           &enabled,
			Adapter:           "codex",
			Binary:            "codex",
			ExecutionLocation: "local",
			InferenceTrust:    "local_private",
			PrivacyPolicy:     "allow_all",
			Capabilities:      []string{"coding"},
			AllowDirectWrites: true,
		},
	}
	rt := NewLocal(cfg, db, &orchestrator.InMemoryEvidenceBus{})
	rt.Executors.Register("coding.codex", CodingExecutor{
		Runner: fakeCodingRunner{result: codingagent.Result{
			BackendID:    "codex",
			Summary:      "implemented change",
			Diff:         "diff --git a/file.txt b/file.txt",
			FilesChanged: []string{"file.txt"},
			Tests:        []codingagent.CommandEvidence{{Args: []string{"go", "test", "./..."}, ExitCode: 0}},
		}},
		Backend:  "codex",
		Config:   cfg,
		Evidence: rt.Evidence,
	})
	input := `{"repository_path":"/tmp/repo","prompt":"implement change","allow_write":true,"allow_direct_writes":true,"test_commands":[["go","test","./..."]]}`
	if err := rt.Workflows.Create(ctx, workflow.Run{
		ID:        "wf-coding",
		TaskID:    "task-coding",
		Status:    workflow.StatusPending,
		InputJSON: `{"input":` + strconv.Quote(input) + `}`,
	}, []workflow.Node{{WorkflowID: "wf-coding", NodeID: "codex", CapabilityID: "coding.codex", Role: string(agent.RoleTool), Status: workflow.NodePending}}); err != nil {
		t.Fatalf("workflow Create() error = %v", err)
	}

	if err := rt.Workflow.RunWorkflow(ctx, "wf-coding"); err != nil {
		t.Fatalf("RunWorkflow() error = %v", err)
	}
	evidence, err := rt.Evidence.ListByTask(ctx, "task-coding")
	if err != nil {
		t.Fatalf("ListByTask() error = %v", err)
	}
	if len(evidence) != 1 || !strings.Contains(evidence[0].Content, "files_changed: file.txt") || !strings.Contains(evidence[0].Content, "go test ./...") {
		t.Fatalf("evidence = %#v, want coding diff and test evidence", evidence)
	}
}

func TestWorkflowEngineExecutesRegisteredMCPExecutor(t *testing.T) {
	ctx := context.Background()
	db := openRuntimeTestDB(t)
	cfg := configForRuntimeTest()
	cfg.MCP.Servers = map[string]config.MCPServerConfig{
		"local": {Enabled: true, Command: "fake-mcp", Tools: []string{"metrics"}, TrustLevel: "local_private", PrivacyClasses: []string{"private"}},
	}
	rt := NewLocal(cfg, db, &orchestrator.InMemoryEvidenceBus{})
	rt.Executors.Register("mcp.local.metrics", MCPExecutor{
		ServerID: "local",
		ToolName: "metrics",
		Config:   cfg.MCP.Servers["local"],
		Client:   fakeMCPClient{output: json.RawMessage(`{"value":"ok"}`)},
		Evidence: rt.Evidence,
	})
	if err := rt.Workflows.Create(ctx, workflow.Run{
		ID: "wf-mcp", TaskID: "task-mcp", Status: workflow.StatusPending, InputJSON: `{"input":"{\"arguments\":{\"scope\":\"local\"}}"}`,
	}, []workflow.Node{{WorkflowID: "wf-mcp", NodeID: "metrics", CapabilityID: "mcp.local.metrics", Role: string(agent.RoleTool), Status: workflow.NodePending}}); err != nil {
		t.Fatalf("workflow Create() error = %v", err)
	}

	if err := rt.Workflow.RunWorkflow(ctx, "wf-mcp"); err != nil {
		t.Fatalf("RunWorkflow() error = %v", err)
	}
	evidence, err := rt.Evidence.ListByTask(ctx, "task-mcp")
	if err != nil {
		t.Fatalf("ListByTask() error = %v", err)
	}
	if len(evidence) != 1 || !strings.Contains(evidence[0].Content, "UNTRUSTED OBSERVATION") || !strings.Contains(evidence[0].Content, `"value":"ok"`) {
		t.Fatalf("evidence = %#v, want untrusted MCP evidence", evidence)
	}
}

func TestWorkflowEngineExecutesAllowlistedToolExecutor(t *testing.T) {
	ctx := context.Background()
	db := openRuntimeTestDB(t)
	cfg := configForRuntimeTest()
	cfg.Tools.Allowlist = []config.ToolConfig{{ID: "tool.echo", Enabled: true, Program: "/bin/echo", Args: []string{"allowlisted"}, SideEffectLevel: "read_only"}}
	rt := NewLocal(cfg, db, &orchestrator.InMemoryEvidenceBus{})
	if err := rt.Workflows.Create(ctx, workflow.Run{
		ID: "wf-tool", TaskID: "task-tool", Status: workflow.StatusPending, InputJSON: `{"input":"ignored ; rm -rf /"}`,
	}, []workflow.Node{{WorkflowID: "wf-tool", NodeID: "echo", CapabilityID: "tool.echo", Role: string(agent.RoleTool), Status: workflow.NodePending}}); err != nil {
		t.Fatalf("workflow Create() error = %v", err)
	}

	if err := rt.Workflow.RunWorkflow(ctx, "wf-tool"); err != nil {
		t.Fatalf("RunWorkflow() error = %v", err)
	}
	evidence, err := rt.Evidence.ListByTask(ctx, "task-tool")
	if err != nil {
		t.Fatalf("ListByTask() error = %v", err)
	}
	if len(evidence) != 1 || !strings.Contains(evidence[0].Content, "allowlisted") || strings.Contains(evidence[0].Content, "rm -rf") {
		t.Fatalf("evidence = %#v, want fixed allowlisted command output", evidence)
	}
}

func TestUsageForTaskAggregatesWorkflowCheckpoints(t *testing.T) {
	ctx := context.Background()
	db := openRuntimeTestDB(t)
	rt := NewLocal(configForRuntimeTest(), db, nil)
	if err := rt.Workflows.Create(ctx, workflow.Run{
		ID: "wf-usage", TaskID: "task-usage", Status: workflow.StatusPending,
	}, []workflow.Node{
		{WorkflowID: "wf-usage", NodeID: "one", CapabilityID: "memory.search", Status: workflow.NodePending},
		{WorkflowID: "wf-usage", NodeID: "two", CapabilityID: "synthesis.local", Status: workflow.NodePending},
	}); err != nil {
		t.Fatalf("workflow Create() error = %v", err)
	}
	if err := rt.Workflows.SaveCheckpoint(ctx, workflow.Checkpoint{WorkflowID: "wf-usage", NodeID: "one", Status: workflow.NodeCompleted, UsageJSON: `{"InputTokens":3,"OutputTokens":5}`}); err != nil {
		t.Fatalf("SaveCheckpoint one error = %v", err)
	}
	if err := rt.Workflows.SaveCheckpoint(ctx, workflow.Checkpoint{WorkflowID: "wf-usage", NodeID: "two", Status: workflow.NodeCompleted, UsageJSON: `{"InputTokens":7,"OutputTokens":11,"EstimatedCostUSD":0.25}`}); err != nil {
		t.Fatalf("SaveCheckpoint two error = %v", err)
	}

	report, err := rt.UsageForTask(ctx, "task-usage")
	if err != nil {
		t.Fatalf("UsageForTask() error = %v", err)
	}
	if report.Total.InputTokens != 10 || report.Total.OutputTokens != 16 || report.Total.EstimatedCostUSD != 0.25 {
		t.Fatalf("report total = %#v", report.Total)
	}
	if len(report.Workflows) != 1 || len(report.Workflows[0].Nodes) != 2 {
		t.Fatalf("report = %#v", report)
	}
}

func TestWorkflowBudgetUsesAccumulatedCheckpointUsage(t *testing.T) {
	ctx := context.Background()
	db := openRuntimeTestDB(t)
	rt := NewLocal(configForRuntimeTest(), db, nil)
	if err := rt.Workflows.Create(ctx, workflow.Run{
		ID:        "wf-budget",
		TaskID:    "task-budget",
		Status:    workflow.StatusPending,
		InputJSON: `{"input":"budget check","max_input_tokens":2}`,
	}, []workflow.Node{
		{WorkflowID: "wf-budget", NodeID: "done", CapabilityID: "memory.search", Status: workflow.NodePending},
		{WorkflowID: "wf-budget", NodeID: "next", CapabilityID: "rag.search", Status: workflow.NodePending},
	}); err != nil {
		t.Fatalf("workflow Create() error = %v", err)
	}
	if err := rt.Workflows.SaveCheckpoint(ctx, workflow.Checkpoint{WorkflowID: "wf-budget", NodeID: "done", Status: workflow.NodeCompleted, UsageJSON: `{"InputTokens":2}`}); err != nil {
		t.Fatalf("SaveCheckpoint() error = %v", err)
	}

	err := rt.Workflow.RunWorkflow(ctx, "wf-budget")
	if !errors.Is(err, ErrWorkflowBudgetExceeded) {
		t.Fatalf("RunWorkflow() error = %v, want ErrWorkflowBudgetExceeded", err)
	}
}

func TestWorkflowVerificationEarlyStopCancelsRemainingWork(t *testing.T) {
	ctx := context.Background()
	db := openRuntimeTestDB(t)
	cfg := configForRuntimeTest()
	cfg.Tools.Allowlist = []config.ToolConfig{{ID: "tool.echo", Enabled: true, Program: "/bin/echo", Args: []string{"should-not-run"}, SideEffectLevel: "read_only"}}
	rt := NewLocal(cfg, db, &orchestrator.InMemoryEvidenceBus{})
	rt.ChatModel = model.ModelMetadata{ID: "local-planner", Model: "qwen3.8:27b-mlx"}
	rt.ChatProvider = &queuedChatProvider{responses: []model.ChatResponse{
		{Content: `{"claims":[{"text":"supported claim","confidence":0.95,"evidence_ids":["ev-supported"]}],"decision_summary":"enough evidence","confidence":0.95,"evidence_ids":["ev-supported"]}`, Usage: model.Usage{InputTokens: 5, OutputTokens: 7}},
	}}
	if err := rt.Evidence.Put(ctx, Evidence{
		ID:           "ev-supported",
		TaskID:       "task-early-stop",
		NodeID:       "seed",
		Claim:        "seed evidence",
		SourceType:   SourceMemory,
		SourceID:     "seed",
		Content:      "supported claim",
		Trust:        0.9,
		PrivacyClass: "local_private",
	}); err != nil {
		t.Fatalf("Evidence Put() error = %v", err)
	}
	if err := rt.Workflows.Create(ctx, workflow.Run{
		ID:        "wf-early-stop",
		TaskID:    "task-early-stop",
		Status:    workflow.StatusPending,
		InputJSON: `{"input":"decide early"}`,
	}, []workflow.Node{
		{WorkflowID: "wf-early-stop", NodeID: "reason", CapabilityID: "reasoning.local", Status: workflow.NodePending},
		{WorkflowID: "wf-early-stop", NodeID: "expensive", CapabilityID: "tool.echo", Dependencies: []string{"reason"}, Status: workflow.NodePending},
	}); err != nil {
		t.Fatalf("workflow Create() error = %v", err)
	}

	if err := rt.Workflow.RunWorkflow(ctx, "wf-early-stop"); err != nil {
		t.Fatalf("RunWorkflow() error = %v", err)
	}
	run, err := rt.Workflows.Get(ctx, "wf-early-stop")
	if err != nil {
		t.Fatalf("workflow Get() error = %v", err)
	}
	if run.Status != workflow.StatusCancelled {
		t.Fatalf("workflow status = %s, want cancelled by early stop", run.Status)
	}
	checkpoints, err := rt.Workflows.ListCheckpoints(ctx, "wf-early-stop")
	if err != nil {
		t.Fatalf("ListCheckpoints() error = %v", err)
	}
	for _, checkpoint := range checkpoints {
		if checkpoint.NodeID == "expensive" {
			t.Fatalf("expensive node checkpoint = %#v, want not launched", checkpoint)
		}
	}
	report, err := rt.UsageForWorkflow(ctx, "wf-early-stop")
	if err != nil {
		t.Fatalf("UsageForWorkflow() error = %v", err)
	}
	if report.Total.InputTokens != 5 || report.Total.OutputTokens != 7 {
		t.Fatalf("usage = %#v, want only reasoning provider usage", report.Total)
	}
}

func TestRuntimeUsesHighConfidenceReadOnlySkillWithoutPlanner(t *testing.T) {
	ctx := context.Background()
	db := openRuntimeTestDB(t)
	rt := NewLocal(configForRuntimeTest(), db, &orchestrator.InMemoryEvidenceBus{})
	planner := &countingPlanner{}
	rt.Planner = planner
	if err := rt.Skills.Add(skill.Manifest{
		ID:          "skill.summary",
		Version:     "1.0.0",
		Name:        "summarize",
		Description: "summarize local memory",
		Status:      skill.StatusActive,
		Permissions: skill.PermissionPolicy{
			MaxLevel: "read_only",
		},
		Requires: skill.Requirements{Capabilities: []string{"memory.search"}},
		Privacy:  skill.PrivacyPolicy{MaxExternalTrust: "local_private"},
		Workflow: skill.Workflow{Nodes: []skill.Node{
			{ID: "memory", Capability: "memory.search", Role: string(agent.RoleMemory)},
		}},
	}, rt.Capabilities); err != nil {
		t.Fatalf("skill Add() error = %v", err)
	}
	if err := db.CreateTask(ctx, storage.Task{
		ID:            "task-skill",
		Title:         "skill",
		Input:         "summarize",
		Status:        "running",
		LeaderModelID: "local-planner",
		PrivacyClass:  "local_private",
	}); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}

	if _, err := rt.Run(ctx, RunRequest{TaskID: "task-skill", Input: "summarize", PrivacyClass: "local_private"}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if planner.calls != 0 {
		t.Fatalf("planner calls = %d, want 0 for high-confidence skill match", planner.calls)
	}
	runs, err := rt.Workflows.List(ctx, 1)
	if err != nil {
		t.Fatalf("workflow List() error = %v", err)
	}
	if len(runs) != 1 || runs[0].SkillID != "skill.summary" || runs[0].SkillVersion != "1.0.0" {
		t.Fatalf("workflow run = %#v, want skill metadata", runs)
	}
	checkpoints, err := rt.Workflows.ListCheckpoints(ctx, runs[0].ID)
	if err != nil {
		t.Fatalf("ListCheckpoints() error = %v", err)
	}
	if len(checkpoints) != 1 || checkpoints[0].NodeID != "memory" {
		t.Fatalf("checkpoints = %#v, want compiled skill workflow node", checkpoints)
	}
}

func TestWorkflowWorkerRecoversRunnableWorkflowOnStartup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db := openRuntimeTestDB(t)
	rt := NewLocal(configForRuntimeTest(), db, &orchestrator.InMemoryEvidenceBus{})
	if err := rt.Workflows.Create(ctx, workflow.Run{
		ID:        "wf-worker",
		TaskID:    "task-worker",
		Status:    workflow.StatusPending,
		InputJSON: `{"input":"worker resume"}`,
	}, []workflow.Node{{WorkflowID: "wf-worker", NodeID: "memory", CapabilityID: "memory.search", Role: string(agent.RoleMemory), Status: workflow.NodePending}}); err != nil {
		t.Fatalf("workflow Create() error = %v", err)
	}
	worker := NewWorkflowWorker(rt.Workflow)
	worker.PollInterval = time.Hour
	worker.Start(ctx)

	deadline := time.After(time.Second)
	for {
		run, err := rt.Workflows.Get(ctx, "wf-worker")
		if err != nil {
			t.Fatalf("workflow Get() error = %v", err)
		}
		if run.Status == workflow.StatusCompleted {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("workflow status = %s, want completed after worker startup recovery", run.Status)
		case <-time.After(10 * time.Millisecond):
		}
	}
	if got := checkpointCount(t, db.SQL, "wf-worker", "memory"); got != 1 {
		t.Fatalf("worker checkpoints = %d, want 1", got)
	}
}

func TestBrowserExecutorHonorsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	db := openRuntimeTestDB(t)
	rt := NewLocal(configForRuntimeTest(), db, nil)
	_, err := (BrowserExecutor{
		Tool:     blockingBrowserTool{},
		Evidence: rt.Evidence,
	}).Execute(ctx, agent.TaskNode{TaskID: "task-browser-cancel", ID: "browser", Type: "browser.read", Role: agent.RoleBrowser, Input: "read"}, workflowInput{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("BrowserExecutor error = %v, want context.Canceled", err)
	}
}

func TestMCPExecutorHonorsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	db := openRuntimeTestDB(t)
	rt := NewLocal(configForRuntimeTest(), db, nil)
	_, err := (MCPExecutor{
		ServerID: "local",
		ToolName: "metrics",
		Client:   fakeMCPClient{},
		Evidence: rt.Evidence,
	}).Execute(ctx, agent.TaskNode{TaskID: "task-mcp-cancel", ID: "mcp", Type: "mcp.local.metrics", Role: agent.RoleTool, Input: "{}"}, workflowInput{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("MCPExecutor error = %v, want context.Canceled", err)
	}
}

func TestStdioMCPClientHonorsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	client := stdioMCPClient{Config: config.MCPServerConfig{Enabled: true, Command: "/bin/sh", Args: []string{"-c", "sleep 2"}}}
	done := make(chan error, 1)
	go func() {
		_, err := client.Call(ctx, "local", "metrics", nil)
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("stdioMCPClient error = nil, want cancellation error")
		}
	case <-time.After(time.Second):
		t.Fatal("stdioMCPClient did not return after cancellation")
	}
}

func TestToolExecutorHonorsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	db := openRuntimeTestDB(t)
	rt := NewLocal(configForRuntimeTest(), db, nil)
	done := make(chan error, 1)
	go func() {
		_, err := (ToolExecutor{
			Config:   config.ToolConfig{ID: "tool.sleep", Enabled: true, Program: "/bin/sh", Args: []string{"-c", "sleep 2"}},
			Evidence: rt.Evidence,
		}).Execute(ctx, agent.TaskNode{TaskID: "task-tool-cancel", ID: "tool", Type: "tool.sleep", Role: agent.RoleTool, Input: "ignored"}, workflowInput{})
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("ToolExecutor error = nil, want cancellation error")
		}
	case <-time.After(time.Second):
		t.Fatal("ToolExecutor did not return after cancellation")
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

type countingPlanner struct {
	calls int
}

func (p *countingPlanner) Plan(ctx context.Context, req RunRequest) ([]agent.TaskNode, model.Usage, error) {
	p.calls++
	return nil, model.Usage{}, errors.New("planner should not be called")
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

type blockingBrowserTool struct{}

func (t blockingBrowserTool) Execute(ctx context.Context, action browser.Action) (browser.Result, error) {
	<-ctx.Done()
	return browser.Result{}, ctx.Err()
}

func (t blockingBrowserTool) Observe(ctx context.Context, sessionID string) (browser.Observation, error) {
	<-ctx.Done()
	return browser.Observation{}, ctx.Err()
}

type fakeCodingRunner struct {
	result codingagent.Result
	err    error
}

func (r fakeCodingRunner) Run(ctx context.Context, req codingagent.Request) (codingagent.Result, error) {
	if err := ctx.Err(); err != nil {
		return codingagent.Result{}, err
	}
	result := r.result
	result.TaskID = req.TaskID
	result.NodeID = req.NodeID
	return result, r.err
}

type fakeMCPClient struct {
	output json.RawMessage
	err    error
}

func (c fakeMCPClient) Call(ctx context.Context, serverID string, toolName string, input json.RawMessage) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return c.output, c.err
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
