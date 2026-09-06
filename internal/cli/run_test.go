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

func writeConfig(t *testing.T, path string, dbPath string) {
	t.Helper()
	content := `app:
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
