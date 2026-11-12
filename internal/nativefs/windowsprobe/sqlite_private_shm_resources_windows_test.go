//go:build windows && nativeprobe_sqlite_identity && nativeprobe_sqlite_lease && nativeprobe_sqlite_permit && nativeprobe_sqlite_persistent && nativeprobe_sqlite_main_read && nativeprobe_sqlite_existing_wal && nativeprobe_sqlite_shm_resources

package windowsprobe

import (
	"bytes"
	"context"
	"errors"
	"golang.org/x/sys/windows"
	"modernc.org/libc"
	sqlite3 "modernc.org/sqlite/lib"
	"os"
	"path/filepath"
	"testing"
	"time"
	"unsafe"
)

type shmResourceTestAuthority struct {
	*existingWALAuthority
	host *persistentSHMResourceHost
}

func (a *shmResourceTestAuthority) MapSHM(k sqlite3.ExperimentalSHMResourceKey, r, s, e int32) (uintptr, error) {
	return a.host.mapRegion(a.c, k, r, s, e)
}
func (a *shmResourceTestAuthority) DetachSHM(k sqlite3.ExperimentalSHMResourceKey, d bool) error {
	return a.host.detach(a.c, k, d)
}

type shmResourceFixture struct {
	dir    string
	a      *shmResourceTestAuthority
	v      uintptr
	holder windows.Handle
	held   bool
	data   []byte
}

