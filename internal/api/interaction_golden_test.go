package api

import (
	"agent/internal/interaction"
	"agent/internal/model"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestV1GoldenReplayFixture(t *testing.T) {
	session := SessionView{ID: "s", ModelID: "fixture", Mode: "default", Files: true, Tools: true}
	fixture := struct {
		Snapshot Snapshot    `json:"snapshot"`
		Replay   []WireEvent `json:"replay"`
		Final    Snapshot    `json:"final"`
		Error    any         `json:"error"`
	}{Error: map[string]any{"schema_version": 1, "error": map[string]string{"code": "provider_changed", "message": "provider_changed"}}}
	r := interaction.Run{ID: "r", SessionID: "s", RequestID: "q", Message: interaction.Message{Content: "edit"}, State: interaction.RunRunning, Version: 1}
	p := interaction.ToolProposal{ID: "p", Call: model.ToolCall{ID: "c", Name: "apply_patch", Arguments: `{"path":"note.txt","content":"new"}`}, RequiresApproval: true, Digest: "digest", ExpiresAt: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)}
	kinds := []string{"run.created", "model.started", "model.text_delta", "model.completed", "tool.proposed", "approval.waiting", "approval.decided", "tool.started", "tool.completed", "model.started", "model.text_delta", "model.completed", "future.unknown", "run.completed"}
	completed := r
	completed.State = interaction.RunCompleted
	completed.Version = 2
	payloads := []any{r, nil, map[string]string{"text": "准备"}, model.TurnResult{Content: "准备"}, p, p, map[string]string{"id": "p", "digest": "digest", "decision": "approved"}, p, interaction.ToolResult{Content: "changed"}, nil, map[string]string{"text": "完成"}, model.TurnResult{Content: "完成"}, map[string]string{"credential": "MUST_NOT_PROJECT"}, completed}
	var view RunView
	for i, kind := range kinds {
		raw, _ := json.Marshal(payloads[i])
		ev := projectEvent(interaction.Event{Version: 1, SessionID: "s", RunID: "r", Sequence: int64(i + 1), Type: kind, Payload: raw})
		applyWire(&view, ev)
		if i == 5 {
			copyRaw, _ := json.Marshal(view)
			var copyView RunView
			json.Unmarshal(copyRaw, &copyView)
			fixture.Snapshot = Snapshot{SchemaVersion: 1, Session: session, Cursor: 6, Runs: []RunView{copyView}}
		}
		if i >= 6 {
			fixture.Replay = append(fixture.Replay, ev)
		}
	}
	fixture.Final = Snapshot{SchemaVersion: 1, Session: session, Cursor: 14, Runs: []RunView{view}}
	raw, _ := json.MarshalIndent(fixture, "", "  ")
	raw = append(raw, '\n')
	path := filepath.Join("..", "..", "protocol", "v1", "replay.golden.json")
	if os.Getenv("UPDATE_WIRE_FIXTURES") == "1" {
		if e := os.WriteFile(path, raw, 0644); e != nil {
			t.Fatal(e)
		}
	}
	want, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if string(want) != string(raw) {
		t.Fatal("v1 golden contract drift; inspect and explicitly regenerate")
	}
}

