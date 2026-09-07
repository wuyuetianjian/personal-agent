package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"agent/internal/agent"
	"agent/internal/model"
)

type reasoningResponse struct {
	Claims          []reasoningClaim `json:"claims"`
	DecisionSummary string           `json:"decision_summary"`
	Confidence      float64          `json:"confidence"`
	EvidenceIDs     []string         `json:"evidence_ids"`
}

type reasoningClaim struct {
	Text        string   `json:"text"`
	Confidence  float64  `json:"confidence"`
	EvidenceIDs []string `json:"evidence_ids"`
}

func (r *Runtime) runReasoningAgent(ctx context.Context, node agent.TaskNode, evidence []Evidence, input workflowInput) (agent.Result, error) {
	if r.ChatProvider == nil {
		text := "reasoning completed with deterministic local evidence review"
		return agent.Result{Text: text, EvidenceIDs: evidenceIDs(evidence), Usage: agentUsage(node.Input, text)}, nil
	}
	maxTokens := r.modelMaxOutputTokens("reasoning", input.MaxOutputTokens, 1024)
	response, err := r.ChatProvider.Chat(ctx, model.ChatRequest{
		Model: r.ChatModel,
		Messages: []model.ChatMessage{
			{Role: "system", Content: "Return only JSON: {\"claims\":[{\"text\":\"...\",\"confidence\":0.0,\"evidence_ids\":[\"...\"]}],\"decision_summary\":\"...\",\"confidence\":0.0,\"evidence_ids\":[\"...\"]}. Do not include chain-of-thought or hidden reasoning."},
			{Role: "user", Content: fmt.Sprintf("task:\n%s\n\nconstraints:\nprivacy_class=%s\nmax_tokens=%d\nmax_cost_usd=%.6f\n\npersisted_evidence:\n%s", node.Input, defaultPrivacyClass(r.PrivacyClass), maxTokens, input.MaxCostUSD, compactEvidenceForModel(evidence))},
		},
		Temperature:     r.modelTemperature("reasoning"),
		MaxOutputTokens: maxTokens,
	})
	if err != nil {
		return agent.Result{ErrorCategory: agent.ErrorRetryable, ErrorMessage: err.Error(), Usage: usageFromModel(response.Usage)}, err
	}
	var decoded reasoningResponse
	if err := json.Unmarshal([]byte(response.Content), &decoded); err != nil {
		return agent.Result{ErrorCategory: agent.ErrorRetryable, ErrorMessage: err.Error(), Usage: usageFromModel(response.Usage)}, err
	}
	if strings.TrimSpace(decoded.DecisionSummary) == "" {
		decoded.DecisionSummary = "reasoning completed"
	}
	ids := filterEvidenceIDs(decoded.EvidenceIDs, evidence)
	if len(ids) == 0 {
		ids = evidenceIDs(evidence)
	}
	claims := make([]agent.Claim, 0, len(decoded.Claims))
	for _, claim := range decoded.Claims {
		if strings.TrimSpace(claim.Text) == "" {
			continue
		}
		claims = append(claims, agent.Claim{
			Text:        claim.Text,
			Confidence:  claim.Confidence,
			EvidenceIDs: filterEvidenceIDs(claim.EvidenceIDs, evidence),
		})
	}
	modelEvidence := Evidence{
		ID:           stableEvidenceID(node.TaskID, string(SourceModel), node.ID),
		TaskID:       node.TaskID,
		NodeID:       node.ID,
		Claim:        "model reasoning summary",
		SourceType:   SourceModel,
		SourceID:     r.ChatModel.ID,
		Content:      decoded.DecisionSummary,
		Score:        decoded.Confidence,
		Trust:        0.7,
		PrivacyClass: defaultPrivacyClass(r.PrivacyClass),
		CreatedAt:    time.Now().UTC(),
	}
	if err := r.Evidence.Put(ctx, modelEvidence); err != nil {
		return agent.Result{ErrorCategory: agent.ErrorRetryable, ErrorMessage: err.Error(), Usage: usageFromModel(response.Usage)}, err
	}
	ids = append(ids, modelEvidence.ID)
	return agent.Result{
		Text:        decoded.DecisionSummary,
		Claims:      claims,
		EvidenceIDs: ids,
		Usage:       usageFromModel(response.Usage),
	}, nil
}

