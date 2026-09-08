package codingagent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestRealCodexCodingAgentE2E(t *testing.T) {
	runRealCodingAgentE2E(t, "codex", os.Getenv("PACHAT_CODEX_CLI_PATH"), NewCodexAdapter)
}

func TestRealClaudeCodingAgentE2E(t *testing.T) {
	runRealCodingAgentE2E(t, "claude", os.Getenv("PACHAT_CLAUDE_CODE_CLI_PATH"), NewClaudeAdapter)
}

func runRealCodingAgentE2E(t *testing.T, id string, binary string, factory func(BackendConfig, Executor) *CLIAdapter) {
	t.Helper()
	if os.Getenv("PACHAT_CODING_AGENT_E2E") != "1" {
		t.Skip("set PACHAT_CODING_AGENT_E2E=1 to run real coding agent CLI E2E tests")
	}
	if binary == "" {
		t.Skip("real coding agent CLI path is not configured")
	}
	repo := initE2ERepo(t)
	registry := NewRegistry()
	registry.Register(factory(BackendConfig{
		ID:                id,
		Enabled:           true,
		Binary:            binary,
		Timeout:           2 * time.Minute,
		ExecutionLocation: ExecutionLocal,
		InferenceTrust:    InferenceLocalPrivate,
		PrivacyPolicy:     PrivacyAllowAll,
		Capabilities:      []Capability{CapabilityCoding, CapabilityCodeTest},
		AllowDirectWrites: true,
	}, nil))
	runner := Runner{Registry: registry, Executor: ProcessExecutor{}, Preferred: []string{id}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	result, err := runner.Run(ctx, Request{
		TaskID:            "task-real-" + id,
		NodeID:            id,
		RepositoryPath:    repo,
		Prompt:            "Create a file named pachat_real_e2e.txt containing the text pachat real coding e2e.",
		Privacy:           RepositoryPrivate,
		Required:          []Capability{CapabilityCoding},
		AllowWrite:        true,
		AllowDirectWrites: true,
		TestCommands:      [][]string{{"/bin/sh", "-c", "test -f pachat_real_e2e.txt && grep -q 'pachat real coding e2e' pachat_real_e2e.txt"}},
	})
	if err != nil {
		t.Fatalf("Run() error = %v; result=%#v", err, result)
	}
	if err := result.ValidateEvidence(); err != nil {
		t.Fatalf("ValidateEvidence() error = %v; result=%#v", err, result)
	}
	if len(result.Tests) == 0 || result.Tests[len(result.Tests)-1].ExitCode != 0 {
		t.Fatalf("tests = %#v, want passing evidence", result.Tests)
	}
}

func initE2ERepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("# coding e2e\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init"}, {"add", "README.md"}, {"commit", "-m", "Initial commit"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=pachat", "GIT_AUTHOR_EMAIL=pachat@example.invalid", "GIT_COMMITTER_NAME=pachat", "GIT_COMMITTER_EMAIL=pachat@example.invalid")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v error = %v output=%s", args, err, out)
		}
	}
	return repo
}
