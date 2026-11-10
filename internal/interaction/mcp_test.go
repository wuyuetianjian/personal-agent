package interaction

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agent/internal/mcpfixture"
)

// The owned test executable is a real newline JSON-RPC peer, not a broker mock.
func TestBrokerMCPPeer(t *testing.T) {
	if os.Getenv("PA_MCP_FIXTURE") != "1" {
		return
	}
	mcpfixture.Serve()
}

func mcpConfig(t *testing.T, marker, era, result string) MCPHostConfig {
	t.Helper()
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	return MCPHostConfig{Enabled: true, Servers: []MCPServerConfig{{ID: "owned", Command: exe, Args: []string{"-test.run=^TestBrokerMCPPeer$"}, Environment: []string{"PA_MCP_FIXTURE=1", "PA_MCP_MARKER=" + marker, "PA_MCP_ERA=" + era, "PA_MCP_RESULT=" + result}, TimeoutMillis: 3000}}}
}
func mcpFixture(t *testing.T, mode PermissionMode, era, result string) (*Store, *WorkspaceBroker, WorkspaceBinding, string, string) {
	t.Helper()
	s, ctx := setup(t)
	marker := filepath.Join(t.TempDir(), "effects")
	b := NewWorkspaceBrokerWithOptions(s, WorkspaceBrokerOptions{MCP: mcpConfig(t, marker, era, result)})
	t.Cleanup(func() { b.CloseMCP() })
	p, e := b.PrepareMCPPolicy(ctx, "s", WorkspacePolicy{Root: t.TempDir(), Mode: mode})
	if e != nil {
		t.Fatal(e)
	}
	w, e := s.BindWorkspace(ctx, "s", p)
	if e != nil {
		t.Fatal(e)
	}
	r, e := s.CreateRun(ctx, "s", "r", Message{})
	if e != nil {
		t.Fatal(e)
	}
	return s, b, w, r.ID, marker
}
func mcpCall(w WorkspaceBinding) ToolCall {
	return ToolCall{ID: "mcp-call", Name: w.Policy.MCP[0].Tools[0].Alias, Arguments: `{"value":"hello 世界"}`}
}
func noEffect(t *testing.T, path string) {
	t.Helper()
	if b, e := os.ReadFile(path); !errors.Is(e, os.ErrNotExist) {
		t.Fatalf("unexpected effect %q %v", b, e)
	}
}
func TestMCPDefaultOffAndInspectionNeverLaunch(t *testing.T) {
	s, ctx := setup(t)
	marker := filepath.Join(t.TempDir(), "effects")
	cfg := mcpConfig(t, marker, "", "")
	cfg.Enabled = false
	b := NewWorkspaceBrokerWithOptions(s, WorkspaceBrokerOptions{MCP: cfg})
	defer b.CloseMCP()
	_ = b.MCPStatus(ctx, "")
	if _, e := b.PrepareMCPPolicy(ctx, "s", WorkspacePolicy{Root: t.TempDir(), Mode: ModeDefault}); e == nil {
		t.Fatal("default-off configuration launched")
	}
	noEffect(t, marker+".launch")
	cfg.Enabled = true
	b = NewWorkspaceBrokerWithOptions(s, WorkspaceBrokerOptions{MCP: cfg})
	defer b.CloseMCP()
	_ = b.MCPStatus(ctx, "")
	noEffect(t, marker+".launch")
}
func TestMCPExactApprovalAndSingleUse(t *testing.T) {
	for _, era := range []string{"modern", "legacy"} {
		t.Run(era, func(t *testing.T) {
			s, b, w, id, marker := mcpFixture(t, ModeAcceptEdits, era, "")
			ctx := context.Background()
			call := mcpCall(w)
			p, e := b.Propose(ctx, id, call)
			if e != nil || !p.RequiresApproval {
				t.Fatal(p, e)
			}
			if got, e := b.Execute(ctx, p.ID); e != nil || !got.IsError {
				t.Fatal(got, e)
			}
			noEffect(t, marker)
			review, e := b.ApprovalReview(ctx, "s", p.ID)
			if e != nil || review.Kind != "mcp" || review.MCP.Arguments != call.Arguments || review.MCP.SchemaDigest == "" || !strings.Contains(review.MCP.Warning, "NON-SANDBOXED") {
				t.Fatal(review, e)
			}
			engine := runWorkspaceTool(t, s, b, id, call)
			if e = engine.Run(ctx, id); !errors.Is(e, ErrWaitingApproval) {
				t.Fatal(e)
			}
			noEffect(t, marker)
			if e = s.DecideApproval(ctx, p.ID, p.Digest, "approved"); e != nil {
				t.Fatal(e)
			}
			if e = engine.Run(ctx, id); e != nil {
				t.Fatal(e)
			}
			data, e := os.ReadFile(marker)
			if e != nil || strings.Count(string(data), "\n") != 1 {
				t.Fatal(string(data), e)
			}
			if got, e := b.Execute(ctx, p.ID); e != nil || !got.IsError {
				t.Fatal("replayed", got, e)
			}
			wantVersion := "2026-07-28"
			if era == "legacy" {
				wantVersion = "2024-11-05"
			}
			if review.MCP.ProtocolVersion != wantVersion {
				t.Fatal("protocol not snapshot-bound", review.MCP.ProtocolVersion)
			}
			if _, e = b.ApprovalReview(ctx, "other", p.ID); e == nil {
				t.Fatal("cross-session review")
			}
		})
	}
}

