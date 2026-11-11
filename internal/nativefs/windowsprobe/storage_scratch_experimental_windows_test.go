//go:build windows && nativeprobe_sqlite_identity

package windowsprobe

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func scratchExpectedCleanup(err, expected error) bool {
	if err == nil || err == expected {
		return true
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, part := range joined.Unwrap() {
			if !scratchExpectedCleanup(part, expected) {
				return false
			}
		}
		return true
	}
	return false
}
func scratchArrangeStoreCleanup(t *testing.T, s *scratchStore, expected error) {
	t.Helper()
	if s == nil {
		return
	}
	t.Cleanup(func() {
		if e := s.Close(); !scratchExpectedCleanup(e, expected) {
			t.Errorf("store cleanup: %v", e)
		}
	})
}
func scratchArrangeFileCleanup(t *testing.T, f *scratchFile, expected error) {
	t.Helper()
	if f == nil {
		return
	}
	// Handle-only file Close. Never cleanup by a returned/generated pathname.
	t.Cleanup(func() {
		if e := f.Close(); !scratchExpectedCleanup(e, expected) {
			t.Errorf("file cleanup: %v", e)
		}
	})
}
func scratchContractStore(t *testing.T, root *Root, hooks scratchHooks) *scratchStore {
	t.Helper()
	s, e := newScratchStore(root, scratchLimits{Files: 4, FileBytes: 16 << 20, TotalBytes: 32 << 20, IOBytes: 1 << 20}, hooks)
	scratchArrangeStoreCleanup(t, s, nil)
	if e != nil || s == nil {
		t.Fatalf("owned scratch adapter not implemented/qualified: %v", e)
	}
	return s
}
func scratchContractFile(t *testing.T, s *scratchStore) *scratchFile {
	t.Helper()
	f, e := s.Create()
	scratchArrangeFileCleanup(t, f, nil)
	if e != nil || f == nil {
		t.Fatalf("scratch create: %v", e)
	}
	return f
}
func TestNativeSQLiteTempScratchOffsetIO(t *testing.T) {
	dir, r := scratchFixture(t)
	baseline := captureHandleInventory(t)
	s := scratchContractStore(t, r, scratchHooks{})
	f := scratchContractFile(t, s)
	id := scratchID(t, f.h)
	if id.Volume != scratchID(t, r.h).Volume {
		t.Fatal("different volume")
	}
	for _, w := range []struct {
		off int64
		b   []byte
	}{{0, []byte("abcd")}, {8, []byte("WXYZ")}, {1, []byte("!")}} {
		if n, e := f.WriteAt(w.b, w.off); e != nil || n != len(w.b) {
			t.Fatalf("write %d %v", n, e)
		}
	}
	if e := f.Sync(); e != nil {
		t.Fatal(e)
	}
	b := bytes.Repeat([]byte{0xee}, 16)
	n, e := f.ReadAt(b, 0)
	if n != 12 || !errors.Is(e, io.EOF) || !bytes.Equal(b, []byte{'a', '!', 'c', 'd', 0, 0, 0, 0, 'W', 'X', 'Y', 'Z', 0, 0, 0, 0}) {
		t.Fatalf("offset/EOF %d %v %x", n, e, b)
	}
	if size, e := f.Size(); e != nil || size != 12 {
		t.Fatalf("size %d %v", size, e)
	}
	if e := f.Truncate(2); e != nil {
		t.Fatal(e)
	}
	if e := f.Truncate(6); e != nil {
		t.Fatal(e)
	}
	b = bytes.Repeat([]byte{0xee}, 8)
	if n, e := f.ReadAt(b, 0); n != 6 || !errors.Is(e, io.EOF) || !bytes.Equal(b, []byte{'a', '!', 0, 0, 0, 0, 0, 0}) {
		t.Fatalf("truncate/EOF %d %v %x", n, e, b)
	}
	b = bytes.Repeat([]byte{0xee}, 4)
	if n, e := f.ReadAt(b, 99); n != 0 || !errors.Is(e, io.EOF) || !bytes.Equal(b, make([]byte, 4)) {
		t.Fatalf("past EOF %d %v %x", n, e, b)
	}
	if scratchID(t, f.h) != id {
		t.Fatal("file identity changed")
	}
	if e := f.Close(); e != nil {
		t.Fatal(e)
	}
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	if entries, e := os.ReadDir(dir); e != nil || len(entries) != 0 {
		t.Fatalf("scratch remains %v %v", entries, e)
	}
	if after := captureHandleInventory(t); !sameDiskHandles(baseline, after) {
		t.Fatalf("disk leak %v -> %v", baseline.files, after.files)
	}
}
func TestNativeSQLiteTempScratchOwnership(t *testing.T) {
	dir, r := scratchFixture(t)
	childPath := filepath.Join(dir, "child")
	if e := os.Mkdir(childPath, 0700); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := os.Remove(childPath); e != nil {
			t.Error(e)
		}
	})
	child, e := r.OpenDirectory([]string{"child"})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := child.Close(); e != nil {
			t.Error(e)
		}
	})
	// Inventory positive control: a known real disk handle must be observed.
	before := captureHandleInventory(t)
	var duplicate windows.Handle
	if e := windows.DuplicateHandle(windows.CurrentProcess(), r.h, windows.CurrentProcess(), &duplicate, 0, false, windows.DUPLICATE_SAME_ACCESS); e != nil {
		t.Fatal(e)
	}
	defer scratchClose(t, &duplicate)
	live := captureHandleInventory(t)
	if before.files[duplicate] || !live.files[duplicate] {
		scratchClose(t, &duplicate)
		t.Fatal("PSS missed known disk duplicate")
	}
	scratchClose(t, &duplicate)
	if after := captureHandleInventory(t); !sameDiskHandles(before, after) {
		t.Fatal("PSS positive control did not restore")
	}
	s := scratchContractStore(t, child, scratchHooks{})
	if len(child.ancestors) == 0 {
		t.Fatal("fixture lacks ancestor guard")
	}
	if s.root == nil || s.root == child || s.root.h == child.h || scratchID(t, s.root.h) != scratchID(t, child.h) || len(s.root.ancestors) != len(child.ancestors) {
		t.Fatal("store did not independently own complete root lineage")
	}
	ancestorIDs := make([]idInfo, len(child.ancestors))
	for i, h := range child.ancestors {
		ancestorIDs[i] = scratchID(t, h)
		if s.root.ancestors[i] == h || scratchID(t, s.root.ancestors[i]) != ancestorIDs[i] {
			t.Fatalf("ancestor %d not independently duplicated", i)
		}
	}

	if e := child.Close(); e != nil {
		t.Fatal(e)
	}
	if e := r.Close(); e != nil {
		t.Fatal(e)
	}
	for i, h := range s.root.ancestors {
		if scratchID(t, h) != ancestorIDs[i] {
			t.Fatalf("owned ancestor %d not alive after source closes", i)
		}
	}
	f := scratchContractFile(t, s)
	if n, e := f.WriteAt([]byte("owned after root close"), 0); e != nil || n != 22 {
		t.Fatalf("independent parent %d %v", n, e)
	}
	if e := s.Close(); !errors.Is(e, errScratchBusy) {
		t.Fatalf("store closed live file: %v", e)
	}
	rejected, e := s.Create()
	scratchArrangeFileCleanup(t, rejected, nil)
	if rejected != nil || !errors.Is(e, errScratchClosed) {
		t.Fatalf("closing store admitted create: %v", e)
	}
	if e := f.Close(); e != nil {
		t.Fatal(e)
	}
	// Allocate a new disk handle after closing old file; repeated close must not touch it.
	_, other := scratchFixture(t)
	held := scratchID(t, other.h)
	if e := f.Close(); e != nil {
		t.Fatal(e)
	}
	if scratchID(t, other.h) != held {
		t.Fatal("repeat close affected unrelated handle")
	}
	if _, e := f.Size(); !errors.Is(e, errScratchClosed) {
		t.Fatalf("closed size %v", e)
	}
	if _, e := f.ReadAt(make([]byte, 1), 0); !errors.Is(e, errScratchClosed) {
		t.Fatalf("closed read %v", e)
	}
	if _, e := f.WriteAt([]byte{1}, 0); !errors.Is(e, errScratchClosed) {
		t.Fatalf("closed write %v", e)
	}
	if e := f.Sync(); !errors.Is(e, errScratchClosed) {
		t.Fatalf("closed sync %v", e)
	}
	if e := f.Truncate(0); !errors.Is(e, errScratchClosed) {
		t.Fatalf("closed truncate %v", e)
	}
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
}
func TestNativeSQLiteTempScratchFailures(t *testing.T) {
	t.Run("preflight", func(t *testing.T) {
		_, r := scratchFixture(t)
		s := scratchContractStore(t, r, scratchHooks{})
		f := scratchContractFile(t, s)
		if _, e := f.WriteAt([]byte("safe"), 0); e != nil {
			t.Fatal(e)
		}
		id := scratchID(t, f.h)
		for _, off := range []int64{-1, 16 << 20, math.MaxInt64} {
			if n, e := f.WriteAt([]byte{1, 2}, off); n != 0 || !errors.Is(e, errScratchLimit) {
				t.Fatalf("preflight %d %v", n, e)
			}
		}
		if n, e := f.WriteAt(make([]byte, (1<<20)+1), 0); n != 0 || !errors.Is(e, errScratchLimit) {
			t.Fatalf("IO limit %d %v", n, e)
		}
		if e := f.Truncate((16 << 20) + 1); !errors.Is(e, errScratchLimit) {
			t.Fatalf("truncate limit %v", e)
		}
		b := make([]byte, 4)
		if n, e := f.ReadAt(b, 0); e != nil || n != 4 || string(b) != "safe" {
			t.Fatalf("preflight mutated %d %v %q", n, e, b)
		}
		if scratchID(t, f.h) != id {
			t.Fatal("preflight identity changed")
		}
		for i := 0; i < 3; i++ {
			scratchContractFile(t, s)
		}
		fRejected, e := s.Create()
		scratchArrangeFileCleanup(t, fRejected, nil)
		if fRejected != nil || !errors.Is(e, errScratchLimit) {
			t.Fatalf("file capacity %v", e)
		}
	})
	t.Run("aggregate_capacity", func(t *testing.T) {
		_, r := scratchFixture(t)
		s := scratchContractStore(t, r, scratchHooks{})
		a := scratchContractFile(t, s)
		b := scratchContractFile(t, s)
		c := scratchContractFile(t, s)
		if e := a.Truncate(16 << 20); e != nil {
			t.Fatal(e)
		}
		if e := b.Truncate(16 << 20); e != nil {
			t.Fatal(e)
		}
		if e := c.Truncate(1); !errors.Is(e, errScratchLimit) {
			t.Fatalf("aggregate limit %v", e)
		}
		if size, e := c.Size(); e != nil || size != 0 {
			t.Fatalf("rejected grow changed size %d %v", size, e)
		}
		if e := a.Truncate(0); e != nil {
			t.Fatal(e)
		}
		if e := c.Truncate(1); e != nil {
			t.Fatalf("quota not released after shrink: %v", e)
		}
	})
	t.Run("flush_and_truncate_errors", func(t *testing.T) {
		for _, op := range []string{"sync", "truncate"} {
			t.Run(op, func(t *testing.T) {
				_, r := scratchFixture(t)
				injected := errors.New("injected " + op)
				hooks := scratchHooks{}
				if op == "sync" {
					hooks.sync = func(windows.Handle) error { return injected }
				} else {
					hooks.truncate = func(windows.Handle, int64) error { return injected }
				}
				s := scratchContractStore(t, r, hooks)
				f := scratchContractFile(t, s)
				var e error
				if op == "sync" {
					e = f.Sync()
				} else {
					e = f.Truncate(3)
				}
				if !errors.Is(e, injected) {
					t.Fatalf("lost %s error: %v", op, e)
				}
				if op == "truncate" && !errors.Is(e, errScratchUncertain) {
					t.Fatalf("truncate failure not uncertain: %v", e)
				}
			})
		}
	})
	t.Run("uncertain_close_never_retried", func(t *testing.T) {
		_, r := scratchFixture(t)
		injected := errors.New("injected close outcome")
		calls := 0
		before := captureHandleInventory(t)
		s, e := newScratchStore(r, scratchLimits{Files: 4, FileBytes: 16 << 20, TotalBytes: 32 << 20, IOBytes: 1 << 20}, scratchHooks{close: func(h windows.Handle) error {
			calls++
			return errors.Join(windows.CloseHandle(h), injected)
		}})
		scratchArrangeStoreCleanup(t, s, injected)
		if e != nil || s == nil {
			t.Fatalf("owned scratch adapter not implemented/qualified: %v", e)
		}
		f, e := s.Create()
		scratchArrangeFileCleanup(t, f, injected)
		if e != nil || f == nil {
			t.Fatalf("scratch create: %v", e)
		}
		if e := f.Close(); !errors.Is(e, injected) {
			t.Fatalf("close error lost: %v", e)
		}
		if f.h != 0 {
			t.Fatal("uncertain close retained numeric ownership")
		}
		_, other := scratchFixture(t)
		id := scratchID(t, other.h)
		if e := f.Close(); !errors.Is(e, injected) || calls != 1 {
			t.Fatalf("uncertain close retried/forgotten: %v calls=%d", e, calls)
		}
		if scratchID(t, other.h) != id {
			t.Fatal("repeat close affected replacement handle")
		}
		if e := other.Close(); e != nil {
			t.Fatal(e)
		}
		if e := s.Close(); !errors.Is(e, injected) {
			t.Fatalf("store lost terminal close error: %v", e)
		}
		if after := captureHandleInventory(t); !sameDiskHandles(before, after) {
			t.Fatalf("fault fixture leaked disk handles %v -> %v", before.files, after.files)
		}
	})

	t.Run("partial_write", func(t *testing.T) {
		_, r := scratchFixture(t)
		calls := 0
		injected := errors.New("injected after real partial write")
		s := scratchContractStore(t, r, scratchHooks{write: func(h windows.Handle, b []byte) (uint32, error) {
			calls++
			var n uint32
			e := windows.WriteFile(h, b[:1], &n, nil)
			return n, errors.Join(e, injected)
		}})
		f := scratchContractFile(t, s)
		if n, e := f.WriteAt([]byte("abc"), 0); n != 1 || !errors.Is(e, errScratchUncertain) || !errors.Is(e, injected) {
			t.Fatalf("partial write %d %v", n, e)
		}
		if _, e := f.WriteAt([]byte("retry"), 0); !errors.Is(e, errScratchUncertain) || calls != 1 {
			t.Fatalf("poison retried write %v calls=%d", e, calls)
		}
		if _, e := f.ReadAt(make([]byte, 1), 0); !errors.Is(e, errScratchUncertain) {
			t.Fatalf("poison read %v", e)
		}
	})
	t.Run("post_create_refusal", func(t *testing.T) {
		dir, r := scratchFixture(t)
		injected := errors.New("post-create reject")
		s := scratchContractStore(t, r, scratchHooks{afterCreate: func(windows.Handle) error { return injected }})
		before := captureHandleInventory(t)
		fRejected, e := s.Create()
		scratchArrangeFileCleanup(t, fRejected, nil)
		if fRejected != nil || !errors.Is(e, injected) {
			t.Fatalf("postcreate %v", e)
		}
		if after := captureHandleInventory(t); !sameDiskHandles(before, after) {
			t.Fatal("postcreate leaked disk handle")
		}
		if entries, e := os.ReadDir(dir); e != nil || len(entries) != 0 {
			t.Fatalf("postcreate left file %v %v", entries, e)
		}
	})
}
func TestNativeSQLiteTempScratchParentMutation(t *testing.T) {
	base, r := scratchFixture(t)
	outside, _ := scratchFixture(t)
	sentinel := filepath.Join(outside, "sentinel")
	if e := os.WriteFile(sentinel, []byte("unchanged-sentinel"), 0600); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		scratchSentinel(t, sentinel)
		if e := os.Remove(sentinel); e != nil {
			t.Error(e)
		}
	})
	victim := filepath.Join(base, "victim")
	if e := os.Mkdir(victim, 0700); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := os.Remove(victim); e != nil {
			t.Error(e)
		}
	})
	child, e := r.OpenDirectory([]string{"victim"})
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if e := child.Close(); e != nil {
			t.Error(e)
		}
	}()
	s := scratchContractStore(t, child, scratchHooks{})
	f := scratchContractFile(t, s)
	if e := f.Close(); e != nil {
		t.Fatal(e)
	}
	attacker, e := reparseMutationOpen(r.h, windows.FILE_WRITE_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE)
	if e != nil {
		t.Fatal(e)
	}
	defer scratchClose(t, &attacker)
	payload := reparseMutationBuffer(t, outside)
	var n uint32
	if e := windows.DeviceIoControl(attacker, windows.FSCTL_SET_REPARSE_POINT, &payload[0], uint32(len(payload)), nil, 0, &n, nil); e != nil {
		t.Fatal(e)
	}
	defer func() {
		var b [8]byte
		binary.LittleEndian.PutUint32(b[:], windows.IO_REPARSE_TAG_MOUNT_POINT)
		if e := windows.DeviceIoControl(attacker, windows.FSCTL_DELETE_REPARSE_POINT, &b[0], 8, nil, 0, &n, nil); e != nil {
			t.Error(e)
		}
	}()
	a, e := os.Stat(filepath.Join(victim, "sentinel"))
	if e != nil {
		t.Fatal(e)
	}
	b, e := os.Stat(sentinel)
	if e != nil || !os.SameFile(a, b) {
		t.Fatal("junction control not established")
	}
	fRejected, e := s.Create()
	scratchArrangeFileCleanup(t, fRejected, nil)
	if fRejected != nil || !errors.Is(e, errUnsafe) {
		t.Fatalf("mutated parent accepted or unknown refusal: %v", e)
	}
	entries, e := os.ReadDir(outside)
	if e != nil || len(entries) != 1 || entries[0].Name() != "sentinel" {
		t.Fatalf("outside changed %v %v", entries, e)
	}
	scratchSentinel(t, sentinel)
}
