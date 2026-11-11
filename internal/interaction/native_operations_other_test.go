//go:build !linux

package interaction

import (
	"agent/internal/runtime"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Synthetic persisted authority exercises refusal of old state, never target
// qualification. It does not call BindWorkspace or a skipping Linux fixture.
func unsupportedWorkspaceFixture(t *testing.T, mode PermissionMode) (*Store, WorkspaceBinding) {
	t.Helper()
	s, ctx := setup(t)
	w := WorkspaceBinding{SessionID: "s", ScopeID: "synthetic-native-scope", RootIdentity: "synthetic-native-root", Policy: WorkspacePolicy{Root: t.TempDir(), Mode: mode, NonSandboxExec: true}}
	raw, err := json.Marshal(w.Policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, `INSERT INTO interaction_workspaces(session_id,scope_id,policy,root_identity) VALUES(?,?,?,?)`, w.SessionID, w.ScopeID, string(raw), w.RootIdentity); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, `INSERT INTO interaction_api_sessions(session_id,request_id,binding,mode) VALUES(?,?,?,?)`, w.SessionID, "synthetic-session", "offline-provider", string(mode)); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(w.Policy.Root, "note.txt"), []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	return s, w
}

func nativeStoredCounts(t *testing.T, s *Store, tables ...string) []int {
	t.Helper()
	var counts []int
	for _, table := range tables {
		var n int
		if e := s.db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); e != nil {
			t.Fatal(e)
		}
		counts = append(counts, n)
	}
	return counts
}

