//go:build windows && nativeprobe_sqlite_identity && nativeprobe_sqlite_lease && nativeprobe_sqlite_permit && nativeprobe_sqlite_persistent && nativeprobe_sqlite_main_read && nativeprobe_sqlite_existing_wal && nativeprobe_sqlite_shm_resources && nativeprobe_sqlite_shm_slots

package windowsprobe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"modernc.org/libc"
	sqlite3 "modernc.org/sqlite/lib"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"
)

const shmSlotChildEnv = "AGENT_PRIVATE_SHM_SLOT_FIXTURE"

type shmSlotAuthority struct {
	*shmResourceTestAuthority
	slots *persistentSHMSlotHost
}

func (a *shmSlotAuthority) LockSHM(k sqlite3.ExperimentalSHMResourceKey, o, n, f int32) (bool, error) {
	return a.slots.lock(a.c, k, o, n, f)
}
func (a *shmSlotAuthority) BarrierSHM(k sqlite3.ExperimentalSHMResourceKey) error {
	return a.slots.barrier(a.c, k)
}
func (a *shmSlotAuthority) DetachSHM(k sqlite3.ExperimentalSHMResourceKey, d bool) error {
	if e := a.slots.detach(a.c, k); e != nil {
		return e
	}
	return a.shmResourceTestAuthority.DetachSHM(k, d)
}
func (a *shmSlotAuthority) Revoke(e error) {
	a.slots.mu.Lock()
	a.slots.last = errors.Join(a.slots.last, e)
	a.slots.mu.Unlock()
	a.shmResourceTestAuthority.Revoke(e)
}

type shmSlotFixture struct {
	base    *shmResourceFixture
	a       *shmSlotAuthority
	file    *existingWALFile
	pointer uintptr
}

