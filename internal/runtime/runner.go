package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"agent/internal/agent"
	"agent/internal/observability"
	"agent/internal/orchestrator"
	"agent/internal/project"
	"agent/internal/skill"
	"agent/internal/verification"
	"agent/internal/workflow"
)

func (r *Runtime) Run(ctx context.Context, req RunRequest) (*RunResult, error) {
	if req.TaskID == "" {
		return nil, fmt.Errorf("runtime run requires task id")
	}
	if strings.TrimSpace(req.Input) == "" {
		return nil, fmt.Errorf("runtime run requires input")
	}
	release, err := r.Governor.Acquire("workflow", 1)
	if err != nil {
		return nil, err
	}
	defer release()
	traceCtx, finish := r.Tracer.Start(ctx, observabilitySpan(req.TaskID, "runtime.run", "workflow"))
	ctx = traceCtx
	defer func() { finish(nil) }()
	if err := r.publish(ctx, req.TaskID, orchestrator.EventNodeStarted, "task", agent.RoleLeader, nil); err != nil {
		return nil, err
	}
	if r.Workflow != nil {
		return r.runViaWorkflow(ctx, req)
	}

	memoryEvidence, err := r.Memory.Search(ctx, req.TaskID, req.Input, 8)
	if err != nil {
		_ = r.Storage.FailTask(ctx, req.TaskID, "memory_failed", err.Error())
		return nil, err
	}
	if err := r.publish(ctx, req.TaskID, orchestrator.EventNodeCompleted, "memory", agent.RoleMemory, evidenceIDs(memoryEvidence)); err != nil {
		return nil, err
	}
	ragEvidence, err := r.RAG.Retrieve(ctx, req.TaskID, req.Input, RetrieveOptions{TopK: 8})
	if err != nil {
		_ = r.Storage.FailTask(ctx, req.TaskID, "retrieval_failed", err.Error())
		return nil, err
	}
	if err := r.publish(ctx, req.TaskID, orchestrator.EventNodeCompleted, "retrieval", agent.RoleRetrieval, evidenceIDs(ragEvidence)); err != nil {
		return nil, err
	}

	allEvidence := dedupeEvidence(append(memoryEvidence, ragEvidence...))
	for i := range allEvidence {
		allEvidence[i].PrivacyClass = defaultPrivacyClass(allEvidence[i].PrivacyClass)
		if allEvidence[i].TaskID == "" {
			allEvidence[i].TaskID = req.TaskID
		}
		if err := r.Evidence.Put(ctx, allEvidence[i]); err != nil {
			_ = r.Storage.FailTask(ctx, req.TaskID, "evidence_failed", err.Error())
			return nil, err
		}
	}

	answer, confidence := synthesizeLocalAnswer(req.Input, allEvidence)
	remoteUsage := modelUsage{}
	if len(allEvidence) == 0 && r.Escalator != nil {
		if err := r.validateProjectEscalation(ctx, req); err != nil {
			_ = r.Storage.FailTask(ctx, req.TaskID, "project_policy_denied", err.Error())
			return nil, err
		}
		escalated, usage, err := r.Escalator.Escalate(ctx, req.Input, allEvidence)
		if err != nil {
			_ = r.Storage.FailTask(ctx, req.TaskID, "public_escalation_failed", err.Error())
			return nil, err
		}
		answer = escalated
		confidence = 0.55
		remoteUsage = modelUsage{input: usage.InputTokens, output: usage.OutputTokens}
	}
	claim := verification.Claim{
		ID:          "claim_final_answer",
		Text:        finalClaimText(req.Input, allEvidence),
		Confidence:  confidence,
		EvidenceIDs: evidenceIDs(allEvidence),
	}
	report := r.Verifier.Verify([]verification.Claim{claim}, verificationEvidence(allEvidence, claim.Text))
	if !report.PassesPolicy && len(allEvidence) > 0 {
		confidence = 0.7
	}
	if len(allEvidence) == 0 && remoteUsage.input+remoteUsage.output == 0 {
		confidence = 0.35
	}
	if err := r.publish(ctx, req.TaskID, orchestrator.EventNodeCompleted, "verification", agent.RoleVerification, evidenceIDs(allEvidence)); err != nil {
		return nil, err
	}
	if err := r.Storage.CompleteTask(ctx, req.TaskID, answer, confidence); err != nil {
		return nil, err
	}
	if err := r.publish(ctx, req.TaskID, orchestrator.EventNodeCompleted, "synthesis", agent.RoleSynthesis, evidenceIDs(allEvidence)); err != nil {
		return nil, err
	}
	result := &RunResult{
		TaskID:         req.TaskID,
		Answer:         answer,
		Confidence:     confidence,
		EvidenceIDs:    evidenceIDs(allEvidence),
		Verification:   report,
		LocalRouteType: RouteMixed,
	}
	result.Usage.InputTokens = estimateTokens(req.Input)
	result.Usage.OutputTokens = estimateTokens(answer)
	result.Usage.RemoteTokens = remoteUsage.input + remoteUsage.output
	if result.Usage.RemoteTokens > 0 {
		result.RemoteCalls = 1
	}
	return result, nil
}