func shmResourceLock(h windows.Handle, ex bool) error {
	f := uint32(windows.LOCKFILE_FAIL_IMMEDIATELY)
	if ex {
		f |= windows.LOCKFILE_EXCLUSIVE_LOCK
	}
	return windows.LockFileEx(h, f, 0, 1, 0, &windows.Overlapped{Offset: 128})
}
func shmResourceUnlock(h windows.Handle) error {
	return windows.UnlockFileEx(h, 0, 1, 0, &windows.Overlapped{Offset: 128})
}
func shmResourceCheck(t *testing.T, e error) {
	t.Helper()
	if e != nil {
		t.Fatal(e)
	}
}
func newSHMResourceFixture(t *testing.T) *shmResourceFixture {
	t.Helper()
	dir, r := persistentFixture(t)
	data := make([]byte, 65536)
	copy(data, "first-region")
	copy(data[32768:], "second-region")
	shmResourceCheck(t, os.WriteFile(filepath.Join(dir, "store.db-shm"), data, 0600))
	o, e := newPersistentWALReadOnlyOwner(r, "store.db", persistentPermitHooks{})
	shmResourceCheck(t, e)
	permitShutdowns.Store(o, &permitShutdown{done: make(chan struct{})})
	// Registered first; cleanup order later closes files and releases fixture DMS.
	t.Cleanup(func() {
		if e := permitOwnerClose(o); e != nil {
			t.Error(e)
		}
	})
	c, e := o.newConnection(context.Background())
	shmResourceCheck(t, e)
	a := &shmResourceTestAuthority{&existingWALAuthority{c: c, root: r}, newPersistentSHMResourceHost(o)}
	f := &shmResourceFixture{dir: dir, a: a, data: data}
	parent := o.broker.lineage[len(o.broker.lineage)-1].h
	f.holder, e = persistentOpenReadOnlyWAL(parent, "store.db-shm")
	t.Cleanup(func() {
		if f.held {
			if e := shmResourceUnlock(f.holder); e != nil {
				t.Error(e)
			}
		}
		if f.holder != 0 && f.holder != windows.InvalidHandle {
			h := f.holder
			f.holder = 0
			if e := windows.CloseHandle(h); e != nil {
				t.Error(e)
			}
		}
		got, e := os.ReadFile(filepath.Join(dir, "store.db-shm"))
		if e != nil || !bytes.Equal(got, data) {
			t.Errorf("fixture mutation: %v", e)
		}
	})
	shmResourceCheck(t, e)
	if f.holder == 0 || f.holder == windows.InvalidHandle {
		t.Fatal("missing fixture handle")
	}
	shmResourceCheck(t, qualifyReadOnlyWALAccess(f.holder))
	info, e := inspect(f.holder)
	shmResourceCheck(t, e)
	if info.ID != o.broker.objects[persistentSHM].id {
		t.Fatal("fixture registered identity mismatch")
	}
	shmResourceCheck(t, shmResourceLock(f.holder, false))
	f.held = true
	probe, e := persistentOpenReadOnlyWAL(parent, "store.db-shm")
	consumeProbe := func() error {
		if probe == 0 || probe == windows.InvalidHandle {
			return nil
		}
		h := probe
		probe = 0
		return windows.CloseHandle(h)
	}
	t.Cleanup(func() {
		if e := consumeProbe(); e != nil {
			t.Error(e)
		}
	})
	shmResourceCheck(t, e)
	if probe == 0 || probe == windows.InvalidHandle {
		t.Fatal("missing probe handle")
	}
	e = shmResourceLock(probe, true)
	if !errors.Is(e, windows.ERROR_LOCK_VIOLATION) {
		var unlockErr error
		if e == nil {
			unlockErr = shmResourceUnlock(probe)
		}
		cleanupErr := errors.Join(unlockErr, consumeProbe())
		t.Fatalf("DMS independent handle control: lock=%v cleanup=%v", e, cleanupErr)
	}
	shmResourceCheck(t, consumeProbe())
	m, e := windows.CreateFileMapping(f.holder, nil, windows.PAGE_READONLY, 0, 65536, nil)
	var p uintptr
	retainedView := false
	cleanupMapping := func() error {
		if retainedView {
			return errors.New("fixture view cleanup uncertain; backing retained")
		}
		if p != 0 {
			base := p
			p = 0
			if err := windows.UnmapViewOfFile(base); err != nil {
				retainedView = true
				return err
			}
		}
		if m != 0 && m != windows.InvalidHandle {
			h := m
			m = 0
			return windows.CloseHandle(h)
		}
		return nil
	}
	t.Cleanup(func() {
		if e := cleanupMapping(); e != nil {
			t.Error(e)
		}
	})
	shmResourceCheck(t, e)
	if m == 0 || m == windows.InvalidHandle {
		t.Fatal("missing mapping handle")
	}
	p, e = windows.MapViewOfFile(m, windows.FILE_MAP_READ, 0, 0, 65536)
	shmResourceCheck(t, e)
	if p == 0 {
		t.Fatal("null fixture view")
	}
	same := bytes.Equal(unsafe.Slice((*byte)(unsafe.Pointer(p)), 65536), data)
	shmResourceCheck(t, cleanupMapping())
	if !same {
		t.Fatal("native readonly fixture map bytes")
	}
	f.v, e = sqlite3.ExperimentalSHMResourcesRegister(a, filepath.Join(dir, "store.db"))
	shmResourceCheck(t, e)
	t.Log("SHM_RESOURCE_FIXTURE_QUALIFIED identity rights readonly-view independent-DMS")
	return f
}
func (f *shmResourceFixture) open(t *testing.T) *existingWALFile {
	return existingWALOpen(t, f.v, filepath.Join(f.dir, "store.db"), 0x101, true)
}
func shmResourceMap(t *testing.T, f *existingWALFile, r int32) (uintptr, int32) {
	t.Helper()
	m := f.methods()
	if m == nil || m.FxShmMap == 0 {
		t.Fatal("missing real xShmMap")
	}
	var p uintptr
	fn := *(*func(*libc.TLS, uintptr, int32, int32, int32, uintptr) int32)(unsafe.Pointer(&m.FxShmMap))
	rc := fn(f.tls, f.file, r, 32768, 0, uintptr(unsafe.Pointer(&p)))
	return p, rc
}
func (f *shmResourceFixture) requireMap(t *testing.T, file *existingWALFile, r int32) uintptr {
	t.Helper()
	before := sqlite3.ExperimentalSHMResourcesCalls(f.v)
	p, rc := shmResourceMap(t, file, r)
	if rc != sqlite3.SQLITE_READONLY || p == 0 {
		if rc == sqlite3.SQLITE_IOERR_SHMMAP && p == 0 && sqlite3.ExperimentalSHMResourcesCalls(f.v) == before+1 && errors.Is(f.a.host.last, errSHMResourcesUnimplemented) {
			t.Fatalf("SHM_RESOURCE_MAP_EXPECTED_RED actual private xShmMap lacks retained map")
		}
		t.Fatalf("map qualification failed rc=%d pointer=%#x", rc, p)
	}
	return p
}
func TestPrivateSHMReadonlyTwoRegionMapping(t *testing.T) {
	f := newSHMResourceFixture(t)
	a := f.open(t)
	p := f.requireMap(t, a, 0)
	q := f.requireMap(t, a, 1)
	if string(unsafe.Slice((*byte)(unsafe.Pointer(p)), 12)) != "first-region" || string(unsafe.Slice((*byte)(unsafe.Pointer(q)), 13)) != "second-region" {
		t.Fatal("mapped region offset mismatch")
	}
}
func TestPrivateSHMConnectionPointerLifetime(t *testing.T) {
	f := newSHMResourceFixture(t)
	a := f.open(t)
	f.requireMap(t, a, 0)
	c, e := f.a.c.owner.newConnection(context.Background())
	shmResourceCheck(t, e)
	authority := &shmResourceTestAuthority{&existingWALAuthority{c: c, root: f.a.root}, f.a.host}
	v, e := sqlite3.ExperimentalSHMResourcesRegister(authority, filepath.Join(f.dir, "store.db"))
	shmResourceCheck(t, e)
	b := existingWALOpen(t, v, filepath.Join(f.dir, "store.db"), 0x101, true)
	q, rc := shmResourceMap(t, b, 0)
	if rc != sqlite3.SQLITE_READONLY || q == 0 {
		t.Fatal("second connection mapping")
	}
	a.close(t)
	if string(unsafe.Slice((*byte)(unsafe.Pointer(q)), 12)) != "first-region" {
		t.Fatal("first close revoked another pointer")
	}
}
func TestPrivateSHMOwnerCloseDrainsPointers(t *testing.T) {
	f := newSHMResourceFixture(t)
	a := f.open(t)
	p := f.requireMap(t, a, 0)
	ticket, e := f.a.host.enterTestCore()
	shmResourceCheck(t, e)
	defer ticket.release()
	done := make(chan error, 1)
	go func() { done <- f.a.host.close() }()
	deadline := time.After(5 * time.Second)
	for {
		f.a.host.mu.Lock()
		closing := f.a.host.closing
		f.a.host.mu.Unlock()
		if closing {
			break
		}
		select {
		case <-deadline:
			t.Fatal("close request not observed")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	select {
	case e := <-done:
		t.Fatalf("close released retained pointer: %v", e)
	case <-time.After(40 * time.Millisecond):
	}
	if string(unsafe.Slice((*byte)(unsafe.Pointer(p)), 12)) != "first-region" {
		t.Fatal("pointer invalid during drain")
	}
	if _, e = f.a.host.enterTestCore(); e == nil {
		t.Fatal("late core admitted")
	}
	detachStarted := make(chan struct{})
	detached := make(chan struct{})
	f.a.host.afterDetachStart = func() { close(detachStarted) }
	go func() { a.close(t); close(detached) }()
	select {
	case <-detachStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("detach not entered")
	}
	select {
	case <-detached:
		t.Fatal("detach released core-held pointer")
	case <-time.After(40 * time.Millisecond):
	}
	if string(unsafe.Slice((*byte)(unsafe.Pointer(p)), 12)) != "first-region" {
		t.Fatal("pointer invalid while detach waits")
	}
	shmResourceCheck(t, ticket.release())
	select {
	case <-detached:
	case <-time.After(5 * time.Second):
		t.Fatal("detach drain timeout")
	}
	f.a.host.afterDetachStart = nil
	select {
	case e := <-done:
		shmResourceCheck(t, e)
	case <-time.After(5 * time.Second):
		t.Fatal("host drain timeout")
	}
}
func TestPrivateSHMFirstOpenerRefusesWithoutMutation(t *testing.T) {
	f := newSHMResourceFixture(t)
	a := f.open(t)
	f.requireMap(t, a, 0)
	a.close(t)
	shmResourceCheck(t, shmResourceUnlock(f.holder))
	f.held = false
	b := f.open(t)
	p, rc := shmResourceMap(t, b, 0)
	if p != 0 || rc != sqlite3.SQLITE_IOERR_SHMMAP {
		t.Fatal("first opener silently initialized")
	}
}
func TestPrivateSHMAcquisitionRollback(t *testing.T) {
	f := newSHMResourceFixture(t)
	a := f.open(t)
	f.requireMap(t, a, 0)
	a.close(t)
	f.a.host.failAfterMap = true
	b := f.open(t)
	p, rc := shmResourceMap(t, b, 0)
	if p != 0 || rc != sqlite3.SQLITE_IOERR_SHMMAP {
		t.Fatal("failed transaction published")
	}
	f.a.host.failAfterMap = false
	f.requireMap(t, b, 0)
}
func TestPrivateSHMForeignAndRetiredAttachment(t *testing.T) {
	f := newSHMResourceFixture(t)
	a := f.open(t)
	f.requireMap(t, a, 0)
	slot := a.methods().FxShmMap
	a.close(t)
	var p uintptr
	fn := *(*func(*libc.TLS, uintptr, int32, int32, int32, uintptr) int32)(unsafe.Pointer(&slot))
	rc := fn(a.tls, a.file, 0, 32768, 0, uintptr(unsafe.Pointer(&p)))
	if p != 0 || rc == sqlite3.SQLITE_READONLY {
		t.Fatal("retired callback revived")
	}
	c, e := f.a.c.owner.newConnection(context.Background())
	shmResourceCheck(t, e)
	p, e = f.a.host.mapRegion(c, sqlite3.ExperimentalSHMResourceKey{File: a.file, Generation: 1}, 0, 32768, 0)
	if p != 0 || e == nil {
		t.Fatal("foreign connection borrowed retired attachment")
	}
}