func TestMCPInvalidArgumentsAndPlanIgnorePeerAnnotations(t *testing.T) {
	for _, mode := range []PermissionMode{ModeDefault, ModePlan} {
		t.Run(string(mode), func(t *testing.T) {
			s, b, w, id, marker := mcpFixture(t, mode, "modern", "")
			call := mcpCall(w)
			if mode == ModeDefault {
				call.Arguments = `{"value":42}`
			} // Actual supported JSON Schema, not just JSON syntax.
			if _, e := b.Propose(context.Background(), id, call); e == nil {
				t.Fatal("unsafe proposal accepted")
			}
			noEffect(t, marker)
			c, e := NewContextAssembler(s).Build(context.Background(), "s", TokenBudget{Total: 32000, Output: 2048})
			if e != nil {
				t.Fatal(e)
			}
			for _, d := range c.Tools {
				if mode == ModePlan && strings.HasPrefix(d.Function.Name, "mcp_") {
					t.Fatal("unclassified tool advertised in plan")
				}
			}
		})
	}
}
func TestMCPHostReadOnlyAndConfigurationCopied(t *testing.T) {
	s, ctx := setup(t)
	marker := filepath.Join(t.TempDir(), "effects")
	cfg := mcpConfig(t, marker, "modern", "")
	cfg.Servers[0].ReadOnlyTools = []string{"write"}
	b := NewWorkspaceBrokerWithOptions(s, WorkspaceBrokerOptions{MCP: cfg})
	defer b.CloseMCP()
	cfg.Servers[0].Command = "/never-execute"
	cfg.Servers[0].ReadOnlyTools[0] = "other"
	cfg.Servers[0].Environment[0] = "PA_MCP_FIXTURE=bad"
	policy, e := b.PrepareMCPPolicy(ctx, "s", WorkspacePolicy{Root: t.TempDir(), Mode: ModePlan})
	if e != nil {
		t.Fatal(e)
	}
	w, e := s.BindWorkspace(ctx, "s", policy)
	if e != nil {
		t.Fatal(e)
	}
	r, e := s.CreateRun(ctx, "s", "r", Message{})
	if e != nil {
		t.Fatal(e)
	}
	p, e := b.Propose(ctx, r.ID, mcpCall(w))
	if e != nil || p.RequiresApproval {
		t.Fatal(p, e)
	}
	engine := runWorkspaceTool(t, s, b, r.ID, mcpCall(w))
	if e = engine.Run(ctx, r.ID); e != nil {
		t.Fatal(e)
	}
	data, e := os.ReadFile(marker)
	if e != nil || strings.Count(string(data), "\n") != 1 {
		t.Fatal(string(data), e)
	}
}
func TestMCPDenialAndSchemaRevocationNoEffect(t *testing.T) {
	for _, kind := range []string{"deny", "schema", "host", "generation"} {
		t.Run(kind, func(t *testing.T) {
			s, b, w, id, marker := mcpFixture(t, ModeDefault, "modern", "")
			ctx := context.Background()
			engine := runWorkspaceTool(t, s, b, id, mcpCall(w))
			if e := engine.Run(ctx, id); !errors.Is(e, ErrWaitingApproval) {
				t.Fatal(e)
			}
			cp, _ := engine.Checkpoint(ctx, id)
			p := cp.Pending[0]
			decision := "approved"
			switch kind {
			case "deny":
				decision = "denied"
			case "schema":
				if e := os.WriteFile(marker+".drift", []byte("drift"), 0600); e != nil {
					t.Fatal(e)
				}
			case "host":
				engine.broker = NewWorkspaceBroker(s)
			case "generation":
				before, e := b.ApprovalReview(ctx, "s", p.ID)
				if e != nil {
					t.Fatal(e)
				}
				b.CloseMCP()
				next := NewWorkspaceBrokerWithOptions(s, WorkspaceBrokerOptions{MCP: mcpConfig(t, marker, "modern", "")})
				defer next.CloseMCP()
				engine.broker = next
				after, e := next.ApprovalReview(ctx, "s", p.ID)
				if e != nil || after.MCP.UnavailableReason == "" || after.MCP.SchemaDigest != before.MCP.SchemaDigest || after.MCP.Generation != before.MCP.Generation {
					t.Fatal(after, e)
				}
			}
			if e := s.DecideApproval(ctx, p.ID, p.Digest, decision); e != nil {
				t.Fatal(e)
			}
			if e := engine.Run(ctx, id); e != nil && !(kind == "deny" && errors.Is(e, ErrConflict)) {
				t.Fatal(e)
			}
			noEffect(t, marker)
			data, _ := os.ReadFile(marker + ".launch")
			if strings.Count(string(data), "\n") != 1 {
				t.Fatal("inspection/restart silently relaunched", string(data))
			}
		})
	}
}
func TestMCPUnknownAndToolErrorsHaveDistinctDurableOutcomes(t *testing.T) {
	for _, kind := range []string{"eof", "task", "input_required", "isError"} {
		t.Run(kind, func(t *testing.T) {
			s, b, w, id, marker := mcpFixture(t, ModeDefault, "modern", kind)
			ctx := context.Background()
			engine := runWorkspaceTool(t, s, b, id, mcpCall(w))
			if e := engine.Run(ctx, id); !errors.Is(e, ErrWaitingApproval) {
				t.Fatal(e)
			}
			cp, _ := engine.Checkpoint(ctx, id)
			p := cp.Pending[0]
			if e := s.DecideApproval(ctx, p.ID, p.Digest, "approved"); e != nil {
				t.Fatal(e)
			}
			err := engine.Run(ctx, id)
			if kind == "isError" {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				if !errors.Is(err, ErrReconciliation) {
					t.Fatal("ambiguous effect advanced", err)
				}
				cp, _ = engine.Checkpoint(ctx, id)
				if cp.Phase != PhaseReconciliation {
					t.Fatal(cp.Phase)
				}
				if e := engine.Recover(ctx, id); !errors.Is(e, ErrReconciliation) {
					t.Fatal(e)
				}
				if e := engine.Run(ctx, id); !errors.Is(e, ErrReconciliation) {
					t.Fatal(e)
				}
			}
			data, e := os.ReadFile(marker)
			if e != nil || strings.Count(string(data), "\n") != 1 {
				t.Fatal("effect count", string(data), e)
			}
			events, e := s.Events(ctx, "s", 0, 100)
			if e != nil {
				t.Fatal(e)
			}
			want := "tool.uncertain"
			if kind == "isError" {
				want = "tool.completed"
			}
			found := false
			for _, ev := range events {
				if ev.Type == want {
					found = true
					if !strings.Contains(string(ev.Payload), "is_error") {
						t.Fatal(ev)
					}
				}
			}
			if !found {
				t.Fatal("no", want)
			}
		})
	}
}

