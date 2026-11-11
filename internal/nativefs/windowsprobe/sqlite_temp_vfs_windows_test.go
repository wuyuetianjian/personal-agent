//go:build windows && nativeprobe_sqlite_identity

package windowsprobe

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"modernc.org/libc"
	sqlite3 "modernc.org/sqlite/lib"
	"testing"
	"unsafe"
)

func TestNativeSQLiteTempVFSRejectsPathsAndFlags(t *testing.T) {
	o, err := NewExperimentalTempVFS()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if e := o.Close(); e != nil {
			t.Error(e)
		}
	}()
	tls := libc.NewTLS()
	defer tls.Close()
	// All callback-visible POD and output storage is foreign allocated.
	// A uintptr passed through a Go func slot does not pin a movable Go stack.
	fileMem := libc.Xcalloc(tls, 1, uint64(unsafe.Sizeof(tempVFSFile{})))
	if fileMem == 0 {
		t.Fatal("allocate foreign file POD")
	}
	defer libc.Xfree(tls, fileMem)
	outMem := libc.Xcalloc(tls, 1, 4)
	if outMem == 0 {
		t.Fatal("allocate foreign output")
	}
	defer libc.Xfree(tls, outMem)
	bufMem := libc.Xcalloc(tls, 1, 66)
	if bufMem == 0 {
		t.Fatal("allocate foreign DL buffer")
	}
	defer libc.Xfree(tls, bufMem)
	for _, name := range []string{"", "C:\\fixture.db", "\\\\server\\share", "\\\\.\\PhysicalDrive0"} {
		for _, flags := range []int32{tempOpenFlags, sqlite3.SQLITE_OPEN_MAIN_DB | sqlite3.SQLITE_OPEN_READWRITE, sqlite3.SQLITE_OPEN_WAL | sqlite3.SQLITE_OPEN_CREATE, tempOpenFlags | 0x40000000} {
			var p uintptr
			if name != "" {
				p, err = libc.CString(name)
				if err != nil {
					t.Fatal(err)
				}
			}
			f := (*tempVFSFile)(unsafe.Pointer(fileMem))
			*f = tempVFSFile{Token: 99}
			f.Base.FpMethods = 99
			*(*int32)(unsafe.Pointer(outMem)) = 99
			slot := (*sqlite3.Tsqlite3_vfs)(unsafe.Pointer(o.vfs)).FxOpen
			if slot == 0 {
				t.Fatal("missing registered xOpen")
			}
			open := *(*func(*libc.TLS, uintptr, uintptr, uintptr, int32, uintptr) int32)(unsafe.Pointer(&struct{ uintptr }{slot}))
			rc := open(tls, o.vfs, p, fileMem, flags, outMem)
			if p != 0 {
				libc.Xfree(tls, p)
			}
			out := *(*int32)(unsafe.Pointer(outMem))
			if rc != sqlite3.SQLITE_CANTOPEN || f.Token != 0 || f.Base.FpMethods != 0 || out != 0 {
				t.Fatalf("refusal flags=%x rc=%d file=%+v out=%d", flags, rc, f, out)
			}
		}
	}
	// Exercise the actual registered dynamic-loader ABI, including void slots.
	v := (*sqlite3.Tsqlite3_vfs)(unsafe.Pointer(o.vfs))
	if v.FxDlOpen == 0 || v.FxDlSym == 0 || v.FxDlError == 0 || v.FxDlClose == 0 {
		t.Fatal("missing explicit dynamic-loader refusal slot")
	}
	dlOpen := *(*func(*libc.TLS, uintptr, uintptr) uintptr)(unsafe.Pointer(&struct{ uintptr }{v.FxDlOpen}))
	dlSym := *(*func(*libc.TLS, uintptr, uintptr, uintptr) uintptr)(unsafe.Pointer(&struct{ uintptr }{v.FxDlSym}))
	dlError := *(*func(*libc.TLS, uintptr, int32, uintptr))(unsafe.Pointer(&struct{ uintptr }{v.FxDlError}))
	dlClose := *(*func(*libc.TLS, uintptr, uintptr))(unsafe.Pointer(&struct{ uintptr }{v.FxDlClose}))
	// Sentinel pointers must not be dereferenced, loaded, or closed.
	if dlOpen(tls, o.vfs, 1) != 0 || dlSym(tls, o.vfs, 1, 1) != 0 {
		t.Fatal("dynamic loading not refused")
	}
	dlClose(tls, o.vfs, 1)
	for _, n := range []int32{0, 1, 8, 64} {
		buf := unsafe.Slice((*byte)(unsafe.Pointer(bufMem)), 66)
		for i := range buf {
			buf[i] = 0xa5
		}
		dlError(tls, o.vfs, n, bufMem+1)
		if buf[0] != 0xa5 || buf[1+int(n)] != 0xa5 {
			t.Fatalf("DlError wrote outside bound n=%d", n)
		}
		if n == 0 {
			continue
		}
		if buf[n] != 0 {
			t.Fatalf("DlError missing terminator n=%d", n)
		}
		limit := int(n) - 1
		if limit > len(tempDLDisabled) {
			limit = len(tempDLDisabled)
		}
		if string(buf[1:1+limit]) != tempDLDisabled[:limit] {
			t.Fatalf("DlError message n=%d", n)
		}
		for _, b := range buf[1+limit : 1+int(n)] {
			if b != 0 {
				t.Fatal("DlError uninitialized tail")
			}
		}
	}
	dlError(tls, o.vfs, 0, 0)
	dlError(tls, o.vfs, -1, 0)
	if s := o.Stats(); s.Opened != 0 || s.WrittenBytes != 0 {
		t.Fatalf("refusal had side effects: %+v", s)
	}
}
func TestNativeSQLiteTempVFSRegistrationOwnership(t *testing.T) {
	t.Run("physical_close_failure_retains_registration", func(t *testing.T) {
		o, e := NewExperimentalTempVFS()
		if e != nil {
			t.Fatal(e)
		}
		// Fault driver owns no OS/SQLite resource. Only this fixture can release its
		// deliberately retained synthetic lease after assertions; production cannot.
		defer func() {
			o.mu.Lock()
			o.physical = 0
			o.mu.Unlock()
			if e := o.Close(); e != nil {
				t.Error(e)
			}
		}()
		injected := errors.New("injected physical close failure")
		raw := &tempCloseFaultConn{failure: injected}
		db := sql.OpenDB(&tempConnector{owner: o, base: &tempCloseFaultDriver{raw: raw}})
		db.SetMaxIdleConns(0)
		conn, e := db.Conn(context.Background())
		if e != nil {
			t.Fatal(e)
		}
		if e := conn.Close(); e != nil {
			t.Fatalf("fixture expected database/sql release result: %v", e)
		}
		if e := db.Close(); e != nil {
			t.Fatalf("empty pool close: %v", e)
		}
		if raw.closes != 1 {
			t.Fatalf("physical close calls=%d", raw.closes)
		}
		if e := o.Close(); !errors.Is(e, ErrExperimentalTempBusy) || !errors.Is(e, injected) {
			t.Fatalf("physical error lost: %v", e)
		}
		if o.vfs == 0 || o.cname == 0 || o.physical != 1 {
			t.Fatal("failed physical close freed registration")
		}

		// The controlled owner path also surfaces the swallowed physical error.
		second, e := NewExperimentalTempVFS()
		if e != nil {
			t.Fatal(e)
		}
		defer func() {
			second.mu.Lock()
			second.physical = 0
			second.mu.Unlock()
			if e := second.Close(); e != nil {
				t.Error(e)
			}
		}()
		raw2 := &tempCloseFaultConn{failure: injected}
		e = second.withDriver(context.Background(), func(*sql.Conn) error { return nil }, &tempCloseFaultDriver{raw: raw2})
		if !errors.Is(e, injected) || raw2.closes != 1 || second.physical != 1 {
			t.Fatalf("owner failed to surface physical close: %v calls=%d physical=%d", e, raw2.closes, second.physical)
		}
	})

	captureHandleInventory(t)
	baseline := captureHandleInventory(t)
	o, err := NewExperimentalTempVFS()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if e := o.Close(); e != nil {
			t.Error(e)
		}
	}()
	err = o.WithConnection(context.Background(), func(conn *sql.Conn) error {
		if e := o.Close(); !errors.Is(e, ErrExperimentalTempBusy) {
			return errors.New("close failed to retain live connection")
		}
		var n int
		if e := conn.QueryRowContext(context.Background(), "SELECT 123").Scan(&n); e != nil {
			return e
		}
		if n != 123 {
			return errors.New("memory SQL mismatch")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = o.Close(); err != nil {
		t.Fatal(err)
	}
	if err = o.Close(); err != nil {
		t.Fatal(err)
	}
	if err = o.WithConnection(context.Background(), func(*sql.Conn) error { return nil }); err == nil {
		t.Fatal("closed registration admitted connection")
	}
	if after := captureHandleInventory(t); !sameDiskHandles(baseline, after) {
		t.Fatalf("registration leaked disk handles: before=%v after=%v", baseline.files, after.files)
	}
	if o.vfs != 0 || o.cname != 0 {
		t.Fatal("registration allocations not released")
	}
}

type tempCloseFaultDriver struct{ raw *tempCloseFaultConn }

func (d *tempCloseFaultDriver) Open(string) (driver.Conn, error) { return d.raw, nil }

type tempCloseFaultConn struct {
	failure error
	closes  int
}

func (c *tempCloseFaultConn) Close() error { c.closes++; return c.failure }
func (*tempCloseFaultConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("fault fixture has no statements")
}
func (*tempCloseFaultConn) Begin() (driver.Tx, error) {
	return nil, errors.New("fault fixture has no transactions")
}
