package interaction

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestTrustedWorkspacePaths(t *testing.T) {
	for _, name := range []string{"Data/a", "NODE_MODULES/a", "a:b", "dir/a.", "dir/a ", "dir/ a", "../a", ".git/config", "a\\b"} {
		if safeTrustedWorkspacePath(name) == nil {
			t.Errorf("accepted %q", name)
		}
	}
	if e := safeTrustedWorkspacePath("src/readme.txt"); e != nil {
		t.Fatal(e)
	}
}
func TestTrustedWorkspaceKnownObjectProtection(t *testing.T) {
	root := t.TempDir()
	db := filepath.Join(root, "state.db")
	if e := os.WriteFile(db, []byte("private"), 0600); e != nil {
		t.Fatal(e)
	}
	info, e := os.Stat(db)
	if e != nil {
		t.Fatal(e)
	}
	w := WorkspaceBinding{Policy: WorkspacePolicy{ProtectedPaths: []string{db, db + "-wal"}}}
	if trustedWorkspacePathProtected(w, filepath.Join(root, "STATE.DB"), nil) == nil {
		t.Fatal("case alias accepted")
	}
	alias := filepath.Join(root, "alias")
	if e = os.Link(db, alias); e != nil {
		t.Fatal(e)
	}
	aliasInfo, e := os.Stat(alias)
	if e != nil {
		t.Fatal(e)
	}
	if !os.SameFile(info, aliasInfo) {
		t.Fatal("hardlink fixture identity mismatch")
	}
	if trustedWorkspacePathProtected(w, alias, aliasInfo) == nil {
		t.Fatal("same object accepted")
	}
	if trustedWorkspacePathProtected(w, db+"-wal", nil) == nil {
		t.Fatal("missing reserved sidecar accepted")
	}
	neighbor := filepath.Join(root, "readme.txt")
	if e := os.WriteFile(neighbor, []byte("public"), 0600); e != nil {
		t.Fatal(e)
	}
	ni, e := os.Stat(neighbor)
	if e != nil {
		t.Fatal(e)
	}
	if e = trustedWorkspacePathProtected(w, neighbor, ni); e != nil {
		t.Fatal(e)
	}
}
func TestTrustedWorkspaceOptInPolicy(t *testing.T) {
	var old WorkspacePolicy
	if e := json.Unmarshal([]byte(`{"Root":"old","Mode":"default"}`), &old); e != nil {
		t.Fatal(e)
	}
	if old.TrustedWorkspace {
		t.Fatal("old policy upgraded")
	}
	if workspaceNativeSupport(old) != currentNativeSupport() {
		t.Fatal("default changed")
	}
	s := workspaceNativeSupport(WorkspacePolicy{TrustedWorkspace: true})
	if s.Replace.Enabled || s.Undo.Enabled || s.Exec.Enabled || s.MCPStdio.Enabled || nativeFilesCapability(s).Enabled {
		t.Fatal("trusted read escalated to bundle/write/process")
	}
}

func TestTrustedWorkspacePreparationProtectsDatabase(t *testing.T) {
	if !platformFileCapability().Enabled && !TrustedWorkspaceCapability().Enabled {
		t.Skip("no workspace adapter on this target")
	}
	s, ctx := setup(t)
	p := WorkspacePolicy{Root: t.TempDir(), Mode: ModeDefault, TrustedWorkspace: !platformFileCapability().Enabled, ProtectedPaths: []string{"untrusted-input"}}
	if p.TrustedWorkspace {
		s = NewStoreWithOptions(s.db, StoreOptions{TrustedWorkspace: true, TrustedWorkspaceRoot: p.Root})
	}
	w, e := s.PrepareWorkspace(ctx, "s", p)
	if e != nil {
		t.Fatal(e)
	}
	var seq int
	var name, db string
	if e = s.db.QueryRowContext(ctx, "PRAGMA database_list").Scan(&seq, &name, &db); e != nil {
		t.Fatal(e)
	}
	db, e = filepath.EvalSymlinks(db)
	if e != nil {
		t.Fatal(e)
	}
	if len(w.Policy.ProtectedPaths) != 4 {
		t.Fatalf("protected paths: %v", w.Policy.ProtectedPaths)
	}
	for i, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		if w.Policy.ProtectedPaths[i] != db+suffix {
			t.Fatal(w.Policy.ProtectedPaths)
		}
	}
	p.Root = filepath.Dir(db)
	if _, e = s.PrepareWorkspace(ctx, "s", p); e == nil {
		t.Fatal("agent DB inside root accepted")
	}
}

func TestTrustedWorkspaceHostGrantNeverPersists(t *testing.T) {
	s, ctx := setup(t)
	root, canonicalErr := filepath.EvalSymlinks(t.TempDir())
	if canonicalErr != nil {
		t.Fatal(canonicalErr)
	}
	on := NewStoreWithOptions(s.db, StoreOptions{TrustedWorkspace: true, TrustedWorkspaceRoot: root})
	p := WorkspacePolicy{Root: root, Mode: ModeDefault, TrustedWorkspace: true}
	on.grantTrustedWorkspace(&p)
	if !p.trustedHostGranted {
		t.Fatal("explicit host/root not granted")
	}
	raw, e := json.Marshal(p)
	if e != nil {
		t.Fatal(e)
	}
	var decoded WorkspacePolicy
	if e = json.Unmarshal(raw, &decoded); e != nil {
		t.Fatal(e)
	}
	if decoded.trustedHostGranted {
		t.Fatal("runtime authority serialized")
	}
	if _, e = s.db.ExecContext(ctx, "INSERT INTO interaction_workspaces(session_id,scope_id,root_identity,policy) VALUES(?,?,?,?)", "s", "scope", "identity", string(raw)); e != nil {
		t.Fatal(e)
	}
	if _, e = s.db.ExecContext(ctx, "INSERT INTO interaction_api_sessions(session_id,request_id,binding,mode) VALUES(?,?,?,?)", "s", "host-restart", "{}", "default"); e != nil {
		t.Fatal(e)
	}
	for _, store := range []*Store{NewStore(s.db), NewStoreWithOptions(s.db, StoreOptions{TrustedWorkspace: true, TrustedWorkspaceRoot: t.TempDir()})} {
		w, e := store.Workspace(ctx, "s")
		if e != nil {
			t.Fatal("history blocked", e)
		}
		if w.Policy.trustedHostGranted || workspaceNativeSupport(w.Policy).Read.Enabled {
			t.Fatal("host-off/wrong-root acquired authority")
		}
		if _, e = workspaceIdentityForPolicy(w.Policy); e == nil {
			t.Fatal("identity performed without host grant")
		}
		if _, e = store.PrepareWorkspace(ctx, "s", p); e == nil {
			t.Fatal("caller copied runtime grant")
		}
		tc, e := NewContextAssembler(store).Build(ctx, "s", TokenBudget{Total: 20000, Output: 1000})
		if e != nil {
			t.Fatal(e)
		}
		if len(tc.Tools) != 0 {
			t.Fatal("host-off context exposed tools")
		}
		tx, e := s.db.BeginTx(ctx, nil)
		if e != nil {
			t.Fatal(e)
		}
		_, _, ke := knowledgeAuthority(ctx, tx, "s", store)
		_, _, se := skillRunWorkspace(ctx, tx, "s", store)
		if e = tx.Rollback(); e != nil {
			t.Fatal(e)
		}
		if !errors.Is(ke, ErrCapabilityUnavailable) || !errors.Is(se, ErrCapabilityUnavailable) {
			t.Fatalf("authority bypass knowledge=%v skill=%v", ke, se)
		}

	}
}