type modelUsage struct {
	input  int
	output int
}

func (r *Runtime) runViaWorkflow(ctx context.Context, req RunRequest) (*RunResult, error) {
	workflowID, err := randomID("wf")
	if err != nil {
		return nil, err
	}
	input, err := json.Marshal(workflowInput{
		Input:           req.Input,
		MaxInputTokens:  req.MaxTokens,
		MaxOutputTokens: req.MaxTokens,
		MaxCostUSD:      req.MaxCostUSD,
	})
	if err != nil {
		return nil, err
	}
	plan, err := r.planWorkflow(ctx, workflowID, req)
	if err != nil {
		_ = r.Storage.FailTask(ctx, req.TaskID, "planning_failed", err.Error())
		return nil, err
	}
	if err := r.Workflows.Create(ctx, workflow.Run{
		ID:           workflowID,
		TaskID:       req.TaskID,
		ProjectID:    req.ProjectID,
		Status:       workflow.StatusPending,
		InputJSON:    string(input),
		SkillID:      plan.SkillID,
		SkillVersion: plan.SkillVersion,
	}, plan.Nodes); err != nil {
		_ = r.Storage.FailTask(ctx, req.TaskID, "workflow_create_failed", err.Error())
		return nil, err
	}
	if err := r.Workflow.RunWorkflow(ctx, workflowID); err != nil {
		_ = r.Storage.FailTask(ctx, req.TaskID, "workflow_failed", err.Error())
		return nil, err
	}
	runState, _ := r.Workflows.Get(ctx, workflowID)
	allEvidence, err := r.Evidence.ListByTask(ctx, req.TaskID)
	if err != nil {
		_ = r.Storage.FailTask(ctx, req.TaskID, "evidence_failed", err.Error())
		return nil, err
	}
	answer, confidence := synthesizeLocalAnswer(req.Input, allEvidence)
	synthesisUsage := agent.Usage{}
	if checkpoint, ok := r.latestCompletedCheckpoint(ctx, workflowID, "synthesis"); ok && strings.TrimSpace(checkpoint.ResultRef) != "" {
		answer = checkpoint.ResultRef
		confidence = 0.75
		synthesisUsage = agentUsageFromJSON(checkpoint.UsageJSON)
	}
	remoteUsage := modelUsage{}
	if len(allEvidence) == 0 && r.Escalator != nil {
		if err := r.validateProjectEscalation(ctx, req); err != nil {
			_ = r.Storage.FailTask(ctx, req.TaskID, "project_policy_denied", err.Error())
			return nil, err
		}
		escalated, usage, err := r.Escalator.Escalate(ctx, req.Input, allEvidence)
		if err != nil {
			_ = r.Storage.FailTask(ctx, req.TaskID, "public_escalation_failed", err.Error())
			return nil, err
		}
		answer = escalated
		confidence = 0.55
		remoteUsage = modelUsage{input: usage.InputTokens, output: usage.OutputTokens}
	}
	claim := verification.Claim{
		ID:          "claim_final_answer",
		Text:        finalClaimText(req.Input, allEvidence),
		Confidence:  confidence,
		EvidenceIDs: evidenceIDs(allEvidence),
	}
	report := r.Verifier.Verify([]verification.Claim{claim}, verificationEvidence(allEvidence, claim.Text))
	if !report.PassesPolicy && len(allEvidence) > 0 {
		confidence = 0.7
	}
	if len(allEvidence) == 0 && remoteUsage.input+remoteUsage.output == 0 {
		confidence = 0.35
	}
	if err := r.Storage.CompleteTask(ctx, req.TaskID, answer, confidence); err != nil {
		return nil, err
	}
	result := &RunResult{
		TaskID:         req.TaskID,
		Answer:         answer,
		Confidence:     confidence,
		EvidenceIDs:    evidenceIDs(allEvidence),
		Verification:   report,
		EarlyStopped:   runState.Status == workflow.StatusCancelled,
		LocalRouteType: RouteMixed,
	}
	result.Usage.InputTokens = estimateTokens(req.Input)
	result.Usage.OutputTokens = estimateTokens(answer)
	if synthesisUsage.InputTokens > 0 || synthesisUsage.OutputTokens > 0 {
		result.Usage.InputTokens = synthesisUsage.InputTokens
		result.Usage.OutputTokens = synthesisUsage.OutputTokens
	}
	result.Usage.RemoteTokens = remoteUsage.input + remoteUsage.output
	if result.Usage.RemoteTokens > 0 {
		result.RemoteCalls = 1
	}
	return result, nil
}

