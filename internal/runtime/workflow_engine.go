package runtime

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"agent/internal/agent"
	"agent/internal/capability"
	"agent/internal/cost"
	"agent/internal/orchestrator"
	"agent/internal/project"
	"agent/internal/workflow"
)

var (
	ErrWorkflowEngineUnavailable = errors.New("workflow engine is unavailable")
	ErrWorkflowCapabilityDenied  = errors.New("workflow capability denied")
	ErrWorkflowCapabilityUnknown = errors.New("workflow capability unknown")
	ErrWorkflowBudgetExceeded    = errors.New("workflow budget exceeded")
)

type WorkflowEngine struct {
	Runtime      *Runtime
	Workflows    workflow.Store
	Projects     project.Store
	CostEnforcer cost.Enforcer
	Parallelism  int
}

type workflowInput struct {
	Input           string  `json:"input"`
	MaxInputTokens  int     `json:"max_input_tokens"`
	MaxOutputTokens int     `json:"max_output_tokens"`
	MaxCostUSD      float64 `json:"max_cost_usd"`
}

func NewWorkflowEngine(rt *Runtime) *WorkflowEngine {
	return &WorkflowEngine{
		Runtime:      rt,
		Workflows:    rt.Workflows,
		Projects:     rt.Projects,
		CostEnforcer: cost.Enforcer{},
		Parallelism:  2,
	}
}

func (e *WorkflowEngine) RunWorkflow(ctx context.Context, workflowID string) error {
	if e == nil || e.Runtime == nil {
		return ErrWorkflowEngineUnavailable
	}
	run, err := e.Workflows.Get(ctx, workflowID)
	if err != nil {
		return err
	}
	input := parseWorkflowInput(run.InputJSON)
	nodes, err := e.Workflows.Recover(ctx, workflowID)
	if err != nil {
		return err
	}
	if len(nodes) == 0 {
		return e.Workflows.UpdateStatus(ctx, workflowID, workflow.StatusCompleted)
	}
	proj, err := e.project(ctx, run.ProjectID)
	if err != nil {
		_ = e.Workflows.UpdateStatus(ctx, workflowID, workflow.StatusFailed)
		return err
	}
	taskNodes := make([]agent.TaskNode, 0, len(nodes))
	for _, node := range nodes {
		if err := e.validateNode(ctx, proj, node); err != nil {
			_ = e.Workflows.SaveCheckpoint(ctx, workflow.Checkpoint{
				WorkflowID: workflowID,
				NodeID:     node.NodeID,
				Status:     workflow.NodeFailed,
				ResultRef:  string(err.Error()),
			})
			_ = e.Workflows.UpdateStatus(ctx, workflowID, workflow.StatusFailed)
			return err
		}
		nodeInput := input.Input
		if strings.TrimSpace(nodeInput) == "" {
			nodeInput = run.InputJSON
		}
		taskNodes = append(taskNodes, agent.TaskNode{
			TaskID:       run.TaskID,
			ID:           node.NodeID,
			Role:         roleForCapability(node.CapabilityID, node.Role),
			Type:         node.CapabilityID,
			Input:        nodeInput,
			Dependencies: append([]string(nil), node.Dependencies...),
			MaxAttempts:  max(1, node.Attempt+1),
			Metadata: map[string]string{
				"workflow_id":     workflowID,
				"capability_id":   node.CapabilityID,
				"idempotency_key": node.IdempotencyKey,
			},
		})
	}
	dag, err := orchestrator.NewDAG(taskNodes)
	if err != nil {
		_ = e.Workflows.UpdateStatus(ctx, workflowID, workflow.StatusFailed)
		return err
	}
	if err := e.Workflows.UpdateStatus(ctx, workflowID, workflow.StatusRunning); err != nil {
		return err
	}
	agents := e.localAgents(workflowID, input)
	result, err := (orchestrator.Scheduler{
		Agents:      agents,
		EvidenceBus: e.Runtime.Events,
		Parallelism: e.Parallelism,
	}).Run(ctx, dag)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			_ = e.Workflows.UpdateStatus(context.Background(), workflowID, workflow.StatusCancelled)
		} else {
			_ = e.Workflows.UpdateStatus(ctx, workflowID, workflow.StatusFailed)
		}
		return err
	}
	if len(result.Failed) > 0 {
		_ = e.Workflows.UpdateStatus(ctx, workflowID, workflow.StatusFailed)
		return fmt.Errorf("workflow %s failed", workflowID)
	}
	if len(result.Cancelled) > 0 {
		return e.Workflows.UpdateStatus(ctx, workflowID, workflow.StatusCancelled)
	}
	return e.Workflows.UpdateStatus(ctx, workflowID, workflow.StatusCompleted)
}

