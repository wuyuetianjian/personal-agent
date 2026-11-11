//go:build windows

package windowsprobe

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func fixture(t *testing.T) (string, *Root) {
	t.Helper()
	dir := t.TempDir()
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(p, windows.FILE_READ_ATTRIBUTES|windows.FILE_LIST_DIRECTORY|windows.SYNCHRONIZE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewRoot(h)
	windows.CloseHandle(h)
	if err != nil {
		t.Fatalf("required local NTFS fixture: %v", err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	})
	return dir, r
}

func TestNativeABI(t *testing.T) {
	if unsafe.Sizeof(idInfo{}) != 24 || unsafe.Sizeof(basicInfo{}) != 40 || unsafe.Sizeof(standardInfo{}) != 24 || unsafe.Sizeof(tagInfo{}) != 8 {
		t.Fatal("Windows metadata ABI mismatch")
	}
	if unsafe.Sizeof(uintptr(0)) == 8 && (unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{}) != 48 || unsafe.Sizeof(windows.IO_STATUS_BLOCK{}) != 16) {
		t.Fatal("native ABI mismatch")
	}
}
func TestNativeBoundedReadAndOwnership(t *testing.T) {
	dir, r := fixture(t)
	for _, n := range []int{0, Limit, Limit + 1} {
		name := "file"
		content := bytes.Repeat([]byte("x"), n)
		if err := os.WriteFile(filepath.Join(dir, name), content, 0600); err != nil {
			t.Fatal(err)
		}
		got, err := r.Read(context.Background(), []string{name})
		if n > Limit {
			if !errors.Is(err, ErrTooLarge) || got != nil {
				t.Fatalf("oversize: %d %v", len(got), err)
			}
		} else if err != nil || !bytes.Equal(got, content) {
			t.Fatalf("size %d: %v", n, err)
		}
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Read(context.Background(), []string{"file"}); err == nil {
		t.Fatal("read closed root")
	}
}
func TestNativeExactNamesAndHardlinks(t *testing.T) {
	dir, r := fixture(t)
	if err := os.Mkdir(filepath.Join(dir, "Sub"), 0700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "Sub", "LongFilename.txt")
	if err := os.WriteFile(p, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := r.Read(context.Background(), []string{"Sub", "LongFilename.txt"}); err != nil || string(got) != "original" {
		t.Fatalf("%q %v", got, err)
	}
	for _, parts := range [][]string{{"sub", "LongFilename.txt"}, {"Sub", "longfilename.txt"}, {"Sub", "LONGFI~1.TXT"}, {"Sub", "LongFilename.txt:stream"}} {
		if _, err := r.Read(context.Background(), parts); err == nil {
			t.Fatalf("alias accepted: %q", parts)
		}
	}
	if err := os.Link(p, filepath.Join(dir, "alias")); err != nil {
		t.Fatal(err)
	}
	for _, parts := range [][]string{{"alias"}, {"Sub", "LongFilename.txt"}} {
		if got, err := r.Read(context.Background(), parts); err == nil || got != nil {
			t.Fatalf("returned known hardlink: %q %v", got, err)
		}
	}

}
func TestNativeWriterAndCancelled(t *testing.T) {
	dir, r := fixture(t)
	p := filepath.Join(dir, "file")
	if err := os.WriteFile(p, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	w, err := os.OpenFile(p, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Read(context.Background(), []string{"file"}); err == nil {
		t.Error("accepted active writer")
	}
	w.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Read(ctx, []string{"file"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}
func TestNativeSymlink(t *testing.T) {
	dir, r := fixture(t)
	if err := os.WriteFile(filepath.Join(dir, "target"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	// Fixture inability is a failure, never a skipped/passing qualification.
	if err := os.Symlink(filepath.Join(dir, "target"), filepath.Join(dir, "link")); err != nil {
		t.Fatalf("required symlink fixture unavailable: %v", err)
	}
	if _, err := r.Read(context.Background(), []string{"link"}); err == nil {
		t.Fatal("followed symlink")
	}
}

func TestNativePinnedIdentityAndConventionalReplacement(t *testing.T) {
	dir, r := fixture(t)
	p := filepath.Join(dir, "file")
	if err := os.WriteFile(p, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	h, err := openRelative(r.h, "file", false)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	before, err := inspect(h)
	if err != nil {
		t.Fatal(err)
	}
	ioGuard, _, err := acquireIOGuard(r.h, "file", before)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(ioGuard)
	// Only the qualified I/O guard participates in share accounting. Metadata
	// alone was demonstrated insufficient by the first native CI run.
	if err := os.Rename(p, filepath.Join(dir, "moved")); err == nil {
		t.Fatal("renamed guarded object")
	}
	if err := os.Remove(p); err == nil {
		t.Fatal("deleted guarded object")
	}
	data, err := openRelative(r.h, "file", true)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(data)
	after, err := inspect(data)
	if err != nil {
		t.Fatal(err)
	}
	if before.ID != after.ID {
		t.Fatal("reopen changed pinned identity")
	}
	var b [8]byte
	var n uint32
	if err := windows.ReadFile(data, b[:], &n, nil); err != nil || string(b[:n]) != "original" {
		t.Fatalf("read pinned: %q %v", b[:n], err)
	}
}
func TestNativeErrorsAndHandleRelease(t *testing.T) {
	dir, r := fixture(t)
	p := filepath.Join(dir, "file")
	if err := os.WriteFile(p, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 64; i++ {
		if _, err := r.Read(context.Background(), []string{"missing"}); err == nil {
			t.Fatal("missing accepted")
		}
		if _, err := r.Read(context.Background(), []string{"file", "child"}); err == nil {
			t.Fatal("file parent accepted")
		}
		if _, err := r.Read(context.Background(), []string{"file"}); err != nil {
			t.Fatal(err)
		}
	}
	// A leaked metadata/data guard would prevent the following write/delete.
	if err := os.WriteFile(p, []byte("changed"), 0600); err != nil {
		t.Fatalf("retained leaf handle: %v", err)
	}
	if err := os.Remove(p); err != nil {
		t.Fatalf("retained delete guard: %v", err)
	}
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if bad, err := NewRoot(windows.Handle(f.Fd())); err == nil {
		bad.Close()
		t.Error("file root accepted")
	}
	f.Close()
	if _, err := NewRoot(windows.InvalidHandle); err == nil {
		t.Fatal("invalid root accepted")
	}
}

func TestNativeJunction(t *testing.T) {
	dir, r := fixture(t)
	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(target, "file"), []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	junction := filepath.Join(dir, "junction")
	if out, err := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", junction, target).CombinedOutput(); err != nil {
		t.Fatalf("required junction fixture unavailable: %v: %s", err, out)
	}
	if got, err := r.Read(context.Background(), []string{"junction", "file"}); err == nil || got != nil {
		t.Fatalf("followed junction: %q %v", got, err)
	}
}

func TestNativeNestedGuardAndHandleCount(t *testing.T) {
	dir, r := fixture(t)
	nested := filepath.Join(dir, "nested")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "file"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	h, err := openRelative(r.h, "nested", false)
	if err != nil {
		t.Fatal(err)
	}
	meta, err := inspect(h)
	if err != nil {
		windows.CloseHandle(h)
		t.Fatal(err)
	}
	ioGuard, _, err := acquireIOGuard(r.h, "nested", meta)
	if err != nil {
		windows.CloseHandle(h)
		t.Fatal(err)
	}
	if err := os.Rename(nested, filepath.Join(dir, "moved")); err == nil {
		windows.CloseHandle(ioGuard)
		windows.CloseHandle(h)
		t.Fatal("renamed guarded intermediate directory")
	}
	if err := windows.CloseHandle(h); err != nil {
		t.Fatal(err)
	}
	if err := windows.CloseHandle(ioGuard); err != nil {
		t.Fatal(err)
	}
	countProc := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetProcessHandleCount")
	if err := countProc.Find(); err != nil {
		t.Fatal(err)
	}
	count := func() uint32 {
		var n uint32
		ok, _, err := countProc.Call(uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&n)))
		if ok == 0 {
			t.Fatalf("GetProcessHandleCount: %v", err)
		}
		return n
	}
	// Warm lazy APIs and both success/error paths before exact count comparison.
	exercise := func() {
		if _, err := r.Read(context.Background(), []string{"nested", "file"}); err != nil {
			t.Fatal(err)
		}
		if _, err := r.Read(context.Background(), []string{"nested", "missing"}); err == nil {
			t.Fatal("accepted missing child")
		}
		if _, err := r.Read(context.Background(), []string{"nested", "file", "child"}); err == nil {
			t.Fatal("accepted file parent")
		}
		copy, err := NewRoot(r.h)
		if err != nil {
			t.Fatal(err)
		}
		if err := copy.Close(); err != nil {
			t.Fatal(err)
		}
	}
	exercise()
	count()
	before := count()
	for i := 0; i < 64; i++ {
		exercise()
	}
	if after := count(); after != before {
		t.Fatalf("process handles: before=%d after=%d", before, after)
	}
	if err := os.Rename(nested, filepath.Join(dir, "moved")); err != nil {
		t.Fatalf("retained intermediate handle: %v", err)
	}
}

func TestNativeMetadataSwapRejectedBeforeIO(t *testing.T) {
	for _, directory := range []bool{false, true} {
		t.Run(map[bool]string{false: "leaf", true: "directory"}[directory], func(t *testing.T) {
			dir, r := fixture(t)
			p := filepath.Join(dir, "object")
			if directory {
				if err := os.Mkdir(p, 0700); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.WriteFile(p, []byte("original"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			metadata, err := openRelative(r.h, "object", false)
			if err != nil {
				t.Fatal(err)
			}
			defer windows.CloseHandle(metadata)
			before, err := inspect(metadata)
			if err != nil {
				t.Fatal(err)
			}
			// Explicit barrier reproduces the observed metadata-only vulnerability.
			if err := os.Rename(p, filepath.Join(dir, "moved")); err != nil {
				t.Fatalf("metadata-only rename fixture: %v", err)
			}
			if directory {
				if err := os.Mkdir(p, 0700); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.WriteFile(p, []byte("replacement"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			guard, _, err := acquireIOGuard(r.h, "object", before)
			if err == nil {
				windows.CloseHandle(guard)
				t.Fatal("qualified a replacement object")
			}
			if guard != 0 {
				t.Fatal("returned a usable handle for replacement")
			}
		})
	}
}

func TestNativeMetadataOnlyRootRejected(t *testing.T) {
	dir := t.TempDir()
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(p, windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	if r, err := NewRoot(h); err == nil {
		r.Close()
		t.Fatal("metadata-only root qualified")
	}
}

func TestNativeDeleteAccessRootRejected(t *testing.T) {
	dir := t.TempDir()
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(p, windows.FILE_LIST_DIRECTORY|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE|windows.DELETE, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	if r, err := NewRoot(h); err == nil {
		r.Close()
		t.Fatal("root with DELETE access qualified")
	}
}

// Runtime assertion deliberately keeps the RED revision cross-compilable.
type directoryOpener interface {
	OpenDirectory([]string) (*Root, error)
}

func directoryAPI(t *testing.T, r *Root) directoryOpener {
	t.Helper()
	opener, ok := any(r).(directoryOpener)
	if !ok {
		t.Fatal("Root.OpenDirectory([]string) (*Root, error) is not implemented")
	}
	return opener
}

func deriveDirectory(t *testing.T, r *Root, parts ...string) *Root {
	t.Helper()
	child, err := directoryAPI(t, r).OpenDirectory(parts)
	if err != nil || child == nil {
		t.Fatalf("derive %q: root=%v err=%v", parts, child, err)
	}
	t.Cleanup(func() {
		if err := child.Close(); err != nil {
			t.Error(err)
		}
	})
	return child
}

func rejectDirectory(t *testing.T, r *Root, parts []string) {
	t.Helper()
	child, err := directoryAPI(t, r).OpenDirectory(parts)
	if child != nil {
		child.Close()
		t.Fatalf("rejected derivation returned usable root: %q, %v", parts, err)
	}
	if err == nil {
		t.Fatalf("accepted unsafe directory: %q", parts)
	}
}

func TestNativeOpenDirectoryScope(t *testing.T) {
	dir, r := fixture(t)
	directoryAPI(t, r)
	if err := os.Mkdir(filepath.Join(dir, "Child"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Child", "file"), []byte("inside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sibling"), []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	child := deriveDirectory(t, r, "Child")
	if got, err := child.Read(context.Background(), []string{"file"}); err != nil || string(got) != "inside" {
		t.Fatalf("child read: %q %v", got, err)
	}
	for _, parts := range [][]string{{"sibling"}, {"..", "sibling"}, {filepath.Join(dir, "sibling")}, {"Child", "file"}} {
		if got, err := child.Read(context.Background(), parts); err == nil || got != nil {
			t.Fatalf("escaped child scope: %q %q %v", parts, got, err)
		}
	}
	if got, err := r.Read(context.Background(), []string{"sibling"}); err != nil || string(got) != "outside" {
		t.Fatalf("parent scope changed: %q %v", got, err)
	}
}

func TestNativeOpenDirectoryIndependentLineage(t *testing.T) {
	dir, r := fixture(t)
	directoryAPI(t, r)
	ancestor := filepath.Join(dir, "Ancestor")
	childPath := filepath.Join(ancestor, "Child")
	if err := os.MkdirAll(childPath, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(childPath, "file"), []byte("inside"), 0600); err != nil {
		t.Fatal(err)
	}
	parent := deriveDirectory(t, r, "Ancestor")
	child := deriveDirectory(t, parent, "Child")
	if err := parent.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{ancestor, childPath} {
		if err := os.Rename(p, p+"Moved"); err == nil {
			t.Fatalf("live descendant lost ancestor rename guard: %s", p)
		}
	}
	if got, err := child.Read(context.Background(), []string{"file"}); err != nil || string(got) != "inside" {
		t.Fatalf("parent closure invalidated child: %q %v", got, err)
	}
	if err := child.Close(); err != nil {
		t.Fatal(err)
	}
	if err := child.Close(); err != nil {
		t.Fatal(err)
	}
	if got, err := child.Read(context.Background(), []string{"file"}); err == nil || got != nil {
		t.Fatalf("closed child read: %q %v", got, err)
	}
	rejectDirectory(t, child, []string{"missing"})
	rejectDirectory(t, parent, []string{"Child"})
	if err := os.Rename(ancestor, ancestor+"Moved"); err != nil {
		t.Fatalf("closed lineage leaked rename guard: %v", err)
	}
}

func TestNativeOpenDirectorySiblingOwnership(t *testing.T) {
	dir, r := fixture(t)
	directoryAPI(t, r)
	p := filepath.Join(dir, "Child")
	if err := os.Mkdir(p, 0700); err != nil {
		t.Fatal(err)
	}
	first := deriveDirectory(t, r, "Child")
	second := deriveDirectory(t, r, "Child")
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(p, p+"Moved"); err == nil {
		t.Fatal("closing one derivation released another's guard")
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(p, p+"Moved"); err != nil {
		t.Fatalf("closed siblings retain guard: %v", err)
	}
	// Closing children must not close the original root.
	third := deriveDirectory(t, r, "ChildMoved")
	if err := third.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestNativeOpenDirectoryRejectsPaths(t *testing.T) {
	dir, r := fixture(t)
	directoryAPI(t, r)
	if err := os.Mkdir(filepath.Join(dir, "Child"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "file"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, parts := range [][]string{nil, {}, {""}, {"."}, {".."}, {"Child", ".."}, {"child"}, {"Child:stream"}, {"Child."}, {"Child "}, {"Child/file"}, {`Child\file`}, {`C:\Child`}, {`\\server\share`}, {"CON"}, {"data"}, {"node_modules"}, {"missing"}, {"file"}, {"file", "Child"}} {
		rejectDirectory(t, r, parts)
	}
	parts := make([]string, 33)
	for i := range parts {
		parts[i] = "d"
	}
	rejectDirectory(t, r, parts)
	// The exact supported boundary must work, not simply reject all long paths.
	parts = parts[:32]
	if err := os.MkdirAll(filepath.Join(append([]string{dir}, parts...)...), 0700); err != nil {
		t.Fatal(err)
	}
	child := deriveDirectory(t, r, parts...)
	if err := child.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestNativeOpenDirectoryRejectsReparse(t *testing.T) {
	dir, r := fixture(t)
	directoryAPI(t, r)
	outside := t.TempDir()
	if err := os.Mkdir(filepath.Join(outside, "Nested"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "Link")); err != nil {
		t.Fatalf("required directory symlink fixture unavailable: %v", err)
	}
	if out, err := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", filepath.Join(dir, "Junction"), outside).CombinedOutput(); err != nil {
		t.Fatalf("required junction fixture unavailable: %v: %s", err, out)
	}
	for _, parts := range [][]string{{"Link"}, {"Link", "Nested"}, {"Junction"}, {"Junction", "Nested"}} {
		rejectDirectory(t, r, parts)
	}
}

func TestNativeOpenDirectoryHandleRelease(t *testing.T) {
	dir, r := fixture(t)
	directoryAPI(t, r)
	if err := os.MkdirAll(filepath.Join(dir, "Outer", "Inner"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Outer", "file"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	countProc := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetProcessHandleCount")
	if err := countProc.Find(); err != nil {
		t.Fatal(err)
	}
	count := func() uint32 {
		var n uint32
		ok, _, err := countProc.Call(uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&n)))
		if ok == 0 {
			t.Fatalf("GetProcessHandleCount: %v", err)
		}
		return n
	}
	exercise := func() {
		// Do not use deriveDirectory: its cleanup closures would retain 64 roots.
		child, err := directoryAPI(t, r).OpenDirectory([]string{"Outer", "Inner"})
		if err != nil || child == nil {
			t.Fatalf("derive: %v", err)
		}
		if err := child.Close(); err != nil {
			t.Fatal(err)
		}
		rejectDirectory(t, r, []string{"Outer", "missing"})
		rejectDirectory(t, r, []string{"Outer", "file"})
		rejectDirectory(t, r, []string{"Outer", "file", "missing"})
		rejectDirectory(t, r, []string{"Outer", "inner"})
	}
	exercise()
	count()
	before := count()
	for i := 0; i < 64; i++ {
		exercise()
	}
	if after := count(); after != before {
		t.Fatalf("derivation leaked process handles: before=%d after=%d", before, after)
	}
	if err := os.Rename(filepath.Join(dir, "Outer"), filepath.Join(dir, "Moved")); err != nil {
		t.Fatalf("derivation retained ancestor guard: %v", err)
	}
}

func TestNativeOpenDirectoryCumulativeDepth(t *testing.T) {
	dir, r := fixture(t)
	directoryAPI(t, r)
	parts := make([]string, 33)
	for i := range parts {
		parts[i] = "d"
	}
	if err := os.MkdirAll(filepath.Join(append([]string{dir}, parts...)...), 0700); err != nil {
		t.Fatal(err)
	}
	// A 16 + 16 derivation reaches the same limit as one 32-component call.
	middle := deriveDirectory(t, r, parts[:16]...)
	last := deriveDirectory(t, middle, parts[16:32]...)
	rejectDirectory(t, last, []string{"d"})
	rejectDirectory(t, middle, parts[16:33])
	if err := last.Close(); err != nil {
		t.Fatal(err)
	}
	if err := middle.Close(); err != nil {
		t.Fatal(err)
	}
	// Failed over-depth derivations must not retain any extra ancestors.
	if err := os.Rename(filepath.Join(dir, "d"), filepath.Join(dir, "Moved")); err != nil {
		t.Fatalf("depth failure retained guard: %v", err)
	}
}
