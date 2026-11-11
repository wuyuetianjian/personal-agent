//go:build windows && nativeprobe_sqlite_identity

// Positive ABI contracts, retained from the native xOpen-refusal RED stage.
// IO behavior is qualified only once these methods actually execute natively.
package windowsprobe

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"math"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"modernc.org/libc"
	sqlite3 "modernc.org/sqlite/lib"
)

type tempABIFixture struct {
	owner             *ExperimentalTempVFS
	tls               *libc.TLS
	file, out, buffer uintptr
	methods           *sqlite3.Tsqlite3_io_methods
	handle            windows.Handle // observed through private test hook; never closed here
	baseline          handleInventory
	expectedCleanup   error
	registeredToken   uintptr
	quarantine        bool
}

func tempABIMalloc(t *testing.T, tls *libc.TLS, n uint64) uintptr {
	t.Helper()
	p := libc.Xcalloc(tls, 1, n)
	if p == 0 {
		t.Fatal("foreign allocation")
	}
	return p
}
func tempABINew(t *testing.T, hooks scratchHooks) *tempABIFixture {
	t.Helper()
	_, root := scratchFixture(t)
	captureHandleInventory(t)
	f := &tempABIFixture{tls: libc.NewTLS(), baseline: captureHandleInventory(t)}
	// Cleanup after a failed assertion must still close a published file before
	// owner unregister/free, then free test-owned POD/buffers. Never free SQLite POD
	// within xClose itself. Future concurrent tests must join workers first.
	t.Cleanup(func() {
		if f.quarantine {
			t.Error("quarantined foreign fixture: callback did not drain; not safe to free")
			return
		}
		if f.file != 0 {
			pod := (*tempVFSFile)(unsafe.Pointer(f.file))
			if pod.Base.FpMethods != 0 {
				m := (*sqlite3.Tsqlite3_io_methods)(unsafe.Pointer(pod.Base.FpMethods))
				close := *(*func(*libc.TLS, uintptr) int32)(unsafe.Pointer(&struct{ uintptr }{m.FxClose}))
				if rc := close(f.tls, f.file); rc != sqlite3.SQLITE_OK {
					t.Errorf("cleanup xClose=%d", rc)
				}
			}
		}
		if f.owner != nil {
			if e := f.owner.Close(); !scratchExpectedCleanup(e, f.expectedCleanup) {
				t.Error(e)
			}
		}
		libc.Xfree(f.tls, f.buffer)
		libc.Xfree(f.tls, f.out)
		libc.Xfree(f.tls, f.file)
		f.tls.Close()
	})
	oldAfter := hooks.afterCreate
	hooks.afterCreate = func(h windows.Handle) error {
		f.handle = h
		if oldAfter != nil {
			return oldAfter(h)
		}
		return nil
	}
	o, e := newExperimentalTempVFSWithScratch(root, hooks)
	f.owner = o
	if o != nil {
		f.registeredToken = o.token
	}
	if e != nil || o == nil {
		t.Fatalf("root-bearing constructor: %v", e)
	}
	f.file = tempABIMalloc(t, f.tls, uint64(unsafe.Sizeof(tempVFSFile{})))
	f.out = tempABIMalloc(t, f.tls, 8)
	f.buffer = tempABIMalloc(t, f.tls, (1<<20)+2)

	v := (*sqlite3.Tsqlite3_vfs)(unsafe.Pointer(o.vfs))
	open := *(*func(*libc.TLS, uintptr, uintptr, uintptr, int32, uintptr) int32)(unsafe.Pointer(&struct{ uintptr }{v.FxOpen}))
	rc := open(f.tls, o.vfs, 0, f.file, tempOpenFlags, f.out)
	if rc != sqlite3.SQLITE_OK {
		t.Fatalf("root-bearing anonymous xOpen refused (interface RED, not IO execution): %d", rc)
	}
	pod := (*tempVFSFile)(unsafe.Pointer(f.file))
	if pod.Base.FpMethods == 0 || pod.Token == 0 || *(*int32)(unsafe.Pointer(f.out)) != tempOpenFlags {
		t.Fatal("invalid published POD/flags")
	}
	f.methods = (*sqlite3.Tsqlite3_io_methods)(unsafe.Pointer(pod.Base.FpMethods))
	if f.methods.FiVersion != 1 {
		t.Fatal("wrong IO version")
	}
	for _, slot := range []uintptr{f.methods.FxClose, f.methods.FxRead, f.methods.FxWrite, f.methods.FxTruncate, f.methods.FxSync, f.methods.FxFileSize, f.methods.FxLock, f.methods.FxUnlock, f.methods.FxCheckReservedLock, f.methods.FxFileControl, f.methods.FxSectorSize, f.methods.FxDeviceCharacteristics} {
		if slot == 0 {
			t.Fatal("missing version-1 callback")
		}
	}
	for _, slot := range []uintptr{f.methods.FxShmMap, f.methods.FxShmLock, f.methods.FxShmBarrier, f.methods.FxShmUnmap, f.methods.FxFetch, f.methods.FxUnfetch} {
		if slot != 0 {
			t.Fatal("unexpected version-2/3 surface")
		}
	}
	return f
}
func (f *tempABIFixture) bytes() []byte {
	return unsafe.Slice((*byte)(unsafe.Pointer(f.buffer)), (1<<20)+2)
}
func (f *tempABIFixture) write(n int32, off int64) int32 {
	fn := *(*func(*libc.TLS, uintptr, uintptr, int32, int64) int32)(unsafe.Pointer(&struct{ uintptr }{f.methods.FxWrite}))
	return fn(f.tls, f.file, f.buffer, n, off)
}
func (f *tempABIFixture) read(n int32, off int64) int32 {
	fn := *(*func(*libc.TLS, uintptr, uintptr, int32, int64) int32)(unsafe.Pointer(&struct{ uintptr }{f.methods.FxRead}))
	return fn(f.tls, f.file, f.buffer, n, off)
}
func (f *tempABIFixture) truncate(size int64) int32 {
	fn := *(*func(*libc.TLS, uintptr, int64) int32)(unsafe.Pointer(&struct{ uintptr }{f.methods.FxTruncate}))
	return fn(f.tls, f.file, size)
}
func (f *tempABIFixture) size() (int64, int32) {
	*(*int64)(unsafe.Pointer(f.out)) = -999
	fn := *(*func(*libc.TLS, uintptr, uintptr) int32)(unsafe.Pointer(&struct{ uintptr }{f.methods.FxFileSize}))
	rc := fn(f.tls, f.file, f.out)
	return *(*int64)(unsafe.Pointer(f.out)), rc
}
func (f *tempABIFixture) sync() int32 {
	fn := *(*func(*libc.TLS, uintptr, int32) int32)(unsafe.Pointer(&struct{ uintptr }{f.methods.FxSync}))
	return fn(f.tls, f.file, sqlite3.SQLITE_SYNC_NORMAL)
}
func (f *tempABIFixture) close() int32 {
	fn := *(*func(*libc.TLS, uintptr) int32)(unsafe.Pointer(&struct{ uintptr }{f.methods.FxClose}))
	return fn(f.tls, f.file)
}

