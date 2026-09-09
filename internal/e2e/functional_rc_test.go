package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agent/internal/agent"
	"agent/internal/browser"
	"agent/internal/codingagent"
	"agent/internal/config"
	"agent/internal/memory"
	"agent/internal/model"
	"agent/internal/notification"
	"agent/internal/orchestrator"
	"agent/internal/permission"
	"agent/internal/rag"
	"agent/internal/runtime"
	"agent/internal/storage"
	"agent/internal/trigger"
	"agent/internal/workflow"
)

func TestFunctionalRCMockBackedE2E(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	rt := runtime.NewLocal(functionalRCConfig(t), db, &orchestrator.InMemoryEvidenceBus{})
	if err := seedFunctionalRCEvidence(ctx, db); err != nil {
		t.Fatal(err)
	}
	public := &rcPublicProvider{}
	rt.Escalator = &runtime.PublicEscalator{Provider: public, Model: model.ModelMetadata{ID: "public-rc"}}
	plannerProvider := rcPlannerProvider(`{"nodes":[{"id":"memory","capability_id":"memory.search","role":"memory","dependencies":[]},{"id":"retrieval","capability_id":"rag.search","role":"retrieval","dependencies":[]},{"id":"reasoning","capability_id":"reasoning.local","role":"reasoning","dependencies":["memory","retrieval"]},{"id":"verification","capability_id":"verification.verify","role":"verification","dependencies":["reasoning"]},{"id":"synthesis","capability_id":"synthesis.local","role":"synthesis","dependencies":["verification"]}]}`)
	rt.Planner = runtime.BoundedModelPlanner{
		Provider:     plannerProvider,
		Model:        model.ModelMetadata{ID: "local-leader", Model: "mock", Capabilities: map[model.Capability]bool{model.CapabilityChat: true, model.CapabilityJSONSchema: true}},
		Capabilities: rt.Capabilities,
		MaxNodes:     8,
	}
	if err := db.CreateTask(ctx, storage.Task{ID: "task-rc-local", Title: "RC local", Input: "functional rc local evidence", Status: "running", LeaderModelID: "local-leader", PrivacyClass: "local_private"}); err != nil {
		t.Fatal(err)
	}
	result, err := rt.Run(ctx, runtime.RunRequest{TaskID: "task-rc-local", Input: "functional rc local evidence", LeaderModelID: "local-leader", PrivacyClass: "local_private"})
	if err != nil {
		t.Fatalf("Runtime.Run() error = %v", err)
	}
	if public.calls != 0 || result.RemoteCalls != 0 {
		t.Fatalf("local evidence used public calls provider=%d result=%d", public.calls, result.RemoteCalls)
	}
	assertWorkflowCheckpoints(t, rt.Workflows, "task-rc-local", "synthesis")

	browserTool := &rcBrowserTool{}
	rt.Executors.Register("browser.read", runtime.BrowserExecutor{Tool: browserTool, Evidence: rt.Evidence})
	rt.Executors.Register("browser.write", runtime.BrowserExecutor{Tool: browserTool, Evidence: rt.Evidence})
	confirmations := permission.NewInMemoryConfirmationStore()
	confirmationID, err := confirmations.Request(ctx, permission.Request{ID: "confirm-rc-browser", TaskID: "task-rc-browser-write", Action: permission.ActionBrowserWrite, Risk: permission.RiskMedium, Target: "browser", ProposedEffect: "write approved"})
	if err != nil {
		t.Fatal(err)
	}
	if err := confirmations.Approve(ctx, confirmationID); err != nil {
		t.Fatal(err)
	}
	if err := runOneNodeWorkflow(ctx, rt, "wf-rc-browser-read", "task-rc-browser-read", "browser-read", "browser.read", string(agent.RoleBrowser), `{"SessionID":"default"}`, ""); err != nil {
		t.Fatalf("browser read workflow: %v", err)
	}
	writeInput := `{"Type":"click","SessionID":"default","Domain":"example.test","URL":"https://example.test/settings","ConfirmationID":"` + confirmationID + `"}`
	if err := runOneNodeWorkflow(ctx, rt, "wf-rc-browser-write", "task-rc-browser-write", "browser-write", "browser.write", string(agent.RoleBrowser), writeInput, "wf-rc-browser-write:browser-write"); err != nil {
		t.Fatalf("browser write workflow: %v", err)
	}
	if browserTool.reads != 1 || browserTool.writes != 1 {
		t.Fatalf("browser tool reads=%d writes=%d", browserTool.reads, browserTool.writes)
	}
	assertEvidenceSource(t, db, "task-rc-browser-read", "browser")
	assertEvidenceSource(t, db, "task-rc-browser-write", "browser")

	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	codex := rcCodingRunner{backend: "codex"}
	claude := rcCodingRunner{backend: "claude"}
	rt.Executors.Register("coding.codex", runtime.CodingExecutor{Runner: codex, Backend: "codex", Config: rt.Config, Evidence: rt.Evidence})
	rt.Executors.Register("coding.claude", runtime.CodingExecutor{Runner: claude, Backend: "claude", Config: rt.Config, Evidence: rt.Evidence})
	codingInput := `{"repository_path":"` + repo + `","prompt":"make rc change","privacy":"private","required":["coding"],"allow_write":true,"test_commands":[["go","test","./..."]]}`
	if err := runOneNodeWorkflow(ctx, rt, "wf-rc-codex", "task-rc-codex", "codex", "coding.codex", string(agent.RoleTool), codingInput, "wf-rc-codex:codex"); err != nil {
		t.Fatalf("codex workflow: %v", err)
	}
	reviewInput := `{"repository_path":"` + repo + `","prompt":"review rc change","privacy":"private","required":["code_review"],"allow_write":false}`
	if err := runOneNodeWorkflow(ctx, rt, "wf-rc-claude", "task-rc-claude", "claude-review", "coding.claude", string(agent.RoleTool), reviewInput, "wf-rc-claude:claude-review"); err != nil {
		t.Fatalf("claude workflow: %v", err)
	}
	assertEvidenceContains(t, db, "task-rc-codex", "backend: codex")
	assertEvidenceContains(t, db, "task-rc-claude", "backend: claude")

	rt.Executors.Register("mcp.local.ping", runtime.MCPExecutor{ServerID: "local", ToolName: "ping", Config: config.MCPServerConfig{Enabled: true}, Client: rcMCPClient{}, Evidence: rt.Evidence})
	if err := runOneNodeWorkflow(ctx, rt, "wf-rc-mcp", "task-rc-mcp", "mcp", "mcp.local.ping", string(agent.RoleTool), `{"arguments":{"message":"discover and call"}}`, "wf-rc-mcp:mcp"); err != nil {
		t.Fatalf("mcp workflow: %v", err)
	}
	assertEvidenceContains(t, db, "task-rc-mcp", "UNTRUSTED OBSERVATION")

	if err := runOneNodeWorkflow(ctx, rt, "wf-rc-idempotent-1", "task-rc-idempotent-1", "browser-write", "browser.write", string(agent.RoleBrowser), writeInput, "rc-side-effect-once"); err != nil {
		t.Fatalf("first idempotent workflow: %v", err)
	}
	restarted := runtime.NewLocal(rt.Config, db, &orchestrator.InMemoryEvidenceBus{})
	restarted.Executors.Register("browser.write", runtime.BrowserExecutor{Tool: browserTool, Evidence: restarted.Evidence})
	if err := runOneNodeWorkflow(ctx, restarted, "wf-rc-idempotent-2", "task-rc-idempotent-2", "browser-write", "browser.write", string(agent.RoleBrowser), writeInput, "rc-side-effect-once"); err != nil {
		t.Fatalf("restarted idempotent workflow: %v", err)
	}
	if browserTool.writes != 2 {
		t.Fatalf("duplicate side effect executed, writes=%d", browserTool.writes)
	}

	triggerStore := trigger.Store{DB: db.SQL}
	notifier := notification.Store{DB: db.SQL}
	if err := triggerStore.Put(ctx, trigger.Trigger{ID: "trigger-rc-condition", ProjectID: "proj-rc", Type: trigger.TypeConditionWatch, Enabled: true, Condition: trigger.ConditionSpec{Evaluator: "false_to_true"}}); err != nil {
		t.Fatal(err)
	}
	daemon := trigger.Daemon{Store: triggerStore, Starter: rcNotificationStarter{Notifications: notifier}, Now: func() time.Time { return time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC) }}
	started, err := daemon.Tick(ctx)
	if err != nil {
		t.Fatalf("condition tick: %v", err)
	}
	if started != 1 {
		t.Fatalf("condition started=%d, want 1", started)
	}
	started, err = daemon.Tick(ctx)
	if err != nil {
		t.Fatalf("condition second tick: %v", err)
	}
	if started != 0 {
		t.Fatalf("condition second started=%d, want 0", started)
	}
	notifications, err := notifier.List(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(notifications) != 1 || notifications[0].TriggerID != "trigger-rc-condition" {
		t.Fatalf("notifications=%#v, want one condition notification", notifications)
	}
}

