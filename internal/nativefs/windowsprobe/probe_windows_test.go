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
	h, err := windows.CreateFile(p, windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
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
	// The open metadata handle is the barrier: conventional rename/delete must
	// fail while it is held. No sleeps or probabilistic racing are used.
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
	if err := os.Rename(nested, filepath.Join(dir, "moved")); err == nil {
		windows.CloseHandle(h)
		t.Fatal("renamed guarded intermediate directory")
	}
	if err := windows.CloseHandle(h); err != nil {
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
