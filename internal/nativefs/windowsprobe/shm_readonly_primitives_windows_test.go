//go:build windows && nativeprobe_shm_readonly

package windowsprobe

// Diagnostic primitives only. No SQLite registration, stock SQL, private bridge,
// production capability, or expanded file rights are provided by this file.
import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

const shmPrimitiveMask = windows.FILE_READ_DATA | windows.FILE_READ_ATTRIBUTES | windows.SYNCHRONIZE

func shmPrimitiveOpen(dir string) (result windows.Handle, resultErr error) {
	p, e := windows.UTF16PtrFromString(dir)
	if e != nil {
		return 0, e
	}
	root, e := windows.CreateFile(p, windows.FILE_LIST_DIRECTORY|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if e != nil {
		if root != 0 && root != windows.InvalidHandle {
			e = errors.Join(e, windows.CloseHandle(root))
		}
		return 0, e
	}
	defer func() {
		if closeErr := windows.CloseHandle(root); closeErr != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("root close: %w", closeErr))
			if result != 0 && result != windows.InvalidHandle {
				resultErr = errors.Join(resultErr, windows.CloseHandle(result))
				result = 0
			}
		}
	}()
	n, e := windows.NewNTUnicodeString("fixture-shm")
	if e != nil {
		return 0, e
	}
	oa := windows.OBJECT_ATTRIBUTES{RootDirectory: root, ObjectName: n}
	oa.Length = uint32(unsafe.Sizeof(oa))
	var h windows.Handle
	var status windows.IO_STATUS_BLOCK
	e = windows.NtCreateFile(&h, shmPrimitiveMask, &oa, &status, nil, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, windows.FILE_OPEN, windows.FILE_OPEN_REPARSE_POINT|windows.FILE_OPEN_NO_RECALL|windows.FILE_SYNCHRONOUS_IO_NONALERT|windows.FILE_NON_DIRECTORY_FILE, 0, 0)
	if e != nil {
		if h != 0 && h != windows.InvalidHandle {
			e = errors.Join(e, windows.CloseHandle(h))
		}
		return 0, e
	}
	bad := func(err error) (windows.Handle, error) { return 0, errors.Join(err, windows.CloseHandle(h)) }
	if status.Status != 0 || status.Information != 1 {
		return bad(fmt.Errorf("not existing regular open: %+v", status))
	}
	var mask uint32
	e = windows.NtQueryInformationFile(h, &status, (*byte)(unsafe.Pointer(&mask)), 4, 8)
	if e != nil || status.Status != 0 || status.Information != 4 || mask != shmPrimitiveMask {
		return bad(fmt.Errorf("rights 0x%x: %w", mask, e))
	}
	var fs [32]uint16
	e = windows.GetVolumeInformationByHandle(h, nil, 0, nil, nil, nil, &fs[0], uint32(len(fs)))
	if e != nil || windows.UTF16ToString(fs[:]) != "NTFS" {
		return bad(fmt.Errorf("requires local NTFS: %s %v", windows.UTF16ToString(fs[:]), e))
	}
	return h, nil
}
func shmPrimitiveFixture(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	data := make([]byte, 65536)
	copy(data, "synthetic-readonly-shm")
	if e := os.WriteFile(filepath.Join(d, "fixture-shm"), data, 0600); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		got, e := os.ReadFile(filepath.Join(d, "fixture-shm"))
		if e != nil || string(got) != string(data) {
			t.Errorf("fixture mutated: %v", e)
		}
	})
	return d
}
func shmPrimitiveHandle(t *testing.T, d string) windows.Handle {
	t.Helper()
	h, e := shmPrimitiveOpen(d)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := windows.CloseHandle(h); e != nil {
			t.Errorf("handle close: %v", e)
		}
	})
	return h
}
func shmPrimitiveLock(h windows.Handle, ex bool, off, n uint32) error {
	flags := uint32(windows.LOCKFILE_FAIL_IMMEDIATELY)
	if ex {
		flags |= windows.LOCKFILE_EXCLUSIVE_LOCK
	}
	return windows.LockFileEx(h, flags, 0, n, 0, &windows.Overlapped{Offset: off})
}
func shmPrimitiveUnlock(h windows.Handle, off, n uint32) error {
	return windows.UnlockFileEx(h, 0, n, 0, &windows.Overlapped{Offset: off})
}
func shmPrimitiveOK(t *testing.T, e error) {
	t.Helper()
	if e != nil {
		t.Fatal(e)
	}
}
func shmPrimitiveBusy(t *testing.T, e error) {
	t.Helper()
	if !errors.Is(e, windows.ERROR_LOCK_VIOLATION) {
		t.Fatalf("want immediate ERROR_LOCK_VIOLATION=33, got %v", e)
	}
}
func shmPrimitiveWitness(t *testing.T) { t.Helper(); t.Log("SHM_PRIMITIVE_CONFIRMED " + t.Name()) }

