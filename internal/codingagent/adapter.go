package codingagent

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"time"
)

type Adapter interface {
	ID() string
	Config() BackendConfig
	Health(ctx context.Context) error
	Run(ctx context.Context, req Request) (Result, error)
}

type CLIAdapter struct {
	cfg      BackendConfig
	executor Executor
}

func NewCLIAdapter(cfg BackendConfig, executor Executor) *CLIAdapter {
	if executor == nil {
		executor = ProcessExecutor{}
	}
	return &CLIAdapter{cfg: cfg, executor: executor}
}

func (a *CLIAdapter) ID() string {
	return a.cfg.ID
}

func (a *CLIAdapter) Config() BackendConfig {
	return a.cfg
}

func (a *CLIAdapter) Health(ctx context.Context) error {
	if !a.cfg.Enabled {
		return ErrBackendDisabled
	}
	if a.cfg.Binary == "" {
		return ErrBackendUnhealthy
	}
	if _, err := exec.LookPath(a.cfg.Binary); err != nil {
		if _, statErr := os.Stat(a.cfg.Binary); statErr != nil {
			return errors.Join(ErrBackendUnhealthy, err)
		}
	}
	return ctx.Err()
}

func (a *CLIAdapter) Run(ctx context.Context, req Request) (Result, error) {
	if err := ValidatePolicy(a.cfg, req); err != nil {
		return Result{}, err
	}
	args := mapRequest(a.cfg.Adapter, req)
	runReq := ExecRequest{
		Args:    append([]string{a.cfg.Binary}, args...),
		Dir:     requestDir(req),
		Env:     SanitizedEnvironment(nil),
		Timeout: firstDuration(req.Timeout, a.cfg.Timeout),
	}
	out, err := a.executor.Run(ctx, runReq)
	result := Result{
		BackendID:     a.cfg.ID,
		TaskID:        req.TaskID,
		NodeID:        req.NodeID,
		Summary:       firstNonEmpty(out.Stdout, out.Stderr),
		WorkspacePath: requestDir(req),
		Commands: []CommandEvidence{{
			Args:       append([]string(nil), runReq.Args...),
			Dir:        runReq.Dir,
			ExitCode:   out.ExitCode,
			Stdout:     out.Stdout,
			Stderr:     out.Stderr,
			StartedAt:  out.StartedAt,
			FinishedAt: out.FinishedAt,
			TimedOut:   out.TimedOut,
			Cancelled:  out.Cancelled,
		}},
		ExitCode:  out.ExitCode,
		TimedOut:  out.TimedOut,
		Cancelled: out.Cancelled,
	}
	if err != nil {
		return result, err
	}
	return result, nil
}

func NewCodexAdapter(cfg BackendConfig, executor Executor) *CLIAdapter {
	cfg.Adapter = "codex"
	return NewCLIAdapter(cfg, executor)
}

func NewClaudeAdapter(cfg BackendConfig, executor Executor) *CLIAdapter {
	cfg.Adapter = "claude"
	return NewCLIAdapter(cfg, executor)
}

func mapRequest(adapter string, req Request) []string {
	switch adapter {
	case "claude":
		return []string{"-p", req.Prompt}
	default:
		return []string{"exec", req.Prompt}
	}
}

func requestDir(req Request) string {
	if req.WorkspacePath != "" {
		return req.WorkspacePath
	}
	return req.RepositoryPath
}

func firstDuration(values ...time.Duration) time.Duration {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
