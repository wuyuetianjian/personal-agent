package cli

import (
	"agent/internal/storage"
	"agent/internal/workflow"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestRunRuntimeTask(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	dbPath := filepath.Join(dir, "agent.db")
	writeConfig(t, configPath, dbPath)

	var out bytes.Buffer
	err := Run(context.Background(), []string{"run", "--config", configPath, "--task", "smoke test"}, &out)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(out.String(), "status=completed") {
		t.Fatalf("output = %q, want completed status", out.String())
	}
	if strings.Contains(out.String(), "No-op task completed.") {
		t.Fatalf("output = %q, still contains no-op answer", out.String())
	}
	if !strings.Contains(out.String(), "remote_tokens=0") || !strings.Contains(out.String(), "answer:") {
		t.Fatalf("output = %q, want runtime answer shape", out.String())
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("database was not created: %v", err)
	}
}

func TestRunLongTaskCommands(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	dbPath := filepath.Join(dir, "agent.db")
	writeConfig(t, configPath, dbPath)

	var runOut bytes.Buffer
	err := Run(context.Background(), []string{"run", "--config", configPath, "--task", "long work", "--long"}, &runOut)
	if err != nil {
		t.Fatalf("Run(long) error = %v", err)
	}
	taskID := ExtractTaskID(runOut.String())
	if taskID == "" {
		t.Fatalf("task id missing from output %q", runOut.String())
	}

	var listOut bytes.Buffer
	if err := Run(context.Background(), []string{"task", "list", "--config", configPath}, &listOut); err != nil {
		t.Fatalf("task list error = %v", err)
	}
	if !strings.Contains(listOut.String(), taskID) || !strings.Contains(listOut.String(), "running") {
		t.Fatalf("task list output = %q", listOut.String())
	}

	var showOut bytes.Buffer
	if err := Run(context.Background(), []string{"task", "show", "--config", configPath, "--id", taskID}, &showOut); err != nil {
		t.Fatalf("task show error = %v", err)
	}
	if !strings.Contains(showOut.String(), "status=running") {
		t.Fatalf("task show output = %q", showOut.String())
	}

	var cancelOut bytes.Buffer
	if err := Run(context.Background(), []string{"task", "cancel", "--config", configPath, "--id", taskID}, &cancelOut); err != nil {
		t.Fatalf("task cancel error = %v", err)
	}
	if !strings.Contains(cancelOut.String(), "status=cancelled") {
		t.Fatalf("task cancel output = %q", cancelOut.String())
	}
}

func TestTaskUsageCommand(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	dbPath := filepath.Join(dir, "agent.db")
	writeConfig(t, configPath, dbPath)
	db, err := storage.OpenSQLite(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.Migrate(context.Background(), db.SQL); err != nil {
		t.Fatal(err)
	}
	store := workflow.Store{DB: db.SQL}
	if err := store.Create(context.Background(), workflow.Run{ID: "wf-usage-cli", TaskID: "task-usage-cli", Status: workflow.StatusPending}, []workflow.Node{{WorkflowID: "wf-usage-cli", NodeID: "synthesis", CapabilityID: "synthesis.local", Status: workflow.NodePending}}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveCheckpoint(context.Background(), workflow.Checkpoint{WorkflowID: "wf-usage-cli", NodeID: "synthesis", Status: workflow.NodeCompleted, UsageJSON: `{"InputTokens":4,"OutputTokens":6,"EstimatedCostUSD":0.125}`}); err != nil {
		t.Fatal(err)
	}
	db.Close()

	var out bytes.Buffer
	if err := Run(context.Background(), []string{"task", "usage", "--config", configPath, "--id", "task-usage-cli"}, &out); err != nil {
		t.Fatalf("task usage error = %v", err)
	}
	got := out.String()
	for _, want := range []string{"task_id=task-usage-cli", "input_tokens=4", "output_tokens=6", "estimated_cost_usd=0.125000", "node_id=synthesis"} {
		if !strings.Contains(got, want) {
			t.Fatalf("task usage output = %q, want %s", got, want)
		}
	}
}

func TestChatStoresMemory(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	dbPath := filepath.Join(dir, "agent.db")
	writeConfig(t, configPath, dbPath)

	var out bytes.Buffer
	input := strings.NewReader("hello\n/memory\n/exit\n")
	err := RunWithIO(context.Background(), []string{"chat", "--config", configPath}, IO{Stdin: input, Stdout: &out})
	if err != nil {
		t.Fatalf("chat error = %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "Local-first answer for: hello") && !strings.Contains(got, "No local evidence was found for: hello") {
		t.Fatalf("chat output missing response: %q", got)
	}
	if !strings.Contains(got, "user_message") || !strings.Contains(got, "hello") {
		t.Fatalf("chat memory output = %q", got)
	}
}

func TestChatHelp(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	dbPath := filepath.Join(dir, "agent.db")
	writeConfig(t, configPath, dbPath)

	var out bytes.Buffer
	input := strings.NewReader("/help\n/exit\n")
	err := RunWithIO(context.Background(), []string{"chat", "--config", configPath}, IO{Stdin: input, Stdout: &out})
	if err != nil {
		t.Fatalf("chat help error = %v", err)
	}
	got := out.String()
	for _, command := range []string{"/help", "/memory", "/exit", "/quit"} {
		if !strings.Contains(got, command) {
			t.Fatalf("chat help output missing %s: %q", command, got)
		}
	}
}

func TestSlashCommandCompleter(t *testing.T) {
	options, _ := slashCommandCompleter().Do([]rune("/"), 1)
	got := make([]string, 0, len(options))
	for _, option := range options {
		got = append(got, string(option))
	}
	joined := strings.Join(got, " ")
	for _, command := range []string{"/help", "/memory", "/exit", "/quit"} {
		suffix := strings.TrimPrefix(command, "/")
		if !strings.Contains(joined, suffix) {
			t.Fatalf("completion options %q missing %s", joined, suffix)
		}
	}
}

func TestRunRequiresTask(t *testing.T) {
	var out bytes.Buffer
	err := Run(context.Background(), []string{"run", "--config", "config.yaml"}, &out)
	if err == nil {
		t.Fatal("Run() error = nil, want usage error")
	}
}

func TestVersionCommand(t *testing.T) {
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"version"}, &out); err != nil {
		t.Fatalf("version error = %v", err)
	}
	got := out.String()
	for _, field := range []string{"version=", "commit=", "build_date=", "go_version=", "config_schema=", "database_schema=", "skill_manifest=", "api_version="} {
		if !strings.Contains(got, field) {
			t.Fatalf("version output missing %s: %q", field, got)
		}
	}
}

func TestCapabilityAndWorkflowCommands(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	dbPath := filepath.Join(dir, "agent.db")
	writeConfig(t, configPath, dbPath)

	var capabilityOut bytes.Buffer
	if err := Run(context.Background(), []string{"capability", "list", "--config", configPath}, &capabilityOut); err != nil {
		t.Fatalf("capability list error = %v", err)
	}
	if !strings.Contains(capabilityOut.String(), "memory.search") || !strings.Contains(capabilityOut.String(), "rag.search") {
		t.Fatalf("capability output = %q", capabilityOut.String())
	}

	db, err := storage.OpenSQLite(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.Migrate(context.Background(), db.SQL); err != nil {
		t.Fatal(err)
	}
	if err := (workflow.Store{DB: db.SQL}).Create(context.Background(), workflow.Run{ID: "workflow-1", TaskID: "task-1", Status: workflow.StatusPending}, nil); err != nil {
		t.Fatal(err)
	}
	db.Close()

	var workflowOut bytes.Buffer
	if err := Run(context.Background(), []string{"workflow", "pause", "--config", configPath, "--id", "workflow-1"}, &workflowOut); err != nil {
		t.Fatalf("workflow pause error = %v", err)
	}
	if !strings.Contains(workflowOut.String(), "status=paused") {
		t.Fatalf("workflow output = %q", workflowOut.String())
	}
}

func TestTriggerCommands(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	dbPath := filepath.Join(dir, "agent.db")
	writeConfig(t, configPath, dbPath)

	var createOut bytes.Buffer
	if err := Run(context.Background(), []string{"trigger", "create", "--config", configPath, "--id", "tr-cli", "--type", "manual"}, &createOut); err != nil {
		t.Fatalf("trigger create error = %v", err)
	}
	if !strings.Contains(createOut.String(), "trigger_id=tr-cli") {
		t.Fatalf("create output = %q", createOut.String())
	}
	var listOut bytes.Buffer
	if err := Run(context.Background(), []string{"trigger", "list", "--config", configPath}, &listOut); err != nil {
		t.Fatalf("trigger list error = %v", err)
	}
	if !strings.Contains(listOut.String(), "tr-cli") {
		t.Fatalf("list output = %q", listOut.String())
	}
	var runOut bytes.Buffer
	if err := Run(context.Background(), []string{"trigger", "run-now", "--config", configPath, "--id", "tr-cli"}, &runOut); err != nil {
		t.Fatalf("trigger run-now error = %v", err)
	}
	if !strings.Contains(runOut.String(), "status=fired") {
		t.Fatalf("run-now output = %q", runOut.String())
	}
}

func TestSkillCommands(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	dbPath := filepath.Join(dir, "agent.db")
	writeConfig(t, configPath, dbPath)
	manifestPath := filepath.Join(dir, "skill.yaml")
	manifest := `id: skill.summary
version: 1.0.0
name: summarize
description: summarize local memory
status: draft
permissions:
  max_level: read_only
privacy:
  max_external_trust: local_private
requires:
  capabilities:
    - memory.search
workflow:
  nodes:
    - id: memory
      capability: memory.search
      role: memory
`
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	var validateOut bytes.Buffer
	if err := Run(context.Background(), []string{"skill", "validate", "--config", configPath, "--path", manifestPath}, &validateOut); err != nil {
		t.Fatalf("skill validate error = %v", err)
	}
	if !strings.Contains(validateOut.String(), "status=OK") {
		t.Fatalf("validate output = %q", validateOut.String())
	}
	var importOut bytes.Buffer
	if err := Run(context.Background(), []string{"skill", "import", "--config", configPath, "--path", manifestPath}, &importOut); err != nil {
		t.Fatalf("skill import error = %v", err)
	}
	if !strings.Contains(importOut.String(), "skill_id=skill.summary") {
		t.Fatalf("import output = %q", importOut.String())
	}
	var enableOut bytes.Buffer
	if err := Run(context.Background(), []string{"skill", "enable", "--config", configPath, "--id", "skill.summary", "--version", "1.0.0"}, &enableOut); err != nil {
		t.Fatalf("skill enable error = %v", err)
	}
	if !strings.Contains(enableOut.String(), "status=enabled") {
		t.Fatalf("enable output = %q", enableOut.String())
	}
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"list", []string{"skill", "list", "--config", configPath}, "skill.summary"},
		{"show", []string{"skill", "show", "--config", configPath, "--id", "skill.summary"}, "version=1.0.0"},
		{"versions", []string{"skill", "versions", "--config", configPath, "--id", "skill.summary"}, "1.0.0"},
	} {
		var out bytes.Buffer
		if err := Run(context.Background(), tc.args, &out); err != nil {
			t.Fatalf("%s error = %v", tc.name, err)
		}
		if !strings.Contains(out.String(), tc.want) {
			t.Fatalf("%s output = %q, want %s", tc.name, out.String(), tc.want)
		}
	}
	var runOut bytes.Buffer
	if err := Run(context.Background(), []string{"skill", "run", "--config", configPath, "--id", "skill.summary", "--input", "local context"}, &runOut); err != nil {
		t.Fatalf("skill run error = %v", err)
	}
	if !strings.Contains(runOut.String(), "skill_id=skill.summary") || !strings.Contains(runOut.String(), "status=completed") {
		t.Fatalf("run output = %q", runOut.String())
	}
	var disableOut bytes.Buffer
	if err := Run(context.Background(), []string{"skill", "disable", "--config", configPath, "--id", "skill.summary"}, &disableOut); err != nil {
		t.Fatalf("skill disable error = %v", err)
	}
	if !strings.Contains(disableOut.String(), "status=disabled") {
		t.Fatalf("disable output = %q", disableOut.String())
	}
}

func TestMCPCommands(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	dbPath := filepath.Join(dir, "agent.db")
	writeConfig(t, configPath, dbPath)
	serverPath := filepath.Join(dir, "fake-mcp")
	writeFakeMCPServer(t, serverPath)
	f, err := os.OpenFile(configPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`
mcp:
  servers:
    local:
      enabled: true
      command: ` + serverPath + `
      tools:
        - ping
      trust_level: local_private
      privacy_classes:
        - private
`); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"list", []string{"mcp", "list", "--config", configPath}, "local"},
		{"health", []string{"mcp", "health", "--config", configPath}, "healthy"},
		{"tools", []string{"mcp", "tools", "--config", configPath}, "mcp.local.ping"},
		{"test", []string{"mcp", "test", "--config", configPath, "--server", "local", "--tool", "ping", "--arguments", `{"message":"hello"}`}, "status=OK"},
	} {
		var out bytes.Buffer
		if err := Run(context.Background(), tc.args, &out); err != nil {
			t.Fatalf("%s error = %v", tc.name, err)
		}
		if !strings.Contains(out.String(), tc.want) {
			t.Fatalf("%s output = %q, want %s", tc.name, out.String(), tc.want)
		}
	}
}

