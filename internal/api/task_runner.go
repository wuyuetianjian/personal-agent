package api

import (
	"context"
	"sync"

	"agent/internal/runtime"
)

type TaskRunner struct {
	mu      sync.Mutex
	runner  RuntimeRunner
	running map[string]context.CancelFunc
}

func NewTaskRunner(runner RuntimeRunner) *TaskRunner {
	return &TaskRunner{
		runner:  runner,
		running: make(map[string]context.CancelFunc),
	}
}

func (r *TaskRunner) Start(parent context.Context, req runtime.RunRequest) bool {
	if r == nil || r.runner == nil || req.TaskID == "" {
		return false
	}
	ctx, cancel := context.WithCancel(parent)
	r.mu.Lock()
	if _, exists := r.running[req.TaskID]; exists {
		r.mu.Unlock()
		cancel()
		return false
	}
	r.running[req.TaskID] = cancel
	r.mu.Unlock()
	go func() {
		defer r.forget(req.TaskID)
		_, _ = r.runner.Run(ctx, req)
	}()
	return true
}

func (r *TaskRunner) Cancel(taskID string) bool {
	if r == nil || taskID == "" {
		return false
	}
	r.mu.Lock()
	cancel, ok := r.running[taskID]
	if ok {
		delete(r.running, taskID)
	}
	r.mu.Unlock()
	if ok {
		cancel()
	}
	return ok
}

func (r *TaskRunner) forget(taskID string) {
	r.mu.Lock()
	delete(r.running, taskID)
	r.mu.Unlock()
}
