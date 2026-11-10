package interaction

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agent/internal/model"
	"agent/internal/storage"
)

func workspaceFixture(t *testing.T, mode PermissionMode) (*Store, *WorkspaceBroker, WorkspaceBinding, string) {
	t.Helper()
	if !platformFileCapability().Enabled {
		t.Skip(platformFileCapability().Reason)
	}
	s, c := setup(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	w, err := s.BindWorkspace(c, "s", WorkspacePolicy{Root: root, Mode: mode, SystemConstraints: []string{"Never disclose secrets."}, UserConstraints: []string{"Only change this workspace."}})
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.CreateRun(c, "s", "request", Message{Content: "note"})
	if err != nil {
		t.Fatal(err)
	}
	return s, NewWorkspaceBroker(s), w, r.ID
}
func patchCall(w WorkspaceBinding) ToolCall {
	raw, _ := json.Marshal(PatchArguments{Scope: w.ScopeID, Path: "note.txt", Preimage: contentHash([]byte("old")), Content: "new"})
	return ToolCall{ID: "edit", Name: "apply_patch", Arguments: string(raw)}
}
func runWorkspaceTool(t *testing.T, s *Store, b *WorkspaceBroker, id string, call ToolCall) *Engine {
	t.Helper()
	turns := 0
	e, err := NewEngine(s, streamFunc(func(context.Context, model.TurnRequest, func(model.ModelEvent) error) (model.TurnResult, error) {
		turns++
		if turns == 1 {
			return model.TurnResult{ToolCalls: []model.ToolCall{call}}, nil
		}
		return model.TurnResult{Content: "done"}, nil
	}), b, EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func TestWorkspaceBindingImmutableAndBeforeRun(t *testing.T) {
	s, _, w, _ := workspaceFixture(t, ModeDefault)
	if _, err := s.BindWorkspace(context.Background(), "s", WorkspacePolicy{Root: t.TempDir(), Mode: ModeDefault}); err == nil {
		t.Fatal("rebound workspace")
	}
	if _, err := s.db.Exec("UPDATE interaction_workspaces SET policy='{}' WHERE session_id='s'"); err == nil {
		t.Fatal("mutable persisted policy")
	}
	got, err := s.Workspace(context.Background(), "s")
	if err != nil || got.ScopeID != w.ScopeID {
		t.Fatal(got, err)
	}
	if err = s.CreateSession(context.Background(), "late"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateRun(context.Background(), "late", "r", Message{}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.BindWorkspace(context.Background(), "late", WorkspacePolicy{Root: t.TempDir(), Mode: ModeDefault}); err == nil {
		t.Fatal("bound after run")
	}
}
func TestWorkspaceProposalRestartApprovalAndStalePreimage(t *testing.T) {
	s, b, w, id := workspaceFixture(t, ModeDefault)
	c := context.Background()
	call := patchCall(w)
	p, err := b.Propose(c, id, call)
	if err != nil {
		t.Fatal(err)
	}
	p2, err := NewWorkspaceBroker(s).Propose(c, id, call)
	if err != nil || p.ID != p2.ID || p.ExpiresAt != p2.ExpiresAt {
		t.Fatal(p, p2, err)
	}
	if result, err := b.Execute(c, p.ID); err != nil || !result.IsError {
		t.Fatal("executed without checkpoint/approval", result, err)
	}
	e := runWorkspaceTool(t, s, b, id, call)
	if err = e.Run(c, id); !errors.Is(err, ErrWaitingApproval) {
		t.Fatal(err)
	}
	if err = s.DecideApproval(c, p.ID, p.Digest, "approved"); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(w.Policy.Root, "note.txt"), []byte("outside writer"), 0600); err != nil {
		t.Fatal(err)
	}
	e.broker = NewWorkspaceBroker(s)
	if err = e.Run(c, id); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(filepath.Join(w.Policy.Root, "note.txt"))
	if string(body) != "outside writer" {
		t.Fatal("stale edit overwrote file")
	}
	cp, _ := e.Checkpoint(c, id)
	raw, _ := json.Marshal(cp.Messages)
	if !strings.Contains(string(raw), "preimage") {
		t.Fatal(string(raw))
	}
}
func TestWorkspaceAcceptEditsPlanAndDisabledExec(t *testing.T) {
	for _, mode := range []PermissionMode{ModePlan, ModeAcceptEdits} {
		t.Run(string(mode), func(t *testing.T) {
			s, b, w, id := workspaceFixture(t, mode)
			c := context.Background()
			p, err := b.Propose(c, id, patchCall(w))
			if mode == ModePlan {
				if err == nil {
					t.Fatal("plan prepared write")
				}
				return
			}
			if err != nil || p.RequiresApproval {
				t.Fatal(p, err)
			}
			e := runWorkspaceTool(t, s, b, id, patchCall(w))
			if err = e.Run(c, id); err != nil {
				t.Fatal(err)
			}
			body, _ := os.ReadFile(filepath.Join(w.Policy.Root, "note.txt"))
			if string(body) != "new" {
				t.Fatal(string(body))
			}
			caps := b.Capabilities()
			if caps.Exec.Enabled || caps.Exec.Reason == "" {
				t.Fatal(caps)
			}
			raw, _ := json.Marshal(ExecArguments{Scope: w.ScopeID, Program: "/bin/echo", Argv: []string{"safe"}, Cwd: ".", Environment: map[string]string{}, TimeoutMillis: 1000})
			if _, err = b.Propose(c, id, ToolCall{ID: "exec", Name: "exec", Arguments: string(raw)}); !errors.Is(err, ErrCapabilityUnavailable) {
				t.Fatal(err)
			}
		})
	}
}
func TestWorkspaceReadPathAndSizeBoundaries(t *testing.T) {
	s, b, w, id := workspaceFixture(t, ModeDefault)
	_ = s
	if err := os.Symlink("note.txt", filepath.Join(w.Policy.Root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(w.Policy.Root, "note.txt"), filepath.Join(w.Policy.Root, "hard")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.Policy.Root, "large"), []byte(strings.Repeat("x", MaxWorkspaceFileBytes+1)), 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"../escape", "/etc/passwd", ".git/config", "data/state.db", "link", "hard", "large"} {
		raw, _ := json.Marshal(ReadArguments{Scope: w.ScopeID, Path: path})
		if _, err := b.Propose(context.Background(), id, ToolCall{ID: path, Name: "read", Arguments: string(raw)}); err == nil {
			t.Errorf("accepted %q", path)
		}
	}
}

func TestWorkspaceRootReplacementAndExecutionSymlinkSwap(t *testing.T) {
	for _, swap := range []string{"root", "symlink"} {
		t.Run(swap, func(t *testing.T) {
			s, b, w, id := workspaceFixture(t, ModeDefault)
			ctx := context.Background()
			e := runWorkspaceTool(t, s, b, id, patchCall(w))
			if err := e.Run(ctx, id); !errors.Is(err, ErrWaitingApproval) {
				t.Fatal(err)
			}
			cp, _ := e.Checkpoint(ctx, id)
			p := cp.Pending[0]
			if err := s.DecideApproval(ctx, p.ID, p.Digest, "approved"); err != nil {
				t.Fatal(err)
			}
			outside := t.TempDir()
			target := filepath.Join(outside, "victim")
			if err := os.WriteFile(target, []byte("old"), 0600); err != nil {
				t.Fatal(err)
			}
			if swap == "root" {
				if err := os.Rename(w.Policy.Root, w.Policy.Root+"-old"); err != nil {
					t.Fatal(err)
				}
				defer os.RemoveAll(w.Policy.Root + "-old")
				if err := os.Mkdir(w.Policy.Root, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(w.Policy.Root, "note.txt"), []byte("old"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Remove(filepath.Join(w.Policy.Root, "note.txt")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, filepath.Join(w.Policy.Root, "note.txt")); err != nil {
					t.Fatal(err)
				}
			}
			if err := e.Run(ctx, id); err != nil {
				t.Fatal(err)
			}
			got, _ := os.ReadFile(target)
			if string(got) != "old" {
				t.Fatal("escaped workspace")
			}
			cp, _ = e.Checkpoint(ctx, id)
			found := false
			for _, m := range cp.Messages {
				if m.Role == "tool" && strings.Contains(m.Content, "Tool refused:") {
					found = true
				}
			}
			if !found {
				t.Fatal("unsafe edit not refused")
			}
		})
	}
}
func TestWorkspaceReadSearchArtifactsAndNoReplay(t *testing.T) {
	s, b, w, id := workspaceFixture(t, ModeDefault)
	ctx := context.Background()
	long := strings.Repeat("needle hello\n", 1000)
	if err := os.WriteFile(filepath.Join(w.Policy.Root, "large.txt"), []byte(long), 0600); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(SearchArguments{Scope: w.ScopeID, Paths: []string{"large.txt"}, Query: "needle"})
	call := ToolCall{ID: "search", Name: "search", Arguments: string(raw)}
	e := runWorkspaceTool(t, s, b, id, call)
	if err := e.Run(ctx, id); err != nil {
		t.Fatal(err)
	}
	cp, _ := e.Checkpoint(ctx, id)
	var ref struct {
		ID string `json:"artifact_id"`
	}
	for _, m := range cp.Messages {
		if m.Role == "tool" {
			if err := json.Unmarshal([]byte(m.Content), &ref); err != nil {
				t.Fatal(err)
			}
		}
	}
	data, err := s.ReadArtifact(ctx, "s", ref.ID)
	if err != nil || !strings.Contains(string(data), "large.txt:1000:needle hello") {
		t.Fatal(len(data), err)
	}
	p, err := b.loadPrepared(ctx, contentHash([]byte(mustDigest(t, id, call)+":"+w.ScopeID)))
	if err != nil {
		t.Fatal(err)
	}
	result, err := b.Execute(ctx, p.Proposal.ID)
	if err != nil || !result.IsError {
		t.Fatal("terminal replay accepted", result, err)
	}
}
func mustDigest(t *testing.T, id string, c ToolCall) string {
	t.Helper()
	d, e := ProposalDigest(id, c)
	if e != nil {
		t.Fatal(e)
	}
	return d
}
func TestWorkspaceSameCallCannotChangeArgumentsOrScope(t *testing.T) {
	_, b, w, id := workspaceFixture(t, ModeDefault)
	ctx := context.Background()
	call := patchCall(w)
	if _, err := b.Propose(ctx, id, call); err != nil {
		t.Fatal(err)
	}
	var a PatchArguments
	if err := json.Unmarshal([]byte(call.Arguments), &a); err != nil {
		t.Fatal(err)
	}
	a.Content = "changed"
	raw, _ := json.Marshal(a)
	call.Arguments = string(raw)
	if _, err := b.Propose(ctx, id, call); err == nil {
		t.Fatal("changed persisted call")
	}
	a.Scope = "different"
	raw, _ = json.Marshal(a)
	call.Arguments = string(raw)
	call.ID = "another"
	if _, err := b.Propose(ctx, id, call); err == nil {
		t.Fatal("wrong scope")
	}
}

func TestWorkspaceReadReturnsExactHashAndScope(t *testing.T) {
	s, b, w, id := workspaceFixture(t, ModeDefault)
	ctx := context.Background()
	raw, _ := json.Marshal(ReadArguments{Scope: w.ScopeID, Path: "note.txt"})
	e := runWorkspaceTool(t, s, b, id, ToolCall{ID: "read", Name: "read", Arguments: string(raw)})
	if err := e.Run(ctx, id); err != nil {
		t.Fatal(err)
	}
	cp, _ := e.Checkpoint(ctx, id)
	var got struct {
		Path, Scope, Content string
		SHA256               string `json:"sha256"`
	}
	for _, m := range cp.Messages {
		if m.Role == "tool" {
			if err := json.Unmarshal([]byte(m.Content), &got); err != nil {
				t.Fatal(err)
			}
		}
	}
	if got.Path != "note.txt" || got.Scope != w.ScopeID || got.Content != "old" || got.SHA256 != contentHash([]byte("old")) {
		t.Fatal(got)
	}
}
func TestWorkspaceEmptyRootRejected(t *testing.T) {
	s, ctx := setup(t)
	if _, err := s.BindWorkspace(ctx, "s", WorkspacePolicy{Mode: ModeDefault}); err == nil {
		t.Fatal("implicitly authorized current working directory")
	}
}

func TestWorkspaceApprovalSurvivesRealDatabaseReopen(t *testing.T) {
	if !platformFileCapability().Enabled {
		t.Skip(platformFileCapability().Reason)
	}
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "state.sqlite")
	db, err := storage.OpenSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = storage.Migrate(ctx, db.SQL); err != nil {
		t.Fatal(err)
	}
	s := NewStore(db.SQL)
	if err = s.CreateSession(ctx, "s"); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err = os.WriteFile(filepath.Join(root, "note.txt"), []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	w, err := s.BindWorkspace(ctx, "s", WorkspacePolicy{Root: root, Mode: ModeDefault})
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.CreateRun(ctx, "s", "r", Message{Content: "edit"})
	if err != nil {
		t.Fatal(err)
	}
	b := NewWorkspaceBroker(s)
	e := runWorkspaceTool(t, s, b, r.ID, patchCall(w))
	if err = e.Run(ctx, r.ID); !errors.Is(err, ErrWaitingApproval) {
		t.Fatal(err)
	}
	cp, err := e.Checkpoint(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	p := cp.Pending[0]
	if err = s.DecideApproval(ctx, p.ID, p.Digest, "approved"); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := storage.OpenSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	s = NewStore(reopened.SQL)
	b = NewWorkspaceBroker(s)
	summary, err := b.ProposalSummary(ctx, p.ID)
	if err != nil || !strings.Contains(summary, w.ScopeID) || !strings.Contains(summary, contentHash([]byte("new"))) {
		t.Fatal(summary, err)
	}
	e, err = NewEngine(s, streamFunc(func(context.Context, model.TurnRequest, func(model.ModelEvent) error) (model.TurnResult, error) {
		return model.TurnResult{Content: "done"}, nil
	}), b, EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Run(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(filepath.Join(root, "note.txt"))
	if string(body) != "new" {
		t.Fatal(string(body))
	}
	result, err := b.Execute(ctx, p.ID)
	if err != nil || !result.IsError {
		t.Fatal("replayed persisted approval", result, err)
	}
}
func TestWorkspaceCancelledApprovalNeverWrites(t *testing.T) {
	s, b, w, id := workspaceFixture(t, ModeDefault)
	ctx := context.Background()
	e := runWorkspaceTool(t, s, b, id, patchCall(w))
	if err := e.Run(ctx, id); !errors.Is(err, ErrWaitingApproval) {
		t.Fatal(err)
	}
	cp, err := e.Checkpoint(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	p := cp.Pending[0]
	if err = s.DecideApproval(ctx, p.ID, p.Digest, "approved"); err != nil {
		t.Fatal(err)
	}
	if err = e.Cancel(ctx, id); err != nil {
		t.Fatal(err)
	}
	if result, err := b.Execute(ctx, p.ID); err != nil || !result.IsError {
		t.Fatal("cancelled call executed", result, err)
	}
	if err = e.Run(ctx, id); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(w.Policy.Root, "note.txt"))
	if string(data) != "old" {
		t.Fatal("cancelled edit applied")
	}
}

func TestWorkspaceCannotContainDatabaseViaSymlinkAlias(t *testing.T) {
	if !platformFileCapability().Enabled {
		t.Skip(platformFileCapability().Reason)
	}
	ctx := context.Background()
	root := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	db, err := storage.OpenSQLite(ctx, filepath.Join(alias, "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = storage.Migrate(ctx, db.SQL); err != nil {
		t.Fatal(err)
	}
	s := NewStore(db.SQL)
	if err = s.CreateSession(ctx, "s"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.BindWorkspace(ctx, "s", WorkspacePolicy{Root: root, Mode: ModeDefault}); err == nil {
		t.Fatal("workspace exposes agent database")
	}
}