func (e *WorkflowEngine) project(ctx context.Context, projectID string) (project.Project, error) {
	if projectID == "" {
		return project.Project{}, nil
	}
	proj, err := e.Projects.Get(ctx, projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return project.Project{}, nil
	}
	return proj, err
}

func (e *WorkflowEngine) validateNode(ctx context.Context, proj project.Project, node workflow.Node) error {
	capabilityID := node.CapabilityID
	capDef, ok := e.Runtime.Capabilities.Lookup(capabilityID)
	if !ok {
		return fmt.Errorf("%w: %s", ErrWorkflowCapabilityUnknown, capabilityID)
	}
	if !capDef.Enabled {
		return fmt.Errorf("%w: %s disabled", ErrWorkflowCapabilityDenied, capabilityID)
	}
	if capDef.Health == capability.HealthUnavailable {
		return fmt.Errorf("%w: %s unavailable", ErrWorkflowCapabilityDenied, capabilityID)
	}
	if proj.ID != "" && !proj.AllowsCapability(capabilityID) {
		return fmt.Errorf("%w: %s not allowed by project %s", ErrWorkflowCapabilityDenied, capabilityID, proj.ID)
	}
	return ctx.Err()
}

func (e *WorkflowEngine) localAgents(workflowID string, input workflowInput) map[agent.Role]agent.SubAgent {
	return map[agent.Role]agent.SubAgent{
		agent.RoleMemory:       workflowSubAgent{role: agent.RoleMemory, engine: e, workflowID: workflowID, input: input},
		agent.RoleRetrieval:    workflowSubAgent{role: agent.RoleRetrieval, engine: e, workflowID: workflowID, input: input},
		agent.RoleVerification: workflowSubAgent{role: agent.RoleVerification, engine: e, workflowID: workflowID, input: input},
		agent.RoleSynthesis:    workflowSubAgent{role: agent.RoleSynthesis, engine: e, workflowID: workflowID, input: input},
	}
}

type workflowSubAgent struct {
	role       agent.Role
	engine     *WorkflowEngine
	workflowID string
	input      workflowInput
}

func (a workflowSubAgent) Role() agent.Role { return a.role }

func (a workflowSubAgent) Execute(ctx context.Context, node agent.TaskNode) (agent.Result, error) {
	usage := cost.Usage{
		TaskID:       node.TaskID,
		NodeID:       node.ID,
		AgentRole:    string(node.Role),
		Operation:    node.Type,
		InputTokens:  estimateTokens(node.Input),
		OutputTokens: 0,
	}
	if check := a.engine.CostEnforcer.Check(a.budget(), usage); check.Status == cost.LimitHardHit {
		result := agent.Result{
			TaskID:        node.TaskID,
			NodeID:        node.ID,
			Role:          node.Role,
			Usage:         agent.Usage{InputTokens: usage.InputTokens},
			ErrorCategory: agent.ErrorBudgetExceeded,
			ErrorMessage:  strings.Join(check.Reasons, "; "),
		}
		_ = a.engine.Workflows.SaveCheckpoint(ctx, workflow.Checkpoint{WorkflowID: a.workflowID, NodeID: node.ID, Status: workflow.NodeFailed, UsageJSON: usageJSON(result), ResultRef: result.ErrorMessage})
		return result, ErrWorkflowBudgetExceeded
	}

	result, err := a.executeLocal(ctx, node)
	if result.TaskID == "" {
		result.TaskID = node.TaskID
	}
	if result.NodeID == "" {
		result.NodeID = node.ID
	}
	if result.Role == "" {
		result.Role = node.Role
	}
	status := workflow.NodeCompleted
	resultRef := result.Text
	if err != nil || result.ErrorCategory != "" {
		status = workflow.NodeFailed
		resultRef = result.ErrorMessage
	}
	if cpErr := a.engine.Workflows.SaveCheckpoint(ctx, workflow.Checkpoint{
		WorkflowID:  a.workflowID,
		NodeID:      node.ID,
		Status:      status,
		EvidenceIDs: result.EvidenceIDs,
		ResultRef:   resultRef,
		UsageJSON:   usageJSON(result),
	}); cpErr != nil && err == nil {
		return result, cpErr
	}
	return result, err
}