func TestSHMPrimitiveRightsAndMap(t *testing.T) {
	d := shmPrimitiveFixture(t)
	a := shmPrimitiveHandle(t, d)
	shmPrimitiveOK(t, shmPrimitiveLock(a, true, 123, 1))
	shmPrimitiveOK(t, shmPrimitiveUnlock(a, 123, 1))
	shmPrimitiveOK(t, shmPrimitiveLock(a, false, 128, 1))
	shmPrimitiveOK(t, shmPrimitiveUnlock(a, 128, 1))
	m, e := windows.CreateFileMapping(a, nil, windows.PAGE_READONLY, 0, 65536, nil)
	defer func() {
		if m == 0 || m == windows.InvalidHandle {
			return
		}
		if e := windows.CloseHandle(m); e != nil {
			t.Error(e)
		}
	}()
	shmPrimitiveOK(t, e)
	if m == 0 || m == windows.InvalidHandle {
		t.Fatal("invalid mapping handle")
	}
	v, e := windows.MapViewOfFile(m, windows.FILE_MAP_READ, 0, 0, 65536)
	defer func() {
		if v == 0 {
			return
		}
		if e := windows.UnmapViewOfFile(v); e != nil {
			t.Error(e)
		}
	}()
	shmPrimitiveOK(t, e)
	if v == 0 {
		t.Fatal("null readonly view")
	}
	if string(unsafe.Slice((*byte)(unsafe.Pointer(v)), 22)) != "synthetic-readonly-shm" {
		t.Fatal("wrong mapped bytes")
	}
	w, e := windows.MapViewOfFile(m, windows.FILE_MAP_WRITE, 0, 0, 65536)
	if w != 0 {
		if cleanupErr := windows.UnmapViewOfFile(w); cleanupErr != nil {
			t.Error(cleanupErr)
		}
		t.Fatalf("unexpected writable map pointer: error=%v", e)
	}
	if !errors.Is(e, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("writable view must fail ERROR_ACCESS_DENIED, got %v", e)
	}
	var written uint32
	if e = windows.WriteFile(a, []byte("x"), &written, nil); !errors.Is(e, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("write denied: %v", e)
	}
	t.Logf("GRANTED_MASK=0x%x RO_EX_LOCK=OK RO_MAPPING=OK", shmPrimitiveMask)
	shmPrimitiveWitness(t)
}
func TestSHMPrimitiveSameProcess(t *testing.T) {
	d := shmPrimitiveFixture(t)
	a, b, c := shmPrimitiveHandle(t, d), shmPrimitiveHandle(t, d), shmPrimitiveHandle(t, d)
	shmPrimitiveOK(t, shmPrimitiveLock(a, true, 123, 1))
	shmPrimitiveBusy(t, shmPrimitiveLock(b, false, 123, 1))
	shmPrimitiveBusy(t, shmPrimitiveLock(b, true, 123, 1))
	shmPrimitiveOK(t, shmPrimitiveUnlock(a, 123, 1))
	shmPrimitiveOK(t, shmPrimitiveLock(a, false, 123, 1))
	shmPrimitiveOK(t, shmPrimitiveLock(b, false, 123, 1))
	shmPrimitiveOK(t, shmPrimitiveUnlock(a, 123, 1))
	shmPrimitiveBusy(t, shmPrimitiveLock(c, true, 123, 1))
	shmPrimitiveOK(t, shmPrimitiveUnlock(b, 123, 1))
	shmPrimitiveOK(t, shmPrimitiveLock(c, true, 123, 1))
	shmPrimitiveOK(t, shmPrimitiveUnlock(c, 123, 1))
	shmPrimitiveWitness(t)
}

