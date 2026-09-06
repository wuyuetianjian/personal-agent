package codingagent

import (
	"context"
	"errors"
)

type Runner struct {
	Registry  *Registry
	Workspace WorkspaceManager
	Executor  Executor
	Preferred []string
}

func (r Runner) Run(ctx context.Context, req Request) (Result, error) {
	registry := r.Registry
	if registry == nil {
		return Result{}, ErrNoBackendAvailable
	}
	selectReq := req
	if selectReq.AllowWrite && selectReq.WorkspacePath == "" && !selectReq.AllowDirectWrites {
		selectReq.AllowWrite = false
	}
	adapter, err := registry.Select(ctx, selectReq, r.Preferred)
	if err != nil {
		return Result{}, err
	}
	cfg := adapter.Config()

	var workspace Workspace
	if req.AllowWrite && req.WorkspacePath == "" && !req.AllowDirectWrites && !cfg.AllowDirectWrites {
		workspace, err = r.Workspace.Create(ctx, req.RepositoryPath, req.TaskID, req.NodeID)
		if err != nil {
			return Result{}, err
		}
		req.WorkspacePath = workspace.Path
		defer func() { _ = r.Workspace.Cleanup(context.Background(), workspace) }()
	}

	result, runErr := adapter.Run(ctx, req)
	if req.WorkspacePath != "" {
		diff, files, err := CollectDiff(ctx, req.WorkspacePath)
		if err == nil {
			result.Diff = diff
			result.FilesChanged = files
		}
	}
	tests, testErr := r.runTests(ctx, req)
	result.Tests = append(result.Tests, tests...)
	if runErr != nil {
		return result, runErr
	}
	if testErr != nil {
		return result, testErr
	}
	if err := result.ValidateEvidence(); err != nil {
		return result, err
	}
	return result, nil
}

func (r Runner) runTests(ctx context.Context, req Request) ([]CommandEvidence, error) {
	if len(req.TestCommands) == 0 {
		return nil, nil
	}
	executor := r.Executor
	if executor == nil {
		executor = ProcessExecutor{}
	}
	var evidence []CommandEvidence
	var joined error
	for _, args := range req.TestCommands {
		out, err := executor.Run(ctx, ExecRequest{
			Args: args,
			Dir:  requestDir(req),
			Env:  SanitizedEnvironment(nil),
		})
		evidence = append(evidence, CommandEvidence{
			Args:       append([]string(nil), args...),
			Dir:        requestDir(req),
			ExitCode:   out.ExitCode,
			Stdout:     out.Stdout,
			Stderr:     out.Stderr,
			StartedAt:  out.StartedAt,
			FinishedAt: out.FinishedAt,
			TimedOut:   out.TimedOut,
			Cancelled:  out.Cancelled,
		})
		if err != nil {
			joined = errors.Join(joined, err)
		}
	}
	return evidence, joined
}
