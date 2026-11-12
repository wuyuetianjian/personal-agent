//go:build windows && nativeprobe_sqlite_identity && nativeprobe_sqlite_lease && nativeprobe_sqlite_persistent && nativeprobe_sqlite_main_read

package windowsprobe

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"golang.org/x/sys/windows"
	"modernc.org/libc"
	_ "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"unsafe"
)

var errMainReadInjectedPanic = errors.New("main-read injected callback panic")

// Accept only the exact captured revocation object, never a substring or an
// errors.Is match on an entire Join tree (which could hide another close error).
func mainReadCleanupOnly(e, expected error) bool {
	if e == nil {
		return true
	}
	if expected != nil && e == expected {
		return true
	}
	if joined, ok := e.(interface{ Unwrap() []error }); ok {
		children := joined.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !mainReadCleanupOnly(child, expected) {
				return false
			}
		}
		return true
	}
	if wrapped, ok := e.(interface{ Unwrap() error }); ok {
		child := wrapped.Unwrap()
		return child != nil && mainReadCleanupOnly(child, expected)
	}
	return false
}

type mainReadTestAuthority struct {
	c                      *persistentLeaseConnection
	opens, borrows, closes int
	panicAfterBorrow       bool
	revocations            []error
}
type mainReadTestLease struct {
	a *mainReadTestAuthority
	l *persistentFileLease
}

func (a *mainReadTestAuthority) Revoke(e error) {
	a.revocations = append(a.revocations, e)
	o := a.c.owner
	o.gate.Lock()
	defer o.gate.Unlock()
	o.terminal = errors.Join(o.terminal, e)
}
func (a *mainReadTestAuthority) OpenMain() (sqlite3.ExperimentalMainReadLease, error) {
	l, e := a.c.acquire(context.Background(), leaseMain)
	if e != nil {
		return nil, e
	}
	a.opens++
	return &mainReadTestLease{a, l}, nil
}
func (l *mainReadTestLease) Borrow(fn func(uintptr) int32) (int32, error) {
	rc := int32(sqlite3.SQLITE_IOERR)
	e := l.a.c.borrow(context.Background(), l.l, func(h windows.Handle) error {
		l.a.borrows++
		rc = fn(uintptr(h))
		if l.a.panicAfterBorrow {
			panic(errMainReadInjectedPanic)
		}
		return nil
	})
	return rc, e
}
func (l *mainReadTestLease) Close() error { l.a.closes++; return l.l.close() }

