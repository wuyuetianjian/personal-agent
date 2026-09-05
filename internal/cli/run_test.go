package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunNoopTask(t *testing.T) {
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
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("database was not created: %v", err)
	}
}

func TestRunRequiresTask(t *testing.T) {
	var out bytes.Buffer
	err := Run(context.Background(), []string{"run", "--config", "config.yaml"}, &out)
	if err == nil {
		t.Fatal("Run() error = nil, want usage error")
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
