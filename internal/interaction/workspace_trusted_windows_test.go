//go:build windows

package interaction

import (
	"agent/internal/nativefs/windowsprobe"
	"bytes"
	"context"
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func trustedWindowsFixture(t *testing.T) (WorkspaceBinding, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "work")
	if e := os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	p, e := windows.UTF16PtrFromString(root)
	if e != nil {
		t.Fatal(e)
	}
	h, e := windows.CreateFile(p, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if e != nil {
		t.Fatal(e)
	}
	var b [32768]uint16
	n, e := windows.GetFinalPathNameByHandle(h, &b[0], uint32(len(b)), 0)
	ce := windows.CloseHandle(h)
	if e != nil || ce != nil || n == 0 || n >= uint32(len(b)) {
		t.Fatalf("canonical fixture setup %v %v length%d", e, ce, n)
	}
	root = strings.TrimPrefix(windows.UTF16ToString(b[:n]), `\\?\`)
	if e = os.WriteFile(filepath.Join(root, "note.txt"), []byte("trusted workspace\n"), 0600); e != nil {
		t.Fatal(e)
	}
	// Existing untagged primitive is the independent fixture control. Failure here
	// is native setup failure, not missing adapter behavior or ordinary-user proof.
	r, e := windowsprobe.OpenLocalRoot(root)
	if e != nil {
		t.Fatalf("foundation fixture setup %v", e)
	}
	raw, e := r.Read(context.Background(), []string{"note.txt"})
	ce = r.Close()
	if e != nil || ce != nil || string(raw) != "trusted workspace\n" {
		t.Fatalf("foundation control %v %v", e, ce)
	}
	t.Log("TRUSTED_WINDOWS_FOUNDATION_CONTROL_OK")
	id, e := trustedWorkspaceIdentity(root)
	if e != nil {
		t.Fatalf("trusted identity contract: %v", e)
	}
	return WorkspaceBinding{RootIdentity: id, Policy: WorkspacePolicy{Root: root, TrustedWorkspace: true, trustedHostGranted: true}}, root
}
func trustedWindowsPositive(t *testing.T, w WorkspaceBinding) {
	t.Helper()
	b, e := readTrustedWorkspaceFile(context.Background(), w, "note.txt")
	if e != nil || string(b) != "trusted workspace\n" {
		t.Fatalf("trusted read positive %q %v", b, e)
	}
}
func TestTrustedWindowsReadOptIn(t *testing.T) {
	w, _ := trustedWindowsFixture(t)
	trustedWindowsPositive(t, w)
	w.Policy.TrustedWorkspace = false
	b, e := readTrustedWorkspaceFile(context.Background(), w, "note.txt")
	if !errors.Is(e, ErrCapabilityUnavailable) || b != nil {
		t.Fatalf("missing optin %q %v", b, e)
	}
}
func TestTrustedWindowsPaths(t *testing.T) {
	w, root := trustedWindowsFixture(t)
	trustedWindowsPositive(t, w)
	for _, name := range []string{"../note.txt", "C:/Windows/win.ini", "note.txt/../note.txt", "a\\b", "a\x00b", ".git/config", "data/x", "Data/x", "node_modules/x", "note.txt:ads", "note.txt.", "note.txt ", "CON", "NUL"} {
		b, e := readTrustedWorkspaceFile(context.Background(), w, name)
		if e == nil || b != nil {
			t.Fatalf("unsafe path accepted %q %v", name, e)
		}
	}
	if e := os.Mkdir(filepath.Join(root, "nested"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(root, "nested", "ok"), []byte("nested"), 0600); e != nil {
		t.Fatal(e)
	}
	b, e := readTrustedWorkspaceFile(context.Background(), w, "nested/ok")
	if e != nil || string(b) != "nested" {
		t.Fatalf("nested read %q %v", b, e)
	}
}
func TestTrustedWindowsReparseAndHardlinks(t *testing.T) {
	w, root := trustedWindowsFixture(t)
	trustedWindowsPositive(t, w)
	if e := os.Symlink(filepath.Join(root, "note.txt"), filepath.Join(root, "link")); e != nil {
		t.Fatalf("symlink setup (not feature refusal) %v", e)
	}
	if e := os.Symlink(root, filepath.Join(root, "dirlink")); e != nil {
		t.Fatal(e)
	}
	if e := os.Link(filepath.Join(root, "note.txt"), filepath.Join(root, "hard")); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"link", "dirlink/note.txt", "hard", "note.txt", "dirlink"} {
		b, e := readTrustedWorkspaceFile(context.Background(), w, name)
		if e == nil || b != nil {
			t.Fatalf("unsafe object accepted %s %v", name, e)
		}
	}
}
func TestTrustedWindowsBudgetAndCancellation(t *testing.T) {
	w, root := trustedWindowsFixture(t)
	trustedWindowsPositive(t, w)
	for _, n := range []int{0, MaxWorkspaceFileBytes, MaxWorkspaceFileBytes + 1} {
		raw := bytes.Repeat([]byte("x"), n)
		if e := os.WriteFile(filepath.Join(root, "bounded"), raw, 0600); e != nil {
			t.Fatal(e)
		}
		b, e := readTrustedWorkspaceFile(context.Background(), w, "bounded")
		if n > MaxWorkspaceFileBytes {
			if !errors.Is(e, ErrBudget) || b != nil {
				t.Fatalf("budget refusal %v", e)
			}
		} else if e != nil || !bytes.Equal(b, raw) {
			t.Fatalf("boundary %d %v", n, e)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b, e := readTrustedWorkspaceFile(ctx, w, "note.txt")
	if !errors.Is(e, context.Canceled) || b != nil {
		t.Fatalf("cancel lost %v", e)
	}
}
func TestTrustedWindowsKnownDatabase(t *testing.T) {
	w, root := trustedWindowsFixture(t)
	trustedWindowsPositive(t, w)
	db := filepath.Join(root, "active-store")
	if e := os.WriteFile(db, []byte("private test database"), 0600); e != nil {
		t.Fatal(e)
	}
	w.Policy.ProtectedPaths = []string{db, db + "-wal", db + "-shm", db + "-journal"}
	for _, name := range []string{"active-store", "active-store-wal", "active-store-shm", "active-store-journal"} {
		if e := os.WriteFile(filepath.Join(root, name), []byte("private fixture"), 0600); e != nil {
			t.Fatal(e)
		}
		b, e := readTrustedWorkspaceFile(context.Background(), w, name)
		if e == nil || b != nil {
			t.Fatalf("known storage read %s %v", name, e)
		}
	}
	trustedWindowsPositive(t, w)
}
func TestTrustedWindowsRootIdentity(t *testing.T) {
	w, root := trustedWindowsFixture(t)
	trustedWindowsPositive(t, w)
	bad := w
	bad.RootIdentity += "changed"
	if b, e := readTrustedWorkspaceFile(context.Background(), bad, "note.txt"); e == nil || b != nil {
		t.Fatal("tampered root identity accepted")
	}
	if e := os.Rename(root, root+"-old"); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(root, "note.txt"), []byte("replacement"), 0600); e != nil {
		t.Fatal(e)
	}
	if b, e := readTrustedWorkspaceFile(context.Background(), w, "note.txt"); e == nil || b != nil {
		t.Fatal("replacement root identity accepted")
	}
}
