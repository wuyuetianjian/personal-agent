// Package apifixture provides an offline deterministic model for SDK/UI acceptance.
// It is not a real provider integration or a fake file broker.
package apifixture

import (
	"agent/internal/api"
	"agent/internal/interaction"
	"agent/internal/model"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type Provider struct{ ExecWorkspace string }

func (p Provider) Stream(ctx context.Context, r model.TurnRequest, emit func(model.ModelEvent) error) (model.TurnResult, error) {
	input, scope := "", ""
	for _, m := range r.Messages {
		if m.Role == "user" {
			input = m.Content
		}
		if strings.HasPrefix(m.Content, "Workspace scope: ") {
			scope = strings.Split(strings.TrimPrefix(m.Content, "Workspace scope: "), ".")[0]
		}
	}
	if input == "wait" {
		<-ctx.Done()
		return model.TurnResult{}, ctx.Err()
	}
	result := model.TurnResult{Content: "你好，真实流式测试完成。\n", Usage: &model.Usage{InputTokens: 100, OutputTokens: 20}}
	if len(r.Messages) > 0 && r.Messages[len(r.Messages)-1].Role == "tool" {
		result.Content = "工具结果已收到。完成。\n"
	} else if input == "mcp" {
		for _, tool := range r.Tools {
			if strings.HasPrefix(tool.Function.Name, "mcp_") {
				result.Content = "准备 MCP 调用，需要精确审批。\n"
				result.ToolCalls = []model.ToolCall{{ID: "fixture-mcp", Name: tool.Function.Name, Arguments: `{"value":"hello 世界"}`}}
				break
			}
		}
	} else if input == "exec" && p.ExecWorkspace != "" && hasExec(r.Tools) {
		args, _ := json.Marshal(interaction.ExecArguments{Scope: scope, Program: "/bin/sh", Argv: []string{"-c", `printf 'once\n' >> exec-marker.txt; printf 'fixture exec output\n'`}, Cwd: p.ExecWorkspace, Environment: map[string]string{"FIXTURE_VALUE": "safe value"}, TimeoutMillis: 1000})
		result.Content = "准备本地测试命令，需要审批。\n"
		result.ToolCalls = []model.ToolCall{{ID: "fixture-exec", Name: "exec", Arguments: string(args)}}
	} else if input == "artifact-read" {
		for _, tool := range r.Tools {
			if tool.Function.Name == "read" {
				args, _ := json.Marshal(interaction.ReadArguments{Scope: scope, Path: "large.txt"})
				result.ToolCalls = []model.ToolCall{{ID: "fixture-artifact", Name: "read", Arguments: string(args)}}
				break
			}
		}
	} else if strings.Contains(input, "edit") && len(r.Tools) > 0 {
		hash := sha256.Sum256([]byte("old\n"))
		args, _ := json.Marshal(interaction.PatchArguments{Scope: scope, Path: "note.txt", Preimage: fmt.Sprintf("%x", hash), Content: "new 你好\n"})
		result.Content = "准备修改 note.txt，需要审批。\n"
		result.ToolCalls = []model.ToolCall{{ID: "fixture-edit", Name: "apply_patch", Arguments: string(args)}}
	}
	delay := 500 * time.Millisecond
	if strings.HasPrefix(input, "history-seed-") {
		delay = 0
	}
	for i, chunk := range strings.SplitAfter(result.Content, "，") {
		if input == "slow-print-fixture" && i == 1 {
			select {
			case <-ctx.Done():
				return model.TurnResult{}, ctx.Err()
			case <-time.After(16 * time.Second):
			}
		}
		select {
		case <-ctx.Done():
			return model.TurnResult{}, ctx.Err()
		case <-time.After(delay):
		}
		if e := emit(model.ModelEvent{Kind: model.EventTextDelta, Text: chunk}); e != nil {
			return model.TurnResult{}, e
		}
	}
	return result, nil
}
func Selection() api.ProviderSelection {
	return api.ProviderSelection{Endpoint: "http://127.0.0.1:1/offline-fixture", ToolsSupported: true, Provider: Provider{}, Model: model.ModelMetadata{ID: "fixture", ProviderID: "offline", Model: "deterministic", ContextWindow: 32000, TrustLevel: model.TrustLocalPrivate, Capabilities: map[model.Capability]bool{model.CapabilityChat: true, model.CapabilityToolCalling: true}, Provider: model.ProviderMetadata{ID: "offline", Type: "fixture", Enabled: true, TrustLevel: model.TrustLocalPrivate}}}
}

func hasExec(tools []model.ToolDefinition) bool {
	for _, t := range tools {
		if t.Function.Name == "exec" {
			return true
		}
	}
	return false
}