func TestFunctionalRCPublicEscalationUsesPrivacyGateway(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	t.Setenv("PACHAT_RC_PRIVACY_SECRET", "rc-secret")
	var providerSawOriginal bool
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "alice@example.com") {
			providerSawOriginal = true
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"public rc answer"}}],"usage":{"prompt_tokens":5,"completion_tokens":3}}`))
	}))
	defer provider.Close()
	enabled := true
	cfg := config.Config{
		App:     config.AppConfig{Name: "personal-agent", Environment: "test", DataDir: dir},
		Storage: config.StorageConfig{Driver: "sqlite", SQLite: config.SQLiteConfig{Path: filepath.Join(dir, "agent.db")}},
		Privacy: config.PrivacyConfig{HMACSecretEnv: "PACHAT_RC_PRIVACY_SECRET", FailClosedForPublicModels: true},
		Models: config.ModelsConfig{
			Providers: map[string]config.ProviderConfig{"public": {Enabled: &enabled, Type: "openai_compatible", BaseURL: provider.URL, TrustLevel: string(model.TrustPublicRemote), RequirePrivacyGateway: true}},
			Registry:  []config.ModelConfig{{ID: "public-rc", Provider: "public", Model: "public-rc", TrustLevel: string(model.TrustPublicRemote), Capabilities: []string{string(model.CapabilityChat)}}},
		},
		Agent: config.AgentConfig{Leader: config.RoleModelConfig{ModelID: "public-rc"}},
	}
	rt, err := runtime.Build(ctx, cfg)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	defer rt.Close()
	rt.Workflow = nil
	if err := rt.Storage.CreateTask(ctx, storage.Task{ID: "task-rc-public", Title: "public", Input: "ask alice@example.com", Status: "running", LeaderModelID: "public-rc", PrivacyClass: "local_private"}); err != nil {
		t.Fatal(err)
	}
	result, err := rt.Run(ctx, runtime.RunRequest{TaskID: "task-rc-public", Input: "ask alice@example.com", LeaderModelID: "public-rc", PrivacyClass: "local_private"})
	if err != nil {
		t.Fatalf("public Runtime.Run() error = %v", err)
	}
	if providerSawOriginal || result.RemoteCalls != 1 || !strings.Contains(result.Answer, "public rc answer") {
		t.Fatalf("privacy/public escalation failed saw_original=%v remote_calls=%d answer=%q", providerSawOriginal, result.RemoteCalls, result.Answer)
	}
}

func seedFunctionalRCEvidence(ctx context.Context, db *storage.DB) error {
	ragStore := rag.NewSQLiteStore(db.SQL, rag.Chunker{MaxTokens: 32})
	if _, err := ragStore.Index(ctx, rag.Document{ID: "doc-rc", SourceURI: "local://rc", Title: "RC", Text: "functional rc local evidence proves memory and rag use without public calls", PrivacyClass: "local_private"}); err != nil {
		return err
	}
	return memory.NewStore(db.SQL).Append(ctx, memory.EpisodicEvent{ID: "mem-rc", TaskID: "task-rc-local", EventType: "note", Summary: "functional rc local evidence", PrivacyClass: "local_private"})
}

func functionalRCConfig(t *testing.T) config.Config {
	t.Helper()
	enabled := true
	return config.Config{
		App:     config.AppConfig{Name: "personal-agent", Environment: "test", DataDir: t.TempDir()},
		Browser: config.BrowserConfig{Enabled: true, ProfileReuse: config.BrowserProfileReuse{AllowedDomains: []string{"example.test"}}},
		MCP:     config.MCPConfig{Servers: map[string]config.MCPServerConfig{"local": {Enabled: true, Tools: []string{"ping"}, TrustLevel: "local_private", PrivacyClasses: []string{"private", "confidential"}}}},
		CodingAgents: config.CodingAgentsConfig{Backends: map[string]config.CodingAgentBackendConfig{
			"codex":  {Enabled: &enabled, Adapter: "codex", InferenceTrust: string(codingagent.InferenceLocalPrivate), Capabilities: []string{string(codingagent.CapabilityCoding), string(codingagent.CapabilityCodeTest)}, AllowDirectWrites: true},
			"claude": {Enabled: &enabled, Adapter: "claude", InferenceTrust: string(codingagent.InferenceLocalPrivate), Capabilities: []string{string(codingagent.CapabilityCodeReview)}},
		}},
		Agent: config.AgentConfig{Leader: config.RoleModelConfig{ModelID: "local-leader"}},
	}
}

func runOneNodeWorkflow(ctx context.Context, rt *runtime.Runtime, workflowID, taskID, nodeID, capabilityID, role, input, idempotencyKey string) error {
	if err := rt.Storage.CreateTask(ctx, storage.Task{ID: taskID, Title: taskID, Input: input, Status: "running", LeaderModelID: rt.LeaderModel, PrivacyClass: "local_private"}); err != nil {
		return err
	}
	raw, err := json.Marshal(map[string]string{"input": input})
	if err != nil {
		return err
	}
	return runWorkflow(ctx, rt, workflowID, taskID, raw, workflow.Node{WorkflowID: workflowID, NodeID: nodeID, CapabilityID: capabilityID, Role: role, Status: workflow.NodePending, IdempotencyKey: idempotencyKey})
}

func runWorkflow(ctx context.Context, rt *runtime.Runtime, workflowID, taskID string, input []byte, node workflow.Node) error {
	if err := rt.Workflows.Create(ctx, workflow.Run{ID: workflowID, TaskID: taskID, Status: workflow.StatusPending, InputJSON: string(input)}, []workflow.Node{node}); err != nil {
		return err
	}
	if err := rt.Workflow.RunWorkflow(ctx, workflowID); err != nil {
		checkpoints, _ := rt.Workflows.ListCheckpoints(ctx, workflowID)
		return fmt.Errorf("%w checkpoints=%#v", err, checkpoints)
	}
	return nil
}

func assertWorkflowCheckpoints(t *testing.T, store workflow.Store, taskID string, nodes ...string) {
	t.Helper()
	runs, err := store.List(context.Background(), 20)
	if err != nil {
		t.Fatal(err)
	}
	workflowID := ""
	for _, run := range runs {
		if run.TaskID == taskID {
			workflowID = run.ID
			break
		}
	}
	if workflowID == "" {
		t.Fatalf("workflow for task %s not found", taskID)
	}
	checkpoints, err := store.ListCheckpoints(context.Background(), workflowID)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, checkpoint := range checkpoints {
		if checkpoint.Status == workflow.NodeCompleted {
			seen[checkpoint.NodeID] = true
		}
	}
	for _, node := range nodes {
		if !seen[node] {
			t.Fatalf("checkpoint %s missing in %#v", node, checkpoints)
		}
	}
}

func assertEvidenceSource(t *testing.T, db *storage.DB, taskID string, source string) {
	t.Helper()
	var count int
	if err := db.SQL.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM evidence WHERE task_id = ? AND type = ?`, taskID, source).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		t.Fatalf("task %s has no %s evidence", taskID, source)
	}
}