func TestNativeSQLiteTempIOReadWriteAndEOF(t *testing.T) {
	f := tempABINew(t, scratchHooks{})
	id := scratchID(t, f.handle)
	copy(f.bytes(), "abcd")
	if rc := f.write(4, 0); rc != sqlite3.SQLITE_OK {
		t.Fatalf("write rc=%d", rc)
	}
	if rc := f.sync(); rc != sqlite3.SQLITE_OK {
		t.Fatalf("sync rc=%d", rc)
	}
	for i := range f.bytes() {
		f.bytes()[i] = 0xa5
	}
	if rc := f.read(8, 0); rc != sqlite3.SQLITE_IOERR_SHORT_READ || !bytes.Equal(f.bytes()[:8], []byte{'a', 'b', 'c', 'd', 0, 0, 0, 0}) || f.bytes()[8] != 0xa5 {
		t.Fatalf("partial EOF rc=%d bytes=%x", rc, f.bytes()[:9])
	}
	for i := range f.bytes() {
		f.bytes()[i] = 0xa5
	}
	if rc := f.read(8, 99); rc != sqlite3.SQLITE_IOERR_SHORT_READ || !bytes.Equal(f.bytes()[:8], make([]byte, 8)) || f.bytes()[8] != 0xa5 {
		t.Fatalf("whole EOF rc=%d bytes=%x", rc, f.bytes()[:9])
	}
	if rc := f.read(4, 0); rc != sqlite3.SQLITE_OK || string(f.bytes()[:4]) != "abcd" {
		t.Fatalf("exact read rc=%d", rc)
	}
	if scratchID(t, f.handle) != id {
		t.Fatal("actual file identity changed")
	}
	if rc := f.truncate(2); rc != sqlite3.SQLITE_OK {
		t.Fatalf("shrink rc=%d", rc)
	}
	if rc := f.truncate(6); rc != sqlite3.SQLITE_OK {
		t.Fatalf("extend rc=%d", rc)
	}
	if size, rc := f.size(); rc != sqlite3.SQLITE_OK || size != 6 {
		t.Fatalf("size=%d rc=%d", size, rc)
	}
	if rc := f.read(6, 0); rc != sqlite3.SQLITE_OK || !bytes.Equal(f.bytes()[:6], []byte{'a', 'b', 0, 0, 0, 0}) {
		t.Fatalf("extension rc=%d", rc)
	}
	s := f.owner.Stats()
	if s.WrittenBytes != 4 || s.ReadBytes != 14 {
		t.Fatalf("actual IO byte accounting %+v", s)
	} // 4+0+4+6 actual bytes; zero-filled EOF tail is not IO.
}