func TestV1RecoveryGoldenReplayFixture(t *testing.T) {
	before := Snapshot{SchemaVersion: 1, Session: SessionView{ID: "s", ModelID: "fixture", Mode: "default", Files: true, Tools: true}, Cursor: 10, Runs: []RunView{{ID: "r", RequestID: "q", Input: "edit", State: "running", Phase: "cancelling", Tools: []ToolView{{ID: "old", Name: "read", Arguments: "{}", State: "completed", Result: "preimage"}, {ID: "p", Name: "apply_patch", Arguments: `{"path":"note.txt","content":"new"}`, State: "executing"}}}}}
	fixture := struct {
		Snapshot Snapshot    `json:"snapshot"`
		Replay   []WireEvent `json:"replay"`
		Final    Snapshot    `json:"final"`
	}{Snapshot: before}
	copyRaw, _ := json.Marshal(before)
	json.Unmarshal(copyRaw, &fixture.Final)
	payloads := []any{interaction.ToolObservation{ProposalID: "p", CallID: "c", ToolResult: interaction.ToolResult{Content: "applied exact replacement"}}, interaction.Run{ID: "r", SessionID: "s", RequestID: "q", Message: interaction.Message{Content: "edit"}, State: interaction.RunCancelled, Version: 2}}
	for i, kind := range []string{"tool.recovered", "run.cancelled"} {
		raw, _ := json.Marshal(payloads[i])
		ev := projectEvent(interaction.Event{Version: 2, SessionID: "s", RunID: "r", Sequence: int64(11 + i), Type: kind, Payload: raw})
		fixture.Replay = append(fixture.Replay, ev)
		applyWire(&fixture.Final.Runs[0], ev)
		fixture.Final.Cursor = ev.Sequence
	}
	raw, _ := json.MarshalIndent(fixture, "", "  ")
	raw = append(raw, '\n')
	path := filepath.Join("..", "..", "protocol", "v1", "recovery.golden.json")
	if os.Getenv("UPDATE_WIRE_FIXTURES") == "1" {
		if e := os.WriteFile(path, raw, 0644); e != nil {
			t.Fatal(e)
		}
	}
	want, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if string(want) != string(raw) {
		t.Fatal("recovery golden drift")
	}
}

