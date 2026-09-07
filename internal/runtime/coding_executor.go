package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"agent/internal/agent"
	"agent/internal/codingagent"
	"agent/internal/config"
)

type CodingRunner interface {
	Run(ctx context.Context, req codingagent.Request) (codingagent.Result, error)
}

type CodingExecutor struct {
	Runner   CodingRunner
	Backend  string
	Config   config.Config
	Evidence EvidenceStore
}

type codingNodeInput struct {
	RepositoryPath    string     `json:"repository_path"`
	WorkspacePath     string     `json:"workspace_path"`
	Prompt            string     `json:"prompt"`
	Privacy           string     `json:"privacy"`
	Required          []string   `json:"required"`
	AllowWrite        bool       `json:"allow_write"`
	AllowDirectWrites bool       `json:"allow_direct_writes"`
	TestCommands      [][]string `json:"test_commands"`
}

func NewCodingExecutor(cfg config.Config, backend string, evidence EvidenceStore) CodingExecutor {
	registry := codingagent.RegistryFromConfig(cfg.CodingAgents, nil)
	preferred := []string{backend}
	if backend == "" && cfg.CodingAgents.DefaultBackend != "" {
		preferred = []string{cfg.CodingAgents.DefaultBackend}
	}
	return CodingExecutor{
		Runner: codingagent.Runner{
			Registry:  registry,
			Workspace: codingagent.WorkspaceManager{Root: filepath.Join(cfg.App.DataDir, "coding-worktrees")},
			Executor:  codingagent.ProcessExecutor{},
			Preferred: preferred,
		},
		Backend:  backend,
		Config:   cfg,
		Evidence: evidence,
	}
}

func (e CodingExecutor) Execute(ctx context.Context, node agent.TaskNode, input workflowInput) (agent.Result, error) {
	if e.Runner == nil {
		return agent.Result{ErrorCategory: agent.ErrorBlockedMissingInput, ErrorMessage: codingagent.ErrNoBackendAvailable.Error(), Usage: agentUsage(node.Input, "")}, codingagent.ErrNoBackendAvailable
	}
	req, err := codingRequestFromNode(node, e.Backend)
	if err != nil {
		return agent.Result{ErrorCategory: agent.ErrorBlockedMissingInput, ErrorMessage: err.Error(), Usage: agentUsage(node.Input, "")}, err
	}
	result, err := e.Runner.Run(ctx, req)
	if err != nil {
		category := agent.ErrorRetryable
		if errors.Is(err, codingagent.ErrNoBackendAvailable) ||
			errors.Is(err, codingagent.ErrBackendDisabled) ||
			errors.Is(err, codingagent.ErrPrivacyDenied) ||
			errors.Is(err, codingagent.ErrDirectWriteDenied) ||
			errors.Is(err, codingagent.ErrInsufficientEvidence) {
			category = agent.ErrorBlockedMissingInput
		}
		return agent.Result{ErrorCategory: category, ErrorMessage: err.Error(), Usage: codingUsage(result)}, err
	}
	content := codingEvidenceText(result)
	if e.Evidence == nil {
		err := errors.New("coding executor evidence store is required")
		return agent.Result{ErrorCategory: agent.ErrorBlockedMissingInput, ErrorMessage: err.Error(), Usage: codingUsage(result)}, err
	}
	evidence := Evidence{
		ID:           stableEvidenceID(node.TaskID, string(SourceTool), node.Type+":"+node.ID),
		TaskID:       node.TaskID,
		NodeID:       node.ID,
		Claim:        "coding agent execution evidence",
		SourceType:   SourceTool,
		SourceID:     result.BackendID,
		Content:      content,
		Score:        1,
		Trust:        0.7,
		PrivacyClass: defaultPrivacyClass(inputPrivacyClass(input)),
		CreatedAt:    time.Now().UTC(),
	}
	if err := e.Evidence.Put(ctx, evidence); err != nil {
		return agent.Result{ErrorCategory: agent.ErrorRetryable, ErrorMessage: err.Error(), Usage: codingUsage(result)}, err
	}
	return agent.Result{Text: result.Summary, EvidenceIDs: []string{evidence.ID}, Usage: codingUsage(result)}, nil
}

func codingRequestFromNode(node agent.TaskNode, backend string) (codingagent.Request, error) {
	var input codingNodeInput
	if err := json.Unmarshal([]byte(node.Input), &input); err != nil {
		input.Prompt = node.Input
	}
	if strings.TrimSpace(input.RepositoryPath) == "" {
		return codingagent.Request{}, errors.New("coding node requires repository_path")
	}
	required := make([]codingagent.Capability, 0, len(input.Required))
	for _, item := range input.Required {
		required = append(required, codingagent.Capability(item))
	}
	if len(required) == 0 {
		required = []codingagent.Capability{codingagent.CapabilityCoding}
	}
	privacy := codingagent.RepositoryPrivacy(input.Privacy)
	if privacy == "" {
		privacy = codingagent.RepositoryPrivate
	}
	return codingagent.Request{
		TaskID:            node.TaskID,
		NodeID:            node.ID,
		RepositoryPath:    input.RepositoryPath,
		WorkspacePath:     input.WorkspacePath,
		Prompt:            firstNonEmptyString(input.Prompt, node.Input),
		Privacy:           privacy,
		Required:          required,
		AllowWrite:        input.AllowWrite,
		AllowDirectWrites: input.AllowDirectWrites,
		TestCommands:      input.TestCommands,
	}, nil
}

func codingEvidenceText(result codingagent.Result) string {
	parts := []string{
		"backend: " + result.BackendID,
		"summary: " + result.Summary,
	}
	if result.WorkspacePath != "" {
		parts = append(parts, "workspace: "+result.WorkspacePath)
	}
	if len(result.FilesChanged) > 0 {
		parts = append(parts, "files_changed: "+strings.Join(result.FilesChanged, ", "))
	}
	if result.Diff != "" {
		parts = append(parts, "diff:\n"+result.Diff)
	}
	for _, test := range result.Tests {
		parts = append(parts, "test: "+strings.Join(test.Args, " ")+" exit="+intString(test.ExitCode))
	}
	return strings.Join(parts, "\n")
}

func codingUsage(result codingagent.Result) agent.Usage {
	return agent.Usage{EstimatedCostUSD: result.EstimatedCostUSD}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func intString(value int) string {
	return strconv.Itoa(value)
}