func TestNativeSQLiteTempIOFailureCodes(t *testing.T) {
	t.Run("malformed_and_capacity", func(t *testing.T) {
		f := tempABINew(t, scratchHooks{})
		copy(f.bytes(), "seed")
		if rc := f.write(4, 0); rc != sqlite3.SQLITE_OK {
			t.Fatal(rc)
		}
		for _, tc := range []struct {
			n   int32
			off int64
		}{{-1, 0}, {4, -1}, {4, math.MaxInt64}, {(1 << 20) + 1, 0}} {
			before := f.owner.Stats()
			for i := range f.bytes() {
				f.bytes()[i] = 0xa5
			}
			if rc := f.write(tc.n, tc.off); rc != sqlite3.SQLITE_IOERR_WRITE {
				t.Fatalf("malformed write %+v rc=%d", tc, rc)
			}
			if !bytes.Equal(f.bytes(), bytes.Repeat([]byte{0xa5}, len(f.bytes()))) {
				t.Fatal("write mutated input buffer")
			}
			if rc := f.read(tc.n, tc.off); rc != sqlite3.SQLITE_IOERR_READ {
				t.Fatalf("malformed read %+v rc=%d", tc, rc)
			}
			// Invalid amount has no valid output range: do not create/clear a slice.
			// Valid amount but invalid offset is a read error with initialized output.
			clearCount := 0
			if tc.n >= 0 && tc.n <= 1<<20 {
				clearCount = int(tc.n)
			}
			if !bytes.Equal(f.bytes()[:clearCount], make([]byte, clearCount)) ||
				!bytes.Equal(f.bytes()[clearCount:], bytes.Repeat([]byte{0xa5}, len(f.bytes())-clearCount)) {
				t.Fatal("read initialized invalid range or left valid error output dirty")
			}
			after := f.owner.Stats()
			if after.ReadBytes != before.ReadBytes || after.WrittenBytes != before.WrittenBytes || after.BackendReadCalls != before.BackendReadCalls || after.BackendWriteCalls != before.BackendWriteCalls {
				t.Fatal("malformed request performed backend IO")
			}
		}
		if rc := f.truncate(-1); rc != sqlite3.SQLITE_IOERR_TRUNCATE {
			t.Fatal(rc)
		}
		if rc := f.truncate((16 << 20) + 1); rc != sqlite3.SQLITE_FULL {
			t.Fatal(rc)
		}
		if rc := f.write(4, 16<<20); rc != sqlite3.SQLITE_FULL {
			t.Fatal(rc)
		}
		if size, rc := f.size(); rc != sqlite3.SQLITE_OK || size != 4 {
			t.Fatalf("preflight altered size=%d rc=%d", size, rc)
		}
		if rc := f.read(4, 0); rc != sqlite3.SQLITE_OK || string(f.bytes()[:4]) != "seed" {
			t.Fatalf("preflight altered bytes rc=%d", rc)
		}
	})
	t.Run("real_partial_write_poison_not_full_or_eof", func(t *testing.T) {
		calls := 0
		f := tempABINew(t, scratchHooks{write: func(h windows.Handle, b []byte) (uint32, error) {
			calls++
			var n uint32
			e := windows.WriteFile(h, b[:1], &n, nil)
			return n, errors.Join(e, errors.New("injected partial"))
		}})
		copy(f.bytes(), "abcd")
		if rc := f.write(4, 0); rc != sqlite3.SQLITE_IOERR_WRITE {
			t.Fatal(rc)
		}
		var st standardInfo
		if e := query(f.handle, windows.FileStandardInfo, &st); e != nil || st.Size != 1 {
			t.Fatalf("partial write was not real: %+v %v", st, e)
		}
		if rc := f.write(4, 0); rc != sqlite3.SQLITE_IOERR_WRITE || calls != 1 {
			t.Fatalf("retry rc=%d calls=%d", rc, calls)
		}
		for i := range f.bytes() {
			f.bytes()[i] = 0xa5
		}
		if rc := f.read(4, 0); rc != sqlite3.SQLITE_IOERR_READ {
			t.Fatalf("poison misclassified as EOF: %d", rc)
		}
		if !bytes.Equal(f.bytes()[:4], make([]byte, 4)) || f.bytes()[4] != 0xa5 {
			t.Fatal("poisoned read output not exactly zero initialized")
		}
		if size, rc := f.size(); rc != sqlite3.SQLITE_IOERR_FSTAT || size != 0 {
			t.Fatalf("failed filesize output=%d rc=%d", size, rc)
		}
	})
	t.Run("sync_error", func(t *testing.T) {
		f := tempABINew(t, scratchHooks{sync: func(windows.Handle) error { return errors.New("sync fault") }})
		if rc := f.sync(); rc != sqlite3.SQLITE_IOERR_FSYNC {
			t.Fatal(rc)
		}
	})
	t.Run("truncate_uncertain", func(t *testing.T) {
		f := tempABINew(t, scratchHooks{truncate: func(windows.Handle, int64) error { return errors.New("truncate fault") }})
		if rc := f.truncate(2); rc != sqlite3.SQLITE_IOERR_TRUNCATE {
			t.Fatal(rc)
		}
	})
}

