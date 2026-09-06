package runtime

import (
	"context"
	"encoding/json"
	"fmt"

	"agent/internal/agent"
	"agent/internal/capability"
	"agent/internal/model"
	"agent/internal/orchestrator"
)

type Planner interface {
	Plan(ctx context.Context, req RunRequest) ([]agent.TaskNode, model.Usage, error)
}

type BoundedModelPlanner struct {
	Provider     model.ChatProvider
	Model        model.ModelMetadata
	Capabilities *capability.Registry
	MaxNodes     int
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
	response, err := p.Provider.Chat(ctx, model.ChatRequest{
		Model: p.Model,
		Messages: []model.ChatMessage{
			{Role: "system", Content: "Return only JSON: {\"nodes\":[{\"id\":\"...\",\"capability_id\":\"...\",\"role\":\"...\",\"dependencies\":[\"...\"]}]}"},
			{Role: "user", Content: req.Input},
		},
		MaxOutputTokens: req.MaxTokens,
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
		if _, ok := p.Capabilities.Lookup(node.CapabilityID); !ok {
			return nil, response.Usage, fmt.Errorf("%w: %s", ErrWorkflowCapabilityUnknown, node.CapabilityID)
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