// Seed the same immutable metadata and one-way approval used by the broker;
// only the native primitive is unavailable. No production support is injected.
func nativeStoredProposal(t *testing.T, s *Store, w WorkspaceBinding, r Run, call ToolCall, review *ExecReview) preparedTool {
	t.Helper()
	ctx := context.Background()
	digest, err := ProposalDigest(r.ID, call)
	if err != nil {
		t.Fatal(err)
	}
	p := preparedTool{RunID: r.ID, SessionID: w.SessionID, ScopeID: w.ScopeID, RootIdentity: w.RootIdentity, Proposal: ToolProposal{ID: contentHash([]byte(digest + ":" + w.ScopeID)), Call: call, Digest: digest, ExpiresAt: time.Now().UTC().Add(5 * time.Minute)}, ExecReview: review}
	if call.Name == "exec" {
		p.Proposal.RequiresApproval = true
		p.Proposal.Digest = execDigest(digest, review)
	}
	if call.Name == "apply_patch" {
		var args PatchArguments
		if err = json.Unmarshal([]byte(call.Arguments), &args); err != nil {
			t.Fatal(err)
		}
		p.Review = &EditReview{Scope: w.ScopeID, Path: args.Path, OldContent: "old", NewContent: args.Content, OldSHA256: args.Preimage, NewSHA256: contentHash([]byte(args.Content))}
		p.Proposal.RequiresApproval = w.Policy.Mode != ModeAcceptEdits
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, `INSERT INTO interaction_tool_proposals(id,run_id,call_id,prepared) VALUES(?,?,?,?)`, p.Proposal.ID, r.ID, call.ID, string(raw)); err != nil {
		t.Fatal(err)
	}
	cp := Checkpoint{Phase: PhaseExecuting, Pending: []ToolProposal{p.Proposal}}
	checkpoint, err := json.Marshal(cp)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, `INSERT INTO interaction_engine(run_id,phase,checkpoint) VALUES(?,?,?)`, r.ID, cp.Phase, string(checkpoint)); err != nil {
		t.Fatal(err)
	}
	if p.Proposal.RequiresApproval {
		if err = s.CreateApproval(ctx, Approval{ID: p.Proposal.ID, RunID: r.ID, ActionDigest: p.Proposal.Digest, ExpiresAt: p.Proposal.ExpiresAt}); err != nil {
			t.Fatal(err)
		}
		if err = s.DecideApproval(ctx, p.Proposal.ID, p.Proposal.Digest, "approved"); err != nil {
			t.Fatal(err)
		}
		if err = s.ConsumeApproval(ctx, p.Proposal.ID, p.Proposal.Digest); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func TestUnavailableNativeProposalAndDispatch(t *testing.T) {
	for _, mode := range []PermissionMode{ModePlan, ModeDefault, ModeAcceptEdits} {
		for _, name := range []string{"read", "search", "apply_patch", "exec"} {
			t.Run(string(mode)+"/"+name, func(t *testing.T) {
				s, w := unsupportedWorkspaceFixture(t, mode)
				ctx := context.Background()
				r, err := s.CreateRun(ctx, "s", "request", Message{Content: "synthetic persisted request"})
				if err != nil {
					t.Fatal(err)
				}
				marker := filepath.Join(w.Policy.Root, "spawned")
				binary, err := os.Executable()
				if err != nil {
					t.Fatal(err)
				}
				review := &ExecReview{Program: binary, Argv: []string{"-test.run=^TestNativeRefusalSpawnSentinel$"}, Cwd: w.Policy.Root, Environment: []EnvironmentChange{{"PA_NATIVE_REFUSAL_MARKER", marker}}, ExecutableIdentity: "synthetic", ExecutableSHA256: "uninspected", TimeoutMillis: 1000}
				var args any
				switch name {
				case "read":
					args = ReadArguments{Scope: w.ScopeID, Path: "note.txt"}
				case "search":
					args = SearchArguments{Scope: w.ScopeID, Paths: []string{"note.txt"}, Query: "old"}
				case "apply_patch":
					args = PatchArguments{Scope: w.ScopeID, Path: "note.txt", Preimage: contentHash([]byte("old")), Content: "new"}
				case "exec":
					args = ExecArguments{Scope: w.ScopeID, Program: binary, Cwd: w.Policy.Root, Argv: review.Argv, Environment: map[string]string{"PA_NATIVE_REFUSAL_MARKER": marker}, TimeoutMillis: 1000}
				}
				raw, _ := json.Marshal(args)
				call := ToolCall{ID: "native-call", Name: name, Arguments: string(raw)}
				b := NewWorkspaceBrokerWithOptions(s, WorkspaceBrokerOptions{NonSandboxExec: true})
				defer b.CloseMCP()
				if p, e := b.Propose(ctx, r.ID, call); !errors.Is(e, ErrCapabilityUnavailable) || p.ID != "" {
					t.Fatal(p, e)
				}
				if got := nativeStoredCounts(t, s, "interaction_tool_proposals", "interaction_artifacts"); !reflect.DeepEqual(got, []int{0, 0}) {
					t.Fatal(got)
				}
				var execReview *ExecReview
				if name == "exec" {
					execReview = review
				}
				p := nativeStoredProposal(t, s, w, r, call, execReview)
				// Incomplete/mismatched checkpoints must refuse before claiming anything.
				for _, cp := range []Checkpoint{{Phase: PhaseRunning, Pending: []ToolProposal{p.Proposal}}, {Phase: PhaseExecuting}, {Phase: PhaseExecuting, Pending: []ToolProposal{{ID: "wrong"}}}} {
					bytes, _ := json.Marshal(cp)
					if _, err = s.db.Exec(`UPDATE interaction_engine SET phase=?,checkpoint=? WHERE run_id=?`, cp.Phase, string(bytes), r.ID); err != nil {
						t.Fatal(err)
					}
					result, e := b.Execute(ctx, p.Proposal.ID)
					if e != nil || !result.IsError {
						t.Fatal(result, e)
					}
					var executed int
					if err = s.db.QueryRow(`SELECT executed FROM interaction_tool_proposals WHERE id=?`, p.Proposal.ID).Scan(&executed); err != nil || executed != 0 {
						t.Fatal("invalid checkpoint claimed", executed, err)
					}
				}
				cp := Checkpoint{Phase: PhaseExecuting, Pending: []ToolProposal{p.Proposal}}
				bytes, _ := json.Marshal(cp)
				if _, err = s.db.Exec(`UPDATE interaction_engine SET phase=?,checkpoint=? WHERE run_id=?`, cp.Phase, string(bytes), r.ID); err != nil {
					t.Fatal(err)
				}
				result, e := b.Execute(ctx, p.Proposal.ID)
				if e != nil || result != (ToolResult{IsError: true, Content: "Tool refused: capability unavailable"}) {
					t.Fatal(result, e)
				}
				var executed int
				if err = s.db.QueryRow(`SELECT executed FROM interaction_tool_proposals WHERE id=?`, p.Proposal.ID).Scan(&executed); err != nil || executed != 1 {
					t.Fatal("refusal must retain one-way claim", executed, err)
				}
				replay, e := b.Execute(ctx, p.Proposal.ID)
				if e != nil || !replay.IsError || replay.Content != "Tool refused: "+ErrConflict.Error() {
					t.Fatal("replayed", replay, e)
				}
				if _, e = b.Propose(ctx, r.ID, call); !errors.Is(e, ErrCapabilityUnavailable) {
					t.Fatal("unavailable stored proposal replayed", e)
				}
				data, e := os.ReadFile(filepath.Join(w.Policy.Root, "note.txt"))
				if e != nil || string(data) != "old" {
					t.Fatal(string(data), e)
				}
				if _, e = os.Stat(marker); !errors.Is(e, os.ErrNotExist) {
					t.Fatal("spawned", e)
				}
				if got := nativeStoredCounts(t, s, "interaction_artifacts", "interaction_file_heads", "interaction_edit_undos"); !reflect.DeepEqual(got, []int{0, 0, 0}) {
					t.Fatal(got)
				}
			})
		}
	}
}

func TestUnavailableNativeStoredOperations(t *testing.T) {
	for _, mode := range []PermissionMode{ModePlan, ModeDefault, ModeAcceptEdits} {
		t.Run(string(mode), func(t *testing.T) {
			s, w := unsupportedWorkspaceFixture(t, mode)
			ctx := context.Background()
			tx, err := s.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = skillRunWorkspace(ctx, tx, "s")
			tx.Rollback()
			if !errors.Is(err, ErrCapabilityUnavailable) {
				t.Fatal(err)
			}
			imported := skillDecide(t, s, skillPrepare(t, s, "import", readonlyManifest), "import-confirm", "confirm")
			enabled, err := s.PrepareSkill(ctx, "s", SkillIntent{RequestID: "enable", Kind: "enable", CatalogID: imported.CatalogID, RevisionID: imported.RevisionID})
			if err != nil {
				t.Fatal(err)
			}
			skillDecide(t, s, enabled, "enable-confirm", "confirm")
			preview, err := s.PrepareSkillRun(ctx, "s", SkillRunIntent{RequestID: "preview", CatalogID: imported.CatalogID, RevisionID: imported.RevisionID, ExecutionProfile: SkillRunProfile, Inputs: json.RawMessage(readonlyInputs)})
			if err != nil || preview.Review.Eligibility != "blocked" || preview.Review.Reason != "unsupported_workspace" {
				t.Fatal(preview, err)
			}
			decision, err := s.DecideSkillRun(ctx, "s", OperatorDecision{RequestID: "confirm", OperationID: preview.OperationID, Digest: preview.Digest, Decision: "confirm"})
			if err != nil || decision.State == "admitted" || decision.RunID != "" {
				t.Fatal(decision, err)
			}
			if got := nativeStoredCounts(t, s, "interaction_runs"); !reflect.DeepEqual(got, []int{0}) {
				t.Fatal(got)
			}
			_, err = s.PrepareKnowledge(ctx, "s", KnowledgeIntent{RequestID: "file-preview", Kind: "ingest", SourceKind: "file", Source: "note.txt"})
			wantKnowledgeError := ErrCapabilityUnavailable
			if mode == ModePlan {
				wantKnowledgeError = ErrKnowledgePolicy
			}
			if !errors.Is(err, wantKnowledgeError) {
				t.Fatal("knowledge authority must refuse", err)
			}
			if got := nativeStoredCounts(t, s, "documents", "document_chunks", "project_document_scopes", "interaction_knowledge_operations", "interaction_knowledge_requests"); !reflect.DeepEqual(got, []int{0, 0, 0, 0, 0}) {
				t.Fatal(got)
			}
			inventory, err := s.EditCheckpoints(ctx, "s")
			if err != nil || inventory.Supported || len(inventory.Checkpoints) != 0 {
				t.Fatal(inventory, err)
			}
			// A genuine-shaped historic success reaches the Undo support guard without
			// performing an edit or weakening immutable DB triggers.
			r, err := s.CreateRun(ctx, "s", "historic", Message{Content: "historic native edit"})
			if err != nil {
				t.Fatal(err)
			}
			p := nativeStoredProposal(t, s, w, r, patchCall(w), nil)
			if _, err = s.db.Exec(`UPDATE interaction_tool_proposals SET executed=1 WHERE id=?`, p.Proposal.ID); err != nil {
				t.Fatal(err)
			}
			if _, err = s.db.Exec(`INSERT INTO interaction_file_heads(scope_id,path,proposal_id) VALUES(?,?,?)`, w.ScopeID, "note.txt", p.Proposal.ID); err != nil {
				t.Fatal(err)
			}
			journal := runtime.SQLiteSideEffectStore{DB: s.db}
			key := fmt.Sprintf("interaction:%d:%s:%s", len(r.ID), r.ID, p.Proposal.ID)
			if _, claimed, e := journal.Claim(ctx, runtime.SideEffectRecord{IdempotencyKey: key, WorkflowID: r.ID, NodeID: p.Proposal.ID, CapabilityID: "apply_patch"}); e != nil || !claimed {
				t.Fatal(claimed, e)
			}
			result, _ := json.Marshal(ToolResult{Content: "Applied exact replacement; SHA256 " + p.Review.NewSHA256})
			if err = journal.Complete(ctx, key, string(result)); err != nil {
				t.Fatal(err)
			}
			if _, err = s.db.Exec(`UPDATE interaction_runs SET state='completed' WHERE id=?`, r.ID); err != nil {
				t.Fatal(err)
			}
			completed, err := json.Marshal(Checkpoint{Phase: PhaseCompleted})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.db.Exec(`UPDATE interaction_engine SET phase=?,checkpoint=? WHERE run_id=?`, PhaseCompleted, string(completed), r.ID); err != nil {
				t.Fatal(err)
			}
			checkpoint, err := editCheckpoint(ctx, s.db, "s", p.Proposal.ID)
			if err != nil || checkpoint.Disposition != "available" {
				t.Fatal(checkpoint, err)
			}
			counts := nativeStoredCounts(t, s, "interaction_runs", "interaction_edit_undos", "interaction_session_operations", "interaction_file_heads")
			if _, err = s.UndoEdit(ctx, "s", undoIntent(t, s, p.Proposal.ID)); !errors.Is(err, ErrCapabilityUnavailable) {
				t.Fatal("Undo support guard not reached", err)
			}
			if !reflect.DeepEqual(counts, nativeStoredCounts(t, s, "interaction_runs", "interaction_edit_undos", "interaction_session_operations", "interaction_file_heads")) {
				t.Fatal("undo reserved state")
			}
			data, e := os.ReadFile(filepath.Join(w.Policy.Root, "note.txt"))
			if e != nil || string(data) != "old" {
				t.Fatal(string(data), e)
			}
		})
	}
}

func TestUnavailableNativeMCPPreparation(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	marker := filepath.Join(root, "mcp")
	config := MCPHostConfig{Enabled: true, Servers: []MCPServerConfig{{ID: "native-fixture", Command: binary, Args: []string{"-test.run=^TestBrokerMCPPeer$"}, Environment: []string{"PA_MCP_FIXTURE=1", "PA_MCP_MARKER=" + marker}, TimeoutMillis: 1000}}}
	if err = ValidateMCPConfig(config); err != nil {
		t.Fatal(err)
	}
	b := NewWorkspaceBrokerWithOptions(nil, WorkspaceBrokerOptions{MCP: config})
	defer b.CloseMCP()
	p := WorkspacePolicy{Root: root, Mode: ModeDefault, SystemConstraints: []string{"unchanged"}}
	before, _ := json.Marshal(p)
	for _, cancelled := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		if cancelled {
			cancel()
		}
		got, e := b.PrepareMCPPolicy(ctx, "native-session", p)
		cancel()
		if cancelled {
			if !errors.Is(e, context.Canceled) {
				t.Fatal(e)
			}
		} else if e != ErrCapabilityUnavailable {
			t.Fatal(e)
		}
		after, _ := json.Marshal(got)
		input, _ := json.Marshal(p)
		if string(after) != string(before) || string(input) != string(before) {
			t.Fatal("policy changed", got)
		}
	}
	b.mcp.mu.Lock()
	admissions, processes, reserved := len(b.mcp.admissions), len(b.mcp.processes), b.mcp.reserved
	b.mcp.mu.Unlock()
	if admissions != 0 || processes != 0 || reserved != 0 {
		t.Fatal(admissions, processes, reserved)
	}
	for _, path := range []string{marker, marker + ".launch"} {
		if _, e := os.Stat(path); !errors.Is(e, os.ErrNotExist) {
			t.Fatal(path, e)
		}
	}
	// A missing executable distinguishes capability refusal from identity hashing.
	config.Servers[0].Command = filepath.Join(root, "must-not-be-inspected")
	missing := NewWorkspaceBrokerWithOptions(nil, WorkspaceBrokerOptions{MCP: config})
	defer missing.CloseMCP()
	if _, e := missing.PrepareMCPPolicy(context.Background(), "missing-command", p); e != ErrCapabilityUnavailable || strings.Contains(fmt.Sprint(e), "must-not") {
		t.Fatal(e)
	}
}