func TestNativeSQLiteTempIOCloseAndDrain(t *testing.T) {
	t.Run("inflight_callback_close_barrier", tempABICallbackDrain)
	f := tempABINew(t, scratchHooks{})
	live := captureHandleInventory(t)
	if f.baseline.files[f.handle] || !live.files[f.handle] {
		t.Fatal("PSS missing actual scratch handle")
	}
	if e := f.owner.Close(); !errors.Is(e, ErrExperimentalTempBusy) {
		t.Fatalf("live file owner close=%v", e)
	}
	if f.owner.vfs == 0 || f.owner.cname == 0 {
		t.Fatal("Busy destroyed registration")
	}
	if f.methods.FiVersion != 1 || f.methods.FxRead == 0 {
		t.Fatal("Busy destroyed methods")
	}
	if rc := f.read(1, 0); rc != sqlite3.SQLITE_IOERR_SHORT_READ {
		t.Fatalf("Busy invalidated file callback rc=%d", rc)
	}
	if rc := f.close(); rc != sqlite3.SQLITE_OK {
		t.Fatalf("close rc=%d", rc)
	}
	pod := (*tempVFSFile)(unsafe.Pointer(f.file))
	if pod.Token != 0 || pod.Base.FpMethods != 0 {
		t.Fatal("close did not consume POD ownership")
	}
	after := captureHandleInventory(t)
	if after.files[f.handle] {
		t.Fatal("physical scratch handle survived close")
	}
	if rc := f.read(1, 0); rc != sqlite3.SQLITE_IOERR_READ {
		t.Fatalf("stale read rc=%d", rc)
	}
	if rc := f.close(); rc != sqlite3.SQLITE_IOERR_CLOSE {
		t.Fatalf("stale close rc=%d", rc)
	}
	// Owner must now close/drain normally; fixture cleanup repeats safely.
	if e := f.owner.Close(); e != nil {
		t.Fatal(e)
	}
	if stats := f.owner.Stats(); stats.Opened != 1 || stats.ClosedSuccessfully != 1 {
		t.Fatalf("success-close accounting %+v", stats)
	}
	tempABIAssertDrained(t, f)

}

