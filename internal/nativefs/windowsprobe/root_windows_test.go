//go:build windows

package windowsprobe

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// Resolve the existing NTFS fixture through its handle: Go's temp pathname can
// contain an 8.3 alias. Do not conflate that spelling with the exact long name.
func localRootPath(t *testing.T) string {
	t.Helper()
	_, trusted := fixture(t)
	var buf [32768]uint16
	n, err := windows.GetFinalPathNameByHandle(trusted.h, &buf[0], uint32(len(buf)), 0)
	if err != nil || n == 0 || n >= uint32(len(buf)) {
		t.Fatalf("fixture long DOS path: length=%d err=%v", n, err)
	}
	path := windows.UTF16ToString(buf[:n])
	if !strings.HasPrefix(path, `\\?\`) {
		t.Fatalf("fixture did not resolve to extended DOS path: %q", path)
	}
	path = strings.TrimPrefix(path, `\\?\`)
	if len(path) < 4 || path[1:3] != `:\` {
		t.Fatalf("fixture must be local drive absolute path: %q", path)
	}
	if err := trusted.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func acquireLocalRoot(t *testing.T, path string) *Root {
	t.Helper()
	root, err := OpenLocalRoot(path)
	if err != nil || root == nil {
		t.Fatalf("OpenLocalRoot(%q): root=%v err=%v", path, root, err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	})
	return root
}

func rejectLocalRoot(t *testing.T, path string) {
	t.Helper()
	root, err := OpenLocalRoot(path)
	if root != nil {
		root.Close()
		t.Fatalf("unsafe acquisition returned root: %q err=%v", path, err)
	}
	if err == nil {
		t.Fatalf("unsafe acquisition accepted: %q", path)
	}
}

func TestNativeOpenLocalRootRead(t *testing.T) {
	path := localRootPath(t)
	if err := os.WriteFile(filepath.Join(path, "file"), []byte("inside"), 0600); err != nil {
		t.Fatal(err)
	}
	root := acquireLocalRoot(t, path)
	got, err := root.Read(context.Background(), []string{"file"})
	if err != nil || string(got) != "inside" {
		t.Fatalf("acquired-root read: %q %v", got, err)
	}
}

func TestNativeOpenLocalRootNamespaceNotToolPolicy(t *testing.T) {
	base := localRootPath(t)
	path := filepath.Join(base, ".foo", "data", "node_modules")
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "file"), []byte("inside"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".foo", "data", "node_modules"} {
		if err := os.Mkdir(filepath.Join(path, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	root := acquireLocalRoot(t, path)
	got, err := root.Read(context.Background(), []string{"file"})
	if err != nil || string(got) != "inside" {
		t.Fatalf("namespace acquisition: %q %v", got, err)
	}
	for _, name := range []string{".foo", "data", "node_modules"} {
		rejectDirectory(t, root, []string{name})
		if got, err := root.Read(context.Background(), []string{name, "file"}); err == nil || got != nil {
			t.Fatalf("tool relative policy changed: %q %v", got, err)
		}
	}
}

func TestNativeOpenLocalRootRejectsReparse(t *testing.T) {
	for _, kind := range []string{"symlink", "junction"} {
		t.Run(kind, func(t *testing.T) {
			base := localRootPath(t)
			target := localRootPath(t)
			if err := os.Mkdir(filepath.Join(target, "Child"), 0700); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(base, "Link")
			if kind == "symlink" {
				if err := os.Symlink(target, link); err != nil {
					t.Fatalf("required directory symlink fixture: %v", err)
				}
			} else {
				if out, err := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
					t.Fatalf("required junction fixture: %v %s", err, out)
				}
			}
			rejectLocalRoot(t, link)
			// Do not filepath.Clean away the hostile intermediate component.
			rejectLocalRoot(t, link+`\Child`)
		})
	}
}

func TestNativeOpenLocalRootRejectsPathAliases(t *testing.T) {
	base := localRootPath(t)
	exact := filepath.Join(base, "ExactCase")
	if err := os.Mkdir(exact, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "file"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"", "relative", base[:2] + "relative", `\relative`, `\\server\share`, `\\?\` + exact, `\\.\` + exact, base + `\exactcase`, exact + ":stream", exact + `\..`, base + `\.\ExactCase`, base + `\CON`, base + `\NUL`, base + `\missing`, base + `\file`} {
		rejectLocalRoot(t, path)
	}
}

func TestNativeOpenLocalRootIndependentOwnership(t *testing.T) {
	base := localRootPath(t)
	ancestor := filepath.Join(base, "Ancestor")
	path := filepath.Join(ancestor, "Root")
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "file"), []byte("inside"), 0600); err != nil {
		t.Fatal(err)
	}
	first := acquireLocalRoot(t, path)
	second := acquireLocalRoot(t, path)
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{ancestor, path} {
		if err := os.Rename(p, p+"Moved"); err == nil {
			t.Fatalf("live acquired root lost lineage guard: %s", p)
		}
	}
	if got, err := second.Read(context.Background(), []string{"file"}); err != nil || string(got) != "inside" {
		t.Fatalf("closing sibling invalidated root: %q %v", got, err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(ancestor, ancestor+"Moved"); err != nil {
		t.Fatalf("closed acquisition retained lineage: %v", err)
	}
}

func TestNativeOpenLocalRootSeparateNamespaceAndRelativeDepth(t *testing.T) {
	base := localRootPath(t)
	// Drive anchor is not a namespace component; account for the canonical temp
	// directory's own depth instead of assuming a runner-specific temp layout.
	baseDepth := len(strings.Split(base[3:], `\`))
	if baseDepth >= 32 {
		t.Fatalf("fixture leaves no namespace depth budget: %d", baseDepth)
	}
	names := make([]string, 32-baseDepth)
	for i := range names {
		names[i] = "n"
	}
	path := filepath.Join(append([]string{base}, names...)...)
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	parts := make([]string, 33)
	for i := range parts {
		parts[i] = "d"
	}
	if err := os.MkdirAll(filepath.Join(append([]string{path}, parts...)...), 0700); err != nil {
		t.Fatal(err)
	}
	rejectLocalRoot(t, path+`\d`)                    // namespace depth 33, object exists
	root := acquireLocalRoot(t, path)                // namespace depth exactly 32
	child := deriveDirectory(t, root, parts[:32]...) // independent tool budget 32
	rejectDirectory(t, root, parts)
	rejectDirectory(t, child, []string{"d"})
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(base, "n"), filepath.Join(base, "Moved")); err == nil {
		t.Fatal("deep child did not independently retain namespace lineage")
	}
	if err := child.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(base, "n"), filepath.Join(base, "Moved")); err != nil {
		t.Fatalf("deep lineage release: %v", err)
	}
}

func TestNativeOpenLocalRootDiskHandleRelease(t *testing.T) {
	base := localRootPath(t)
	path := filepath.Join(base, "Root")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "file"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	exercise := func() {
		root, err := OpenLocalRoot(path)
		if err != nil || root == nil {
			t.Fatalf("root acquisition: %v", err)
		}
		if err := root.Close(); err != nil {
			t.Fatal(err)
		}
		for _, suffix := range []string{`\missing`, `\file`, `\file\child`} {
			rejectLocalRoot(t, path+suffix)
		}
	}
	exercise()
	captureHandleInventory(t)
	before := captureHandleInventory(t)
	for i := 0; i < 64; i++ {
		exercise()
	}
	after := captureHandleInventory(t)
	logHandleDelta(t, before, after)
	if !sameDiskHandles(before, after) {
		t.Fatal("root acquisition changed exact disk handle set")
	}
	if err := os.Rename(path, filepath.Join(base, "Moved")); err != nil {
		t.Fatal(fmt.Errorf("root acquisition retained guard: %w", err))
	}
}

func TestNativeOpenLocalRootVolumeRoot(t *testing.T) {
	path := localRootPath(t)
	root := acquireLocalRoot(t, path[:3])
	s, err := inspect(root.h)
	if err != nil || s.Standard.Directory == 0 || root.depth != 0 || len(root.ancestors) != 0 {
		t.Fatalf("volume-root acquisition: directory=%d depth=%d ancestors=%d error=%v", s.Standard.Directory, root.depth, len(root.ancestors), err)
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := root.Read(context.Background(), []string{"anything"}); err == nil {
		t.Fatal("closed volume root retained authority")
	}
}