type shmPrimitiveRequest struct {
	Op             string
	Exclusive      bool
	Offset, Length uint32
}
type shmPrimitiveReply struct {
	Error  uint32
	Detail string
}

func shmPrimitiveErr(e error) shmPrimitiveReply {
	if e == nil {
		return shmPrimitiveReply{}
	}
	var n syscall.Errno
	if errors.As(e, &n) {
		return shmPrimitiveReply{Error: uint32(n)}
	}
	return shmPrimitiveReply{Error: 0xffffffff, Detail: e.Error()}
}

// Only invoked by the exact child command. Never selected by the native gate.
func TestSHMPrimitiveChild(t *testing.T) {
	d := os.Getenv("AGENT_SHM_PRIMITIVE_DIR")
	if d == "" {
		t.Fatal("child fixture required")
	}
	h, e := shmPrimitiveOpen(d)
	if e != nil {
		t.Fatal(e)
	}
	closed := false
	defer func() {
		if !closed {
			if e := windows.CloseHandle(h); e != nil {
				t.Error(e)
			}
		}
	}()
	enc := json.NewEncoder(os.Stdout)
	if e = enc.Encode(shmPrimitiveReply{}); e != nil {
		t.Fatal(e)
	}
	dec := json.NewDecoder(os.Stdin)
	for {
		var q shmPrimitiveRequest
		if e = dec.Decode(&q); e != nil {
			t.Fatal(e)
		}
		switch q.Op {
		case "lock":
			e = shmPrimitiveLock(h, q.Exclusive, q.Offset, q.Length)
		case "unlock":
			e = shmPrimitiveUnlock(h, q.Offset, q.Length)
		case "close":
			e = windows.CloseHandle(h)
			closed = true
		default:
			t.Fatal("bad child command")
		}
		if err := enc.Encode(shmPrimitiveErr(e)); err != nil {
			t.Fatal(err)
		}
		if closed {
			return
		}
	}
}

type shmPrimitiveChild struct {
	in     io.WriteCloser
	scan   *bufio.Scanner
	cmd    *exec.Cmd
	cancel context.CancelFunc
	done   bool
}