func TestMCPDisconnectOffloadedLateEffectNeverReplays(t *testing.T) {
	s, b, w, id, marker := mcpFixture(t, ModeDefault, "modern", "eof")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	exe, _ := os.Executable()
	worker := exec.CommandContext(ctx, exe, "-test.run=^TestBrokerMCPPeer$")
	worker.Env = []string{"PA_MCP_FIXTURE=1", "PA_MCP_JOB_WORKER=1", "PA_MCP_MARKER=" + marker}
	if e := worker.Start(); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		cancel()
		if worker.ProcessState == nil {
			_ = worker.Wait()
		}
	})
	engine := runWorkspaceTool(t, s, b, id, mcpCall(w))
	if e := engine.Run(ctx, id); !errors.Is(e, ErrWaitingApproval) {
		t.Fatal(e)
	}
	cp, _ := engine.Checkpoint(ctx, id)
	p := cp.Pending[0]
	if e := s.DecideApproval(ctx, p.ID, p.Digest, "approved"); e != nil {
		t.Fatal(e)
	}
	if e := engine.Run(ctx, id); !errors.Is(e, ErrReconciliation) {
		t.Fatal(e)
	}
	if e := worker.Wait(); e != nil {
		t.Fatal("owned async effect worker", e)
	}
	data, e := os.ReadFile(marker + ".late")
	if e != nil || string(data) != "late\n" {
		t.Fatal("missing offloaded late effect after disconnect", string(data), e)
	}
	if e = engine.Recover(context.Background(), id); !errors.Is(e, ErrReconciliation) {
		t.Fatal(e)
	}
	if e = engine.Run(context.Background(), id); !errors.Is(e, ErrReconciliation) {
		t.Fatal(e)
	}
	data, _ = os.ReadFile(marker)
	if strings.Count(string(data), "\n") != 1 {
		t.Fatal("replayed", string(data))
	}
}
func TestMCPCancelIsIntentNotStoppedProof(t *testing.T) {
	s, b, w, id, marker := mcpFixture(t, ModeDefault, "modern", "cancel")
	ctx := context.Background()
	engine := runWorkspaceTool(t, s, b, id, mcpCall(w))
	if e := engine.Run(ctx, id); !errors.Is(e, ErrWaitingApproval) {
		t.Fatal(e)
	}
	cp, _ := engine.Checkpoint(ctx, id)
	p := cp.Pending[0]
	if e := s.DecideApproval(ctx, p.ID, p.Digest, "approved"); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { done <- engine.Run(ctx, id) }()
	until := time.Now().Add(2 * time.Second)
	for time.Now().Before(until) {
		if _, e := os.Stat(marker); e == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if e := engine.Cancel(ctx, id); e != nil {
		t.Fatal(e)
	}
	if e := <-done; !errors.Is(e, ErrReconciliation) {
		t.Fatal("cancel claimed stopped", e)
	}
	until = time.Now().Add(2 * time.Second)
	for time.Now().Before(until) {
		// WriteFile creates the marker before writing its bytes. Under load,
		// existence alone can expose an empty in-progress fixture result.
		if data, e := os.ReadFile(marker + ".late"); e == nil && string(data) == "late\n" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	data, e := os.ReadFile(marker + ".late")
	if e != nil || string(data) != "late\n" {
		t.Fatal("late effect not observed", string(data), e)
	}
	cp, _ = engine.Checkpoint(ctx, id)
	if cp.Phase != PhaseReconciliation {
		t.Fatal(cp.Phase)
	}
}

// Directory identity must not include mutable mtime/atime: a trusted server's
// ordinary file creation is not a replacement of its configured directory.
func TestMCPDirectoryIdentityStableAcrossOrdinaryFiles(t *testing.T) {
	s, ctx := setup(t)
	root := t.TempDir()
	marker := filepath.Join(root, "effects")
	cfg := mcpConfig(t, marker, "modern", "")
	cfg.Servers[0].Directory = root
	b := NewWorkspaceBrokerWithOptions(s, WorkspaceBrokerOptions{MCP: cfg})
	defer b.CloseMCP()
	policy, e := b.PrepareMCPPolicy(ctx, "s", WorkspacePolicy{Root: root, Mode: ModeDefault})
	if e != nil {
		t.Fatal(e)
	}
	w, e := s.BindWorkspace(ctx, "s", policy)
	if e != nil {
		t.Fatal(e)
	}
	r, e := s.CreateRun(ctx, "s", "r", Message{})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = b.Propose(ctx, r.ID, mcpCall(w)); e != nil {
		t.Fatal("ordinary server launch marker falsely revoked cwd identity", e)
	}
}

func TestMCPExecutableReplacementRevokesApproval(t *testing.T) {
	s, ctx := setup(t)
	marker := filepath.Join(t.TempDir(), "effects")
	cfg := mcpConfig(t, marker, "modern", "")
	raw, e := os.ReadFile(cfg.Servers[0].Command)
	if e != nil {
		t.Fatal(e)
	}
	command := filepath.Join(t.TempDir(), "owned-peer")
	if e = os.WriteFile(command, raw, 0700); e != nil {
		t.Fatal(e)
	}
	cfg.Servers[0].Command = command
	b := NewWorkspaceBrokerWithOptions(s, WorkspaceBrokerOptions{MCP: cfg})
	defer b.CloseMCP()
	policy, e := b.PrepareMCPPolicy(ctx, "s", WorkspacePolicy{Root: t.TempDir(), Mode: ModeDefault})
	if e != nil {
		t.Fatal(e)
	}
	w, e := s.BindWorkspace(ctx, "s", policy)
	if e != nil {
		t.Fatal(e)
	}
	r, e := s.CreateRun(ctx, "s", "r", Message{})
	if e != nil {
		t.Fatal(e)
	}
	engine := runWorkspaceTool(t, s, b, r.ID, mcpCall(w))
	if e = engine.Run(ctx, r.ID); !errors.Is(e, ErrWaitingApproval) {
		t.Fatal(e)
	}
	cp, _ := engine.Checkpoint(ctx, r.ID)
	p := cp.Pending[0]
	if e = os.WriteFile(command+".replacement", []byte("changed trusted executable"), 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.Rename(command+".replacement", command); e != nil {
		t.Fatal(e)
	}
	review, e := b.ApprovalReview(ctx, "s", p.ID)
	if e != nil || review.MCP.UnavailableReason == "" {
		t.Fatal(review, e)
	}
	if e = s.DecideApproval(ctx, p.ID, p.Digest, "approved"); e != nil {
		t.Fatal(e)
	}
	if e = engine.Run(ctx, r.ID); e != nil {
		t.Fatal(e)
	}
	noEffect(t, marker)
}
func TestMCPSameToolNamesDoNotCrossServersOrSessions(t *testing.T) {
	s, ctx := setup(t)
	marker := filepath.Join(t.TempDir(), "effects")
	cfg := mcpConfig(t, marker, "modern", "")
	second := cfg.Servers[0]
	second.ID = "second"
	cfg.Servers = append(cfg.Servers, second)
	b := NewWorkspaceBrokerWithOptions(s, WorkspaceBrokerOptions{MCP: cfg})
	defer b.CloseMCP()
	policy, e := b.PrepareMCPPolicy(ctx, "s", WorkspacePolicy{Root: t.TempDir(), Mode: ModeDefault})
	if e != nil {
		t.Fatal(e)
	}
	w, e := s.BindWorkspace(ctx, "s", policy)
	if e != nil {
		t.Fatal(e)
	}
	if w.Policy.MCP[0].Tools[0].Alias == w.Policy.MCP[1].Tools[0].Alias {
		t.Fatal("server name collision")
	}
	if e = s.CreateSession(ctx, "other"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.BindWorkspace(ctx, "other", WorkspacePolicy{Root: t.TempDir(), Mode: ModeAcceptEdits}); e != nil {
		t.Fatal(e)
	}
	r, e := s.CreateRun(ctx, "other", "r", Message{})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = b.Propose(ctx, r.ID, mcpCall(w)); e == nil {
		t.Fatal("cross-session capability accepted")
	}
	noEffect(t, marker)
}

func TestMCPBoundedArtifactAndEmptyObservation(t *testing.T) {
	for _, kind := range []string{"large", "near", "empty"} {
		t.Run(kind, func(t *testing.T) {
			s, b, w, id, _ := mcpFixture(t, ModeDefault, "modern", kind)
			ctx := context.Background()
			engine := runWorkspaceTool(t, s, b, id, mcpCall(w))
			if e := engine.Run(ctx, id); !errors.Is(e, ErrWaitingApproval) {
				t.Fatal(e)
			}
			cp, _ := engine.Checkpoint(ctx, id)
			p := cp.Pending[0]
			if e := s.DecideApproval(ctx, p.ID, p.Digest, "approved"); e != nil {
				t.Fatal(e)
			}
			if e := engine.Run(ctx, id); e != nil {
				t.Fatal(e)
			}
			cp, _ = engine.Checkpoint(ctx, id)
			for _, msg := range cp.Messages {
				if msg.Role != "tool" {
					continue
				}
				if len(msg.Content) > MaxInlineToolBytes {
					t.Fatal("unbounded inline MCP output")
				}
				var out struct {
					Outcome  string `json:"outcome"`
					Artifact struct {
						ID string `json:"artifact_id"`
					} `json:"artifact"`
				}
				if e := json.Unmarshal([]byte(msg.Content), &out); e != nil {
					t.Fatal(e)
				}
				if kind == "empty" {
					if out.Outcome != "empty" {
						t.Fatal(msg.Content)
					}
				} else {
					if out.Artifact.ID == "" {
						t.Fatal("missing output artifact", msg.Content)
					}
					raw, e := s.ReadArtifact(ctx, "s", out.Artifact.ID)
					if e != nil || len(raw) < 3900 {
						t.Fatal(len(raw), e)
					}
					if _, e = s.ReadArtifact(ctx, "other", out.Artifact.ID); e == nil {
						t.Fatal("cross-session artifact")
					}
				}
				return
			}
			t.Fatal("no tool observation")
		})
	}
}
func TestMCPStatusPreservesRevokedBoundServer(t *testing.T) {
	s, b, _, _, _ := mcpFixture(t, ModeDefault, "modern", "")
	b.CloseMCP()
	revoked := NewWorkspaceBroker(s)
	status := revoked.MCPStatus(context.Background(), "s")
	if len(status.Servers) != 1 || status.Servers[0].ID != "owned" || status.Servers[0].State != "disconnected" {
		t.Fatal("revoked bound server hidden", status)
	}
}

func TestMCPCancelledPreparationDoesNotLaunch(t *testing.T) {
	s, ctx := setup(t)
	marker := filepath.Join(t.TempDir(), "effects")
	b := NewWorkspaceBrokerWithOptions(s, WorkspaceBrokerOptions{MCP: mcpConfig(t, marker, "modern", "")})
	defer b.CloseMCP()
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, e := b.PrepareMCPPolicy(cancelled, "s", WorkspacePolicy{Root: t.TempDir(), Mode: ModeDefault}); e == nil {
		t.Fatal("cancelled preparation succeeded")
	}
	noEffect(t, marker+".launch")
}

func TestMCPIntegrationDoesNotInheritHostCredentials(t *testing.T) {
	t.Setenv("PA_AGENT_TOKEN", "owned-test-host-auth-secret")
	t.Setenv("OPENAI_API_KEY", "owned-test-model-secret")
	s, b, w, id, _ := mcpFixture(t, ModeDefault, "modern", "environment")
	ctx := context.Background()
	engine := runWorkspaceTool(t, s, b, id, mcpCall(w))
	if e := engine.Run(ctx, id); !errors.Is(e, ErrWaitingApproval) {
		t.Fatal(e)
	}
	cp, _ := engine.Checkpoint(ctx, id)
	p := cp.Pending[0]
	if e := s.DecideApproval(ctx, p.ID, p.Digest, "approved"); e != nil {
		t.Fatal(e)
	}
	if e := engine.Run(ctx, id); e != nil {
		t.Fatal(e)
	}
	cp, _ = engine.Checkpoint(ctx, id)
	raw, _ := json.Marshal(cp.Messages)
	if strings.Contains(string(raw), "owned-test-host-auth-secret") || strings.Contains(string(raw), "owned-test-model-secret") || !strings.Contains(string(raw), "auth=;model=") {
		t.Fatal("host credential inherited or fixture did not report environment", string(raw))
	}
}

func TestMCPAdmissionReservationDoesNotBlockHostInspectionOrClose(t *testing.T) {
	s, ctx := setup(t)
	marker := filepath.Join(t.TempDir(), "effect")
	cfg := mcpConfig(t, marker, "", "")
	cfg.Servers[0].Environment = append(cfg.Servers[0].Environment, "PA_MCP_PAUSE_DISCOVERY=1")
	b := NewWorkspaceBrokerWithOptions(s, WorkspaceBrokerOptions{MCP: cfg})
	defer b.CloseMCP()
	root := t.TempDir()
	policy := WorkspacePolicy{Root: root, Mode: ModeDefault}
	if e := os.WriteFile(marker+".release", []byte("go"), 0600); e != nil {
		t.Fatal(e)
	}
	ready, e := b.PrepareMCPPolicy(ctx, "ready", policy)
	if e != nil {
		t.Fatal(e)
	}
	client, _, e := b.liveMCP("ready", ready.MCP[0])
	if e != nil {
		t.Fatal(e)
	}
	os.Remove(marker + ".release")
	os.Remove(marker + ".paused")
	done := make(chan error, 1)
	go func() { _, err := b.PrepareMCPPolicy(ctx, "paused", policy); done <- err }()
	until := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(marker + ".paused"); err == nil {
			break
		}
		if time.Now().After(until) {
			t.Fatal("peer not paused")
		}
		time.Sleep(time.Millisecond)
	}
	for _, check := range []func() error{
		func() error { b.MCPStatus(ctx, ""); return nil },
		func() error { _, _, e := b.liveMCP("ready", ready.MCP[0]); return e },
		func() error {
			_, e := b.PrepareMCPPolicy(ctx, "paused", policy)
			if !errors.Is(e, ErrConflict) {
				return fmt.Errorf("duplicate reservation: %v", e)
			}
			return nil
		},
	} {
		returned := make(chan error, 1)
		go func() { returned <- check() }()
		select {
		case e := <-returned:
			if e != nil {
				t.Error(e)
			}
		case <-time.After(500 * time.Millisecond):
			t.Error("host lock held through discovery")
		}
	}
	closed := make(chan struct{}, 2)
	for i := 0; i < 2; i++ {
		go func() { b.CloseMCP(); closed <- struct{}{} }()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-closed:
		case <-time.After(2 * time.Second):
			t.Error("close did not cancel admission")
		}
	}
	select {
	case e := <-done:
		if e == nil {
			t.Error("published after close")
		}
	case <-time.After(4 * time.Second):
		t.Fatal("admission leaked")
	}
	select {
	case <-client.Done():
	default:
		t.Error("close returned before child reaped")
	}
	data, _ := os.ReadFile(marker + ".launch")
	if strings.Count(string(data), "\n") != 2 {
		t.Fatal("duplicate startup", string(data))
	}
}

func TestMCPAdmissionForgetCancelsPendingAndReleasesCapacity(t *testing.T) {
	s, ctx := setup(t)
	marker := filepath.Join(t.TempDir(), "effect")
	cfg := mcpConfig(t, marker, "", "")
	cfg.Servers[0].Environment = append(cfg.Servers[0].Environment, "PA_MCP_PAUSE_DISCOVERY=1")
	b := NewWorkspaceBrokerWithOptions(s, WorkspaceBrokerOptions{MCP: cfg})
	defer b.CloseMCP()
	policy := WorkspacePolicy{Root: t.TempDir(), Mode: ModeDefault}
	finished := make(chan error, 1)
	go func() { _, e := b.PrepareMCPPolicy(ctx, "pending", policy); finished <- e }()
	until := time.Now().Add(5 * time.Second)
	for {
		if _, e := os.Stat(marker + ".paused"); e == nil {
			break
		}
		if time.Now().After(until) {
			t.Fatal("not paused")
		}
		time.Sleep(time.Millisecond)
	}
	forgotten := make(chan struct{})
	go func() { b.ForgetMCPSession("pending"); close(forgotten) }()
	select {
	case <-forgotten:
	case <-time.After(time.Second):
		t.Fatal("forget blocked on external discovery")
	}
	if e := <-finished; e == nil {
		t.Fatal("published forgotten admission")
	}
	if _, e := b.PrepareMCPPolicy(ctx, "pending", policy); !errors.Is(e, ErrConflict) {
		t.Fatal("relaunch allowed", e)
	}
	b.mcp.mu.Lock()
	reserved, live := b.mcp.reserved, len(b.mcp.processes)
	b.mcp.mu.Unlock()
	if reserved != 0 || live != 0 {
		t.Fatal("leaked reservation", reserved, live)
	}
	if e := os.WriteFile(marker+".release", []byte("go"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := b.PrepareMCPPolicy(ctx, "new", policy); e != nil {
		t.Fatal("fresh admission blocked", e)
	}
	data, _ := os.ReadFile(marker + ".launch")
	if strings.Count(string(data), "\n") != 2 {
		t.Fatal(string(data))
	}
}

func TestMCPAdmissionCapacityIncludesUnpublishedProcesses(t *testing.T) {
	s, ctx := setup(t)
	marker := filepath.Join(t.TempDir(), "effect")
	cfg := mcpConfig(t, marker, "", "")
	cfg.Servers[0].Environment = append(cfg.Servers[0].Environment, "PA_MCP_PAUSE_DISCOVERY=1")
	server := cfg.Servers[0]
	cfg.Servers = nil
	for i := 0; i < 8; i++ {
		copy := server
		copy.ID = fmt.Sprintf("owned-%d", i)
		cfg.Servers = append(cfg.Servers, copy)
	}
	b := NewWorkspaceBrokerWithOptions(s, WorkspaceBrokerOptions{MCP: cfg})
	defer b.CloseMCP()
	policy := WorkspacePolicy{Root: t.TempDir(), Mode: ModeDefault}
	done := make(chan error, 2)
	for _, session := range []string{"first", "second"} {
		go func() { _, e := b.PrepareMCPPolicy(ctx, session, policy); done <- e }()
	}
	until := time.Now().Add(5 * time.Second)
	for {
		data, _ := os.ReadFile(marker + ".launch")
		if strings.Count(string(data), "\n") == 2 {
			break
		}
		if time.Now().After(until) {
			t.Fatal("admissions not independently starting")
		}
		time.Sleep(time.Millisecond)
	}
	returned := make(chan error, 1)
	go func() { _, e := b.PrepareMCPPolicy(ctx, "overflow", policy); returned <- e }()
	select {
	case e := <-returned:
		if !errors.Is(e, ErrCapacity) {
			t.Fatal("unreserved capacity", e)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("capacity check blocked behind startup")
	}
	b.CloseMCP()
	for i := 0; i < 2; i++ {
		if e := <-done; e == nil {
			t.Fatal("published after shutdown")
		}
	}
	data, _ := os.ReadFile(marker + ".launch")
	if strings.Count(string(data), "\n") != 2 {
		t.Fatal("overflow launched", string(data))
	}
}
