package reliability

import (
	"errors"
	"sync"

	"agent/internal/config"
)

var ErrResourceLimitExceeded = errors.New("resource limit exceeded")

type Resource string

const (
	ResourceWorkflow       Resource = "workflow"
	ResourceModelCall      Resource = "model_call"
	ResourceBrowserSession Resource = "browser_session"
	ResourceCodingAgent    Resource = "coding_agent"
	ResourceMCPCall        Resource = "mcp_call"
	ResourceOpenFile       Resource = "open_file"
	ResourceTemporaryDisk  Resource = "temporary_disk"
)

type Governor struct {
	mu     sync.Mutex
	limits map[Resource]int64
	used   map[Resource]int64
}

func NewGovernor(cfg config.ResourceLimitsConfig) *Governor {
	return &Governor{
		limits: map[Resource]int64{
			ResourceWorkflow:       int64(cfg.MaxConcurrentWorkflows),
			ResourceModelCall:      int64(cfg.MaxModelCalls),
			ResourceBrowserSession: int64(cfg.MaxBrowserSessions),
			ResourceCodingAgent:    int64(cfg.MaxCodingAgents),
			ResourceMCPCall:        int64(cfg.MaxMCPCalls),
			ResourceOpenFile:       int64(cfg.MaxOpenFiles),
			ResourceTemporaryDisk:  cfg.MaxTemporaryDiskBytes,
		},
		used: map[Resource]int64{},
	}
}

func (g *Governor) Acquire(resource Resource, amount int64) (func(), error) {
	if g == nil {
		return func() {}, nil
	}
	if amount <= 0 {
		amount = 1
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	limit := g.limits[resource]
	if limit > 0 && g.used[resource]+amount > limit {
		return nil, ErrResourceLimitExceeded
	}
	g.used[resource] += amount
	released := false
	return func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		if released {
			return
		}
		g.used[resource] -= amount
		if g.used[resource] < 0 {
			g.used[resource] = 0
		}
		released = true
	}, nil
}

func (g *Governor) Snapshot() map[string]int64 {
	out := map[string]int64{}
	if g == nil {
		return out
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for resource, value := range g.used {
		out[string(resource)] = value
	}
	return out
}

func (g *Governor) Ready() bool {
	if g == nil {
		return true
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for resource, used := range g.used {
		limit := g.limits[resource]
		if limit > 0 && used > limit {
			return false
		}
	}
	return true
}