func (r *Runtime) runSynthesisAgent(ctx context.Context, node agent.TaskNode, evidence []Evidence, input workflowInput) (agent.Result, error) {
	if r.ChatProvider == nil {
		answer, _ := synthesizeLocalAnswer(node.Input, evidence)
		return agent.Result{Text: answer, EvidenceIDs: evidenceIDs(evidence), Usage: agentUsage(node.Input, answer)}, nil
	}
	maxTokens := r.modelMaxOutputTokens("synthesis", input.MaxOutputTokens, 2048)
	response, err := r.ChatProvider.Chat(ctx, model.ChatRequest{
		Model: r.ChatModel,
		Messages: []model.ChatMessage{
			{Role: "system", Content: "Write the final answer using only supported persisted evidence. Exclude unsupported claims. Surface conflicts or insufficient evidence plainly. Do not include chain-of-thought."},
			{Role: "user", Content: fmt.Sprintf("task:\n%s\n\nconstraints:\nprivacy_class=%s\nmax_tokens=%d\nmax_cost_usd=%.6f\n\npersisted_evidence:\n%s", node.Input, defaultPrivacyClass(r.PrivacyClass), maxTokens, input.MaxCostUSD, compactEvidenceForModel(evidence))},
		},
		Temperature:     r.modelTemperature("synthesis"),
		MaxOutputTokens: maxTokens,
	})
	if err != nil {
		return agent.Result{ErrorCategory: agent.ErrorRetryable, ErrorMessage: err.Error(), Usage: usageFromModel(response.Usage)}, err
	}
	answer := strings.TrimSpace(response.Content)
	if answer == "" {
		answer = "No supported answer could be synthesized from the available evidence."
	}
	return agent.Result{Text: answer, EvidenceIDs: evidenceIDs(evidence), Usage: usageFromModel(response.Usage)}, nil
}

func compactEvidenceForModel(evidence []Evidence) string {
	if len(evidence) == 0 {
		return "[]"
	}
	lines := make([]string, 0, len(evidence))
	for _, item := range evidence {
		content := strings.TrimSpace(item.Content)
		if len(content) > 700 {
			content = content[:700]
		}
		lines = append(lines, fmt.Sprintf("- id=%s source=%s trust=%.2f privacy=%s content=%q", item.ID, item.SourceType, item.Trust, defaultPrivacyClass(item.PrivacyClass), content))
	}
	return strings.Join(lines, "\n")
}

func filterEvidenceIDs(ids []string, evidence []Evidence) []string {
	allowed := make(map[string]bool, len(evidence))
	for _, item := range evidence {
		allowed[item.ID] = true
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if allowed[id] {
			out = append(out, id)
		}
	}
	return out
}

func usageFromModel(usage model.Usage) agent.Usage {
	return agent.Usage{InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens}
}

func (r *Runtime) modelTemperature(role string) float64 {
	if settings, ok := r.Config.Agent.SubAgents[role]; ok {
		return settings.Temperature
	}
	return r.Config.Agent.Leader.Temperature
}

func (r *Runtime) modelMaxOutputTokens(role string, requestMax int, fallback int) int {
	if requestMax > 0 {
		return requestMax
	}
	if settings, ok := r.Config.Agent.SubAgents[role]; ok && settings.MaxOutputTokens > 0 {
		return settings.MaxOutputTokens
	}
	if r.Config.Agent.Leader.MaxOutputTokens > 0 {
		return r.Config.Agent.Leader.MaxOutputTokens
	}
	return fallback
}