func (a workflowSubAgent) budget() cost.Budget {
	return cost.Budget{
		MaxInputTokens:  a.input.MaxInputTokens,
		MaxOutputTokens: a.input.MaxOutputTokens,
		HardLimitUSD:    a.input.MaxCostUSD,
	}
}

func (a workflowSubAgent) executeLocal(ctx context.Context, node agent.TaskNode) (agent.Result, error) {
	switch node.Type {
	case "memory.search":
		evidence, err := a.engine.Runtime.Memory.Search(ctx, node.TaskID, node.Input, 8)
		if err != nil {
			return agent.Result{ErrorCategory: agent.ErrorRetryable, ErrorMessage: err.Error()}, err
		}
		if err := putEvidence(ctx, a.engine.Runtime.Evidence, evidence); err != nil {
			return agent.Result{ErrorCategory: agent.ErrorRetryable, ErrorMessage: err.Error()}, err
		}
		return agent.Result{Text: "memory search completed", EvidenceIDs: evidenceIDs(evidence), Usage: agentUsage(node.Input, "memory search completed")}, nil
	case "rag.search":
		evidence, err := a.engine.Runtime.RAG.Retrieve(ctx, node.TaskID, node.Input, RetrieveOptions{TopK: 8})
		if err != nil {
			return agent.Result{ErrorCategory: agent.ErrorRetryable, ErrorMessage: err.Error()}, err
		}
		if err := putEvidence(ctx, a.engine.Runtime.Evidence, evidence); err != nil {
			return agent.Result{ErrorCategory: agent.ErrorRetryable, ErrorMessage: err.Error()}, err
		}
		return agent.Result{Text: "rag search completed", EvidenceIDs: evidenceIDs(evidence), Usage: agentUsage(node.Input, "rag search completed")}, nil
	case "verification.verify":
		evidence, err := a.engine.Runtime.Evidence.ListByTask(ctx, node.TaskID)
		if err != nil {
			return agent.Result{ErrorCategory: agent.ErrorRetryable, ErrorMessage: err.Error()}, err
		}
		ids := evidenceIDs(evidence)
		return agent.Result{Text: "verification completed", EvidenceIDs: ids, Usage: agentUsage(node.Input, "verification completed")}, nil
	case "synthesis.local":
		evidence, err := a.engine.Runtime.Evidence.ListByTask(ctx, node.TaskID)
		if err != nil {
			return agent.Result{ErrorCategory: agent.ErrorRetryable, ErrorMessage: err.Error()}, err
		}
		answer, _ := synthesizeLocalAnswer(node.Input, evidence)
		return agent.Result{Text: answer, EvidenceIDs: evidenceIDs(evidence), Usage: agentUsage(node.Input, answer)}, nil
	default:
		msg := "unsupported runtime workflow capability: " + node.Type
		return agent.Result{ErrorCategory: agent.ErrorBlockedMissingInput, ErrorMessage: msg}, errors.New(msg)
	}
}

func parseWorkflowInput(raw string) workflowInput {
	var in workflowInput
	if strings.TrimSpace(raw) == "" {
		return in
	}
	if err := json.Unmarshal([]byte(raw), &in); err == nil {
		return in
	}
	in.Input = raw
	return in
}

func roleForCapability(capabilityID string, fallback string) agent.Role {
	switch capabilityID {
	case "memory.search":
		return agent.RoleMemory
	case "rag.search":
		return agent.RoleRetrieval
	case "verification.verify":
		return agent.RoleVerification
	case "synthesis.local":
		return agent.RoleSynthesis
	default:
		return agent.Role(fallback)
	}
}

func putEvidence(ctx context.Context, store EvidenceStore, evidence []Evidence) error {
	for _, item := range evidence {
		if err := store.Put(ctx, item); err != nil {
			return err
		}
	}
	return nil
}

func agentUsage(input string, output string) agent.Usage {
	return agent.Usage{InputTokens: estimateTokens(input), OutputTokens: estimateTokens(output)}
}

func usageJSON(result agent.Result) string {
	raw, err := json.Marshal(result.Usage)
	if err != nil {
		return "{}"
	}
	return string(raw)
}
