//go:build darwin

package interaction

import (
	"bytes"
	"context"
	"errors"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"testing"
)

func trustedDarwinFixture(t *testing.T) (WorkspaceBinding, string) {
	t.Helper()
	root := t.TempDir()
	var fs unix.Statfs_t
	if e := unix.Statfs(root, &fs); e != nil {
		t.Fatal(e)
	}
	if unix.ByteSliceToString(fs.Fstypename[:]) != "apfs" {
		t.Fatal("trusted workspace qualification requires actual APFS")
	}
	if e := os.WriteFile(filepath.Join(root, "note.txt"), []byte("trusted workspace\n"), 0600); e != nil {
		t.Fatal(e)
	}
	control, e := os.ReadFile(filepath.Join(root, "note.txt"))
	if e != nil || string(control) != "trusted workspace\n" {
		t.Fatalf("trusted APFS fixture control %q %v", control, e)
	}
	t.Log("TRUSTED_DARWIN_FIXTURE_READY APFS regular bytes=18")
	id, e := trustedWorkspaceIdentity(root)
	if errors.Is(e, errTrustedDarwinUnimplemented) {
		t.Fatalf("TRUSTED_DARWIN_EXPECTED_RED root identity explicitly unimplemented: %v", e)
	}
	if e != nil {
		t.Fatal(e)
	}
	// Internal primitive fixture only; real host/session grants are tested by API integration.
	return WorkspaceBinding{RootIdentity: id, Policy: WorkspacePolicy{Root: root, TrustedWorkspace: true, trustedHostGranted: true}}, root
}
func trustedDarwinPositive(t *testing.T, w WorkspaceBinding) {
	t.Helper()
	b, e := readTrustedWorkspaceFile(context.Background(), w, "note.txt")
	if e != nil || string(b) != "trusted workspace\n" {
		t.Fatalf("trusted Darwin read positive: %q %v", b, e)
	}
}
func TestTrustedDarwinWorkspaceRead(t *testing.T) {
	w, _ := trustedDarwinFixture(t)
	trustedDarwinPositive(t, w)
	if trustedWorkspacePlatformCapability().Reason == "" {
		t.Fatal("trusted adapter capability must explain its readiness/scope")
	}
	noHost := w
	noHost.Policy.trustedHostGranted = false
	if b, e := readTrustedWorkspaceFile(context.Background(), noHost, "note.txt"); !errors.Is(e, ErrCapabilityUnavailable) || b != nil {
		t.Fatal("missing host grant accepted", e)
	}
	w.Policy.TrustedWorkspace = false
	b, e := readTrustedWorkspaceFile(context.Background(), w, "note.txt")
	if !errors.Is(e, ErrCapabilityUnavailable) || b != nil {
		t.Fatalf("missing opt-in %q %v", b, e)
	}
}
func TestTrustedDarwinWorkspacePaths(t *testing.T) {
	w, root := trustedDarwinFixture(t)
	trustedDarwinPositive(t, w)
	for _, name := range []string{"../note.txt", "/etc/passwd", "note.txt/../note.txt", "a\\b", "a\x00b", ".git/config", "data/x", "Data/x", "node_modules/x"} {
		b, e := readTrustedWorkspaceFile(context.Background(), w, name)
		if e == nil || b != nil {
			t.Fatalf("path accepted %q %v", name, e)
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
		t.Fatal(string(b), e)
	}
	protected := w
	protected.Policy.ProtectedPaths = []string{filepath.Join(root, "note.txt")}
	b, e = readTrustedWorkspaceFile(context.Background(), protected, "note.txt")
	if e == nil || b != nil {
		t.Fatal("explicit known database object not excluded")
	}
}
func TestTrustedDarwinWorkspaceUnsafeFiles(t *testing.T) {
	w, root := trustedDarwinFixture(t)
	trustedDarwinPositive(t, w)
	if e := os.Symlink("note.txt", filepath.Join(root, "link")); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(root, filepath.Join(root, "dirlink")); e != nil {
		t.Fatal(e)
	}
	if e := unix.Mkfifo(filepath.Join(root, "pipe"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Link(filepath.Join(root, "note.txt"), filepath.Join(root, "hard")); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"link", "dirlink/note.txt", "pipe", "hard", "note.txt"} {
		b, e := readTrustedWorkspaceFile(context.Background(), w, name)
		if e == nil || b != nil {
			t.Fatalf("unsafe accepted %s %v", name, e)
		}
	}
}
func TestTrustedDarwinWorkspaceBounds(t *testing.T) {
	w, root := trustedDarwinFixture(t)
	trustedDarwinPositive(t, w)
	for _, n := range []int{0, MaxWorkspaceFileBytes, MaxWorkspaceFileBytes + 1} {
		data := bytes.Repeat([]byte("x"), n)
		if e := os.WriteFile(filepath.Join(root, "bounded"), data, 0600); e != nil {
			t.Fatal(e)
		}
		b, e := readTrustedWorkspaceFile(context.Background(), w, "bounded")
		if n > MaxWorkspaceFileBytes {
			if e == nil || b != nil {
				t.Fatal("oversized accepted")
			}
		} else if e != nil || !bytes.Equal(b, data) {
			t.Fatalf("boundary %d %v", n, e)
		}
	}
}
func TestTrustedDarwinWorkspaceIdentityAndCancel(t *testing.T) {
	w, _ := trustedDarwinFixture(t)
	trustedDarwinPositive(t, w)
	bad := w
	bad.RootIdentity += "changed"
	b, e := readTrustedWorkspaceFile(context.Background(), bad, "note.txt")
	if e == nil || b != nil {
		t.Fatal("root mismatch accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b, e = readTrustedWorkspaceFile(ctx, w, "note.txt")
	if !errors.Is(e, context.Canceled) || b != nil {
		t.Fatal("cancellation lost", e)
	}
}
func TestTrustedDarwinWorkspaceCloseErrors(t *testing.T) {
	w, _ := trustedDarwinFixture(t)
	trustedDarwinPositive(t, w)
	fault := errors.New("reported close failure")
	calls := 0
	b, e := readTrustedDarwinWithClose(context.Background(), w, "note.txt", func(f *os.File) error {
		calls++
		native := f.Close()
		if native != nil {
			t.Error(native)
		}
		return errors.Join(native, fault)
	})
	if !errors.Is(e, fault) || b != nil || calls != 2 {
		t.Fatalf("cleanup data=%q err=%v calls=%d", b, e, calls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls = 0
	b, e = readTrustedDarwinWithClose(ctx, w, "note.txt", func(f *os.File) error {
		calls++
		e := f.Close()
		cancel()
		return e
	})
	if !errors.Is(e, context.Canceled) || b != nil || calls != 2 {
		t.Fatalf("post-read cancellation leaked data=%q err=%v calls=%d", b, e, calls)
	}
}
