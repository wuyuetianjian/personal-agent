package cli

import (
	"agent/internal/storage"
	"agent/internal/workflow"
	"bytes"
	"context"
	"os"
	"path/filepath"
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
	if !strings.Contains(got, "Recorded message in local memory.") {
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
