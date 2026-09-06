package codingagent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceIsolationAndDiffCollection(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()
	source := t.TempDir()
	runGit(t, source, "init")
	runGit(t, source, "config", "user.email", "test@example.invalid")
	runGit(t, source, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, source, "add", "README.md")
	runGit(t, source, "commit", "-m", "initial")

	manager := WorkspaceManager{Root: t.TempDir()}
	workspace, err := manager.Create(ctx, source, "task", "node")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	defer func() { _ = manager.Cleanup(ctx, workspace) }()

	if err := os.WriteFile(filepath.Join(workspace.Path, "README.md"), []byte("base\nchanged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sourceBytes, err := os.ReadFile(filepath.Join(source, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(sourceBytes) != "base\n" {
		t.Fatalf("source worktree was modified: %q", string(sourceBytes))
	}

	diff, files, err := CollectDiff(ctx, workspace.Path)
	if err != nil {
		t.Fatalf("CollectDiff() error = %v", err)
	}
	if !strings.Contains(diff, "+changed") {
		t.Fatalf("diff missing change: %q", diff)
	}
	if len(files) != 1 || files[0] != "README.md" {
		t.Fatalf("files = %#v, want README.md", files)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}
