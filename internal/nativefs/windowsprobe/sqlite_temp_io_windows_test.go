//go:build windows && nativeprobe_sqlite_identity

// Positive ABI contracts. This stage deliberately refuses root-bearing xOpen;
// failures there are interface RED, not proof that any IO method executed.
package windowsprobe

import (
	"bytes"
	"errors"
	"math"
	"testing"
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

// Follow-up barrier test design, not silently replaced with a timing-only claim:
// write hook holds actual kernel IO, xClose starts through its public slot;
// owner.Close remains Busy, metadata proves the live handle; release -> actual
// write bytes verified by close hook -> close succeeds -> callback/file counts
// zero. Use separate libc.TLS per goroutine, foreign buffers per callback, and
// join every worker before freeing POD/TLS. Timeout failure must quarantine all
// still-referenced foreign allocations rather than free under a stuck callback.
// Integrate after callback reference/statement-drain interfaces are finalized.