func TestV1ExecGoldenReplayFixture(t *testing.T) {
	review := interaction.ApprovalReview{Kind: "exec", Exec: &interaction.ExecReview{
		EnvironmentChanges: []interaction.EnvironmentChange{}, LaunchMechanism: "Linux pinned executable at /proc/self/fd/3", Warning: interaction.NonSandboxExecWarning, Scope: "scope", RootIdentity: "fixture-root", Program: "/usr/bin/printf", ExecutableIdentity: "fixture-executable", ExecutableSHA256: "fixture-hash", Argv: []string{"%s", "hello 世界"}, Cwd: "/fixture/workspace", CwdIdentity: "fixture-cwd", EnvironmentPolicy: interaction.ExecEnvironmentPolicy, Environment: []interaction.EnvironmentChange{{Name: "LANG", Value: "C.UTF-8"}, {Name: "PATH", Value: "/usr/bin:/bin"}}, TimeoutMillis: 1000, OutputLimitBytes: interaction.MaxExecOutputBytes, TerminationScope: "direct child only; descendants not attested stopped",
	}}
	before := Snapshot{SchemaVersion: 1, Session: SessionView{ID: "s", ModelID: "fixture", Mode: "acceptEdits", Files: true, Tools: true, NonSandboxExec: true, ExecWarning: interaction.NonSandboxExecWarning}, Cursor: 4, Runs: []RunView{{ID: "r", RequestID: "q", Input: "run", State: "running", Phase: "executing", Tools: []ToolView{{ID: "p", Name: "exec", Arguments: `{"program":"/usr/bin/printf","argv":["%s","hello 世界"]}`, State: "executing"}}}}}
	fixture := struct {
		Review   interaction.ApprovalReview `json:"review"`
		Snapshot Snapshot                   `json:"snapshot"`
		Replay   []WireEvent                `json:"replay"`
		Final    Snapshot                   `json:"final"`
	}{Review: review, Snapshot: before}
	raw, _ := json.Marshal(before)
	json.Unmarshal(raw, &fixture.Final)
	observation := interaction.ToolObservation{ProposalID: "p", CallID: "c", ToolResult: interaction.ToolResult{Content: `{"stdout":"hello 世界","stderr":"","exit_code":0,"direct_child_waited":true,"descendants_stopped":false,"outcome":"unknown_requires_reconciliation"}`, IsError: true}}
	for i, kind := range []string{"tool.uncertain", "run.needs_reconciliation"} {
		raw, _ := json.Marshal(observation)
		ev := projectEvent(interaction.Event{Version: 1, SessionID: "s", RunID: "r", Sequence: int64(5 + i), Type: kind, Payload: raw})
		fixture.Replay = append(fixture.Replay, ev)
		applyWire(&fixture.Final.Runs[0], ev)
		fixture.Final.Cursor = ev.Sequence
	}
	if fixture.Final.Runs[0].Phase != "needs_reconciliation" || fixture.Final.Runs[0].Tools[0].State != "unknown" {
		t.Fatal(fixture.Final)
	}
	raw, _ = json.MarshalIndent(fixture, "", "  ")
	raw = append(raw, '\n')
	path := filepath.Join("..", "..", "protocol", "v1", "exec.golden.json")
	if os.Getenv("UPDATE_WIRE_FIXTURES") == "1" {
		if err := os.WriteFile(path, raw, 0644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != string(raw) {
		t.Fatal("exec fixture drift")
	}
}

func TestV1MCPGoldenReplayFixture(t *testing.T) {
	status := interaction.MCPStatus{Enabled: true, Warning: interaction.MCPWarning, Limits: interaction.MCPLimits, Servers: []interaction.MCPServerView{{ID: "owned", State: "discovered", Reason: "Discovery is not a tool call.", ProtocolVersion: "2026-07-28", ConfigFingerprint: "config-digest", Tools: 1}}}
	before := Snapshot{SchemaVersion: 1, Session: SessionView{ID: "s", ModelID: "fixture", Mode: "default", Files: true, Tools: true, MCP: &status}, Cursor: 6, Runs: []RunView{{ID: "r", RequestID: "q", Input: "mcp", State: "running", Phase: "waiting_approval", Tools: []ToolView{{ID: "p", Name: "mcp_owned", Arguments: `{"value":"hello 世界"}`, State: "proposed"}}, Approval: &ApprovalView{ID: "p", Digest: "approval-digest", ExpiresAt: "2030-01-01T00:00:00Z", Decision: "pending"}}}}
	fixture := struct {
		Snapshot Snapshot                   `json:"snapshot"`
		Review   interaction.ApprovalReview `json:"review"`
		Replay   []WireEvent                `json:"replay"`
		Final    Snapshot                   `json:"final"`
	}{Snapshot: before, Review: interaction.ApprovalReview{Kind: "mcp", MCP: &interaction.MCPReview{ServerID: "owned", Tool: "write", Arguments: `{"value":"hello 世界"}`, SchemaDigest: "schema-digest", ConfigFingerprint: "config-digest", ProtocolVersion: "2026-07-28", Generation: "process-generation", Command: "/owned/helper", EnvironmentPolicy: interaction.MCPEnvironmentPolicy, EffectClass: "unknown/writable", Warning: interaction.MCPWarning}}}
	raw, _ := json.Marshal(before)
	json.Unmarshal(raw, &fixture.Final)
	p := interaction.ToolProposal{ID: "p", Call: model.ToolCall{ID: "c", Name: "mcp_owned", Arguments: `{"value":"hello 世界"}`}, RequiresApproval: true, Digest: "approval-digest", ExpiresAt: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)}
	payloads := []any{p, interaction.ToolObservation{ProposalID: "p", CallID: "c", ToolResult: interaction.ToolResult{Content: `{"server_id":"owned","tool":"write","certainty":"possibly_sent","outcome":"unknown_requires_reconciliation"}`, IsError: true}}, nil}
	for i, kind := range []string{"tool.started", "tool.uncertain", "run.needs_reconciliation"} {
		raw, _ := json.Marshal(payloads[i])
		ev := projectEvent(interaction.Event{Version: 1, SessionID: "s", RunID: "r", Sequence: int64(7 + i), Type: kind, Payload: raw})
		fixture.Replay = append(fixture.Replay, ev)
		applyWire(&fixture.Final.Runs[0], ev)
		fixture.Final.Cursor = ev.Sequence
	}
	raw, _ = json.MarshalIndent(fixture, "", "  ")
	raw = append(raw, '\n')
	path := filepath.Join("..", "..", "protocol", "v1", "mcp.golden.json")
	if os.Getenv("UPDATE_WIRE_FIXTURES") == "1" {
		if e := os.WriteFile(path, raw, 0644); e != nil {
			t.Fatal(e)
		}
	}
	want, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if string(want) != string(raw) {
		t.Fatal("MCP golden drift; inspect and explicitly regenerate")
	}
}
