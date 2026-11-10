package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"time"

	"agent/internal/agent"
	"agent/internal/config"
	"agent/internal/mcp"
	"agent/internal/security"
)

type MCPClient interface {
	Call(ctx context.Context, serverID string, toolName string, input json.RawMessage) (json.RawMessage, error)
}

type MCPExecutor struct {
	ServerID string
	ToolName string
	Config   config.MCPServerConfig
	Client   MCPClient
	Evidence EvidenceStore
}

type mcpNodeInput struct {
	Arguments json.RawMessage `json:"arguments"`
}

func (e MCPExecutor) Execute(ctx context.Context, node agent.TaskNode, input workflowInput) (agent.Result, error) {
	if !e.Config.Enabled && e.Client == nil {
		err := errors.New("mcp executor disabled")
		return agent.Result{ErrorCategory: agent.ErrorBlockedMissingInput, ErrorMessage: err.Error(), Usage: agentUsage(node.Input, "")}, err
	}
	if e.Evidence == nil {
		err := errors.New("mcp executor evidence store is required before execution")
		return agent.Result{ErrorCategory: agent.ErrorBlockedMissingInput, ErrorMessage: err.Error(), Usage: agentUsage(node.Input, "")}, err
	}
	client := e.Client
	if client == nil {
		spec := security.CommandSpec{Program: e.Config.Command, Args: e.Config.Args}
		if err := spec.Validate(); err != nil {
			return agent.Result{ErrorCategory: agent.ErrorBlockedMissingInput, ErrorMessage: err.Error(), Usage: agentUsage(node.Input, "")}, err
		}
		client = stdioMCPClient{Config: e.Config}
	}
	args := json.RawMessage(node.Input)
	var parsed mcpNodeInput
	if err := json.Unmarshal([]byte(node.Input), &parsed); err == nil && len(parsed.Arguments) > 0 {
		args = parsed.Arguments
	}
	output, callErr := client.Call(ctx, e.ServerID, e.ToolName, args)
	category := agent.ErrorCategory("")
	if callErr != nil {
		// An old injected client has no certainty contract. Treat untyped failures
		// conservatively; neither transport errors nor known tool failures imply rollback.
		category = agent.ErrorEffectUnknown
		var ce *mcp.CallError
		if errors.As(callErr, &ce) {
			if ce.Certainty == mcp.NotSent {
				category = agent.ErrorBlockedMissingInput
			}
			if ce.Certainty == mcp.KnownResponse {
				category = agent.ErrorVerificationFailed
			}
		}
		if category == agent.ErrorBlockedMissingInput || len(output) == 0 {
			return agent.Result{ErrorCategory: category, ErrorMessage: callErr.Error(), Usage: agentUsage(node.Input, string(output))}, callErr
		}
	}
	content := "UNTRUSTED OBSERVATION\nmcp_server: " + e.ServerID + "\nmcp_tool: " + e.ToolName + "\noutput:\n" + string(output)
	evidence := Evidence{
		ID:           stableEvidenceID(node.TaskID, string(SourceTool), node.Type+":"+node.ID),
		TaskID:       node.TaskID,
		NodeID:       node.ID,
		Claim:        "mcp tool execution evidence",
		SourceType:   SourceTool,
		SourceID:     e.ServerID + "." + e.ToolName,
		Content:      content,
		Score:        1,
		Trust:        0.55,
		PrivacyClass: defaultPrivacyClass(inputPrivacyClass(input)),
		CreatedAt:    time.Now().UTC(),
	}
	if err := e.Evidence.Put(ctx, evidence); err != nil {
		// The effect has already happened (or its outcome is uncertain). Retrying
		// this executor would repeat the tool, not merely repair the evidence write.
		if category == "" {
			category = agent.ErrorVerificationFailed
		}
		storeErr := errors.New("mcp observation could not be persisted; reconcile evidence without repeating the tool")
		if callErr != nil {
			storeErr = errors.Join(callErr, storeErr)
		}
		return agent.Result{Text: content, ErrorCategory: category, ErrorMessage: storeErr.Error(), Usage: agentUsage(node.Input, content)}, storeErr
	}
	result := agent.Result{Text: content, EvidenceIDs: []string{evidence.ID}, Usage: agentUsage(node.Input, content), ErrorCategory: category}
	if callErr != nil {
		result.ErrorMessage = callErr.Error()
	}
	return result, callErr
}

type stdioMCPClient struct {
	Config config.MCPServerConfig
}

func (c stdioMCPClient) Call(ctx context.Context, serverID string, toolName string, input json.RawMessage) (json.RawMessage, error) {
	runCtx := ctx
	cancel := func() {}
	if c.Config.Timeout.Duration > 0 {
		runCtx, cancel = context.WithTimeout(ctx, c.Config.Timeout.Duration)
	}
	defer cancel()
	if err := runCtx.Err(); err != nil {
		return nil, mcp.PreflightError(err)
	}
	// Resolve only the explicitly configured command. No shell, installation,
	// inherited environment credentials, HTTP endpoint, or automatic reconnection.
	command, err := exec.LookPath(c.Config.Command)
	if err != nil {
		return nil, mcp.PreflightError(err)
	}
	client, err := mcp.OpenStdio(runCtx, mcp.StdioConfig{Command: command, Args: c.Config.Args, RequestTimeout: c.Config.Timeout.Duration})
	if err != nil {
		return nil, mcp.PreflightError(err)
	}
	defer client.Close()
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	result, err := client.CallTool(runCtx, toolName, input)
	return result.Raw, err
}