// Positive native method-table smoke, NOT a successful SQLite SQL/WAL reader.
// Stock SQL only creates/holds the fixture; no stock read supplies tested bytes.
func TestMainReadBridgePrivateMethods(t *testing.T) {
	expectedProbe := errors.New("expected probe")
	if !mainReadCleanupOnly(errors.Join(expectedProbe), expectedProbe) || mainReadCleanupOnly(errors.Join(expectedProbe, windows.ERROR_ACCESS_DENIED), expectedProbe) || mainReadCleanupOnly(errors.New(expectedProbe.Error()), expectedProbe) {
		t.Fatal("cleanup leaf checker unsound")
	}
	dir := localRootPath(t)
	name := filepath.Join(dir, "main.db")
	db, e := sql.Open("sqlite", name)
	if e != nil {
		t.Fatal(e)
	}
	db.SetMaxOpenConns(1)
	defer func() {
		if e := db.Close(); e != nil {
			t.Error(e)
		}
	}()
	var mode string
	if e = db.QueryRow("PRAGMA journal_mode=WAL").Scan(&mode); e != nil || mode != "wal" {
		t.Fatalf("stock WAL fixture: %q %v", mode, e)
	}
	for _, q := range []string{"PRAGMA wal_autocheckpoint=0", "CREATE TABLE fixture(v TEXT)", "INSERT INTO fixture VALUES ('held-stock-wal')"} {
		if _, e = db.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		s, e := os.Stat(name + suffix)
		if e != nil || s.Size() == 0 {
			t.Fatalf("real fixture %s: %v", suffix, e)
		}
	}
	root, e := OpenLocalRoot(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if e := root.Close(); e != nil {
			t.Error(e)
		}
	}()
	owner, e := newPersistentLeaseOwner(root, "main.db", persistentLeaseHooks{})
	if e != nil {
		t.Fatal(e)
	}
	var a *mainReadTestAuthority
	defer func() {
		e := owner.close()
		var expected error
		if a != nil && len(a.revocations) > 0 {
			if len(a.revocations) != 1 || !errors.Is(a.revocations[0], errMainReadInjectedPanic) {
				t.Errorf("unexpected revoke source/count: %v", a.revocations)
			} else {
				expected = a.revocations[0]
			}
		}
		if !mainReadCleanupOnly(e, expected) {
			t.Errorf("unexpected cleanup error leaf: %v", e)
		}
	}()
	c, e := owner.newConnection(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	// Native positive prerequisite proves the main identity and independent data
	// handle can be qualified, without reading bytes outside private methods.
	control, e := c.acquire(context.Background(), leaseMain)
	if e != nil {
		t.Fatal(e)
	}
	if e = c.borrow(context.Background(), control, func(h windows.Handle) error { _, e := inspect(h); return e }); e != nil {
		t.Fatal(e)
	}
	if e = control.close(); e != nil {
		t.Fatal(e)
	}
	a = &mainReadTestAuthority{c: c}
	tls := libc.NewTLS()
	defer tls.Close()
	stockRead := func() {
		t.Helper()
		var got string
		if e := db.QueryRow("SELECT v FROM fixture").Scan(&got); e != nil || got != "held-stock-wal" {
			t.Fatalf("held stock connection changed: value=%q error=%v", got, e)
		}
	}
	stockRead() // Isolation control only, never supplies private-method bytes.
	defaultVFS := sqlite3.Xsqlite3_vfs_find(tls, 0)
	vfs, e := sqlite3.ExperimentalMainReadRegister(a, name)
	if e != nil {
		t.Fatal(e)
	}
	if vfs == 0 || vfs == defaultVFS || sqlite3.Xsqlite3_vfs_find(tls, 0) != defaultVFS {
		t.Fatal("not isolated private VFS")
	}
	v := (*sqlite3.Tsqlite3_vfs)(unsafe.Pointer(vfs))
	if v.FszOsFile <= 0 {
		t.Fatal("invalid real stock allocation size")
	}
	file := libc.Xcalloc(tls, 1, uint64(v.FszOsFile))
	if file == 0 {
		t.Fatal("foreign file allocation failed")
	}
	defer libc.Xfree(tls, file)
	buf := libc.Xcalloc(tls, 1, 128)
	if buf == 0 {
		t.Fatal("foreign buffer allocation failed")
	}
	defer libc.Xfree(tls, buf)
	out := libc.Xcalloc(tls, 1, 8)
	if out == 0 {
		t.Fatal("foreign output allocation failed")
	}
	defer libc.Xfree(tls, out)
	z, e := libc.CString(name + "\x00")
	if e != nil {
		t.Fatal(e)
	}
	defer libc.Xfree(tls, z)
	var methods *sqlite3.Tsqlite3_io_methods
	closed := false
	defer func() {
		if !closed && (*sqlite3.Tsqlite3_file)(unsafe.Pointer(file)).FpMethods != 0 {
			m := (*sqlite3.Tsqlite3_io_methods)(unsafe.Pointer((*sqlite3.Tsqlite3_file)(unsafe.Pointer(file)).FpMethods))
			if m.FxClose == 0 {
				t.Error("cleanup: published methods missing xClose; owner retains lease cleanup")
				return
			}
			fn := *(*func(*libc.TLS, uintptr) int32)(unsafe.Pointer(&struct{ uintptr }{m.FxClose}))
			closed = true
			if rc := fn(tls, file); rc != sqlite3.SQLITE_OK {
				t.Errorf("cleanup close: %d", rc)
			}
		}
	}()
	open := *(*func(*libc.TLS, uintptr, uintptr, uintptr, int32, uintptr) int32)(unsafe.Pointer(&struct{ uintptr }{v.FxOpen}))
	for _, flags := range []int32{sqlite3.SQLITE_OPEN_READWRITE | sqlite3.SQLITE_OPEN_MAIN_DB, sqlite3.SQLITE_OPEN_READONLY | sqlite3.SQLITE_OPEN_WAL, sqlite3.SQLITE_OPEN_READONLY | sqlite3.SQLITE_OPEN_MAIN_DB | sqlite3.SQLITE_OPEN_CREATE} {
		if rc := open(tls, vfs, z, file, flags, out); rc == sqlite3.SQLITE_OK || (*sqlite3.Tsqlite3_file)(unsafe.Pointer(file)).FpMethods != 0 || a.opens != 0 {
			t.Fatal("unsupported flags reached acquisition/publication")
		}
	}
	wrong, e := libc.CString(name + ".other\x00")
	if e != nil {
		t.Fatal(e)
	}
	defer libc.Xfree(tls, wrong)
	if rc := open(tls, vfs, wrong, file, sqlite3.SQLITE_OPEN_READONLY|sqlite3.SQLITE_OPEN_MAIN_DB, out); rc == sqlite3.SQLITE_OK || (*sqlite3.Tsqlite3_file)(unsafe.Pointer(file)).FpMethods != 0 || a.opens != 0 {
		t.Fatal("unbound name reached acquisition/publication")
	}
	before := sqlite3.ExperimentalMainReadRefusals(vfs)
	hitsBefore := sqlite3.ExperimentalMainReadHits(vfs)
	rc := open(tls, vfs, z, file, sqlite3.SQLITE_OPEN_READONLY|sqlite3.SQLITE_OPEN_MAIN_DB, out)
	stockRead() // Must run before EXPECTED_RED aborts the test.
	if sqlite3.Xsqlite3_vfs_find(tls, 0) != defaultVFS {
		t.Fatal("default VFS changed after xOpen")
	}
	if sqlite3.ExperimentalMainReadHits(vfs) != hitsBefore+1 {
		t.Fatal("bound xOpen dispatch not counted")
	}
	if rc == sqlite3.SQLITE_OK && sqlite3.ExperimentalMainReadRefusals(vfs) != before {
		t.Fatal("successful open counted as refusal")
	}
	if rc != sqlite3.SQLITE_OK {
		if rc == sqlite3.SQLITE_CANTOPEN && sqlite3.ExperimentalMainReadRefusals(vfs) == before+1 && (*sqlite3.Tsqlite3_file)(unsafe.Pointer(file)).FpMethods == 0 && a.opens == 0 {
			t.Fatalf("MAIN_READ_BRIDGE_EXPECTED_RED: real stock WAL fixture and main identity qualified; actual bound nondefault xOpen refused; methods assertions not reached")
		}
		t.Fatalf("unexpected private open failure: %d", rc)
	}
	if (*sqlite3.Tsqlite3_file)(unsafe.Pointer(file)).FpMethods == 0 {
		t.Fatal("successful open without methods")
	}
	methods = (*sqlite3.Tsqlite3_io_methods)(unsafe.Pointer((*sqlite3.Tsqlite3_file)(unsafe.Pointer(file)).FpMethods))
	for _, slot := range []uintptr{methods.FxRead, methods.FxFileSize, methods.FxLock, methods.FxUnlock, methods.FxCheckReservedLock, methods.FxClose} {
		if slot == 0 {
			t.Fatal("missing required method")
		}
	}
	if a.opens != 1 || *(*int32)(unsafe.Pointer(out)) != sqlite3.SQLITE_OPEN_READONLY {
		t.Fatal("main acquisition/readonly flags")
	}
	for _, slot := range []uintptr{methods.FxWrite, methods.FxTruncate, methods.FxSync, methods.FxFileControl, methods.FxSectorSize, methods.FxDeviceCharacteristics, methods.FxShmMap, methods.FxShmLock, methods.FxShmBarrier, methods.FxShmUnmap, methods.FxFetch, methods.FxUnfetch} {
		if slot == 0 {
			t.Fatal("private method table incomplete")
		}
	}
	write := *(*func(*libc.TLS, uintptr, uintptr, int32, int64) int32)(unsafe.Pointer(&struct{ uintptr }{methods.FxWrite}))
	truncate := *(*func(*libc.TLS, uintptr, int64) int32)(unsafe.Pointer(&struct{ uintptr }{methods.FxTruncate}))
	syncfn := *(*func(*libc.TLS, uintptr, int32) int32)(unsafe.Pointer(&struct{ uintptr }{methods.FxSync}))
	if write(tls, file, buf, 16, 0) != sqlite3.SQLITE_READONLY || truncate(tls, file, 0) != sqlite3.SQLITE_READONLY || syncfn(tls, file, 0) != sqlite3.SQLITE_READONLY {
		t.Fatal("write/truncate/sync not refused")
	}
	controlfn := *(*func(*libc.TLS, uintptr, int32, uintptr) int32)(unsafe.Pointer(&struct{ uintptr }{methods.FxFileControl}))
	for _, op := range []int32{sqlite3.SQLITE_FCNTL_WIN32_GET_HANDLE, sqlite3.SQLITE_FCNTL_NULL_IO, sqlite3.SQLITE_FCNTL_SIZE_HINT, sqlite3.SQLITE_FCNTL_CHUNK_SIZE, sqlite3.SQLITE_FCNTL_TEMPFILENAME, sqlite3.SQLITE_FCNTL_PERSIST_WAL, sqlite3.SQLITE_FCNTL_WIN32_AV_RETRY} {
		*(*uint64)(unsafe.Pointer(out)) = 0x5a5a
		if controlfn(tls, file, op, out) != sqlite3.SQLITE_NOTFOUND || *(*uint64)(unsafe.Pointer(out)) != 0x5a5a {
			t.Fatalf("unsafe file control %d", op)
		}
	}
	*(*int64)(unsafe.Pointer(out)) = 1 << 20
	if controlfn(tls, file, sqlite3.SQLITE_FCNTL_MMAP_SIZE, out) != sqlite3.SQLITE_OK || *(*int64)(unsafe.Pointer(out)) != 0 {
		t.Fatal("mapping not disabled")
	}
	fetch := *(*func(*libc.TLS, uintptr, int64, int32, uintptr) int32)(unsafe.Pointer(&struct{ uintptr }{methods.FxFetch}))
	unfetch := *(*func(*libc.TLS, uintptr, int64, uintptr) int32)(unsafe.Pointer(&struct{ uintptr }{methods.FxUnfetch}))
	*(*uintptr)(unsafe.Pointer(out)) = 123
	if fetch(tls, file, 0, 16, out) != sqlite3.SQLITE_OK || *(*uintptr)(unsafe.Pointer(out)) != 0 || unfetch(tls, file, 0, 123) != sqlite3.SQLITE_IOERR_MMAP {
		t.Fatal("mmap fallback/invalid pointer refusal")
	}
	shmmap := *(*func(*libc.TLS, uintptr, int32, int32, int32, uintptr) int32)(unsafe.Pointer(&struct{ uintptr }{methods.FxShmMap}))
	*(*uintptr)(unsafe.Pointer(out)) = 123
	if shmmap(tls, file, 0, 32768, 0, out) != sqlite3.SQLITE_IOERR_SHMMAP || *(*uintptr)(unsafe.Pointer(out)) != 0 {
		t.Fatal("SHM map accepted")
	}
	read := *(*func(*libc.TLS, uintptr, uintptr, int32, int64) int32)(unsafe.Pointer(&struct{ uintptr }{methods.FxRead}))
	stockCalls, restoreStock := mainReadStockCounter(t, tls, defaultVFS)
	defer restoreStock()
	stockCallsBefore := stockCalls()
	readBorrows := a.borrows
	if rc = read(tls, file, buf, 16, 0); rc != sqlite3.SQLITE_OK || !bytes.Equal(unsafe.Slice((*byte)(unsafe.Pointer(buf)), 16), []byte("SQLite format 3\x00")) {
		t.Fatalf("private header rc=%d", rc)
	}
	if a.borrows != readBorrows+1 {
		t.Fatal("header bypassed lease borrow")
	}
	if stockCalls() != stockCallsBefore {
		t.Fatal("private read used mutable stock ReadFile slot")
	}
	// A separate stock connection has a cold pager, ensuring the forwarding
	// override genuinely observes stock IO rather than a cached held query.
	cold, e := sql.Open("sqlite", name)
	if e != nil {
		t.Fatal(e)
	}
	var coldValue string
	coldErr := cold.QueryRow("SELECT v FROM fixture").Scan(&coldValue)
	coldClose := cold.Close()
	if coldErr != nil || coldClose != nil || coldValue != "held-stock-wal" || stockCalls() <= stockCallsBefore {
		t.Fatalf("stock ReadFile positive control: value=%q query=%v close=%v calls=%d", coldValue, coldErr, coldClose, stockCalls())
	}
	restoreStock()

	size := *(*func(*libc.TLS, uintptr, uintptr) int32)(unsafe.Pointer(&struct{ uintptr }{methods.FxFileSize}))
	if rc = size(tls, file, out); rc != sqlite3.SQLITE_OK {
		t.Fatalf("size %d", rc)
	}
	end := *(*int64)(unsafe.Pointer(out))
	if end < 16 {
		t.Fatal("invalid main length")
	}
	b := unsafe.Slice((*byte)(unsafe.Pointer(buf)), 32)
	for i := range b {
		b[i] = 0xa5
	}
	readBorrows = a.borrows
	if rc = read(tls, file, buf, 32, end); rc != sqlite3.SQLITE_IOERR_SHORT_READ || !bytes.Equal(b, make([]byte, 32)) {
		t.Fatalf("SHORT_READ/zero-fill rc=%d bytes=%x", rc, b)
	}
	if a.borrows != readBorrows+1 {
		t.Fatal("short read bypassed lease borrow")
	}
	lock := *(*func(*libc.TLS, uintptr, int32) int32)(unsafe.Pointer(&struct{ uintptr }{methods.FxLock}))
	unlock := *(*func(*libc.TLS, uintptr, int32) int32)(unsafe.Pointer(&struct{ uintptr }{methods.FxUnlock}))
	reserved := *(*func(*libc.TLS, uintptr, uintptr) int32)(unsafe.Pointer(&struct{ uintptr }{methods.FxCheckReservedLock}))
	if rc = lock(tls, file, sqlite3.SQLITE_LOCK_SHARED); rc != sqlite3.SQLITE_OK {
		t.Fatalf("shared lock %d", rc)
	}
	if rc = reserved(tls, file, out); rc != sqlite3.SQLITE_OK || *(*int32)(unsafe.Pointer(out)) != 0 {
		t.Fatalf("reserved check %d", rc)
	}
	// A held stock WAL connection may itself retain a main shared lock, so
	// shared-range contention cannot isolate the private xLock. Instead prove
	// xCheckReservedLock observes a real independently-held RESERVED byte lock.
	other, e := c.acquire(context.Background(), leaseMain)
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if e := other.close(); e != nil {
			t.Error(e)
		}
	}()
	ov := windows.Overlapped{Offset: 0x40000001}
	e = c.borrow(context.Background(), other, func(h windows.Handle) error {
		return windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &ov)
	})
	if e != nil {
		t.Fatal(e)
	}
	// Keep only kernel lock state, never a raw handle, across method calls.
	// Avoid reentering lease borrow while the external witness is borrowed.
	rc = reserved(tls, file, out)
	found := *(*int32)(unsafe.Pointer(out))
	e = c.borrow(context.Background(), other, func(h windows.Handle) error {
		return windows.UnlockFileEx(h, 0, 1, 0, &ov)
	})
	if e != nil {
		t.Fatal(e)
	}
	if rc != sqlite3.SQLITE_OK || found != 1 {
		t.Fatalf("private reserved-lock witness rc=%d found=%d", rc, found)
	}
	if rc = reserved(tls, file, out); rc != sqlite3.SQLITE_OK || *(*int32)(unsafe.Pointer(out)) != 0 {
		t.Fatalf("released reserved-lock witness rc=%d", rc)
	}
	if rc = unlock(tls, file, sqlite3.SQLITE_LOCK_NONE); rc != sqlite3.SQLITE_OK {
		t.Fatalf("unlock %d", rc)
	}
	closefn := *(*func(*libc.TLS, uintptr) int32)(unsafe.Pointer(&struct{ uintptr }{methods.FxClose}))
	closed = true
	if rc = closefn(tls, file); rc != sqlite3.SQLITE_OK {
		t.Fatalf("close %d", rc)
	}
	if a.closes != 1 || a.borrows < 6 {
		t.Fatalf("bridge not used: opens=%d borrows=%d closes=%d", a.opens, a.borrows, a.closes)
	}
	if sqlite3.Xsqlite3_vfs_find(tls, 0) != defaultVFS {
		t.Fatal("default VFS changed")
	}
	stockRead()
	// Reuse the SQLite allocation only after xClose. The new registry generation
	// must publish a fresh lease, then a panicking borrowed method must revoke
	// the host only AFTER the old lease RLock unwinds and return no partial data.
	if rc = open(tls, vfs, z, file, sqlite3.SQLITE_OPEN_READONLY|sqlite3.SQLITE_OPEN_MAIN_DB, out); rc != sqlite3.SQLITE_OK {
		t.Fatalf("generation reopen %d", rc)
	}
	closed = false
	a.panicAfterBorrow = true
	for i := range b {
		b[i] = 0xa5
	}
	if rc = read(tls, file, buf, 16, 0); rc != sqlite3.SQLITE_IOERR || !bytes.Equal(b[:16], make([]byte, 16)) {
		t.Fatalf("panic returned bytes/incorrect rc: %d %x", rc, b[:16])
	}
	a.panicAfterBorrow = false
	if owner.evidence().Terminal == nil || len(a.revocations) != 1 || !errors.Is(a.revocations[0], errMainReadInjectedPanic) {
		t.Fatal("panic did not revoke host")
	}
	if rc = read(tls, file, buf, 16, 0); rc != sqlite3.SQLITE_IOERR {
		t.Fatalf("terminal bridge admitted read: %d", rc)
	}
	closed = true
	if rc = closefn(tls, file); rc != sqlite3.SQLITE_OK {
		t.Fatalf("terminal lease close %d", rc)
	}
	if rc = closefn(tls, file); rc != sqlite3.SQLITE_OK || a.closes != 2 {
		t.Fatalf("close retried/changed: rc=%d calls=%d", rc, a.closes)
	}
}

