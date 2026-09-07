package runtime

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"

	"agent/internal/agent"
	"agent/internal/config"
	"agent/internal/security"
)

type ToolExecutor struct {
	Config   config.ToolConfig
	Evidence EvidenceStore
}

func (e ToolExecutor) Execute(ctx context.Context, node agent.TaskNode, input workflowInput) (agent.Result, error) {
	if !e.Config.Enabled {
		err := errors.New("tool executor disabled")
		return agent.Result{ErrorCategory: agent.ErrorBlockedMissingInput, ErrorMessage: err.Error(), Usage: agentUsage(node.Input, "")}, err
	}
	spec := security.CommandSpec{Program: e.Config.Program, Args: e.Config.Args}
	if err := spec.Validate(); err != nil {
		return agent.Result{ErrorCategory: agent.ErrorBlockedMissingInput, ErrorMessage: err.Error(), Usage: agentUsage(node.Input, "")}, err
	}
	runCtx := ctx
	cancel := func() {}
	if e.Config.Timeout.Duration > 0 {
		runCtx, cancel = context.WithTimeout(ctx, e.Config.Timeout.Duration)
	}
	defer cancel()
	cmd := exec.CommandContext(runCtx, e.Config.Program, e.Config.Args...)
	output, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(output))
	if err != nil {
		return agent.Result{ErrorCategory: agent.ErrorRetryable, ErrorMessage: err.Error(), Usage: agentUsage(node.Input, text)}, err
	}
	if e.Evidence == nil {
		err := errors.New("tool executor evidence store is required")
		return agent.Result{ErrorCategory: agent.ErrorBlockedMissingInput, ErrorMessage: err.Error(), Usage: agentUsage(node.Input, text)}, err
	}
	content := "command: " + e.Config.Program
	if len(e.Config.Args) > 0 {
		content += " " + strings.Join(e.Config.Args, " ")
	}
	if text != "" {
		content += "\noutput:\n" + text
	}
	evidence := Evidence{
		ID:           stableEvidenceID(node.TaskID, string(SourceTool), node.Type+":"+node.ID),
		TaskID:       node.TaskID,
		NodeID:       node.ID,
		Claim:        "allowlisted tool execution evidence",
		SourceType:   SourceTool,
		SourceID:     e.Config.ID,
		Content:      content,
		Score:        1,
		Trust:        0.65,
		PrivacyClass: defaultPrivacyClass(inputPrivacyClass(input)),
		CreatedAt:    time.Now().UTC(),
	}
	if err := e.Evidence.Put(ctx, evidence); err != nil {
		return agent.Result{ErrorCategory: agent.ErrorRetryable, ErrorMessage: err.Error(), Usage: agentUsage(node.Input, content)}, err
	}
	return agent.Result{Text: content, EvidenceIDs: []string{evidence.ID}, Usage: agentUsage(node.Input, content)}, nil
}
