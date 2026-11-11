//go:build darwin

package darwinprobe

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"golang.org/x/sys/unix"
)

func rootFixture(t *testing.T) (string, *os.File) {
	t.Helper()
	p := t.TempDir()
	f, e := os.Open(p)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { f.Close() })
	var fs unix.Statfs_t
	if e = unix.Fstatfs(int(f.Fd()), &fs); e != nil {
		t.Fatal(e)
	}
	if unix.ByteSliceToString(fs.Fstypename[:]) != "apfs" {
		t.Fatal("native qualification requires actual APFS, not a skip")
	}
	return p, f
}
func TestOwnedDuplicationAndClose(t *testing.T) {
	_, f := rootFixture(t)
	d, e := ObserveDirectory(f, nil)
	if e != nil {
		t.Fatal(e)
	}
	fd := d.fds[0]
	if e = f.Close(); e != nil {
		t.Fatal(e)
	}
	var st unix.Stat_t
	if e = unix.Fstat(fd, &st); e != nil {
		t.Fatalf("caller close invalidated owned handle: %v", e)
	}
	if e = d.Close(); e != nil {
		t.Fatal(e)
	}
	if e = unix.Fstat(fd, &st); !errors.Is(e, unix.EBADF) {
		t.Fatalf("owned fd survived: %v", e)
	}
	if e = d.Close(); !errors.Is(e, os.ErrClosed) {
		t.Fatalf("repeat close: %v", e)
	}
	if _, e = d.ObserveRegular("x", 1); !errors.Is(e, os.ErrClosed) {
		t.Fatalf("closed observation: %v", e)
	}
}
func TestCloseReportsDescriptorError(t *testing.T) {
	_, f := rootFixture(t)
	d, e := ObserveDirectory(f, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = unix.Close(d.fds[0]); e != nil {
		t.Fatal(e)
	}
	if e = d.Close(); !errors.Is(e, unix.EBADF) {
		t.Fatalf("lost close error: %v", e)
	}
}
func TestPinnedRename(t *testing.T) {
	root, f := rootFixture(t)
	if e := os.Mkdir(filepath.Join(root, "parent"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(root, "parent", "leaf"), []byte("old"), 0600); e != nil {
		t.Fatal(e)
	}
	d, e := ObserveDirectory(f, []string{"parent"})
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	before, e := d.ObserveRegular("leaf", 3)
	if e != nil {
		t.Fatal(e)
	}
	moved := root + "-moved"
	if e = os.Rename(root, moved); e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(moved)
	if e = os.Rename(filepath.Join(moved, "parent"), filepath.Join(moved, "renamed")); e != nil {
		t.Fatal(e)
	}
	if e = os.MkdirAll(filepath.Join(root, "parent"), 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(root, "parent", "leaf"), []byte("replacement"), 0600); e != nil {
		t.Fatal(e)
	}
	after, e := d.ObserveRegular("leaf", 3)
	if e != nil {
		t.Fatal(e)
	}
	if before != after {
		t.Fatalf("followed renamed pathname: before=%+v after=%+v", before, after)
	}
}
func TestLeafRefusalsAndNoFIFOReader(t *testing.T) {
	p, f := rootFixture(t)
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	must(os.WriteFile(filepath.Join(p, "regular"), []byte("abc"), 0600))
	must(os.WriteFile(filepath.Join(p, "linked"), []byte("a"), 0600))
	must(os.Link(filepath.Join(p, "linked"), filepath.Join(p, "link2")))
	must(os.Symlink("regular", filepath.Join(p, "symlink")))
	must(os.Mkdir(filepath.Join(p, "directory"), 0700))
	must(unix.Mkfifo(filepath.Join(p, "fifo"), 0600))
	d, e := ObserveDirectory(f, nil)
	must(e)
	defer d.Close()
	obs, e := d.ObserveRegular("regular", 3)
	must(e)
	if obs.Size != 3 {
		t.Fatal(obs)
	}
	for _, name := range []string{"linked", "link2", "symlink", "directory", "fifo", "missing", "../regular", "a/b", ""} {
		if _, e = d.ObserveRegular(name, 3); e == nil {
			t.Errorf("accepted %q", name)
		}
	}
	if _, e = d.ObserveRegular("regular", 2); e == nil {
		t.Fatal("accepted oversized")
	}
	if _, e = d.ObserveRegular("regular", -1); e == nil {
		t.Fatal("accepted negative limit")
	}
	fd, e := unix.Open(filepath.Join(p, "fifo"), unix.O_WRONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if fd >= 0 {
		unix.Close(fd)
	}
	if !errors.Is(e, unix.ENXIO) {
		t.Fatalf("FIFO acquired a reader: %v", e)
	}
}
func TestDirectoryRefusalsAndCleanup(t *testing.T) {
	p, f := rootFixture(t)
	if e := os.Mkdir(filepath.Join(p, "dir"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink("dir", filepath.Join(p, "sym")); e != nil {
		t.Fatal(e)
	}
	before := openDescriptorSnapshot(t)
	for i := 0; i < 100; i++ {
		for _, parts := range [][]string{{"sym"}, {"dir", "missing"}, {".."}, {"a/b"}, make([]string, MaxDepth+1)} {
			if d, e := ObserveDirectory(f, parts); e == nil {
				d.Close()
				t.Fatalf("accepted %q", parts)
			}
		}
	}
	after := openDescriptorSnapshot(t)
	if !slices.Equal(before, after) {
		t.Fatalf("descriptor set changed: %v -> %v", before, after)
	}
	if _, e := ObserveDirectory(nil, nil); e == nil {
		t.Fatal("accepted nil root")
	}
	file, e := os.CreateTemp(p, "not-dir")
	if e != nil {
		t.Fatal(e)
	}
	defer file.Close()
	if d, e := ObserveDirectory(file, nil); e == nil {
		d.Close()
		t.Fatal("accepted regular root")
	}
	closed, e := os.Open(p)
	if e != nil {
		t.Fatal(e)
	}
	closed.Close()
	if _, e := ObserveDirectory(closed, nil); e == nil {
		t.Fatal("accepted closed root")
	}
}

func TestBoundedLineageClosesAllDescriptors(t *testing.T) {
	p, f := rootFixture(t)
	parts := make([]string, MaxDepth)
	for i := range parts {
		parts[i] = "d"
		p = filepath.Join(p, "d")
		if e := os.Mkdir(p, 0700); e != nil {
			t.Fatal(e)
		}
	}
	d, e := ObserveDirectory(f, parts)
	if e != nil {
		t.Fatal(e)
	}
	if len(d.fds) != MaxDepth+1 {
		t.Fatalf("lineage size %d", len(d.fds))
	}
	fds := append([]int(nil), d.fds...)
	for _, fd := range fds {
		flags, e := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
		if e != nil || flags&unix.FD_CLOEXEC == 0 {
			t.Fatalf("descriptor %d not close-on-exec: %v", fd, e)
		}
	}
	// A single broken descriptor must not prevent cleanup of the rest.
	if e = unix.Close(fds[len(fds)/2]); e != nil {
		t.Fatal(e)
	}
	if e = d.Close(); !errors.Is(e, unix.EBADF) {
		t.Fatalf("close error: %v", e)
	}
	for _, fd := range fds {
		var st unix.Stat_t
		if e = unix.Fstat(fd, &st); !errors.Is(e, unix.EBADF) {
			t.Fatalf("descriptor %d survived: %v", fd, e)
		}
	}
}
func TestRejectNonAPFSRoot(t *testing.T) {
	f, e := os.Open("/dev")
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	var fs unix.Statfs_t
	if e = unix.Fstatfs(int(f.Fd()), &fs); e != nil {
		t.Fatal(e)
	}
	if unix.ByteSliceToString(fs.Fstypename[:]) == "apfs" {
		t.Fatal("expected /dev to provide actual non-APFS fixture")
	}
	if d, e := ObserveDirectory(f, nil); e == nil {
		d.Close()
		t.Fatal("accepted non-APFS root")
	}
}

func TestZeroValueObservationReturnsClosed(t *testing.T) {
	var d DirectoryObservation
	if _, err := d.ObserveRegular("leaf", 1); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("zero-value observation: %v", err)
	}
}

func TestZeroValueClose(t *testing.T) {
	var d DirectoryObservation
	if err := d.Close(); err != nil {
		t.Fatalf("first zero-value close: %v", err)
	}
	if err := d.Close(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("repeat zero-value close: %v", err)
	}
	if _, err := d.ObserveRegular("leaf", 1); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed zero-value observation: %v", err)
	}
}

// TestDescriptorSnapshotTracksOpenClose guards the leak-check fixture itself.
func TestDescriptorSnapshotTracksOpenClose(t *testing.T) {
	before := openDescriptorSnapshot(t)
	f, err := os.Open("/dev/null")
	if err != nil {
		t.Fatal(err)
	}
	fd := int(f.Fd())
	during := openDescriptorSnapshot(t)
	if !slices.Contains(during, fd) || len(during) != len(before)+1 {
		f.Close()
		t.Fatalf("snapshot did not detect opened fd %d: %v -> %v", fd, before, during)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	after := openDescriptorSnapshot(t)
	if !slices.Equal(before, after) {
		t.Fatalf("snapshot did not return to baseline: %v -> %v", before, after)
	}
}

// openDescriptorSnapshot reads names only: Go 1.26 ReadDir may fstatat entries
// with unknown type, which can fail with EBADF on Darwin synthetic /dev/fd entries.
// F_GETFD distinguishes live slots without opening their targets. The enumerator
// itself is excluded consistently. Unexpected enumeration/descriptor errors fail
// the test; no error is treated as an empty successful snapshot.
func openDescriptorSnapshot(t *testing.T) []int {
	t.Helper()
	dir, err := os.Open("/dev/fd")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := dir.Close(); err != nil {
			t.Errorf("close descriptor enumerator: %v", err)
		}
	}()
	own := int(dir.Fd())
	names, err := dir.Readdirnames(-1)
	if err != nil {
		t.Fatal(err)
	}
	result := make([]int, 0, len(names))
	seen := make(map[int]bool, len(names))
	for _, name := range names {
		fd, err := strconv.Atoi(name)
		if err != nil || fd < 0 {
			t.Fatalf("unexpected /dev/fd entry %q: %v", name, err)
		}
		if fd == own {
			continue
		}
		if seen[fd] {
			t.Fatalf("duplicate /dev/fd entry %q", name)
		}
		seen[fd] = true
		_, err = unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
		if errors.Is(err, unix.EBADF) {
			continue
		}
		if err != nil {
			t.Fatalf("inspect fd %d: %v", fd, err)
		}
		result = append(result, fd)
	}
	slices.Sort(result)
	return result
}
