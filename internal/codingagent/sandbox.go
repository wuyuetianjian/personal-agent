package codingagent

import (
	"errors"
	"strings"
)

var ErrSandboxPolicy = errors.New("coding agent sandbox policy violation")

type SandboxPolicy struct {
	RequireIsolatedWorktree bool
	AllowDirectWrites       bool
	AllowedEnv              []string
}

type SandboxRequest struct {
	WorkspacePath string
	SourcePath    string
	DirectWrites  bool
	Env           map[string]string
}

func (p SandboxPolicy) Validate(req SandboxRequest) error {
	if p.RequireIsolatedWorktree && (req.WorkspacePath == "" || req.WorkspacePath == req.SourcePath) {
		return ErrSandboxPolicy
	}
	if req.DirectWrites && !p.AllowDirectWrites {
		return ErrSandboxPolicy
	}
	allowed := map[string]bool{}
	for _, key := range p.AllowedEnv {
		allowed[key] = true
	}
	for key := range req.Env {
		if strings.ContainsRune(key, '\x00') || strings.Contains(strings.ToLower(key), "secret") || strings.Contains(strings.ToLower(key), "token") {
			return ErrSandboxPolicy
		}
		if len(allowed) > 0 && !allowed[key] {
			return ErrSandboxPolicy
		}
	}
	return nil
}