func shmSlotNative(h windows.Handle, o, n int32, exclusive, unlock bool) error {
	if unlock {
		return windows.UnlockFileEx(h, 0, uint32(n), 0, &windows.Overlapped{Offset: uint32(120 + o)})
	}
	flags := uint32(windows.LOCKFILE_FAIL_IMMEDIATELY)
	if exclusive {
		flags |= windows.LOCKFILE_EXCLUSIVE_LOCK
	}
	return windows.LockFileEx(h, flags, 0, uint32(n), 0, &windows.Overlapped{Offset: uint32(120 + o)})
}
func shmSlotHandle(t *testing.T, f *shmResourceFixture) windows.Handle {
	t.Helper()
	b := f.a.c.owner.broker
	h, e := persistentOpenReadOnlyWAL(b.lineage[len(b.lineage)-1].h, "store.db-shm")
	t.Cleanup(func() {
		if h != 0 && h != windows.InvalidHandle {
			if e := windows.CloseHandle(h); e != nil {
				t.Error(e)
			}
		}
	})
	shmResourceCheck(t, e)
	shmResourceCheck(t, qualifyReadOnlyWALAccess(h))
	return h
}
func newSHMSlotFixture(t *testing.T) *shmSlotFixture {
	t.Helper()
	b := newSHMResourceFixture(t)
	// Independent native slot control before any private slot callback.
	a, c := shmSlotHandle(t, b), shmSlotHandle(t, b)
	shmResourceCheck(t, shmSlotNative(a, 3, 1, true, false))
	busy := shmSlotNative(c, 3, 1, false, false)
	if busy == nil {
		shmResourceCheck(t, shmSlotNative(c, 3, 1, false, true))
	}
	cleanup := shmSlotNative(a, 3, 1, true, true)
	if !errors.Is(busy, windows.ERROR_LOCK_VIOLATION) || cleanup != nil {
		t.Fatalf("native slot qualification conflict=%v cleanup=%v", busy, cleanup)
	}
	authority := &shmSlotAuthority{b.a, newPersistentSHMSlotHost(b.a.host)}
	v, e := sqlite3.ExperimentalSHMSlotsRegister(authority, filepath.Join(b.dir, "store.db"))
	shmResourceCheck(t, e)
	b.v = v
	file := b.open(t)
	ptr := b.requireMap(t, file, 0)
	t.Log("SHM_SLOT_FIXTURE_QUALIFIED private-B-map independent-RO-slot-conflict")
	return &shmSlotFixture{b, authority, file, ptr}
}
func shmSlotCall(t *testing.T, f *existingWALFile, o, n, flags int32) int32 {
	t.Helper()
	m := f.methods()
	if m == nil || m.FxShmLock == 0 {
		t.Fatal("missing actual xShmLock")
	}
	fn := *(*func(*libc.TLS, uintptr, int32, int32, int32) int32)(unsafe.Pointer(&m.FxShmLock))
	return fn(f.tls, f.file, o, n, flags)
}
func (f *shmSlotFixture) requireReady(t *testing.T) {
	t.Helper()
	before, _ := sqlite3.ExperimentalSHMSlotsCalls(f.base.v)
	rc := shmSlotCall(t, f.file, 3, 1, 6)
	after, _ := sqlite3.ExperimentalSHMSlotsCalls(f.base.v)
	if rc != sqlite3.SQLITE_OK {
		if rc == sqlite3.SQLITE_IOERR_SHMLOCK && after == before+1 && errors.Is(f.a.slots.last, errSHMSlotsUnimplemented) {
			t.Fatal("SHM_SLOT_LOCK_EXPECTED_RED actual private xShmLock lacks native slot implementation")
		}
		t.Fatalf("slot positive contract rc=%d", rc)
	}
	shmSlotWant(t, shmSlotCall(t, f.file, 3, 1, 5), sqlite3.SQLITE_OK)
}
func shmSlotWant(t *testing.T, got, want int32) {
	t.Helper()
	if got != want {
		t.Fatalf("private slot rc=%d want=%d", got, want)
	}
}
func shmSlotBarrier(t *testing.T, f *existingWALFile) {
	t.Helper()
	m := f.methods()
	if m == nil || m.FxShmBarrier == 0 {
		t.Fatal("missing actual barrier")
	}
	fn := *(*func(*libc.TLS, uintptr))(unsafe.Pointer(&m.FxShmBarrier))
	fn(f.tls, f.file)
}
func TestPrivateSHMSlotSharedExclusiveProtocol(t *testing.T) {
	f := newSHMSlotFixture(t)
	f.requireReady(t)
	before := f.a.slots.nativeUnlocks
	shmSlotWant(t, shmSlotCall(t, f.file, 3, 1, 5), sqlite3.SQLITE_OK)
	if f.a.slots.nativeUnlocks != before {
		t.Fatal("unheld unlock reached native")
	}
	shmSlotWant(t, shmSlotCall(t, f.file, 3, 1, 6), sqlite3.SQLITE_OK)
	count := f.a.slots.nativeLocks
	shmSlotWant(t, shmSlotCall(t, f.file, 3, 1, 6), sqlite3.SQLITE_OK)
	if count != f.a.slots.nativeLocks {
		t.Fatal("repeated shared added native layer")
	}
	shmSlotWant(t, shmSlotCall(t, f.file, 3, 1, 5), sqlite3.SQLITE_OK)
	shmSlotWant(t, shmSlotCall(t, f.file, 3, 1, 10), sqlite3.SQLITE_OK)
	shmSlotWant(t, shmSlotCall(t, f.file, 3, 1, 10), sqlite3.SQLITE_BUSY)
	shmSlotWant(t, shmSlotCall(t, f.file, 3, 1, 9), sqlite3.SQLITE_OK)
	probe := shmSlotHandle(t, f.base)
	for _, mode := range []struct{ lock, wrongUnlock, unlock int32 }{{6, 9, 5}, {10, 5, 9}} {
		shmSlotWant(t, shmSlotCall(t, f.file, 3, 1, mode.lock), sqlite3.SQLITE_OK)
		beforeUnlock := f.a.slots.nativeUnlocks
		shmSlotWant(t, shmSlotCall(t, f.file, 3, 1, mode.wrongUnlock), sqlite3.SQLITE_IOERR_SHMLOCK)
		if f.a.slots.nativeUnlocks != beforeUnlock {
			t.Fatal("cross-mode rejection changed native holdings")
		}
		conflict := shmSlotNative(probe, 3, 1, true, false)
		if conflict == nil {
			shmResourceCheck(t, shmSlotNative(probe, 3, 1, true, true))
		}
		if !errors.Is(conflict, windows.ERROR_LOCK_VIOLATION) {
			t.Fatal("cross-mode unlock released original grant")
		}
		shmSlotWant(t, shmSlotCall(t, f.file, 3, 1, mode.unlock), sqlite3.SQLITE_OK)
		shmResourceCheck(t, shmSlotNative(probe, 3, 1, true, false))
		shmResourceCheck(t, shmSlotNative(probe, 3, 1, true, true))
	}
	for _, bad := range [][3]int32{{-1, 1, 6}, {8, 1, 6}, {0, 0, 10}, {7, 2, 10}, {0, 1, 0}, {0, 2, 6}, {0, 2, 5}} {
		if rc := shmSlotCall(t, f.file, bad[0], bad[1], bad[2]); rc == sqlite3.SQLITE_OK {
			t.Fatal("invalid slot request accepted")
		}
	}
}
func TestPrivateSHMSlotIndependentConnections(t *testing.T) {
	f := newSHMSlotFixture(t)
	f.requireReady(t)
	c, e := f.a.c.owner.newConnection(context.Background())
	shmResourceCheck(t, e)
	a := &shmSlotAuthority{&shmResourceTestAuthority{&existingWALAuthority{c: c, root: f.a.root}, f.a.host}, f.a.slots}
	v, e := sqlite3.ExperimentalSHMSlotsRegister(a, filepath.Join(f.base.dir, "store.db"))
	shmResourceCheck(t, e)
	b := existingWALOpen(t, v, filepath.Join(f.base.dir, "store.db"), 0x101, true)
	if p, rc := shmResourceMap(t, b, 0); p == 0 || rc != sqlite3.SQLITE_READONLY {
		t.Fatal("second mapping failed")
	}
	shmSlotWant(t, shmSlotCall(t, f.file, 3, 1, 6), sqlite3.SQLITE_OK)
	shmSlotWant(t, shmSlotCall(t, b, 3, 1, 6), sqlite3.SQLITE_OK)
	f.file.close(t)
	probe := shmSlotHandle(t, f.base)
	e = shmSlotNative(probe, 3, 1, true, false)
	if e == nil {
		shmResourceCheck(t, shmSlotNative(probe, 3, 1, true, true))
	}
	if !errors.Is(e, windows.ERROR_LOCK_VIOLATION) {
		t.Fatalf("first close released other connection lock: %v", e)
	}
	shmSlotWant(t, shmSlotCall(t, b, 3, 1, 5), sqlite3.SQLITE_OK)
	shmResourceCheck(t, shmSlotNative(probe, 3, 1, true, false))
	shmResourceCheck(t, shmSlotNative(probe, 3, 1, true, true))
}
func TestPrivateSHMSlotCrossProcessConflict(t *testing.T) {
	if shmSlotChild(t) {
		return
	}
	f := newSHMSlotFixture(t)
	f.requireReady(t)
	shmSlotWant(t, shmSlotCall(t, f.file, 3, 1, 10), sqlite3.SQLITE_OK)
	shmResourceCheck(t, shmSlotRunChild(t, shmSlotChildRequest{Mode: "probe", Path: filepath.Join(f.base.dir, "store.db-shm"), Busy: true}))
	shmSlotWant(t, shmSlotCall(t, f.file, 3, 1, 9), sqlite3.SQLITE_OK)
	shmResourceCheck(t, shmSlotRunChild(t, shmSlotChildRequest{Mode: "probe", Path: filepath.Join(f.base.dir, "store.db-shm")}))
	shmSlotWant(t, shmSlotCall(t, f.file, 3, 1, 6), sqlite3.SQLITE_OK)
	shmResourceCheck(t, shmSlotRunChild(t, shmSlotChildRequest{Mode: "probe", Path: filepath.Join(f.base.dir, "store.db-shm")}))
	shmResourceCheck(t, shmSlotRunChild(t, shmSlotChildRequest{Mode: "probe", Path: filepath.Join(f.base.dir, "store.db-shm"), Exclusive: true, Busy: true}))
	shmSlotWant(t, shmSlotCall(t, f.file, 3, 1, 5), sqlite3.SQLITE_OK)
	shmResourceCheck(t, shmSlotRunChild(t, shmSlotChildRequest{Mode: "probe", Path: filepath.Join(f.base.dir, "store.db-shm"), Exclusive: true}))
}
func TestPrivateSHMSlotExactRangeLedger(t *testing.T) {
	f := newSHMSlotFixture(t)
	f.requireReady(t)
	for _, r := range [][2]int32{{1, 2}, {2, 1}, {4, 4}} {
		shmSlotWant(t, shmSlotCall(t, f.file, r[0], r[1], 10), sqlite3.SQLITE_OK)
		shmSlotWant(t, shmSlotCall(t, f.file, r[0], r[1], 9), sqlite3.SQLITE_OK)
	}
	shmSlotWant(t, shmSlotCall(t, f.file, 4, 1, 10), sqlite3.SQLITE_OK)
	shmSlotWant(t, shmSlotCall(t, f.file, 5, 1, 10), sqlite3.SQLITE_OK)
	before := f.a.slots.nativeUnlocks
	if rc := shmSlotCall(t, f.file, 4, 2, 9); rc == sqlite3.SQLITE_OK {
		t.Fatal("merged range falsely unlocked")
	}
	if before != f.a.slots.nativeUnlocks {
		t.Fatal("merged rejection changed native holdings")
	}
	probe := shmSlotHandle(t, f.base)
	for _, o := range []int32{4, 5} {
		e := shmSlotNative(probe, o, 1, false, false)
		if e == nil {
			shmResourceCheck(t, shmSlotNative(probe, o, 1, false, true))
		}
		if !errors.Is(e, windows.ERROR_LOCK_VIOLATION) {
			t.Fatal("merged rejection lost original range")
		}
		shmSlotWant(t, shmSlotCall(t, f.file, o, 1, 9), sqlite3.SQLITE_OK)
	}
}
func TestPrivateSHMBarrierVisibility(t *testing.T) {
	if shmSlotChild(t) {
		return
	}
	f := newSHMSlotFixture(t)
	f.requireReady(t)
	path := filepath.Join(f.base.dir, "store.db-shm")
	t.Cleanup(func() {
		if e := shmSlotRunChild(t, shmSlotChildRequest{Mode: "write", Path: path}); e != nil {
			t.Error(e)
		}
	})
	shmResourceCheck(t, shmSlotRunChild(t, shmSlotChildRequest{Mode: "write", Path: path, Value: 0x5a}))
	shmSlotWant(t, shmSlotCall(t, f.file, 3, 1, 6), sqlite3.SQLITE_OK)
	before := f.a.slots.barriers
	_, callbackBefore := sqlite3.ExperimentalSHMSlotsCalls(f.base.v)
	shmSlotBarrier(t, f.file)
	_, callbackAfter := sqlite3.ExperimentalSHMSlotsCalls(f.base.v)
	if callbackAfter != callbackBefore+1 || f.a.slots.barriers != before+1 {
		t.Fatal("actual barrier missing")
	}
	if *(*byte)(unsafe.Pointer(f.pointer + 4096)) != 0x5a {
		t.Fatal("mapped child write not visible")
	}
	shmSlotWant(t, shmSlotCall(t, f.file, 3, 1, 5), sqlite3.SQLITE_OK)
}
func TestPrivateSHMSlotDetachAndFaultRetention(t *testing.T) {
	if shmSlotChild(t) {
		return
	}
	f := newSHMSlotFixture(t)
	f.requireReady(t)
	shmSlotWant(t, shmSlotCall(t, f.file, 4, 4, 10), sqlite3.SQLITE_OK)
	key := shmResourceLiveKey(t, f.base, f.file)
	ticket, e := f.a.host.enterTestCore(f.a.c, key)
	shmResourceCheck(t, e)
	defer ticket.release()
	entered, done := make(chan struct{}), make(chan struct{})
	f.a.slots.afterDrainStart = func() { close(entered) }
	go func() { f.file.close(t); close(done) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("slot detach did not begin drain")
	}
	if _, e := f.a.host.enterTestCore(f.a.c, key); e == nil {
		t.Fatal("slot draining admitted new core")
	}
	probe := shmSlotHandle(t, f.base)
	conflict := shmSlotNative(probe, 4, 4, true, false)
	if conflict == nil {
		shmResourceCheck(t, shmSlotNative(probe, 4, 4, true, true))
	}
	if !errors.Is(conflict, windows.ERROR_LOCK_VIOLATION) {
		t.Fatal("slot released while pointer ticket alive")
	}
	select {
	case <-done:
		t.Fatal("detach ignored core ticket")
	case <-time.After(40 * time.Millisecond):
	}
	shmResourceCheck(t, ticket.release())
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("slot detach drain stuck")
	}
	f.a.slots.afterDrainStart = nil
	shmResourceCheck(t, shmSlotNative(probe, 4, 4, true, false))
	shmResourceCheck(t, shmSlotNative(probe, 4, 4, true, true))
	shmResourceCheck(t, shmSlotRunChild(t, shmSlotChildRequest{Mode: "retained"}))
}

