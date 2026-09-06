package codingagent

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"syscall"
	"time"
)

type Executor interface {
	Run(ctx context.Context, req ExecRequest) (ExecResult, error)
}

type ExecRequest struct {
	Args    []string
	Dir     string
	Env     []string
	Timeout time.Duration
}

type ExecResult struct {
	ExitCode   int
	Stdout     string
	Stderr     string
	StartedAt  time.Time
	FinishedAt time.Time
	TimedOut   bool
	Cancelled  bool
}

type ProcessExecutor struct {
	GracePeriod time.Duration
}

func (e ProcessExecutor) Run(ctx context.Context, req ExecRequest) (ExecResult, error) {
	if len(req.Args) == 0 {
		return ExecResult{ExitCode: -1}, errors.New("exec args are required")
	}
	started := time.Now().UTC()
	runCtx := ctx
	cancel := func() {}
	if req.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, req.Timeout)
	}
	defer cancel()

	cmd := exec.CommandContext(runCtx, req.Args[0], req.Args[1:]...)
	cmd.Dir = req.Dir
	cmd.Env = req.Env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return ExecResult{
			ExitCode:   -1,
			Stdout:     stdout.String(),
			Stderr:     stderr.String(),
			StartedAt:  started,
			FinishedAt: time.Now().UTC(),
		}, err
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	var waitErr error
	select {
	case waitErr = <-done:
	case <-runCtx.Done():
		e.terminateProcessGroup(cmd.Process.Pid)
		waitErr = <-done
	}

	finished := time.Now().UTC()
	result := ExecResult{
		ExitCode:   exitCode(waitErr),
		Stdout:     stdout.String(),
		Stderr:     stderr.String(),
		StartedAt:  started,
		FinishedAt: finished,
		TimedOut:   req.Timeout > 0 && errors.Is(runCtx.Err(), context.DeadlineExceeded),
		Cancelled:  errors.Is(ctx.Err(), context.Canceled),
	}
	if waitErr != nil {
		return result, waitErr
	}
	return result, nil
}

func (e ProcessExecutor) terminateProcessGroup(pid int) {
	grace := e.GracePeriod
	if grace <= 0 {
		grace = 200 * time.Millisecond
	}
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	time.Sleep(grace)
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}
