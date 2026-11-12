//go:build darwin && nativeprobe_darwin_ownedfd

package darwinprobe

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func ownedFDCheck(t *testing.T, e error) {
	t.Helper()
	if e != nil {
		t.Fatal(e)
	}
}
func ownedFDFixture(t *testing.T) (*os.File, string, []byte) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "trusted-regular")
	data := bytes.Repeat([]byte("trusted-owned-object\n"), 4096)
	ownedFDCheck(t, os.WriteFile(path, data, 0600))
	f, e := os.Open(path)
	ownedFDCheck(t, e)
	t.Cleanup(func() {
		if e := f.Close(); e != nil && !errors.Is(e, os.ErrClosed) {
			t.Error(e)
		}
	})
	var st unix.Stat_t
	var fs unix.Statfs_t
	ownedFDCheck(t, unix.Fstat(int(f.Fd()), &st))
	ownedFDCheck(t, unix.Fstatfs(int(f.Fd()), &fs))
	flags, e := unix.FcntlInt(f.Fd(), unix.F_GETFL, 0)
	ownedFDCheck(t, e)
	if unix.ByteSliceToString(fs.Fstypename[:]) != "apfs" || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 || flags != unix.O_RDONLY {
		t.Fatal("fixture requires actual APFS readonly regular single-link FD")
	}
	control := make([]byte, len(data))
	n, e := unix.Pread(int(f.Fd()), control, 0)
	ownedFDCheck(t, e)
	if n != len(data) || !bytes.Equal(data, control) {
		t.Fatal("independent trusted fixture pread control")
	}
	t.Logf("OWNED_FD_TRUSTED_FIXTURE APFS readonly regular links=1 bytes=%d", len(data))
	return f, path, data
}
func ownedFDPositive(t *testing.T, f *os.File, data []byte) {
	t.Helper()
	b, e := ReadTrustedRegular(context.Background(), f, int64(len(data)))
	if errors.Is(e, errOwnedFDUnimplemented) {
		t.Fatalf("OWNED_FD_EXPECTED_RED trusted APFS readonly regular FD refused: %v", e)
	}
	ownedFDCheck(t, e)
	if !bytes.Equal(b, data) {
		t.Fatal("owned FD positive content mismatch")
	}
}
func ownedFDRefused(t *testing.T, b []byte, e error) {
	t.Helper()
	if e == nil || b != nil {
		t.Fatalf("expected error and nil bytes: len=%d error=%v", len(b), e)
	}
}
func TestOwnedFDReadPreservesOffset(t *testing.T) {
	f, _, data := ownedFDFixture(t)
	_, e := f.Seek(7, io.SeekStart)
	ownedFDCheck(t, e)
	ownedFDPositive(t, f, data)
	offset, e := f.Seek(0, io.SeekCurrent)
	ownedFDCheck(t, e)
	if offset != 7 {
		t.Fatalf("caller offset=%d", offset)
	}
	ops := ownedFDSystemOps()
	seen := false
	ops.afterDup = func(fd int) error {
		flags, e := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
		if e != nil {
			return e
		}
		if flags&unix.FD_CLOEXEC == 0 {
			return errors.New("owned FD lacks CLOEXEC")
		}
		seen = true
		return nil
	}
	b, e := readTrustedRegularWithOps(context.Background(), f, int64(len(data)), ops)
	ownedFDCheck(t, e)
	if !seen || !bytes.Equal(b, data) {
		t.Fatal("owned descriptor/control mismatch")
	}
	offset, e = f.Seek(0, io.SeekCurrent)
	ownedFDCheck(t, e)
	if offset != 7 {
		t.Fatal("pread changed source cursor")
	}
}
func TestOwnedFDCallerCloseAndPathReplacement(t *testing.T) {
	f, path, data := ownedFDFixture(t)
	ownedFDPositive(t, f, data)
	ops := ownedFDSystemOps()
	var owned int = -1
	ops.afterDup = func(fd int) error {
		owned = fd
		return errors.Join(f.Close(), os.Rename(path, path+"-original"), unix.Mkfifo(path, 0600))
	}
	b, e := readTrustedRegularWithOps(context.Background(), f, int64(len(data)), ops)
	ownedFDCheck(t, e)
	if !bytes.Equal(b, data) {
		t.Fatal("read replacement rather than already owned object")
	}
	var st unix.Stat_t
	ownedFDCheck(t, unix.Lstat(path, &st))
	if st.Mode&unix.S_IFMT != unix.S_IFIFO {
		t.Fatal("replacement fixture not FIFO")
	}
	if e = unix.Fstat(owned, &st); !errors.Is(e, unix.EBADF) {
		t.Fatalf("owned duplicate not closed: %v", e)
	}
}
func TestOwnedFDAdmissionRefusals(t *testing.T) {
	f, path, data := ownedFDFixture(t)
	ownedFDPositive(t, f, data)
	b, e := ReadTrustedRegular(context.Background(), nil, 1)
	ownedFDRefused(t, b, e)
	// Access-mode bits alone are insufficient: additional status flags may
	// describe a non-readable descriptor. Qualify the deliberately strict subset.
	raw, e := f.SyscallConn()
	ownedFDCheck(t, e)
	var flagErr error
	ownedFDCheck(t, raw.Control(func(fd uintptr) { _, flagErr = unix.FcntlInt(fd, unix.F_SETFL, unix.O_NONBLOCK) }))
	ownedFDCheck(t, flagErr)
	var observed int
	ownedFDCheck(t, raw.Control(func(fd uintptr) { observed, flagErr = unix.FcntlInt(fd, unix.F_GETFL, 0) }))
	ownedFDCheck(t, flagErr)
	if observed&unix.O_ACCMODE != unix.O_RDONLY || observed&unix.O_NONBLOCK == 0 {
		t.Fatalf("extra-status readonly control flags=%#x", observed)
	}
	b, e = ReadTrustedRegular(context.Background(), f, int64(len(data)))
	ownedFDRefused(t, b, e)
	ownedFDCheck(t, raw.Control(func(fd uintptr) { _, flagErr = unix.FcntlInt(fd, unix.F_SETFL, 0) }))
	ownedFDCheck(t, flagErr)
	for _, flag := range []int{os.O_RDWR, os.O_WRONLY} {
		other, e := os.OpenFile(path, flag, 0)
		ownedFDCheck(t, e)
		b, e = ReadTrustedRegular(context.Background(), other, int64(len(data)))
		ce := other.Close()
		ownedFDRefused(t, b, e)
		ownedFDCheck(t, ce)
	}
	dir, e := os.Open(filepath.Dir(path))
	ownedFDCheck(t, e)
	b, e = ReadTrustedRegular(context.Background(), dir, int64(len(data)))
	ce := dir.Close()
	ownedFDRefused(t, b, e)
	ownedFDCheck(t, ce)
	r, w, e := os.Pipe()
	ownedFDCheck(t, e)
	b, e = ReadTrustedRegular(context.Background(), r, 1)
	ce = errors.Join(r.Close(), w.Close())
	ownedFDRefused(t, b, e)
	ownedFDCheck(t, ce)
	fd, e := unix.Open(path, unix.O_EVTONLY|unix.O_CLOEXEC, 0)
	ownedFDCheck(t, e)
	event := os.NewFile(uintptr(fd), "trusted-event-only-fixture")
	if event == nil {
		t.Fatal("event FD wrapper")
	}
	b, e = ReadTrustedRegular(context.Background(), event, int64(len(data)))
	ce = event.Close()
	ownedFDRefused(t, b, e)
	ownedFDCheck(t, ce)
	ownedFDCheck(t, os.Link(path, path+"-alias"))
	b, e = ReadTrustedRegular(context.Background(), f, int64(len(data)))
	ownedFDRefused(t, b, e)
	ownedFDCheck(t, os.Remove(path+"-alias"))
	ownedFDCheck(t, f.Close())
	b, e = ReadTrustedRegular(context.Background(), f, int64(len(data)))
	ownedFDRefused(t, b, e)
}
func TestOwnedFDBoundsAndObservedMutation(t *testing.T) {
	f, path, data := ownedFDFixture(t)
	ownedFDPositive(t, f, data)
	for _, limit := range []int64{-1, 1<<20 + 1, int64(len(data) - 1)} {
		b, e := ReadTrustedRegular(context.Background(), f, limit)
		ownedFDRefused(t, b, e)
	}
	ops := ownedFDSystemOps()
	calls := 0
	ops.pread = func(fd int, p []byte, offset int64) (int, error) { calls++; return unix.Pread(fd, p[:1], offset) }
	b, e := readTrustedRegularWithOps(context.Background(), f, int64(len(data)), ops)
	ownedFDRefused(t, b, e)
	if calls == 0 || calls > 32 {
		t.Fatalf("pread budget calls=%d", calls)
	}
	// Invalid/short-zero and interrupted native returns never publish a prefix.
	for _, mode := range []string{"zero", "negative", "oversized", "interrupted", "partial-error"} {
		ops = ownedFDSystemOps()
		attempts := 0
		ops.pread = func(fd int, p []byte, offset int64) (int, error) {
			attempts++
			switch mode {
			case "zero":
				return 0, nil
			case "negative":
				return -1, nil
			case "oversized":
				return len(p) + 1, nil
			case "interrupted":
				return 0, unix.EINTR
			default:
				if attempts == 1 {
					return unix.Pread(fd, p[:1], offset)
				}
				return 0, unix.EIO
			}
		}
		b, e = readTrustedRegularWithOps(context.Background(), f, int64(len(data)), ops)
		ownedFDRefused(t, b, e)
		if attempts == 0 || attempts > 32 {
			t.Fatalf("invalid return call budget: %s %d", mode, attempts)
		}
	}
	ops = ownedFDSystemOps()
	calls = 0
	ops.pread = func(fd int, p []byte, offset int64) (int, error) {
		n, e := unix.Pread(fd, p, offset)
		calls++
		if calls == 1 {
			e = errors.Join(e, os.WriteFile(path, append(append([]byte{}, data...), byte('x')), 0600))
		}
		return n, e
	}
	b, e = readTrustedRegularWithOps(context.Background(), f, int64(len(data)+1), ops)
	ownedFDRefused(t, b, e)
	// Largest admitted object must fit the fixed per-call/total-read budgets.
	largest := bytes.Repeat([]byte("L"), 1<<20)
	ownedFDCheck(t, os.WriteFile(path, largest, 0600))
	ops = ownedFDSystemOps()
	calls = 0
	ops.pread = func(fd int, p []byte, offset int64) (int, error) {
		calls++
		if len(p) > 64*1024 {
			return 0, errors.New("oversized pread buffer")
		}
		return unix.Pread(fd, p, offset)
	}
	b, e = readTrustedRegularWithOps(context.Background(), f, 1<<20, ops)
	ownedFDCheck(t, e)
	if !bytes.Equal(b, largest) || calls == 0 || calls > 32 {
		t.Fatalf("maximum object/call budget %d", calls)
	}
	ownedFDCheck(t, os.WriteFile(path, nil, 0600))
	b, e = ReadTrustedRegular(context.Background(), f, 0)
	ownedFDCheck(t, e)
	if len(b) != 0 {
		t.Fatal("empty source returned bytes")
	}
}
func TestOwnedFDCancellationAndCloseRace(t *testing.T) {
	f, path, data := ownedFDFixture(t)
	ownedFDPositive(t, f, data)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b, e := ReadTrustedRegular(ctx, f, int64(len(data)))
	ownedFDRefused(t, b, e)
	if !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	ops := ownedFDSystemOps()
	closes := 0
	ops.afterDup = func(fd int) error {
		done := make(chan error, 1)
		go func() { done <- f.Close() }()
		e := <-done
		cancel()
		return e
	}
	ops.close = func(fd int) error { closes++; return unix.Close(fd) }
	b, e = readTrustedRegularWithOps(ctx, f, int64(len(data)), ops)
	ownedFDRefused(t, b, e)
	if !errors.Is(e, context.Canceled) || closes != 1 {
		t.Fatalf("cancel/cleanup e=%v closes=%d", e, closes)
	}
	// Cancellation during successful cleanup must also prevent publication.
	fresh, e := os.Open(path)
	ownedFDCheck(t, e)
	defer func() { ownedFDCheck(t, fresh.Close()) }()
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	ops = ownedFDSystemOps()
	closes = 0
	ops.close = func(fd int) error { closes++; err := unix.Close(fd); cancel(); return err }
	b, e = readTrustedRegularWithOps(ctx, fresh, int64(len(data)), ops)
	ownedFDRefused(t, b, e)
	if !errors.Is(e, context.Canceled) || closes != 1 {
		t.Fatalf("late cancellation e=%v closes=%d", e, closes)
	}
}
func TestOwnedFDReadAndCloseFailures(t *testing.T) {
	f, _, data := ownedFDFixture(t)
	ownedFDPositive(t, f, data)
	readErr, closeErr := errors.New("injected pread error"), errors.New("reported close uncertainty")
	for _, failRead := range []bool{false, true} {
		ops := ownedFDSystemOps()
		closes := 0
		owned := -1
		ops.afterDup = func(fd int) error { owned = fd; return nil }
		if failRead {
			ops.pread = func(int, []byte, int64) (int, error) { return 0, readErr }
		}
		var nativeCloseErr error
		ops.close = func(fd int) error {
			closes++
			nativeCloseErr = unix.Close(fd)
			return errors.Join(nativeCloseErr, closeErr)
		}
		b, e := readTrustedRegularWithOps(context.Background(), f, int64(len(data)), ops)
		ownedFDRefused(t, b, e)
		ownedFDCheck(t, nativeCloseErr) // Injected failure must not hide a real close failure.
		if !errors.Is(e, closeErr) || (failRead && !errors.Is(e, readErr)) || closes != 1 {
			t.Fatalf("lost causes/cleanup e=%v closes=%d", e, closes)
		}
		var st unix.Stat_t
		if e = unix.Fstat(owned, &st); !errors.Is(e, unix.EBADF) {
			t.Fatalf("duplicate cleanup not observed %v", e)
		}
	}

	// A trusted seam panic still consumes cleanup once and returns no bytes.
	for _, stage := range []string{"after-dup", "pread", "close"} {
		ops := ownedFDSystemOps()
		closes := 0
		var nativeCloseErr error
		if stage == "after-dup" {
			ops.afterDup = func(int) error { panic(readErr) }
		}
		if stage == "pread" {
			ops.pread = func(int, []byte, int64) (int, error) { panic(readErr) }
		}
		ops.close = func(fd int) error {
			closes++
			nativeCloseErr = unix.Close(fd)
			if stage == "close" {
				panic(closeErr)
			}
			return nativeCloseErr
		}
		b, e := readTrustedRegularWithOps(context.Background(), f, int64(len(data)), ops)
		ownedFDRefused(t, b, e)
		ownedFDCheck(t, nativeCloseErr)
		cause := readErr
		if stage == "close" {
			cause = closeErr
		}
		if !errors.Is(e, cause) || closes != 1 {
			t.Fatalf("panic cause/cleanup %s: %v closes=%d", stage, e, closes)
		}
	}
}