// Requires planned stats fields; these are observations, not authorization flags.
func tempABIAssertDrained(t *testing.T, f *tempABIFixture) {
	t.Helper()
	if f.owner.vfs != 0 || f.owner.cname != 0 || f.owner.ioMethods != 0 {
		t.Fatal("registration allocations retained after terminal drain")
	}
	tempOwners.Lock()
	_, registered := tempOwners.m[f.registeredToken]
	tempOwners.Unlock()
	if registered {
		t.Fatal("owner registry token leaked")
	}
	stats := f.owner.Stats()
	if stats.LiveFiles != 0 || stats.InflightCallbacks != 0 {
		t.Fatalf("file token/callback reference leak: %+v", stats)
	}
	if after := captureHandleInventory(t); !sameDiskHandles(f.baseline, after) {
		t.Fatalf("exact disk inventory mismatch: before=%v after=%v", f.baseline.files, after.files)
	}
}
func TestNativeSQLiteTempIOCloseErrorIsNotSuccess(t *testing.T) {
	t.Run("real_sql_destructor_preserves_terminal_error", func(t *testing.T) {
		_, root := scratchFixture(t)
		baseline := captureHandleInventory(t)
		injected := errors.New("real SQL TEMP xClose failure")
		closeCalls := 0
		owner, e := newExperimentalTempVFSWithScratch(root, scratchHooks{close: func(h windows.Handle) error {
			closeCalls++
			return errors.Join(windows.CloseHandle(h), injected)
		}})
		if owner != nil {
			t.Cleanup(func() {
				if e := owner.Close(); !scratchExpectedCleanup(e, injected) {
					t.Error(e)
				}
			})
		}
		if e != nil {
			t.Fatal(e)
		}
		callbackCompleted := false
		var callbackErr error
		err := owner.WithConnection(context.Background(), func(conn *sql.Conn) (result error) {
			defer func() { callbackErr = result }()
			for _, q := range []string{"PRAGMA temp_store=FILE", "PRAGMA temp.cache_size=8", "PRAGMA cache_spill=ON", "PRAGMA temp.journal_mode=MEMORY", "CREATE TEMP TABLE closeproof(payload BLOB)"} {
				if _, e := conn.ExecContext(context.Background(), q); e != nil {
					return e
				}
			}
			stmt, e := conn.PrepareContext(context.Background(), "INSERT INTO closeproof VALUES (?)")
			if e != nil {
				return e
			}
			defer func() { result = errors.Join(result, stmt.Close()) }()
			payload := bytes.Repeat([]byte{'d'}, 8192)
			for i := 0; i < 64; i++ {
				if _, e := stmt.ExecContext(context.Background(), payload); e != nil {
					return e
				}
			}
			var count int
			if e := conn.QueryRowContext(context.Background(), "SELECT count(*) FROM closeproof").Scan(&count); e != nil {
				return e
			}
			if count != 64 {
				return errors.New("closeproof SQL count mismatch")
			}
			callbackCompleted = true
			return nil
		})
		if !callbackCompleted || callbackErr != nil {
			t.Fatalf("SQL callback did not complete successfully: completed=%v error=%v", callbackCompleted, callbackErr)
		}
		if !errors.Is(err, injected) {
			t.Fatalf("WithConnection lost real SQL VFS error: %v", err)
		}
		if closeCalls == 0 || owner.Stats().Opened == 0 || owner.Stats().WrittenBytes == 0 {
			t.Fatal("real SQL fixture never opened/wrote/closed TEMP")
		}
		owner.mu.Lock()
		physicalErr := owner.closeFailure
		terminal := owner.ioCloseErr
		owner.mu.Unlock()
		if physicalErr != nil || !errors.Is(terminal, injected) {
			t.Fatalf("expected successful SQLite driver close but retained VFS error: physical=%v VFS=%v", physicalErr, terminal)
		}
		if e := owner.Close(); !errors.Is(e, injected) {
			t.Fatalf("owner lost real SQL terminal error: %v", e)
		}
		stats := owner.Stats()
		if stats.ClosedSuccessfully != 0 || stats.LiveFiles != 0 || stats.InflightCallbacks != 0 || stats.PhysicalConnections != 0 || stats.RegistrationAlive || stats.MethodsAlive {
			t.Fatalf("false successful close or incomplete failure cleanup: %+v", stats)
		}
		if after := captureHandleInventory(t); !sameDiskHandles(baseline, after) {
			t.Fatalf("real SQL failure cleanup disk leak %v -> %v", baseline.files, after.files)
		}
	})

	t.Run("driver_destructor_swallows_vfs_rc", func(t *testing.T) {
		injected := errors.New("real VFS close hook failure")
		f := tempABINew(t, scratchHooks{close: func(h windows.Handle) error { return errors.Join(windows.CloseHandle(h), injected) }})
		f.expectedCleanup = injected
		rc := int32(-1)
		destructors := 0
		// Safe fake driver simulates sqlite3_close_v2 ignoring VFS xClose rc.
		// The VFS callback and its owned Windows file close are REAL, not a
		// manually assigned owner error. This is not a claim of real SQL coverage.
		d := &tempDestructorDriver{close: func() { destructors++; rc = f.close() }}
		err := f.owner.withDriver(context.Background(), func(*sql.Conn) error { return nil }, d)
		if rc != sqlite3.SQLITE_IOERR_CLOSE || destructors != 1 {
			t.Fatalf("destructor did not execute real VFS close: rc=%d count=%d", rc, destructors)
		}
		if !errors.Is(err, injected) {
			t.Fatalf("WithConnection reported success/lost VFS terminal error: %v", err)
		}
		if e := f.owner.Close(); !errors.Is(e, injected) {
			t.Fatalf("owner.Close lost VFS error: %v", e)
		}
		tempABIAssertDrained(t, f)
	})

	injected := errors.New("injected after actual close")
	calls := 0
	f := tempABINew(t, scratchHooks{close: func(h windows.Handle) error { calls++; return errors.Join(windows.CloseHandle(h), injected) }})
	f.expectedCleanup = injected
	if rc := f.close(); rc != sqlite3.SQLITE_IOERR_CLOSE {
		t.Fatalf("close failure rc=%d", rc)
	}
	pod := (*tempVFSFile)(unsafe.Pointer(f.file))
	if pod.Token != 0 || pod.Base.FpMethods != 0 {
		t.Fatal("uncertain close kept numeric POD ownership")
	}
	stats := f.owner.Stats()
	if stats.ClosedSuccessfully != 0 {
		t.Fatalf("failed close counted successful: %+v", stats)
	}
	if rc := f.close(); rc != sqlite3.SQLITE_IOERR_CLOSE || calls != 1 {
		t.Fatalf("close retry rc=%d calls=%d", rc, calls)
	}
	// This fixture's hook closed the actual OS handle then injected uncertainty.
	// Releasing refcount-free registration storage is cleanup, never a successful
	// file close. Preserve the terminal injected error on every owner.Close.
	if e := f.owner.Close(); !errors.Is(e, injected) {
		t.Fatalf("terminal close error lost: %v", e)
	}
	tempABIAssertDrained(t, f)
}

