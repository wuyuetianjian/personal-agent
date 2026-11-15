//go:build !windows

package interaction

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTrustedWorkspaceReservedSidecarParentAlias(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "storage")
	alias := filepath.Join(root, "alias")
	if e := os.Mkdir(dir, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(dir, alias); e != nil {
		t.Fatal(e)
	}
	original, e := os.Stat(dir)
	if e != nil {
		t.Fatal(e)
	}
	linked, e := os.Stat(alias)
	if e != nil {
		t.Fatal(e)
	}
	if !os.SameFile(original, linked) {
		t.Fatal("parent alias qualification failed")
	}
	w := WorkspaceBinding{Policy: WorkspacePolicy{ProtectedPaths: []string{filepath.Join(dir, "agent.db-shm")}}}
	if e = trustedWorkspacePathProtected(w, filepath.Join(alias, "AGENT.DB-SHM"), nil); e == nil {
		t.Fatal("absent sidecar through same parent accepted")
	}
	if e = trustedWorkspacePathProtected(w, filepath.Join(alias, "readme.txt"), nil); e != nil {
		t.Fatal("normal neighbor refused", e)
	}
}