func (r *Runtime) latestCompletedCheckpoint(ctx context.Context, workflowID string, nodeID string) (workflow.Checkpoint, bool) {
	checkpoints, err := r.Workflows.ListCheckpoints(ctx, workflowID)
	if err != nil {
		return workflow.Checkpoint{}, false
	}
	for i := len(checkpoints) - 1; i >= 0; i-- {
		if checkpoints[i].NodeID == nodeID && checkpoints[i].Status == workflow.NodeCompleted {
			return checkpoints[i], true
		}
	}
	return workflow.Checkpoint{}, false
}

func agentUsageFromJSON(raw string) agent.Usage {
	var usage agent.Usage
	if strings.TrimSpace(raw) == "" {
		return usage
	}
	if err := json.Unmarshal([]byte(raw), &usage); err != nil {
		return agent.Usage{}
	}
	return usage
}

type workflowPlan struct {
	Nodes        []workflow.Node
	SkillID      string
	SkillVersion string
}

func (r *Runtime) planWorkflow(ctx context.Context, workflowID string, req RunRequest) (workflowPlan, error) {
	proj, err := r.projectForRun(ctx, req.ProjectID)
	if err != nil {
		return workflowPlan{}, err
	}
	if err := r.validateProjectModel(proj, req); err != nil {
		return workflowPlan{}, err
	}
	if plan, matched, err := r.planSkillWorkflow(ctx, workflowID, req, proj); matched || err != nil {
		return plan, err
	}
	nodes, err := r.planWorkflowNodes(ctx, workflowID, req)
	if err != nil {
		return workflowPlan{}, err
	}
	return workflowPlan{Nodes: nodes, SkillID: "runtime.default", SkillVersion: "v1"}, nil
}

const highConfidenceSkillMatch = 1.0

func (r *Runtime) planSkillWorkflow(ctx context.Context, workflowID string, req RunRequest, proj project.Project) (workflowPlan, bool, error) {
	if err := ctx.Err(); err != nil {
		return workflowPlan{}, false, err
	}
	if r.Skills == nil || r.Capabilities == nil {
		return workflowPlan{}, false, nil
	}
	matches := r.Skills.Match(skill.MatchRequest{
		Intent:          req.Input,
		PrivacyClass:    defaultPrivacyClass(req.PrivacyClass),
		AllowedSkillIDs: proj.AllowedSkills,
	})
	for _, match := range matches {
		if proj.ID != "" && !proj.AllowsSkill(match.Manifest.ID) {
			return workflowPlan{}, true, fmt.Errorf("%w: skill %s not allowed by project %s", ErrWorkflowProjectDenied, match.Manifest.ID, proj.ID)
		}
		if match.Score < highConfidenceSkillMatch || !r.skillIsReadOnly(match.Manifest) {
			continue
		}
		nodes, err := r.compileSkillWorkflow(workflowID, match.Manifest)
		if err != nil {
			return workflowPlan{}, true, err
		}
		return workflowPlan{Nodes: nodes, SkillID: match.Manifest.ID, SkillVersion: match.Manifest.Version}, true, nil
	}
	return workflowPlan{}, false, nil
}