func assertEvidenceContains(t *testing.T, db *storage.DB, taskID string, want string) {
	t.Helper()
	var count int
	if err := db.SQL.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM evidence WHERE task_id = ? AND content_text LIKE ?`, taskID, "%"+want+"%").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		t.Fatalf("task %s evidence missing %q", taskID, want)
	}
}

type rcPlannerProvider string

func (p rcPlannerProvider) Chat(ctx context.Context, request model.ChatRequest) (model.ChatResponse, error) {
	if err := ctx.Err(); err != nil {
		return model.ChatResponse{}, err
	}
	return model.ChatResponse{Content: string(p), Usage: model.Usage{InputTokens: 10, OutputTokens: 20}}, nil
}

type rcPublicProvider struct {
	calls int
}

func (p *rcPublicProvider) Chat(ctx context.Context, request model.ChatRequest) (model.ChatResponse, error) {
	p.calls++
	return model.ChatResponse{Content: "public should not be called", Usage: model.Usage{InputTokens: 1, OutputTokens: 1}}, ctx.Err()
}

type rcBrowserTool struct {
	reads  int
	writes int
}

func (t *rcBrowserTool) Observe(ctx context.Context, sessionID string) (browser.Observation, error) {
	t.reads++
	return browser.Observation{URL: "https://example.test", Title: "RC Browser", VisibleText: "browser read evidence"}, ctx.Err()
}

func (t *rcBrowserTool) Execute(ctx context.Context, action browser.Action) (browser.Result, error) {
	t.writes++
	if action.ConfirmationID == "" {
		return browser.Result{Action: action, Error: &browser.ActionError{Message: "missing confirmation"}}, nil
	}
	return browser.Result{Action: action, Decision: browser.DecisionAllowed, Observation: browser.Observation{Title: "Browser write", VisibleText: "write executed"}, RecordedAt: time.Now().UTC()}, ctx.Err()
}

type rcCodingRunner struct {
	backend string
}

func (r rcCodingRunner) Run(ctx context.Context, req codingagent.Request) (codingagent.Result, error) {
	if err := ctx.Err(); err != nil {
		return codingagent.Result{}, err
	}
	summary := r.backend + " completed"
	return codingagent.Result{
		BackendID:     r.backend,
		TaskID:        req.TaskID,
		NodeID:        req.NodeID,
		Summary:       summary,
		WorkspacePath: filepath.Join(req.RepositoryPath, ".rc-worktree-"+r.backend),
		Diff:          "diff --git a/main.go b/main.go\n+// rc " + r.backend,
		FilesChanged:  []string{"main.go"},
		Tests:         []codingagent.CommandEvidence{{Args: []string{"go", "test", "./..."}, ExitCode: 0, Stdout: "ok"}},
	}, nil
}

type rcMCPClient struct{}

func (rcMCPClient) Call(ctx context.Context, serverID string, toolName string, input json.RawMessage) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return json.RawMessage(`{"content":[{"type":"text","text":"pong"}]}`), nil
}

type rcNotificationStarter struct {
	Notifications notification.Store
}

func (s rcNotificationStarter) StartTriggerWorkflow(ctx context.Context, tr trigger.Trigger) (string, error) {
	return "wf-" + tr.ID, s.Notifications.Put(ctx, notification.Notification{ID: "note-" + tr.ID, ProjectID: tr.ProjectID, TriggerID: tr.ID, Title: "condition changed", Severity: "info"})
}