// Synthetic regular files isolate private shared-lock ownership from stock
// SQLite's retained WAL locks. This is not a SQLite database/interop fixture.
func TestMainReadBridgeLockTransitions(t *testing.T) {
	dir, root := persistentFixture(t)
	o, e := newPersistentLeaseOwner(root, "store.db", persistentLeaseHooks{})
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if e := o.close(); e != nil {
			t.Error(e)
		}
	}()
	c, e := o.newConnection(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	control, e := c.acquire(context.Background(), leaseMain)
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if e := control.close(); e != nil {
			t.Error(e)
		}
	}()
	witness, e := c.acquire(context.Background(), leaseMain)
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if e := witness.close(); e != nil {
			t.Error(e)
		}
	}()
	ov := windows.Overlapped{Offset: 0x40000002}
	controlLocked := false
	defer func() {
		if controlLocked {
			if e := c.borrow(context.Background(), control, func(h windows.Handle) error { return windows.UnlockFileEx(h, 0, 510, 0, &ov) }); e != nil {
				t.Error(e)
			}
		}
	}()
	// Independent FILE_OBJECT positive control must establish actual conflict
	// under this process/token before interpreting the later private-VFS lock.
	if e = c.borrow(context.Background(), control, func(h windows.Handle) error {
		return windows.LockFileEx(h, windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 510, 0, &ov)
	}); e != nil {
		t.Fatal(e)
	}
	controlLocked = true
	probe := func(conflict bool) {
		t.Helper()
		var lockErr, unlockErr error
		e := c.borrow(context.Background(), witness, func(h windows.Handle) error {
			lockErr = windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 510, 0, &ov)
			if lockErr == nil {
				unlockErr = windows.UnlockFileEx(h, 0, 510, 0, &ov)
			}
			return nil
		})
		if e != nil || unlockErr != nil {
			t.Fatalf("witness access/cleanup: %v %v", e, unlockErr)
		}
		if conflict {
			if lockErr != windows.ERROR_LOCK_VIOLATION {
				t.Fatalf("expected real lock conflict, got %v", lockErr)
			}
		} else if lockErr != nil {
			t.Fatalf("expected unlocked range, got %v", lockErr)
		}
	}
	probe(true)
	if e = c.borrow(context.Background(), control, func(h windows.Handle) error { return windows.UnlockFileEx(h, 0, 510, 0, &ov) }); e != nil {
		t.Fatal(e)
	}
	controlLocked = false
	probe(false)
	t.Log("MAIN_READ_LOCK_NATIVE_CONTROL_OK: independent FILE_OBJECT shared/exclusive conflict and release verified")
	a := &mainReadTestAuthority{c: c}
	tls := libc.NewTLS()
	defer tls.Close()
	if rc := sqlite3.Xsqlite3_initialize(tls); rc != sqlite3.SQLITE_OK {
		t.Fatalf("SQLite initialize %d", rc)
	}
	stock := sqlite3.Xsqlite3_vfs_find(tls, 0)
	name := filepath.Join(dir, "store.db")
	vfs, e := sqlite3.ExperimentalMainReadRegister(a, name)
	if e != nil {
		t.Fatal(e)
	}
	if vfs == stock || vfs == 0 || sqlite3.Xsqlite3_vfs_find(tls, 0) != stock {
		t.Fatal("private/default VFS isolation")
	}
	v := (*sqlite3.Tsqlite3_vfs)(unsafe.Pointer(vfs))
	if v.FszOsFile <= 0 || v.FxOpen == 0 {
		t.Fatal("invalid VFS allocation/open")
	}
	file := libc.Xcalloc(tls, 1, uint64(v.FszOsFile))
	if file == 0 {
		t.Fatal("file allocation")
	}
	defer libc.Xfree(tls, file)
	out := libc.Xcalloc(tls, 1, 8)
	if out == 0 {
		t.Fatal("out allocation")
	}
	defer libc.Xfree(tls, out)
	z, e := libc.CString(name + "\x00")
	if e != nil {
		t.Fatal(e)
	}
	defer libc.Xfree(tls, z)
	closed := false
	defer func() {
		if !closed && (*sqlite3.Tsqlite3_file)(unsafe.Pointer(file)).FpMethods != 0 {
			m := (*sqlite3.Tsqlite3_io_methods)(unsafe.Pointer((*sqlite3.Tsqlite3_file)(unsafe.Pointer(file)).FpMethods))
			if m.FxClose == 0 {
				t.Error("cleanup missing xClose; owner retains lease cleanup")
				return
			}
			closefn := *(*func(*libc.TLS, uintptr) int32)(unsafe.Pointer(&struct{ uintptr }{m.FxClose}))
			closed = true
			if rc := closefn(tls, file); rc != sqlite3.SQLITE_OK {
				t.Errorf("cleanup close %d", rc)
			}
		}
	}()
	open := *(*func(*libc.TLS, uintptr, uintptr, uintptr, int32, uintptr) int32)(unsafe.Pointer(&struct{ uintptr }{v.FxOpen}))
	before := sqlite3.ExperimentalMainReadRefusals(vfs)
	hitsBefore := sqlite3.ExperimentalMainReadHits(vfs)
	rc := open(tls, vfs, z, file, sqlite3.SQLITE_OPEN_READONLY|sqlite3.SQLITE_OPEN_MAIN_DB, out)
	if sqlite3.Xsqlite3_vfs_find(tls, 0) != stock {
		t.Fatal("default changed after open")
	}
	if sqlite3.ExperimentalMainReadHits(vfs) != hitsBefore+1 {
		t.Fatal("bound xOpen dispatch not counted")
	}
	if rc == sqlite3.SQLITE_OK && sqlite3.ExperimentalMainReadRefusals(vfs) != before {
		t.Fatal("successful open counted as refusal")
	}
	if rc != sqlite3.SQLITE_OK {
		if rc == sqlite3.SQLITE_CANTOPEN && sqlite3.ExperimentalMainReadRefusals(vfs) == before+1 && (*sqlite3.Tsqlite3_file)(unsafe.Pointer(file)).FpMethods == 0 && a.opens == 0 {
			t.Fatal("MAIN_READ_BRIDGE_EXPECTED_RED: synthetic fixture native lock control verified; actual bound private xOpen refused; private lock assertions not reached")
		}
		t.Fatalf("unexpected private open %d", rc)
	}
	p := (*sqlite3.Tsqlite3_file)(unsafe.Pointer(file)).FpMethods
	if p == 0 {
		t.Fatal("missing methods")
	}
	m := (*sqlite3.Tsqlite3_io_methods)(unsafe.Pointer(p))
	if m.FxLock == 0 || m.FxUnlock == 0 || m.FxClose == 0 {
		t.Fatal("missing lock/close method")
	}
	if a.opens != 1 || *(*int32)(unsafe.Pointer(out)) != sqlite3.SQLITE_OPEN_READONLY {
		t.Fatal("private acquisition/flags")
	}
	lock := *(*func(*libc.TLS, uintptr, int32) int32)(unsafe.Pointer(&struct{ uintptr }{m.FxLock}))
	unlock := *(*func(*libc.TLS, uintptr, int32) int32)(unsafe.Pointer(&struct{ uintptr }{m.FxUnlock}))
	if rc = lock(tls, file, sqlite3.SQLITE_LOCK_SHARED); rc != sqlite3.SQLITE_OK {
		t.Fatalf("private shared lock %d", rc)
	}
	probe(true)
	if rc = unlock(tls, file, sqlite3.SQLITE_LOCK_NONE); rc != sqlite3.SQLITE_OK {
		t.Fatalf("private unlock %d", rc)
	}
	probe(false)
	readfn := *(*func(*libc.TLS, uintptr, uintptr, int32, int64) int32)(unsafe.Pointer(&struct{ uintptr }{m.FxRead}))
	if m.FxRead == 0 {
		t.Fatal("missing read method")
	}
	pod := (*sqlite3.TwinFile)(unsafe.Pointer(file))
	for _, set := range []func(bool){
		func(on bool) {
			pod.FmmapSize = 0
			if on {
				pod.FmmapSize = 1
			}
		},
		func(on bool) {
			pod.FmmapSizeMax = 0
			if on {
				pod.FmmapSizeMax = 1
			}
		},
		func(on bool) {
			pod.FpMapRegion = 0
			if on {
				pod.FpMapRegion = 1
			}
		},
		func(on bool) {
			pod.FhMap = 0
			if on {
				pod.FhMap = 1
			}
		},
		func(on bool) {
			pod.FnFetchOut = 0
			if on {
				pod.FnFetchOut = 1
			}
		},
	} {
		set(true)
		*(*uint64)(unsafe.Pointer(out)) = ^uint64(0)
		rc = readfn(tls, file, out, 8, 0)
		set(false) // restore only our injected state before any failure/cleanup
		if rc != sqlite3.SQLITE_IOERR || *(*uint64)(unsafe.Pointer(out)) != 0 {
			t.Fatalf("unexpected mapping state accepted rc=%d", rc)
		}
	}
	closefn := *(*func(*libc.TLS, uintptr) int32)(unsafe.Pointer(&struct{ uintptr }{m.FxClose}))
	closed = true
	if rc = closefn(tls, file); rc != sqlite3.SQLITE_OK {
		t.Fatalf("private close %d", rc)
	}
	if a.closes != 1 || a.borrows < 2 {
		t.Fatalf("private lock bridge unused: borrows=%d closes=%d", a.borrows, a.closes)
	}
}