func (r *Runtime) skillIsReadOnly(manifest skill.Manifest) bool {
	if manifest.Permissions.MaxLevel != "" && manifest.Permissions.MaxLevel != "read_only" {
		return false
	}
	if len(manifest.Workflow.Nodes) == 0 {
		return false
	}
	for _, node := range manifest.Workflow.Nodes {
		if node.Capability == "" {
			return false
		}
		capDef, ok := r.Capabilities.Lookup(node.Capability)
		if !ok || !capDef.Enabled || capDef.Health == "unavailable" {
			return false
		}
		if capDef.SideEffectLevel != "" && capDef.SideEffectLevel != "read_only" {
			return false
		}
	}
	return true
}

func (r *Runtime) projectForRun(ctx context.Context, projectID string) (project.Project, error) {
	if strings.TrimSpace(projectID) == "" {
		return project.Project{}, nil
	}
	proj, err := r.Projects.Get(ctx, projectID)
	if err != nil {
		return project.Project{}, err
	}
	return proj, nil
}

func (r *Runtime) validateProjectModel(proj project.Project, req RunRequest) error {
	if proj.ID == "" {
		return nil
	}
	modelID := req.LeaderModelID
	if modelID == "" {
		modelID = r.LeaderModel
	}
	if strings.TrimSpace(modelID) == "" {
		return nil
	}
	if !proj.AllowsModel(modelID) {
		return fmt.Errorf("%w: leader model %s not allowed by project %s", ErrWorkflowProjectDenied, modelID, proj.ID)
	}
	return nil
}

func (r *Runtime) validateProjectEscalation(ctx context.Context, req RunRequest) error {
	proj, err := r.projectForRun(ctx, req.ProjectID)
	if err != nil {
		return err
	}
	if proj.ID == "" || r.Escalator == nil {
		return nil
	}
	if proj.PrivacyClass == "confidential" {
		return fmt.Errorf("%w: public escalation denied for confidential project %s", ErrWorkflowProjectDenied, proj.ID)
	}
	modelID := r.Escalator.Model.ID
	if modelID != "" && !proj.AllowsModel(modelID) {
		return fmt.Errorf("%w: public escalation model %s not allowed by project %s", ErrWorkflowProjectDenied, modelID, proj.ID)
	}
	return nil
}

func (r *Runtime) compileSkillWorkflow(workflowID string, manifest skill.Manifest) ([]workflow.Node, error) {
	if err := skill.Validate(manifest, r.Capabilities); err != nil {
		return nil, err
	}
	nodes := make([]workflow.Node, 0, len(manifest.Workflow.Nodes))
	for _, node := range manifest.Workflow.Nodes {
		nodes = append(nodes, workflow.Node{
			WorkflowID:     workflowID,
			NodeID:         node.ID,
			CapabilityID:   node.Capability,
			Role:           node.Role,
			Dependencies:   append([]string(nil), node.DependsOn...),
			Status:         workflow.NodePending,
			IdempotencyKey: workflowID + ":" + node.ID,
		})
	}
	return nodes, nil
}