func shmPrimitiveStart(t *testing.T, d string) *shmPrimitiveChild {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSHMPrimitiveChild$", "-test.count=1")
	for _, s := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(s), "AGENT_SHM_PRIMITIVE_DIR=") {
			cmd.Env = append(cmd.Env, s)
		}
	}
	cmd.Env = append(cmd.Env, "AGENT_SHM_PRIMITIVE_DIR="+d)
	in, e := cmd.StdinPipe()
	shmPrimitiveOK(t, e)
	out, e := cmd.StdoutPipe()
	shmPrimitiveOK(t, e)
	cmd.Stderr = os.Stderr
	shmPrimitiveOK(t, cmd.Start())
	c := &shmPrimitiveChild{in: in, scan: bufio.NewScanner(out), cmd: cmd, cancel: cancel}
	t.Cleanup(func() {
		if !c.done {
			c.cancel()
			_ = c.in.Close()
			if e := c.cmd.Wait(); e != nil {
				t.Logf("aborted child after test failure: %v", e)
			}
		}
		c.cancel()
	})
	shmPrimitiveOK(t, c.receive(t))
	return c
}
func (c *shmPrimitiveChild) receive(t *testing.T) error {
	t.Helper()
	if !c.scan.Scan() {
		t.Fatalf("child no reply: %v", c.scan.Err())
	}
	var r shmPrimitiveReply
	if e := json.Unmarshal(c.scan.Bytes(), &r); e != nil {
		t.Fatalf("child protocol %q: %v", c.scan.Text(), e)
	}
	if r.Error != 0 {
		return syscall.Errno(r.Error)
	}
	return nil
}
func (c *shmPrimitiveChild) ask(t *testing.T, q shmPrimitiveRequest) error {
	t.Helper()
	if e := json.NewEncoder(c.in).Encode(q); e != nil {
		t.Fatal(e)
	}
	return c.receive(t)
}
func (c *shmPrimitiveChild) close(t *testing.T) {
	t.Helper()
	shmPrimitiveOK(t, c.ask(t, shmPrimitiveRequest{Op: "close"}))
	shmPrimitiveOK(t, c.in.Close())
	for c.scan.Scan() {
	}
	shmPrimitiveOK(t, c.scan.Err())
	waitErr := c.cmd.Wait()
	c.done = true
	c.cancel()
	shmPrimitiveOK(t, waitErr)
}
func TestSHMPrimitiveCrossProcess(t *testing.T) {
	d := shmPrimitiveFixture(t)
	a := shmPrimitiveHandle(t, d)
	c := shmPrimitiveStart(t, d)
	shmPrimitiveOK(t, shmPrimitiveLock(a, true, 123, 1))
	shmPrimitiveBusy(t, c.ask(t, shmPrimitiveRequest{"lock", false, 123, 1}))
	shmPrimitiveBusy(t, c.ask(t, shmPrimitiveRequest{"lock", true, 123, 1}))
	shmPrimitiveOK(t, shmPrimitiveUnlock(a, 123, 1))
	shmPrimitiveOK(t, shmPrimitiveLock(a, false, 123, 1))
	shmPrimitiveOK(t, c.ask(t, shmPrimitiveRequest{"lock", false, 123, 1}))
	shmPrimitiveOK(t, shmPrimitiveUnlock(a, 123, 1))
	shmPrimitiveBusy(t, shmPrimitiveLock(a, true, 123, 1))
	shmPrimitiveOK(t, c.ask(t, shmPrimitiveRequest{"unlock", false, 123, 1}))
	shmPrimitiveOK(t, shmPrimitiveLock(a, true, 123, 1))
	shmPrimitiveOK(t, shmPrimitiveUnlock(a, 123, 1))
	c.close(t)
	shmPrimitiveWitness(t)
}
func TestSHMPrimitiveRepeatedShared(t *testing.T) {
	d := shmPrimitiveFixture(t)
	a, b := shmPrimitiveHandle(t, d), shmPrimitiveHandle(t, d)
	// Actual OS duplicate acquisition, not the adapter model below.
	shmPrimitiveOK(t, shmPrimitiveLock(a, false, 123, 1))
	shmPrimitiveOK(t, shmPrimitiveLock(a, false, 123, 1))
	shmPrimitiveOK(t, shmPrimitiveUnlock(a, 123, 1))
	shmPrimitiveBusy(t, shmPrimitiveLock(b, true, 123, 1))
	shmPrimitiveOK(t, shmPrimitiveUnlock(a, 123, 1))
	shmPrimitiveOK(t, shmPrimitiveLock(b, true, 123, 1))
	shmPrimitiveOK(t, shmPrimitiveUnlock(b, 123, 1))
	t.Log("OS_REPEATED_SHARED=2 EXACT_UNLOCKS=2")
	// Explicit test-only adapter model. Not a private VFS implementation.
	held := false
	calls := 0
	take := func() {
		if !held {
			shmPrimitiveOK(t, shmPrimitiveLock(a, false, 123, 1))
			calls++
			held = true
		}
	}
	take()
	take()
	if calls != 1 {
		t.Fatal("test model failed dedup")
	}
	shmPrimitiveOK(t, shmPrimitiveUnlock(a, 123, 1))
	held = false
	shmPrimitiveOK(t, shmPrimitiveLock(b, true, 123, 1))
	shmPrimitiveOK(t, shmPrimitiveUnlock(b, 123, 1))
	t.Log("TEST_ADAPTER_ONLY_DEDUP_CALLS=1")
	shmPrimitiveWitness(t)
}
func TestSHMPrimitiveExactUnlock(t *testing.T) {
	d := shmPrimitiveFixture(t)
	a := shmPrimitiveHandle(t, d)
	c := shmPrimitiveStart(t, d)
	shmPrimitiveOK(t, shmPrimitiveLock(a, true, 123, 1))
	shmPrimitiveOK(t, shmPrimitiveLock(a, true, 124, 1))
	e := shmPrimitiveUnlock(a, 123, 2)
	if e == nil {
		t.Fatal("combined unlock accepted")
	}
	t.Logf("COMBINED_UNLOCK_ERROR=%v", e)
	for _, off := range []uint32{123, 124} {
		shmPrimitiveBusy(t, c.ask(t, shmPrimitiveRequest{"lock", true, off, 1}))
		shmPrimitiveOK(t, shmPrimitiveUnlock(a, off, 1))
		shmPrimitiveOK(t, c.ask(t, shmPrimitiveRequest{"lock", true, off, 1}))
		shmPrimitiveOK(t, c.ask(t, shmPrimitiveRequest{"unlock", false, off, 1}))
	}
	c.close(t)
	shmPrimitiveWitness(t)
}
func TestSHMPrimitiveDMS(t *testing.T) {
	d := shmPrimitiveFixture(t)
	a, b, c := shmPrimitiveHandle(t, d), shmPrimitiveHandle(t, d), shmPrimitiveHandle(t, d)
	// Same byte protocol as SQLite DMS; these are NOT stock SQL connections.
	shmPrimitiveOK(t, shmPrimitiveLock(a, false, 128, 1))
	shmPrimitiveBusy(t, shmPrimitiveLock(b, true, 128, 1))
	shmPrimitiveOK(t, shmPrimitiveLock(b, false, 128, 1))
	shmPrimitiveOK(t, shmPrimitiveUnlock(a, 128, 1))
	shmPrimitiveBusy(t, shmPrimitiveLock(c, true, 128, 1))
	shmPrimitiveOK(t, shmPrimitiveUnlock(b, 128, 1))
	shmPrimitiveOK(t, shmPrimitiveLock(c, true, 128, 1))
	shmPrimitiveOK(t, shmPrimitiveUnlock(c, 128, 1))
	child := shmPrimitiveStart(t, d)
	shmPrimitiveOK(t, child.ask(t, shmPrimitiveRequest{"lock", false, 128, 1}))
	shmPrimitiveBusy(t, shmPrimitiveLock(b, true, 128, 1))
	shmPrimitiveOK(t, shmPrimitiveLock(b, false, 128, 1))
	shmPrimitiveOK(t, child.ask(t, shmPrimitiveRequest{"unlock", false, 128, 1}))
	shmPrimitiveBusy(t, shmPrimitiveLock(c, true, 128, 1))
	shmPrimitiveOK(t, shmPrimitiveUnlock(b, 128, 1))
	shmPrimitiveOK(t, shmPrimitiveLock(c, true, 128, 1))
	shmPrimitiveOK(t, shmPrimitiveUnlock(c, 128, 1))
	child.close(t)
	t.Log("DMS_PROTOCOL_PRIMITIVES_ONLY_NOT_STOCK_SQL")
	shmPrimitiveWitness(t)
}
func TestSHMPrimitiveIndependentClose(t *testing.T) {
	d := shmPrimitiveFixture(t)
	a, e := shmPrimitiveOpen(d)
	shmPrimitiveOK(t, e)
	closed := false
	defer func() {
		if !closed {
			if e := windows.CloseHandle(a); e != nil {
				t.Error(e)
			}
		}
	}()
	b, c := shmPrimitiveHandle(t, d), shmPrimitiveHandle(t, d)
	shmPrimitiveOK(t, shmPrimitiveLock(a, false, 123, 1))
	shmPrimitiveOK(t, shmPrimitiveLock(b, false, 123, 1))
	e = windows.CloseHandle(a)
	closed = true
	shmPrimitiveOK(t, e)
	shmPrimitiveBusy(t, shmPrimitiveLock(c, true, 123, 1))
	shmPrimitiveOK(t, shmPrimitiveUnlock(b, 123, 1))
	shmPrimitiveOK(t, shmPrimitiveLock(c, true, 123, 1))
	shmPrimitiveOK(t, shmPrimitiveUnlock(c, 123, 1))
	shmPrimitiveWitness(t)
}