// Test-only forwarding override through the stock VFS's documented syscall
// controls. The private generated algorithms must not consult this mutable slot.
// Tests are deliberately not parallel; restore runs before fixture teardown.
// Keep forwarding closures alive through process exit even if a future broken
// stock implementation rejects restoration. Such a test fails, but the global
// syscall slot must never retain a collected Go closure.
var mainReadStockHookRoots struct {
	sync.Mutex
	values []any
}

func mainReadStockCounter(t *testing.T, tls *libc.TLS, stock uintptr) (func() uint64, func()) {
	t.Helper()
	v := (*sqlite3.Tsqlite3_vfs)(unsafe.Pointer(stock))
	if v.FxGetSystemCall == 0 || v.FxSetSystemCall == 0 {
		t.Fatal("stock syscall controls absent")
	}
	get := *(*func(*libc.TLS, uintptr, uintptr) uintptr)(unsafe.Pointer(&struct{ uintptr }{v.FxGetSystemCall}))
	set := *(*func(*libc.TLS, uintptr, uintptr, uintptr) int32)(unsafe.Pointer(&struct{ uintptr }{v.FxSetSystemCall}))
	z, e := libc.CString("ReadFile")
	if e != nil {
		t.Fatal(e)
	}
	original := get(tls, stock, z)
	if original == 0 {
		libc.Xfree(tls, z)
		t.Fatal("stock ReadFile missing")
	}
	orig := *(*func(*libc.TLS, sqlite3.THANDLE, sqlite3.TLPVOID, sqlite3.TDWORD, sqlite3.TLPDWORD, sqlite3.TLPOVERLAPPED) sqlite3.TBOOL)(unsafe.Pointer(&struct{ uintptr }{original}))
	var calls atomic.Uint64
	hook := func(tls *libc.TLS, h sqlite3.THANDLE, buf sqlite3.TLPVOID, n sqlite3.TDWORD, got sqlite3.TLPDWORD, ov sqlite3.TLPOVERLAPPED) sqlite3.TBOOL {
		calls.Add(1)
		return orig(tls, h, buf, n, got, ov)
	}
	var boxed any = hook
	mainReadStockHookRoots.Lock()
	mainReadStockHookRoots.values = append(mainReadStockHookRoots.values, boxed)
	mainReadStockHookRoots.Unlock()
	ptr := (*[2]uintptr)(unsafe.Pointer(&boxed))[1] // official __ccgo_fp representation
	if rc := set(tls, stock, z, ptr); rc != sqlite3.SQLITE_OK {
		libc.Xfree(tls, z)
		t.Fatalf("install stock ReadFile forwarding hook %d", rc)
	}
	var once sync.Once
	restore := func() {
		once.Do(func() {
			if rc := set(tls, stock, z, original); rc != sqlite3.SQLITE_OK {
				t.Errorf("restore stock ReadFile %d", rc)
			}
			if get(tls, stock, z) != original {
				t.Error("stock ReadFile restoration mismatch")
			}
			libc.Xfree(tls, z)
			runtime.KeepAlive(boxed)
		})
	}
	return calls.Load, restore
}