// Real write callback pinned before xClose; all buffers/TLS remain foreign and
// alive until BOTH workers have joined. Timeout is failure plus quarantine,
// never freeing memory that a stuck callback could still access.
func tempABICallbackDrain(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	f := tempABINew(t, scratchHooks{
		write: func(h windows.Handle, b []byte) (uint32, error) {
			close(entered)
			<-release
			var n uint32
			e := windows.WriteFile(h, b, &n, nil)
			return n, e
		},
		close: func(h windows.Handle) error {
			_, seekErr := windows.Seek(h, 0, 0)
			data := make([]byte, 4)
			var n uint32
			readErr := windows.ReadFile(h, data, &n, nil)
			var mismatch error
			if n != 4 || string(data) != "live" {
				mismatch = errors.New("xClose did not observe actual completed write bytes")
			}
			return errors.Join(seekErr, readErr, mismatch, windows.CloseHandle(h))
		},
	})
	id := scratchID(t, f.handle)
	writeTLS, closeTLS := libc.NewTLS(), libc.NewTLS()
	input := libc.Xcalloc(writeTLS, 1, 4)
	if input == 0 {
		writeTLS.Close()
		closeTLS.Close()
		t.Fatal("barrier foreign input")
	}
	copy(unsafe.Slice((*byte)(unsafe.Pointer(input)), 4), "live")
	t.Cleanup(func() {
		if f.quarantine {
			return
		}
		libc.Xfree(writeTLS, input)
		writeTLS.Close()
		closeTLS.Close()
	})
	writeFn := *(*func(*libc.TLS, uintptr, uintptr, int32, int64) int32)(unsafe.Pointer(&struct{ uintptr }{f.methods.FxWrite}))
	closeFn := *(*func(*libc.TLS, uintptr) int32)(unsafe.Pointer(&struct{ uintptr }{f.methods.FxClose}))
	written, closed := make(chan int32, 1), make(chan int32, 1)
	f.quarantine = true
	go func() { written <- writeFn(writeTLS, f.file, input, 4, 0) }()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("write callback did not reach kernel barrier")
	}
	go func() { closed <- closeFn(closeTLS, f.file) }()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for f.owner.Stats().InflightCallbacks < 2 {
		select {
		case rc := <-closed:
			t.Fatalf("xClose bypassed live callback: %d", rc)
		case <-deadline.C:
			t.Fatal("xClose did not enter callback drain")
		case <-ticker.C:
		}
	}
	if e := f.owner.Close(); !errors.Is(e, ErrExperimentalTempBusy) {
		t.Fatalf("owner freed live callbacks: %v", e)
	}
	if f.owner.ioMethods == 0 || f.owner.vfs == 0 || scratchID(t, f.handle) != id {
		t.Fatal("callback barrier lost actual handle/table")
	}
	select {
	case rc := <-closed:
		t.Fatalf("xClose finished before write release: %d", rc)
	default:
	}
	unblock()
	var writeRC, closeRC int32
	select {
	case writeRC = <-written:
	case <-time.After(10 * time.Second):
		t.Fatal("write did not drain")
	}
	select {
	case closeRC = <-closed:
	case <-time.After(10 * time.Second):
		t.Fatal("close did not drain")
	}
	f.quarantine = false
	if writeRC != sqlite3.SQLITE_OK || closeRC != sqlite3.SQLITE_OK {
		t.Fatalf("write=%d close=%d", writeRC, closeRC)
	}
	if e := f.owner.Close(); e != nil {
		t.Fatal(e)
	}
	tempABIAssertDrained(t, f)
}

type tempDestructorDriver struct{ close func() }

func (d *tempDestructorDriver) Open(string) (driver.Conn, error) {
	return &tempDestructorConn{onClose: d.close}, nil
}

type tempDestructorConn struct{ onClose func() }

func (c *tempDestructorConn) Close() error { c.onClose(); return nil }
func (*tempDestructorConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("destructor fixture does not execute SQL")
}
func (*tempDestructorConn) Begin() (driver.Tx, error) {
	return nil, errors.New("destructor fixture does not execute transactions")
}
