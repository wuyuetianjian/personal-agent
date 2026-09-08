package runtime

import (
	"context"
	"encoding/json"

	"agent/internal/agent"
	"agent/internal/cost"
)

type UsageReport struct {
	TaskID       string      `json:"task_id"`
	Workflows    []UsageRun  `json:"workflows"`
	Total        agent.Usage `json:"total"`
	UnknownUsage bool        `json:"unknown_usage"`
}

type UsageRun struct {
	WorkflowID string      `json:"workflow_id"`
	Nodes      []UsageNode `json:"nodes"`
	Total      agent.Usage `json:"total"`
}

type UsageNode struct {
	NodeID      string      `json:"node_id"`
	Status      string      `json:"status"`
	ResultRef   string      `json:"result_ref"`
	Usage       agent.Usage `json:"usage"`
	Unknown     bool        `json:"unknown"`
	EvidenceIDs []string    `json:"evidence_ids"`
}

func (r *Runtime) UsageForTask(ctx context.Context, taskID string) (UsageReport, error) {
	rows, err := r.Storage.SQL.QueryContext(ctx, `SELECT id FROM workflow_runs WHERE task_id = ? ORDER BY started_at ASC, id ASC`, taskID)
	if err != nil {
		return UsageReport{}, err
	}
	defer rows.Close()
	report := UsageReport{TaskID: taskID}
	var workflowIDs []string
	for rows.Next() {
		var workflowID string
		if err := rows.Scan(&workflowID); err != nil {
			return UsageReport{}, err
		}
		workflowIDs = append(workflowIDs, workflowID)
	}
	if err := rows.Err(); err != nil {
		return UsageReport{}, err
	}
	if err := rows.Close(); err != nil {
		return UsageReport{}, err
	}
	for _, workflowID := range workflowIDs {
		run, err := r.UsageForWorkflow(ctx, workflowID)
		if err != nil {
			return UsageReport{}, err
		}
		report.Workflows = append(report.Workflows, run)
		report.Total = addAgentUsage(report.Total, run.Total)
		if hasUnknownUsage(run.Nodes) {
			report.UnknownUsage = true
		}
	}
	return report, nil
}

func (r *Runtime) UsageForWorkflow(ctx context.Context, workflowID string) (UsageRun, error) {
	checkpoints, err := r.Workflows.ListCheckpoints(ctx, workflowID)
	if err != nil {
		return UsageRun{}, err
	}
	run := UsageRun{WorkflowID: workflowID}
	for _, checkpoint := range checkpoints {
		usage := agentUsageFromJSON(checkpoint.UsageJSON)
		node := UsageNode{
			NodeID:      checkpoint.NodeID,
			Status:      string(checkpoint.Status),
			ResultRef:   checkpoint.ResultRef,
			Usage:       usage,
			Unknown:     usage.InputTokens == 0 && usage.OutputTokens == 0 && usage.BillableUnits == 0 && usage.EstimatedCostUSD == 0,
			EvidenceIDs: append([]string(nil), checkpoint.EvidenceIDs...),
		}
		run.Nodes = append(run.Nodes, node)
		run.Total = addAgentUsage(run.Total, usage)
	}
	return run, nil
}

func (r *Runtime) workflowBudget(ctx context.Context, workflowID string, input workflowInput) cost.Budget {
	run, _ := r.UsageForWorkflow(ctx, workflowID)
	return cost.Budget{
		MaxInputTokens:      input.MaxInputTokens,
		MaxOutputTokens:     input.MaxOutputTokens,
		HardLimitUSD:        input.MaxCostUSD,
		CurrentInputTokens:  run.Total.InputTokens,
		CurrentOutputTokens: run.Total.OutputTokens,
		CurrentCostUSD:      run.Total.EstimatedCostUSD,
	}
}

func addAgentUsage(a agent.Usage, b agent.Usage) agent.Usage {
	a.InputTokens += b.InputTokens
	a.OutputTokens += b.OutputTokens
	a.BillableUnits += b.BillableUnits
	a.EstimatedCostUSD += b.EstimatedCostUSD
	return a
}

func hasUnknownUsage(nodes []UsageNode) bool {
	for _, node := range nodes {
		if node.Unknown {
			return true
		}
	}
	return false
}

func usageReportJSON(report UsageReport) string {
	raw, err := json.Marshal(report)
	if err != nil {
		return "{}"
	}
	return string(raw)
}
