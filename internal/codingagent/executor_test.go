package codingagent

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestProcessExecutorCompletes(t *testing.T) {
	result, err := (ProcessExecutor{}).Run(context.Background(), ExecRequest{
		Args: []string{"/bin/sh", "-c", "printf ok"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Stdout != "ok" || result.ExitCode != 0 {
		t.Fatalf("Run() = stdout %q exit %d", result.Stdout, result.ExitCode)
	}
}

func TestProcessExecutorTimeout(t *testing.T) {
	result, err := (ProcessExecutor{GracePeriod: 10 * time.Millisecond}).Run(context.Background(), ExecRequest{
		Args:    []string{"/bin/sh", "-c", "sleep 2"},
		Timeout: 20 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("Run() error = nil, want timeout-related error")
	}
	if !result.TimedOut {
		t.Fatalf("TimedOut = false, want true")
	}
}

func TestProcessExecutorCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan ExecResult, 1)
	errs := make(chan error, 1)
	go func() {
		result, err := (ProcessExecutor{GracePeriod: 10 * time.Millisecond}).Run(ctx, ExecRequest{
			Args: []string{"/bin/sh", "-c", "sleep 2"},
		})
		done <- result
		errs <- err
	}()
	cancel()
	select {
	case result := <-done:
		err := <-errs
		if err == nil {
			t.Fatal("Run() error = nil, want cancellation error")
		}
		if !result.Cancelled && !errors.Is(ctx.Err(), context.Canceled) {
			t.Fatalf("Cancelled = false")
		}
	case <-time.After(time.Second):
		t.Fatal("Run() did not return after cancellation")
	}
}
