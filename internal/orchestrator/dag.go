package orchestrator

import (
	"errors"
	"fmt"
	"sort"

	"agent/internal/agent"
)

var (
	ErrDuplicateNodeID   = errors.New("duplicate node id")
	ErrMissingNodeID     = errors.New("missing node id")
	ErrMissingDependency = errors.New("missing dependency")
	ErrSelfDependency    = errors.New("self dependency")
	ErrCycleDetected     = errors.New("dag cycle detected")
)

type DAG struct {
	nodes      map[string]agent.TaskNode
	order      []string
	dependents map[string][]string
}

func NewDAG(nodes []agent.TaskNode) (*DAG, error) {
	dag := &DAG{
		nodes:      make(map[string]agent.TaskNode, len(nodes)),
		order:      make([]string, 0, len(nodes)),
		dependents: make(map[string][]string),
	}
	for _, node := range nodes {
		if node.ID == "" {
			return nil, ErrMissingNodeID
		}
		if _, exists := dag.nodes[node.ID]; exists {
			return nil, fmt.Errorf("%w: %s", ErrDuplicateNodeID, node.ID)
		}
		dag.nodes[node.ID] = normalizeNode(node)
		dag.order = append(dag.order, node.ID)
	}
	for _, node := range dag.nodes {
		for _, depID := range node.Dependencies {
			if depID == node.ID {
				return nil, fmt.Errorf("%w: %s", ErrSelfDependency, node.ID)
			}
			if _, ok := dag.nodes[depID]; !ok {
				return nil, fmt.Errorf("%w: %s depends on %s", ErrMissingDependency, node.ID, depID)
			}
			dag.dependents[depID] = append(dag.dependents[depID], node.ID)
		}
	}
	for id := range dag.dependents {
		sort.Strings(dag.dependents[id])
	}
	if err := dag.rejectCycles(); err != nil {
		return nil, err
	}
	return dag, nil
}

func (d *DAG) Nodes() []agent.TaskNode {
	nodes := make([]agent.TaskNode, 0, len(d.order))
	for _, id := range d.order {
		nodes = append(nodes, d.nodes[id])
	}
	return nodes
}

func (d *DAG) Node(id string) (agent.TaskNode, bool) {
	node, ok := d.nodes[id]
	return node, ok
}

func (d *DAG) Dependents(id string) []string {
	values := d.dependents[id]
	out := make([]string, len(values))
	copy(out, values)
	return out
}

func (d *DAG) Ready(completed map[string]bool, blocked map[string]bool) []agent.TaskNode {
	ready := make([]agent.TaskNode, 0)
	for _, id := range d.order {
		if completed[id] || blocked[id] {
			continue
		}
		node := d.nodes[id]
		ok := true
		for _, depID := range node.Dependencies {
			if !completed[depID] {
				ok = false
				break
			}
		}
		if ok {
			ready = append(ready, node)
		}
	}
	return ready
}

func normalizeNode(node agent.TaskNode) agent.TaskNode {
	if node.MaxAttempts <= 0 {
		node.MaxAttempts = 1
	}
	return node
}

func (d *DAG) rejectCycles() error {
	const (
		unvisited = 0
		visiting  = 1
		visited   = 2
	)
	state := make(map[string]int, len(d.nodes))
	var visit func(string) error
	visit = func(id string) error {
		switch state[id] {
		case visiting:
			return fmt.Errorf("%w: %s", ErrCycleDetected, id)
		case visited:
			return nil
		}
		state[id] = visiting
		for _, depID := range d.nodes[id].Dependencies {
			if err := visit(depID); err != nil {
				return err
			}
		}
		state[id] = visited
		return nil
	}
	for _, id := range d.order {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}
