package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"agent/internal/agent"
	"agent/internal/config"
	"agent/internal/mcp"
	"agent/internal/orchestrator"
	"agent/internal/workflow"
)

type countedMCP struct {
	calls int
	err   error
}

func (c *countedMCP) Call(context.Context, string, string, json.RawMessage) (json.RawMessage, error) {
	c.calls++
	return json.RawMessage(`{"content":[],"isError":false}`), c.err
}

type failingMCPEvidence struct{}

func (failingMCPEvidence) Put(context.Context, Evidence) error { return errors.New("storage failed") }
func (failingMCPEvidence) Get(context.Context, string) (Evidence, error) {
	return Evidence{}, errors.New("unused")
}
func (failingMCPEvidence) ListByTask(context.Context, string) ([]Evidence, error) { return nil, nil }

type mcpSubAgent struct{ executor MCPExecutor }

func (mcpSubAgent) Role() agent.Role { return agent.RoleTool }
func (a mcpSubAgent) Execute(ctx context.Context, n agent.TaskNode) (agent.Result, error) {
	return a.executor.Execute(ctx, n, workflowInput{})
}
func TestMCPMissingEvidenceStopsBeforeCall(t *testing.T) {
	c := &countedMCP{}
	r, e := (MCPExecutor{Client: c}).Execute(context.Background(), agent.TaskNode{Input: "{}"}, workflowInput{})
	if e == nil || c.calls != 0 || r.ErrorCategory != agent.ErrorBlockedMissingInput {
		t.Fatalf("calls=%d result=%+v err=%v", c.calls, r, e)
	}
}
func TestMCPEffectsAreNeverAutomaticallyRetried(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		category agent.ErrorCategory
	}{
		{"untyped-error", errors.New("unknown execution"), agent.ErrorCategory("effect_outcome_unknown")},
		{"possibly-sent", &mcp.CallError{Certainty: mcp.PossiblySent, Kind: "transport"}, agent.ErrorCategory("effect_outcome_unknown")},
		{"known-response", &mcp.CallError{Certainty: mcp.KnownResponse, Kind: "tool failure"}, agent.ErrorVerificationFailed},
		{"not-sent", &mcp.CallError{Certainty: mcp.NotSent, Kind: "invalid arguments"}, agent.ErrorBlockedMissingInput},
		{"evidence-after-effect", nil, agent.ErrorVerificationFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &countedMCP{err: tc.err}
			ex := MCPExecutor{Client: c, Evidence: failingMCPEvidence{}}
			r, e := ex.Execute(context.Background(), agent.TaskNode{Input: "{}"}, workflowInput{})
			if e == nil || r.ErrorCategory != tc.category {
				t.Fatalf("%+v %v", r, e)
			}
			if tc.err == nil && !strings.Contains(r.Text, "UNTRUSTED OBSERVATION") {
				t.Fatal("lost post-effect observation")
			}
			c.calls = 0
			dag, e := orchestrator.NewDAG([]agent.TaskNode{{ID: "mcp", TaskID: "task", Role: agent.RoleTool, Input: "{}", MaxAttempts: 3}})
			if e != nil {
				t.Fatal(e)
			}
			_, _ = (orchestrator.Scheduler{Agents: map[agent.Role]agent.SubAgent{agent.RoleTool: mcpSubAgent{ex}}, Parallelism: 1}).Run(context.Background(), dag)
			if c.calls != 1 {
				t.Fatalf("effect called %d times", c.calls)
			}
		})
	}
}

type completeFailsStore struct{ SideEffectStore }

func (s completeFailsStore) Complete(context.Context, string, string) error {
	return errors.New("completion persistence failed")
}
func TestMCPWorkflowPersistenceFailureCannotReplay(t *testing.T) {
	for _, stage := range []string{"checkpoint", "completion", "evidence", "unknown"} {
		t.Run(stage, func(t *testing.T) {
			ctx := context.Background()
			db := openRuntimeTestDB(t)
			cfg := configForRuntimeTest()
			cfg.MCP.Servers = map[string]config.MCPServerConfig{"local": {Enabled: true, Command: "unused-owned-fixture", Tools: []string{"echo"}, TrustLevel: "local_private", PrivacyClasses: []string{"private"}}}
			rt := NewLocal(cfg, db, nil)
			c := &countedMCP{}
			evidence := EvidenceStore(rt.Evidence)
			if stage == "evidence" {
				evidence = failingMCPEvidence{}
			}
			if stage == "unknown" {
				c.err = &mcp.CallError{Certainty: mcp.PossiblySent, Kind: "EOF"}
			}
			rt.Executors.Register("mcp.local.echo", MCPExecutor{Client: c, Evidence: evidence})
			if stage == "completion" {
				rt.SideEffects = completeFailsStore{rt.SideEffects}
			}
			if err := rt.Workflows.Create(ctx, workflow.Run{ID: "mcp-wf", TaskID: "task", Status: workflow.StatusRunning}, []workflow.Node{{NodeID: "mcp-node", CapabilityID: "mcp.local.echo", Role: string(agent.RoleTool)}}); err != nil {
				t.Fatal(err)
			}
			if stage == "checkpoint" {
				if _, e := db.SQL.Exec(`CREATE TRIGGER reject_mcp_checkpoint BEFORE INSERT ON workflow_checkpoints BEGIN SELECT RAISE(FAIL, 'synthetic checkpoint failure'); END`); e != nil {
					t.Fatal(e)
				}
			}
			executor := workflowSubAgent{role: agent.RoleTool, engine: rt.Workflow, workflowID: "mcp-wf"}
			node := agent.TaskNode{ID: "mcp-node", TaskID: "task", Type: "mcp.local.echo", Role: agent.RoleTool, Input: "{}"}
			result, err := executor.Execute(ctx, node)
			if err == nil || result.ErrorCategory == "" || result.ErrorCategory == agent.ErrorRetryable {
				t.Fatalf("post-effect failure became retryable: %+v %v", result, err)
			}
			if stage == "checkpoint" {
				_, _ = db.SQL.Exec(`DROP TRIGGER reject_mcp_checkpoint`)
			}
			// Simulate recovery by executing the same durable workflow/node again even
			// when the old checkpoint could not be saved. No caller key was supplied.
			_, _ = executor.Execute(ctx, node)
			if c.calls != 1 {
				t.Fatalf("MCP effect repeated after %s failure: %d calls", stage, c.calls)
			}
		})
	}
}
