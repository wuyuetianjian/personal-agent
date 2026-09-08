package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"agent/internal/agent"
	"agent/internal/capability"
	"agent/internal/config"
	"agent/internal/runtime"
	"agent/internal/storage"
	"agent/internal/workflow"
)

func mcpCommand(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return usageError("mcp requires list, health, tools, or test")
	}
	switch args[0] {
	case "list":
		return mcpList(ctx, args[1:], stdout)
	case "health":
		return mcpHealth(ctx, args[1:], stdout)
	case "tools":
		return mcpTools(ctx, args[1:], stdout)
	case "test":
		return mcpTest(ctx, args[1:], stdout)
	default:
		return usageError("unknown mcp subcommand " + args[0])
	}
}

func mcpList(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("mcp list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	jsonOutput := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" {
		return usageError("mcp list requires --config")
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writePrettyJSON(stdout, map[string]any{"servers": cfg.MCP.Servers})
	}
	for id, server := range cfg.MCP.Servers {
		fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\n", id, boolText(server.Enabled), server.TrustLevel, strings.Join(server.Tools, ","))
	}
	return nil
}

func mcpHealth(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("mcp health", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	jsonOutput := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" {
		return usageError("mcp health requires --config")
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	type health struct {
		ID      string `json:"id"`
		Status  string `json:"status"`
		Enabled bool   `json:"enabled"`
		Detail  string `json:"detail,omitempty"`
	}
	var checks []health
	for id, server := range cfg.MCP.Servers {
		item := health{ID: id, Enabled: server.Enabled, Status: "disabled"}
		if server.Enabled {
			item.Status = "healthy"
			item.Detail = "command available"
			if err := commandAvailable(server.Command); err != nil {
				item.Status = "unavailable"
				item.Detail = err.Error()
			}
		}
		checks = append(checks, item)
	}
	if *jsonOutput {
		return writePrettyJSON(stdout, map[string]any{"checks": checks})
	}
	for _, item := range checks {
		fmt.Fprintf(stdout, "%s\t%s\t%s\n", item.ID, item.Status, item.Detail)
	}
	return nil
}

func mcpTools(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("mcp tools", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	serverFilter := fs.String("server", "", "server id")
	jsonOutput := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" {
		return usageError("mcp tools requires --config")
	}
	cfg, db, err := openConfiguredDB(ctx, *configPath)
	if err != nil {
		return err
	}
	defer db.Close()
	rt := runtime.NewLocal(cfg, db, nil)
	caps := rt.Capabilities.List(capability.KindMCPTool)
	type tool struct {
		ID       string `json:"id"`
		ServerID string `json:"server_id"`
		Name     string `json:"name"`
		Enabled  bool   `json:"enabled"`
		Health   string `json:"health"`
	}
	var out []tool
	for _, cap := range caps {
		parts := strings.SplitN(strings.TrimPrefix(cap.ID, "mcp."), ".", 2)
		if len(parts) != 2 {
			continue
		}
		if *serverFilter != "" && parts[0] != *serverFilter {
			continue
		}
		out = append(out, tool{ID: cap.ID, ServerID: parts[0], Name: parts[1], Enabled: cap.Enabled, Health: string(cap.Health)})
	}
	if *jsonOutput {
		return writePrettyJSON(stdout, map[string]any{"tools": out})
	}
	for _, item := range out {
		fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\n", item.ID, item.ServerID, item.Health, boolText(item.Enabled))
	}
	return nil
}

func mcpTest(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("mcp test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	serverID := fs.String("server", "", "server id")
	toolName := fs.String("tool", "", "tool name")
	arguments := fs.String("arguments", "{}", "tool arguments JSON")
	jsonOutput := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" || *serverID == "" || *toolName == "" {
		return usageError("mcp test requires --config, --server, and --tool")
	}
	var raw json.RawMessage
	if err := json.Unmarshal([]byte(*arguments), &raw); err != nil {
		return err
	}
	cfg, db, err := openConfiguredDB(ctx, *configPath)
	if err != nil {
		return err
	}
	defer db.Close()
	server, ok := cfg.MCP.Servers[*serverID]
	if !ok {
		return usageError("mcp server not found")
	}
	if !toolConfigured(server.Tools, *toolName) {
		return usageError("mcp tool not configured")
	}
	taskID, err := newID("task")
	if err != nil {
		return err
	}
	if err := db.CreateTask(ctx, storage.Task{ID: taskID, Title: "mcp test " + *serverID + "." + *toolName, Input: string(raw), Status: "running", LeaderModelID: cfg.Agent.Leader.ModelID, PrivacyClass: "local_private"}); err != nil {
		return err
	}
	rt := runtime.NewLocal(cfg, db, nil)
	nodeInput, err := json.Marshal(map[string]json.RawMessage{"arguments": raw})
	if err != nil {
		return err
	}
	workflowID, err := newID("wf")
	if err != nil {
		return err
	}
	if err := rt.Workflows.Create(ctx, workflow.Run{ID: workflowID, TaskID: taskID, Status: workflow.StatusPending, InputJSON: `{"input":` + strconv.Quote(string(nodeInput)) + `}`}, []workflow.Node{{WorkflowID: workflowID, NodeID: *toolName, CapabilityID: "mcp." + *serverID + "." + *toolName, Role: string(agent.RoleTool), Status: workflow.NodePending}}); err != nil {
		return err
	}
	if err := rt.Workflow.RunWorkflow(ctx, workflowID); err != nil {
		if checkpoints, cpErr := rt.Workflows.ListCheckpoints(ctx, workflowID); cpErr == nil && len(checkpoints) > 0 {
			last := checkpoints[len(checkpoints)-1]
			if last.ResultRef != "" {
				err = fmt.Errorf("%w: %s", err, last.ResultRef)
			}
		}
		_ = db.FailTask(ctx, taskID, "mcp_test_failed", err.Error())
		return err
	}
	evidence, err := rt.Evidence.ListByTask(ctx, taskID)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writePrettyJSON(stdout, map[string]any{"task_id": taskID, "workflow_id": workflowID, "evidence": evidence})
	}
	fmt.Fprintf(stdout, "server=%s\ntool=%s\ntask_id=%s\nworkflow_id=%s\nstatus=OK\n", *serverID, *toolName, taskID, workflowID)
	for _, item := range evidence {
		fmt.Fprintf(stdout, "evidence_id=%s\n", item.ID)
	}
	return nil
}

func commandAvailable(program string) error {
	if strings.TrimSpace(program) == "" {
		return fmt.Errorf("command is required")
	}
	if filepath.IsAbs(program) {
		info, err := os.Stat(program)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return fmt.Errorf("command is a directory")
		}
		return nil
	}
	_, err := exec.LookPath(program)
	return err
}

func toolConfigured(tools []string, name string) bool {
	if len(tools) == 0 {
		return name == "call"
	}
	for _, tool := range tools {
		if tool == name {
			return true
		}
	}
	return false
}
