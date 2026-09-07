package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"agent/internal/agent"
	"agent/internal/capability"
	"agent/internal/model"
	"agent/internal/orchestrator"
)

type Planner interface {
	Plan(ctx context.Context, req RunRequest) ([]agent.TaskNode, model.Usage, error)
}

type BoundedModelPlanner struct {
	Provider        model.ChatProvider
	Model           model.ModelMetadata
	Capabilities    *capability.Registry
	Temperature     float64
	MaxOutputTokens int
	MaxNodes        int
}

type plannerResponse struct {
	Nodes []plannerNode `json:"nodes"`
}

type plannerNode struct {
	ID           string   `json:"id"`
	CapabilityID string   `json:"capability_id"`
	Role         string   `json:"role"`
	Dependencies []string `json:"dependencies"`
}

func (p BoundedModelPlanner) Plan(ctx context.Context, req RunRequest) ([]agent.TaskNode, model.Usage, error) {
	if p.Provider == nil {
		return nil, model.Usage{}, model.ErrUnsupportedOperation
	}
	if p.Capabilities == nil {
		return nil, model.Usage{}, ErrWorkflowCapabilityUnknown
	}
	response, err := p.Provider.Chat(ctx, model.ChatRequest{
		Model: p.Model,
		Messages: []model.ChatMessage{
			{Role: "system", Content: p.systemPrompt()},
			{Role: "user", Content: plannerUserPrompt(req)},
		},
		Temperature:     p.Temperature,
		MaxOutputTokens: p.outputTokenLimit(req),
	})
	if err != nil {
		return nil, response.Usage, err
	}
	var decoded plannerResponse
	if err := json.Unmarshal([]byte(response.Content), &decoded); err != nil {
		return nil, response.Usage, err
	}
	maxNodes := p.MaxNodes
	if maxNodes <= 0 {
		maxNodes = 8
	}
	if len(decoded.Nodes) == 0 || len(decoded.Nodes) > maxNodes {
		return nil, response.Usage, fmt.Errorf("planner returned %d nodes outside limit %d", len(decoded.Nodes), maxNodes)
	}
	nodes := make([]agent.TaskNode, 0, len(decoded.Nodes))
	for _, node := range decoded.Nodes {
		capabilityDef, ok := p.Capabilities.Lookup(node.CapabilityID)
		if !ok {
			return nil, response.Usage, fmt.Errorf("%w: %s", ErrWorkflowCapabilityUnknown, node.CapabilityID)
		}
		if !capabilityDef.Enabled {
			return nil, response.Usage, fmt.Errorf("%w: %s disabled", ErrWorkflowCapabilityDenied, node.CapabilityID)
		}
		nodes = append(nodes, agent.TaskNode{
			TaskID:       req.TaskID,
			ID:           node.ID,
			Type:         node.CapabilityID,
			Role:         roleForCapability(node.CapabilityID, node.Role),
			Input:        req.Input,
			Dependencies: append([]string(nil), node.Dependencies...),
			MaxAttempts:  1,
		})
	}
	if _, err := orchestrator.NewDAG(nodes); err != nil {
		return nil, response.Usage, err
	}
	return nodes, response.Usage, nil
}

func (p BoundedModelPlanner) outputTokenLimit(req RunRequest) int {
	if req.MaxTokens > 0 {
		return req.MaxTokens
	}
	return p.MaxOutputTokens
}

func (p BoundedModelPlanner) systemPrompt() string {
	capabilities := "none"
	if p.Capabilities != nil {
		list := p.Capabilities.List("")
		ids := make([]string, 0, len(list))
		for _, item := range list {
			if item.Enabled {
				ids = append(ids, item.ID)
			}
		}
		if len(ids) > 0 {
			capabilities = strings.Join(ids, ", ")
		}
	}
	return "Return only JSON with this exact shape: {\"nodes\":[{\"id\":\"...\",\"capability_id\":\"...\",\"role\":\"...\",\"dependencies\":[\"...\"]}]}. " +
		"Use only these enabled capability_id values: " + capabilities + ". " +
		"Do not invent tools, endpoints, or capabilities. Keep the graph bounded, acyclic, and necessary for the task. " +
		"If local memory or local RAG is sufficient, prefer local read-only capabilities."
}

func plannerUserPrompt(req RunRequest) string {
	return fmt.Sprintf("task_id: %s\nprivacy_class: %s\nleader_model_id: %s\nmax_iterations: %d\nmax_tokens: %d\nmax_cost_usd: %.6f\ninput:\n%s",
		req.TaskID,
		defaultPrivacyClass(req.PrivacyClass),
		req.LeaderModelID,
		req.MaxIterations,
		req.MaxTokens,
		req.MaxCostUSD,
		req.Input,
	)
}