type shmSlotChildRequest struct {
	Mode, Path string
	Busy       bool
	Exclusive  bool
	Value      byte
}

func shmSlotRunChild(t *testing.T, r shmSlotChildRequest) error {
	t.Helper()
	payload, e := json.Marshal(r)
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^"+t.Name()+"$", "-test.timeout=25s")
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(key, shmSlotChildEnv) {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, shmSlotChildEnv+"="+string(payload))
	out, e := cmd.CombinedOutput()
	if e != nil || strings.Count(string(out), "SHM_SLOT_CHILD_OK "+r.Mode) != 1 {
		return fmt.Errorf("slot child %s: %v %s", r.Mode, e, out)
	}
	return nil
}
func shmSlotChild(t *testing.T) bool {
	raw := os.Getenv(shmSlotChildEnv)
	if raw == "" {
		return false
	}
	var r shmSlotChildRequest
	shmResourceCheck(t, json.Unmarshal([]byte(raw), &r))
	if r.Mode == "retained" {
		shmSlotRetainedChild(t)
		return true
	}
	if r.Mode == "probe" {
		root, e := OpenLocalRoot(filepath.Dir(r.Path))
		shmResourceCheck(t, e)
		defer func() {
			if e := root.Close(); e != nil {
				t.Error(e)
			}
		}()
		h, e := persistentOpenReadOnlyWAL(root.h, filepath.Base(r.Path))
		defer func() {
			if h != 0 && h != windows.InvalidHandle {
				if e := windows.CloseHandle(h); e != nil {
					t.Error(e)
				}
			}
		}()
		shmResourceCheck(t, e)
		shmResourceCheck(t, qualifyReadOnlyWALAccess(h))
		e = shmSlotNative(h, 3, 1, r.Exclusive, false)
		if e == nil {
			shmResourceCheck(t, shmSlotNative(h, 3, 1, r.Exclusive, true))
		}
		if (r.Busy && !errors.Is(e, windows.ERROR_LOCK_VIOLATION)) || (!r.Busy && e != nil) {
			t.Fatalf("child native conflict: %v", e)
		}
	} else if r.Mode == "write" {
		// Writer is a fixture-only cooperating external process, not private authority.
		file, e := os.OpenFile(r.Path, os.O_RDWR, 0)
		shmResourceCheck(t, e)
		h := windows.Handle(file.Fd())
		e = shmSlotNative(h, 3, 1, true, false)
		if e != nil {
			shmResourceCheck(t, errors.Join(e, file.Close()))
		}
		n, writeErr := file.WriteAt([]byte{r.Value}, 4096)
		cleanup := errors.Join(shmSlotNative(h, 3, 1, true, true), file.Close())
		if n != 1 || writeErr != nil || cleanup != nil {
			t.Fatalf("writer %d %v cleanup=%v", n, writeErr, cleanup)
		}
	} else {
		t.Fatal("unknown slot child mode")
	}
	fmt.Println("SHM_SLOT_CHILD_OK " + r.Mode)
	return true
}
func shmSlotRetainedChild(t *testing.T) {
	f := newSHMSlotFixture(t)
	f.requireReady(t)
	shmSlotWant(t, shmSlotCall(t, f.file, 3, 1, 10), sqlite3.SQLITE_OK)
	fault := errors.New("slot unlock uncertain")
	attempts := 0
	f.a.slots.unlock = func(windows.Handle, int32, int32) error { attempts++; return fault }
	slot := f.file.methods().FxClose
	closeFn := *(*func(*libc.TLS, uintptr) int32)(unsafe.Pointer(&slot))
	first := closeFn(f.file.tls, f.file.file)
	second := closeFn(f.file.tls, f.file.file)
	if first != sqlite3.SQLITE_IOERR_CLOSE || second != first || attempts != 1 {
		fmt.Println("slot retained close failed", first, second, attempts)
		os.Exit(2)
	}
	result := make(chan error, 1)
	go func() { result <- f.a.host.close() }()
	select {
	case e := <-result:
		if !errors.Is(e, errSHMRetained) || !errors.Is(e, fault) {
			fmt.Println("slot retained result", e)
			os.Exit(2)
		}
	case <-time.After(5 * time.Second):
		fmt.Println("slot host close blocked")
		os.Exit(2)
	}
	f.a.c.owner.mu.Lock()
	closing := f.a.c.owner.closing
	f.a.c.owner.mu.Unlock()
	if closing {
		fmt.Println("slot retention closed base owner")
		os.Exit(2)
	}
	broker := f.a.c.owner.broker
	probe, e := persistentOpenReadOnlyWAL(broker.lineage[len(broker.lineage)-1].h, "store.db-shm")
	if e != nil {
		if probe != 0 && probe != windows.InvalidHandle {
			e = errors.Join(e, windows.CloseHandle(probe))
		}
		fmt.Println("retained probe setup", e)
		os.Exit(2)
	}
	conflict := shmSlotNative(probe, 3, 1, false, false)
	var cleanup error
	if conflict == nil {
		cleanup = shmSlotNative(probe, 3, 1, false, true)
	}
	cleanup = errors.Join(cleanup, windows.CloseHandle(probe))
	if !errors.Is(conflict, windows.ERROR_LOCK_VIOLATION) || cleanup != nil {
		fmt.Println("uncertain slot lost real lock", conflict, cleanup)
		os.Exit(2)
	}
	if t.Failed() {
		fmt.Println("slot retained prior failure")
		os.Exit(2)
	}
	fmt.Println("SHM_SLOT_CHILD_OK retained")
	os.Exit(0)
}
