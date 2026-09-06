package codingagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type WorkspaceManager struct {
	Root string
}

type Workspace struct {
	SourcePath string
	Path       string
	Branch     string
}

func (m WorkspaceManager) Create(ctx context.Context, sourcePath, taskID, nodeID string) (Workspace, error) {
	if sourcePath == "" {
		return Workspace{}, errors.New("source repository path is required")
	}
	root := m.Root
	if root == "" {
		root = filepath.Join(os.TempDir(), "pachat-coding-worktrees")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return Workspace{}, err
	}
	branch := "pachat/" + safeID(taskID+"-"+nodeID)
	path := filepath.Join(root, safeID(sourcePath+"-"+branch))
	if err := git(ctx, sourcePath, "worktree", "add", "-B", branch, path, "HEAD"); err != nil {
		return Workspace{}, err
	}
	return Workspace{SourcePath: sourcePath, Path: path, Branch: branch}, nil
}

func (m WorkspaceManager) Cleanup(ctx context.Context, workspace Workspace) error {
	if workspace.Path == "" || workspace.SourcePath == "" {
		return nil
	}
	if err := git(ctx, workspace.SourcePath, "worktree", "remove", "--force", workspace.Path); err != nil {
		return err
	}
	return git(ctx, workspace.SourcePath, "worktree", "prune")
}

func CollectDiff(ctx context.Context, workspacePath string) (string, []string, error) {
	diff, err := gitOutput(ctx, workspacePath, "diff", "--binary", "HEAD")
	if err != nil {
		return "", nil, err
	}
	names, err := gitOutput(ctx, workspacePath, "diff", "--name-only", "HEAD")
	if err != nil {
		return "", nil, err
	}
	var files []string
	for _, line := range strings.Split(names, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			files = append(files, line)
		}
	}
	return diff, files, nil
}

func git(ctx context.Context, dir string, args ...string) error {
	return exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...).Run()
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...).Output()
	return string(out), err
}

func safeID(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:16]
}