func (r *Runtime) planWorkflowNodes(ctx context.Context, workflowID string, req RunRequest) ([]workflow.Node, error) {
	if r.Planner != nil {
		planned, _, err := r.Planner.Plan(ctx, req)
		if err != nil {
			return nil, err
		}
		return workflowNodesFromAgentNodes(workflowID, planned), nil
	}
	return []workflow.Node{
		{WorkflowID: workflowID, NodeID: "memory", CapabilityID: "memory.search", Role: string(agent.RoleMemory), Status: workflow.NodePending, IdempotencyKey: workflowID + ":memory"},
		{WorkflowID: workflowID, NodeID: "retrieval", CapabilityID: "rag.search", Role: string(agent.RoleRetrieval), Status: workflow.NodePending, IdempotencyKey: workflowID + ":retrieval"},
		{WorkflowID: workflowID, NodeID: "reasoning", CapabilityID: "reasoning.local", Role: string(agent.RoleReasoning), Dependencies: []string{"memory", "retrieval"}, Status: workflow.NodePending, IdempotencyKey: workflowID + ":reasoning"},
		{WorkflowID: workflowID, NodeID: "verification", CapabilityID: "verification.verify", Role: string(agent.RoleVerification), Dependencies: []string{"reasoning"}, Status: workflow.NodePending, IdempotencyKey: workflowID + ":verification"},
		{WorkflowID: workflowID, NodeID: "synthesis", CapabilityID: "synthesis.local", Role: string(agent.RoleSynthesis), Dependencies: []string{"verification"}, Status: workflow.NodePending, IdempotencyKey: workflowID + ":synthesis"},
	}, nil
}

func workflowNodesFromAgentNodes(workflowID string, nodes []agent.TaskNode) []workflow.Node {
	out := make([]workflow.Node, 0, len(nodes))
	for _, node := range nodes {
		out = append(out, workflow.Node{
			WorkflowID:     workflowID,
			NodeID:         node.ID,
			CapabilityID:   node.Type,
			Role:           string(node.Role),
			Dependencies:   append([]string(nil), node.Dependencies...),
			Status:         workflow.NodePending,
			IdempotencyKey: workflowID + ":" + node.ID,
		})
	}
	return out
}

func randomID(prefix string) (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return prefix + "_" + hex.EncodeToString(b[:]), nil
}

func observabilitySpan(taskID, name, kind string) observability.TraceSpan {
	return observability.TraceSpan{
		Name:   name,
		TaskID: taskID,
		Kind:   kind,
	}
}

func (r *Runtime) publish(ctx context.Context, taskID string, eventType orchestrator.EventType, nodeID string, role agent.Role, ids []string) error {
	if r.Events == nil {
		return nil
	}
	return r.Events.Publish(ctx, orchestrator.Event{
		Type:        eventType,
		TaskID:      taskID,
		NodeID:      nodeID,
		Role:        role,
		EvidenceIDs: append([]string(nil), ids...),
	})
}

func synthesizeLocalAnswer(input string, evidence []Evidence) (string, float64) {
	if len(evidence) == 0 {
		return "No local evidence was found for: " + input, 0.35
	}
	var b strings.Builder
	b.WriteString("Local-first answer for: ")
	b.WriteString(input)
	b.WriteString("\n\nEvidence summary:")
	for i, item := range evidence {
		if i >= 5 {
			break
		}
		b.WriteString("\n- [")
		b.WriteString(item.ID)
		b.WriteString("] ")
		b.WriteString(strings.TrimSpace(item.Content))
	}
	b.WriteString("\n\nPublic model calls: 0")
	return b.String(), 0.9
}

func finalClaimText(input string, evidence []Evidence) string {
	if len(evidence) == 0 {
		return "No local evidence was found for: " + input
	}
	return "Local evidence was found for: " + input
}

func verificationEvidence(evidence []Evidence, claimText string) []verification.Evidence {
	out := make([]verification.Evidence, 0, len(evidence))
	for _, item := range evidence {
		out = append(out, verification.Evidence{
			ID:       item.ID,
			Text:     item.Content,
			Supports: []string{claimText},
		})
	}
	return out
}

func evidenceIDs(evidence []Evidence) []string {
	ids := make([]string, 0, len(evidence))
	for _, item := range evidence {
		if item.ID != "" {
			ids = append(ids, item.ID)
		}
	}
	return ids
}

func dedupeEvidence(items []Evidence) []Evidence {
	seen := make(map[string]bool, len(items))
	out := make([]Evidence, 0, len(items))
	for _, item := range items {
		if item.ID == "" || seen[item.ID] {
			continue
		}
		seen[item.ID] = true
		out = append(out, item)
	}
	return out
}

func estimateTokens(text string) int {
	words := len(strings.Fields(text))
	if words == 0 && strings.TrimSpace(text) != "" {
		return 1
	}
	return words
}
