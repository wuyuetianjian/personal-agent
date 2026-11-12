//go:build windows && nativeprobe_sqlite_identity && nativeprobe_sqlite_lease && nativeprobe_sqlite_permit && nativeprobe_sqlite_persistent && nativeprobe_sqlite_main_read && nativeprobe_sqlite_existing_wal && nativeprobe_sqlite_shm_resources

package windowsprobe

import (
	"bytes"
	"context"
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

type shmResourceTestAuthority struct {
	*existingWALAuthority
	host *persistentSHMResourceHost
}

func (a *shmResourceTestAuthority) Revoke(e error) {
	a.host.poison(e)
	a.existingWALAuthority.Revoke(e)
}
func (a *shmResourceTestAuthority) AttachSHM(k sqlite3.ExperimentalSHMResourceKey) error {
	return a.host.attach(a.c, k)
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
func shmResourceLiveKey(t *testing.T, f *shmResourceFixture, a *existingWALFile) sqlite3.ExperimentalSHMResourceKey {
	t.Helper()
	f.a.host.mu.Lock()
	defer f.a.host.mu.Unlock()
	for k, v := range f.a.host.attachments {
		if k.File == a.file && !v.retired {
			return k
		}
	}
	t.Fatal("missing live attachment key")
	return sqlite3.ExperimentalSHMResourceKey{}
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
	key := shmResourceLiveKey(t, f, b)
	ticket, e := f.a.host.enterTestCore(c, key)
	shmResourceCheck(t, e)
	defer ticket.release()
	entered, done := make(chan struct{}), make(chan struct{})
	f.a.host.afterDetachStart = func() { close(entered) }
	go func() { b.close(t); close(done) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("connection detach did not enter")
	}
	if _, e := f.a.host.enterTestCore(c, key); e == nil {
		t.Fatal("fresh core ticket entered draining attachment")
	}
	select {
	case <-done:
		t.Fatal("connection detach ignored live pointer")
	case <-time.After(40 * time.Millisecond):
	}
	if string(unsafe.Slice((*byte)(unsafe.Pointer(q)), 12)) != "first-region" {
		t.Fatal("core pointer lost during connection detach")
	}
	shmResourceCheck(t, ticket.release())
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("connection detach stuck")
	}
	f.a.host.afterDetachStart = nil
	if _, e := f.a.host.enterTestCore(c, key); e == nil {
		t.Fatal("retired attachment admitted ticket")
	}
}
func TestPrivateSHMOwnerCloseDrainsPointers(t *testing.T) {
	f := newSHMResourceFixture(t)
	a := f.open(t)
	p := f.requireMap(t, a, 0)
	ticket, e := f.a.host.enterTestCore(f.a.c, shmResourceLiveKey(t, f, a))
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
	if _, e = f.a.host.enterTestCore(f.a.c, shmResourceLiveKey(t, f, a)); e == nil {
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
	if _, e := f.a.host.enterTestCore(f.a.c, shmResourceLiveKey(t, f, a)); e == nil {
		t.Fatal("new core admitted during detach")
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
	if shmRetainedChild(t) {
		return
	}
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
	shmResourceFaultMatrix(t)
	shmResourceAttachFailures(t)
	shmResourceClosePublication(t)
	shmResourceRetainedChildren(t)
}
func TestPrivateSHMForeignAndRetiredAttachment(t *testing.T) {
	f := newSHMResourceFixture(t)
	a := f.open(t)
	f.requireMap(t, a, 0)
	var key sqlite3.ExperimentalSHMResourceKey
	f.a.host.mu.Lock()
	for k := range f.a.host.attachments {
		if k.File == a.file {
			key = k
		}
	}
	f.a.host.mu.Unlock()
	if key.Generation == 0 {
		t.Fatal("missing real live key")
	}
	c, e := f.a.c.owner.newConnection(context.Background())
	shmResourceCheck(t, e)
	if p, e := f.a.host.mapRegion(c, key, 0, 32768, 0); p != 0 || !errors.Is(e, errPersistentPermitForeign) {
		t.Fatal("live same-owner cross-connection accepted")
	}
	if p, e := f.a.host.mapRegion(f.a.c, key, 0, 32768, 0); p == 0 || e != nil {
		t.Fatal("original connection live-key positive control failed")
	}
	slot := a.methods().FxShmMap
	a.close(t)
	var p uintptr
	fn := *(*func(*libc.TLS, uintptr, int32, int32, int32, uintptr) int32)(unsafe.Pointer(&slot))
	rc := fn(a.tls, a.file, 0, 32768, 0, uintptr(unsafe.Pointer(&p)))
	if p != 0 || rc == sqlite3.SQLITE_READONLY {
		t.Fatal("retired callback revived")
	}
	if p, e := f.a.host.mapRegion(f.a.c, key, 0, 32768, 0); p != 0 || !errors.Is(e, errPersistentPermitForeign) {
		t.Fatal("same connection retired key accepted")
	}
	// Same numeric slot with a new, explicit generation cannot revive the old key.
	newer := key
	newer.Generation += 1000000
	shmResourceCheck(t, f.a.host.attach(f.a.c, newer))
	if p, e := f.a.host.mapRegion(f.a.c, newer, 0, 32768, 0); p == 0 || e != nil {
		t.Fatal("new generation control failed")
	}
	if p, e := f.a.host.mapRegion(f.a.c, key, 0, 32768, 0); p != 0 || e == nil {
		t.Fatal("old generation revived by slot reuse")
	}
	shmResourceCheck(t, f.a.host.detach(f.a.c, newer, false))
}

type shmAttachFaultAuthority struct {
	*shmResourceTestAuthority
	fault             error
	after, panicAfter bool
}

func (a *shmAttachFaultAuthority) AttachSHM(k sqlite3.ExperimentalSHMResourceKey) error {
	if a.after {
		if e := a.shmResourceTestAuthority.AttachSHM(k); e != nil {
			return e
		}
	}
	if a.panicAfter {
		panic(a.fault)
	}
	return a.fault
}
func shmResourceAttachFailures(t *testing.T) {
	for _, after := range []bool{false, true} {
		f := newSHMResourceFixture(t)
		a := &shmAttachFaultAuthority{shmResourceTestAuthority: f.a, fault: errors.New("attach failed"), after: after}
		v, e := sqlite3.ExperimentalSHMResourcesRegister(a, filepath.Join(f.dir, "store.db"))
		shmResourceCheck(t, e)
		existingWALOpen(t, v, filepath.Join(f.dir, "store.db"), 0x101, false)
		f.a.host.mu.Lock()
		for _, entry := range f.a.host.attachments {
			if !entry.retired {
				t.Error("failed attach retained a live pin without cleanup failure")
			}
		}
		f.a.host.mu.Unlock()
		good := f.open(t)
		f.requireMap(t, good, 0)
	}
}
func shmResourceFaultMatrix(t *testing.T) {
	for _, kind := range []string{"open-result-error", "mapping-result-error", "view-result-error"} {
		f := newSHMResourceFixture(t)
		a := f.open(t)
		h := f.a.host
		fault := errors.New(kind)
		var target windows.Handle
		closes, unmaps := 0, 0
		h.hooks.close = func(v windows.Handle) error {
			if v == target {
				closes++
			}
			return windows.CloseHandle(v)
		}
		switch kind {
		case "open-result-error":
			h.hooks.open = func(p windows.Handle, n string) (windows.Handle, error) {
				v, e := persistentOpenReadOnlyWAL(p, n)
				target = v
				return v, errors.Join(e, fault)
			}
		case "mapping-result-error":
			h.hooks.mapping = func(v windows.Handle) (windows.Handle, error) {
				m, e := windows.CreateFileMapping(v, nil, windows.PAGE_READONLY, 0, 0, nil)
				target = m
				return m, errors.Join(e, fault)
			}
		case "view-result-error":
			h.hooks.view = func(m windows.Handle, o uint64, n uintptr) (uintptr, error) {
				p, e := windows.MapViewOfFile(m, windows.FILE_MAP_READ, uint32(o>>32), uint32(o), n)
				return p, errors.Join(e, fault)
			}
			h.hooks.unmap = func(p uintptr) error { unmaps++; return windows.UnmapViewOfFile(p) }
		}
		if p, rc := shmResourceMap(t, a, 0); p != 0 || rc != sqlite3.SQLITE_IOERR_SHMMAP {
			t.Fatalf("%s published failed acquisition", kind)
		}
		if kind == "view-result-error" {
			if unmaps != 1 {
				t.Fatal("view+error not consumed once")
			}
		} else if target == 0 || closes != 1 {
			t.Fatalf("%s handle+error not consumed once: %d", kind, closes)
		}
		if h.last != nil || h.node != nil {
			t.Fatal("determinate rollback retained or poisoned")
		}
		h.hooks = shmResourceHooks{}
		f.requireMap(t, a, 0)
		a.close(t)
	}
	// Geometry failures must leave the known existing node intact.
	f := newSHMResourceFixture(t)
	a := f.open(t)
	f.requireMap(t, a, 0)
	for _, r := range []int32{-1, 2, 2147483647} {
		if p, rc := shmResourceMap(t, a, r); p != 0 || rc != sqlite3.SQLITE_IOERR_SHMMAP {
			t.Fatal("invalid region admitted")
		}
	}
	f.requireMap(t, a, 0)
}
func shmResourceClosePublication(t *testing.T) {
	f := newSHMResourceFixture(t)
	a := f.open(t)
	h := f.a.host
	entered, release := make(chan struct{}), make(chan struct{})
	h.hooks.beforePublish = func() { close(entered); <-release }
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	result := make(chan int32, 1)
	go func() { _, rc := shmResourceMap(t, a, 0); result <- rc }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("map never reached publication barrier")
	}
	closed := make(chan error, 1)
	go func() { closed <- h.close() }()
	deadline := time.After(5 * time.Second)
	for {
		h.mu.Lock()
		closing := h.closing
		h.mu.Unlock()
		if closing {
			break
		}
		select {
		case <-deadline:
			t.Fatal("close did not start")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	close(release)
	select {
	case rc := <-result:
		if rc != sqlite3.SQLITE_IOERR_SHMMAP {
			t.Fatal("map published after close request")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("map rollback stuck")
	}
	if h.node != nil {
		t.Fatal("cancelled acquisition left a published node")
	}
	a.close(t)
	select {
	case e := <-closed:
		shmResourceCheck(t, e)
	case <-time.After(5 * time.Second):
		t.Fatal("close publication drain stuck")
	}
}

const shmRetainedEnv = "AGENT_PRIVATE_SHM_RETAINED_FIXTURE"

func shmResourceRetainedChildren(t *testing.T) {
	for _, kind := range []string{"unmap", "mapping-close", "data-close", "unlock", "close-panic", "detach-panic", "unpublished-unmap", "metadata-close", "exclusive-unlock", "attach-panic", "unmap-panic"} {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPrivateSHMAcquisitionRollback$", "-test.timeout=25s")
		for _, entry := range os.Environ() {
			if key, _, ok := strings.Cut(entry, "="); !ok || !strings.EqualFold(key, shmRetainedEnv) {
				cmd.Env = append(cmd.Env, entry)
			}
		}
		cmd.Env = append(cmd.Env, shmRetainedEnv+"="+kind)
		out, e := cmd.CombinedOutput()
		cancel()
		if e != nil || strings.Count(string(out), "SHM_RETAINED_NORMAL_EXIT "+kind) != 1 {
			t.Fatalf("retained child %s: %v %s", kind, e, out)
		}
	}
}

// Normal process exit, not Go test cleanup or a timeout, retires deliberately
// uncertain native resources. Parent accepts only explicit witness and exit 0.
func shmRetainedChild(t *testing.T) bool {
	kind := os.Getenv(shmRetainedEnv)
	if kind == "" {
		return false
	}
	f := newSHMResourceFixture(t)
	a := f.open(t)
	f.requireMap(t, a, 0)
	h := f.a.host
	fault := errors.New("uncertain " + kind)
	if kind == "attach-panic" {
		a.close(t)
		faulty := &shmAttachFaultAuthority{shmResourceTestAuthority: f.a, fault: fault, after: true, panicAfter: true}
		v, e := sqlite3.ExperimentalSHMResourcesRegister(faulty, filepath.Join(f.dir, "store.db"))
		shmResourceCheck(t, e)
		existingWALOpen(t, v, filepath.Join(f.dir, "store.db"), 0x101, false)
		// Cleanup succeeded, so this panic poisons but must not leave a live pin.
		h.mu.Lock()
		live := false
		for _, entry := range h.attachments {
			live = live || !entry.retired
		}
		h.mu.Unlock()
		if live || !errors.Is(h.owner.close(), fault) || t.Failed() {
			fmt.Println("attach panic cleanup/poison failure")
			os.Exit(2)
		}
		fmt.Println("SHM_RETAINED_NORMAL_EXIT " + kind)
		os.Exit(0)
	}
	if kind == "unpublished-unmap" || kind == "metadata-close" || kind == "exclusive-unlock" {
		a.close(t)
		a = f.open(t)
	}
	calls := 0
	switch kind {
	case "exclusive-unlock":
		shmResourceCheck(t, shmResourceUnlock(f.holder))
		f.held = false
		h.hooks.unlock = func(windows.Handle) error { calls++; return fault }
	case "unpublished-unmap":
		h.failAfterMap = true
		h.hooks.unmap = func(uintptr) error { calls++; return fault }
	case "metadata-close":
		h.hooks.closeMetadata = func(v windows.Handle) error {
			calls++
			nativeErr := windows.CloseHandle(v)
			if nativeErr != nil {
				t.Errorf("metadata close positive control: %v", nativeErr)
			}
			return errors.Join(nativeErr, fault)
		}
	case "unmap":
		h.hooks.unmap = func(uintptr) error { calls++; return fault }
	case "unlock":
		h.hooks.unlock = func(windows.Handle) error { calls++; return fault }
	case "mapping-close", "data-close", "close-panic":
		target := h.node.h
		if kind != "data-close" {
			target = h.node.views[0].mapping
		}
		h.hooks.close = func(v windows.Handle) error {
			if v == target {
				calls++
				if kind == "close-panic" {
					panic(fault)
				}
				return fault
			}
			return windows.CloseHandle(v)
		}
	case "detach-panic", "unmap-panic":
		h.afterDetachStart = func() { calls++; panic(fault) }
	default:
		fmt.Println("unknown retained case")
		os.Exit(2)
	}
	if kind == "unpublished-unmap" || kind == "metadata-close" || kind == "exclusive-unlock" {
		if p, rc := shmResourceMap(t, a, 0); p != 0 || rc != sqlite3.SQLITE_IOERR_SHMMAP {
			fmt.Println("uncertain acquisition published")
			os.Exit(2)
		}
		if kind == "exclusive-unlock" && (h.node == nil || !h.node.exclusiveDMS || !h.node.attemptedDMSUnlock) {
			fmt.Println("EX DMS ledger lost")
			os.Exit(2)
		}
		if kind == "unpublished-unmap" && (h.node == nil || len(h.node.unpublished) != 1 || h.node.unpublished[0].attemptedBase == 0 || h.node.unpublished[0].mapping == 0) {
			fmt.Println("unpublished view ledger lost")
			os.Exit(2)
		}
	}
	if kind == "unmap-panic" {
		slot := a.methods().FxShmUnmap
		fn := *(*func(*libc.TLS, uintptr, int32) int32)(unsafe.Pointer(&slot))
		if rc := fn(a.tls, a.file, 0); rc != sqlite3.SQLITE_IOERR_SHMMAP {
			fmt.Println("unmap panic not contained", rc)
			os.Exit(2)
		}
	}
	slot := a.methods().FxClose
	fn := *(*func(*libc.TLS, uintptr) int32)(unsafe.Pointer(&slot))
	first := fn(a.tls, a.file)
	second := fn(a.tls, a.file)
	if first != sqlite3.SQLITE_IOERR_CLOSE || second != first || calls != 1 {
		fmt.Println("retained close result/consume violation", first, second, calls)
		os.Exit(2)
	}
	if kind != "detach-panic" && kind != "unmap-panic" && (h.node == nil || !h.node.retained || h.last == nil) {
		fmt.Println("missing retained ledger")
		os.Exit(2)
	}
	h.owner.mu.Lock()
	beforeClose := h.owner.closing || h.owner.closed
	h.owner.mu.Unlock()
	if beforeClose {
		fmt.Println("owner unexpectedly closing before retained drain")
		os.Exit(2)
	}
	hostDone := make(chan error, 1)
	go func() { hostDone <- h.close() }()
	select {
	case e := <-hostDone:
		if !errors.Is(e, errSHMRetained) || !errors.Is(e, fault) {
			fmt.Println("host retained result missing", e)
			os.Exit(2)
		}
	case <-time.After(5 * time.Second):
		fmt.Println("host retained result blocked")
		os.Exit(2)
	}
	h.owner.mu.Lock()
	premature := h.owner.closing || h.owner.closed
	h.owner.mu.Unlock()
	if premature {
		fmt.Println("retained host called underlying owner close")
		os.Exit(2)
	}
	done := make(chan error, 1)
	go func() { done <- h.owner.close() }()
	deadline := time.After(5 * time.Second)
	for {
		h.owner.mu.Lock()
		closing := h.owner.closing
		h.owner.mu.Unlock()
		if closing {
			break
		}
		select {
		case <-deadline:
			fmt.Println("owner close not started")
			os.Exit(2)
		default:
			time.Sleep(time.Millisecond)
		}
	}
	select {
	case e := <-done:
		fmt.Println("retained pin released", e)
		os.Exit(2)
	case <-time.After(50 * time.Millisecond):
	}
	got, e := os.ReadFile(filepath.Join(f.dir, "store.db-shm"))
	if e != nil || !bytes.Equal(got, f.data) {
		fmt.Println("retained fixture mutated", e)
		os.Exit(2)
	}
	if t.Failed() {
		fmt.Println("retained child recorded earlier failure")
		os.Exit(2)
	}
	fmt.Println("SHM_RETAINED_NORMAL_EXIT " + kind)
	os.Exit(0)
	return true
}
