package runtime

import (
	"context"
	"errors"
	"sync"

	"agent/internal/agent"
)

var ErrCapabilityExecutorNotFound = errors.New("capability executor not found")

type CapabilityExecutor interface {
	Execute(ctx context.Context, node agent.TaskNode, input workflowInput) (agent.Result, error)
}

type CapabilityExecutorRegistry struct {
	mu        sync.RWMutex
	executors map[string]CapabilityExecutor
}

func NewCapabilityExecutorRegistry() *CapabilityExecutorRegistry {
	return &CapabilityExecutorRegistry{executors: make(map[string]CapabilityExecutor)}
}

func (r *CapabilityExecutorRegistry) Register(capabilityID string, executor CapabilityExecutor) {
	if r == nil || capabilityID == "" || executor == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.executors[capabilityID] = executor
}

func (r *CapabilityExecutorRegistry) Lookup(capabilityID string) (CapabilityExecutor, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	executor, ok := r.executors[capabilityID]
	return executor, ok
}