func writeFakeMCPServer(t *testing.T, path string) {
	t.Helper()
	frames := mcpFrame(`{"jsonrpc":"2.0","id":1,"result":{}}`) +
		mcpFrame(`{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"ping"}]}}`) +
		mcpFrame(`{"jsonrpc":"2.0","id":3,"result":{"content":[{"type":"text","text":"pong"}]}}`)
	script := "#!/bin/sh\ncat <<'EOF'\n" + frames + "\nEOF\nsleep 1\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func mcpFrame(payload string) string {
	return "Content-Length: " + strconv.Itoa(len(payload)) + "\r\n\r\n" + payload
}

func TestP12ExtraCommands(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	dbPath := filepath.Join(dir, "agent.db")
	writeConfig(t, configPath, dbPath)

	db, err := storage.OpenSQLite(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.Migrate(context.Background(), db.SQL); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.ExecContext(context.Background(), `INSERT INTO notification_inbox (id, project_id, trigger_id, title, body, dedup_key, status, created_at, read_at) VALUES ('notif-cli', 'default', 'trigger-cli', 'Title', 'Body', 'dedup-cli', 'unread', CURRENT_TIMESTAMP, NULL)`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	var notificationOut bytes.Buffer
	if err := Run(context.Background(), []string{"notification", "list", "--config", configPath}, &notificationOut); err != nil {
		t.Fatalf("notification list error = %v", err)
	}
	if !strings.Contains(notificationOut.String(), "notif-cli") {
		t.Fatalf("notification output = %q", notificationOut.String())
	}
	var modelOut bytes.Buffer
	if err := Run(context.Background(), []string{"model", "discover", "--config", configPath}, &modelOut); err != nil {
		t.Fatalf("model discover error = %v", err)
	}
	if !strings.Contains(modelOut.String(), "local-planner") {
		t.Fatalf("model output = %q", modelOut.String())
	}
	watchDir := filepath.Join(dir, "watch")
	if err := os.MkdirAll(watchDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(watchDir, "note.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	var watcherOut bytes.Buffer
	if err := Run(context.Background(), []string{"watcher", "scan", "--config", configPath, "--path", watchDir}, &watcherOut); err != nil {
		t.Fatalf("watcher scan error = %v", err)
	}
	if !strings.Contains(watcherOut.String(), "events=1") {
		t.Fatalf("watcher output = %q", watcherOut.String())
	}
}

func TestP12InitConfigValidateDoctorAndMaintenance(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	dbPath := filepath.Join(dir, "data", "agent.db")

	var initOut bytes.Buffer
	if err := Run(context.Background(), []string{"init", "--config", configPath, "--data-dir", filepath.Join(dir, "data"), "--db-path", dbPath}, &initOut); err != nil {
		t.Fatalf("init error = %v", err)
	}
	if !strings.Contains(initOut.String(), "privacy_secret_env=PERSONAL_AGENT_PRIVACY_HMAC_SECRET") {
		t.Fatalf("init output exposed wrong shape: %q", initOut.String())
	}

	var validateOut bytes.Buffer
	if err := Run(context.Background(), []string{"config", "validate", "--config", configPath}, &validateOut); err != nil {
		t.Fatalf("config validate error = %v", err)
	}
	if !strings.Contains(validateOut.String(), "OK") {
		t.Fatalf("validate output = %q", validateOut.String())
	}

	var doctorOut bytes.Buffer
	if err := Run(context.Background(), []string{"doctor", "--config", configPath}, &doctorOut); err != nil {
		t.Fatalf("doctor error = %v", err)
	}
	if !strings.Contains(doctorOut.String(), "database") || !strings.Contains(doctorOut.String(), "privacy_secret") {
		t.Fatalf("doctor output = %q", doctorOut.String())
	}

	var integrityOut bytes.Buffer
	if err := Run(context.Background(), []string{"storage", "integrity", "--config", configPath}, &integrityOut); err != nil {
		t.Fatalf("storage integrity error = %v", err)
	}
	if !strings.Contains(integrityOut.String(), "integrity=ok") {
		t.Fatalf("integrity output = %q", integrityOut.String())
	}

	var migrateOut bytes.Buffer
	if err := Run(context.Background(), []string{"storage", "migrate", "--config", configPath}, &migrateOut); err != nil {
		t.Fatalf("storage migrate error = %v", err)
	}
	if !strings.Contains(migrateOut.String(), "migrate=ok") {
		t.Fatalf("migrate output = %q", migrateOut.String())
	}
}

func TestP12KnowledgeProjectAndBackupCommands(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	dbPath := filepath.Join(dir, "agent.db")
	writeConfig(t, configPath, dbPath)
	sourcePath := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(sourcePath, []byte("NFS latency was resolved by checking mount options."), 0o644); err != nil {
		t.Fatal(err)
	}

	var addOut bytes.Buffer
	if err := Run(context.Background(), []string{"knowledge", "add", "--config", configPath, sourcePath}, &addOut); err != nil {
		t.Fatalf("knowledge add error = %v", err)
	}
	if !strings.Contains(addOut.String(), "chunks=") {
		t.Fatalf("knowledge add output = %q", addOut.String())
	}

	var statusOut bytes.Buffer
	if err := Run(context.Background(), []string{"knowledge", "status", "--config", configPath}, &statusOut); err != nil {
		t.Fatalf("knowledge status error = %v", err)
	}
	if !strings.Contains(statusOut.String(), "documents=1") {
		t.Fatalf("knowledge status output = %q", statusOut.String())
	}

	var projectOut bytes.Buffer
	if err := Run(context.Background(), []string{"project", "create", "--config", configPath, "--id", "proj-1", "--name", "Test Project"}, &projectOut); err != nil {
		t.Fatalf("project create error = %v", err)
	}
	if !strings.Contains(projectOut.String(), "project_id=proj-1") {
		t.Fatalf("project output = %q", projectOut.String())
	}

	var listOut bytes.Buffer
	if err := Run(context.Background(), []string{"project", "list", "--config", configPath, "--json"}, &listOut); err != nil {
		t.Fatalf("project list error = %v", err)
	}
	if !strings.Contains(listOut.String(), "Test Project") {
		t.Fatalf("project list output = %q", listOut.String())
	}

	backupPath := filepath.Join(dir, "backup.zip")
	var backupOut bytes.Buffer
	if err := Run(context.Background(), []string{"backup", "create", "--config", configPath, "--output", backupPath}, &backupOut); err != nil {
		t.Fatalf("backup create error = %v", err)
	}
	if _, err := os.Stat(backupPath); err != nil {
		t.Fatalf("backup not created: %v", err)
	}

	var restoreOut bytes.Buffer
	if err := Run(context.Background(), []string{"backup", "restore", "--config", configPath, "--input", backupPath}, &restoreOut); err != nil {
		t.Fatalf("backup restore dry-run error = %v", err)
	}
	if !strings.Contains(restoreOut.String(), "restore=dry-run") {
		t.Fatalf("restore output = %q", restoreOut.String())
	}
}

func writeConfig(t *testing.T, path string, dbPath string) {
	t.Helper()
	content := `config_version: 1
app:
  name: personal-agent
  environment: test
  data_dir: .
storage:
  driver: sqlite
  sqlite:
    path: ` + dbPath + `
privacy:
  hmac_secret_env: PERSONAL_AGENT_PRIVACY_HMAC_SECRET
models:
  default_provider: local
  providers:
    local:
      type: openai_compatible
      trust_level: local_private
agent:
  leader:
    model_id: local-planner
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}
