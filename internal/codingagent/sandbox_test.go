package codingagent

import (
	"errors"
	"testing"
)

func TestSandboxPolicyRequiresIsolatedWorktreeAndMinimalEnv(t *testing.T) {
	policy := SandboxPolicy{RequireIsolatedWorktree: true, AllowedEnv: []string{"PATH"}}
	if err := policy.Validate(SandboxRequest{WorkspacePath: "/tmp/worktree", SourcePath: "/repo", Env: map[string]string{"PATH": "/bin"}}); err != nil {
		t.Fatalf("safe sandbox request rejected: %v", err)
	}
	for _, req := range []SandboxRequest{
		{WorkspacePath: "/repo", SourcePath: "/repo"},
		{WorkspacePath: "/tmp/worktree", SourcePath: "/repo", DirectWrites: true},
		{WorkspacePath: "/tmp/worktree", SourcePath: "/repo", Env: map[string]string{"API_TOKEN": "secret"}},
		{WorkspacePath: "/tmp/worktree", SourcePath: "/repo", Env: map[string]string{"HOME": "/Users/sunny"}},
	} {
		if !errors.Is(policy.Validate(req), ErrSandboxPolicy) {
			t.Fatalf("request %+v was not rejected", req)
		}
	}
}
