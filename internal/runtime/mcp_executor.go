package runtime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"agent/internal/agent"
	"agent/internal/config"
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
	output, err := client.Call(ctx, e.ServerID, e.ToolName, args)
	if err != nil {
		return agent.Result{ErrorCategory: agent.ErrorRetryable, ErrorMessage: err.Error(), Usage: agentUsage(node.Input, string(output))}, err
	}
	content := "UNTRUSTED OBSERVATION\nmcp_server: " + e.ServerID + "\nmcp_tool: " + e.ToolName + "\noutput:\n" + string(output)
	if e.Evidence == nil {
		err := errors.New("mcp executor evidence store is required")
		return agent.Result{ErrorCategory: agent.ErrorBlockedMissingInput, ErrorMessage: err.Error(), Usage: agentUsage(node.Input, content)}, err
	}
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
		return agent.Result{ErrorCategory: agent.ErrorRetryable, ErrorMessage: err.Error(), Usage: agentUsage(node.Input, content)}, err
	}
	return agent.Result{Text: content, EvidenceIDs: []string{evidence.ID}, Usage: agentUsage(node.Input, content)}, nil
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
	cmd := exec.CommandContext(runCtx, c.Config.Command, c.Config.Args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	defer func() { _ = cmd.Process.Kill() }()

	reader := bufio.NewReader(stdout)
	if err := writeMCPMessage(stdin, 1, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "pachat", "version": "1"},
	}); err != nil {
		return nil, err
	}
	if _, err := readMCPMessage(reader); err != nil {
		return nil, err
	}
	if err := writeMCPNotification(stdin, "notifications/initialized", map[string]any{}); err != nil {
		return nil, err
	}
	if err := writeMCPMessage(stdin, 2, "tools/list", map[string]any{}); err != nil {
		return nil, err
	}
	if _, err := readMCPMessage(reader); err != nil {
		return nil, err
	}
	if err := writeMCPMessage(stdin, 3, "tools/call", map[string]any{"name": toolName, "arguments": rawJSONMap(input)}); err != nil {
		return nil, err
	}
	response, err := readMCPMessage(reader)
	if err != nil {
		return nil, err
	}
	if err := cmd.Process.Kill(); err == nil {
		_ = cmd.Wait()
	}
	if stderr.Len() > 0 && len(response) == 0 {
		return nil, errors.New(stderr.String())
	}
	return response, nil
}

func writeMCPMessage(w io.Writer, id int, method string, params any) error {
	return writeMCPPayload(w, map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
}

func writeMCPNotification(w io.Writer, method string, params any) error {
	return writeMCPPayload(w, map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func writeMCPPayload(w io.Writer, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "Content-Length: %d\r\n\r\n%s", len(raw), raw)
	return err
}

func readMCPMessage(r *bufio.Reader) (json.RawMessage, error) {
	length := 0
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 && strings.EqualFold(strings.TrimSpace(parts[0]), "Content-Length") {
			length, _ = strconv.Atoi(strings.TrimSpace(parts[1]))
		}
	}
	if length <= 0 {
		return nil, errors.New("mcp response missing content length")
	}
	raw := make([]byte, length)
	if _, err := io.ReadFull(r, raw); err != nil {
		return nil, err
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  any             `json:"error"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}
	if envelope.Error != nil {
		return nil, fmt.Errorf("mcp error: %v", envelope.Error)
	}
	if len(envelope.Result) == 0 {
		return json.RawMessage(raw), nil
	}
	return envelope.Result, nil
}

func rawJSONMap(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]any{"input": string(raw)}
	}
	return out
}
