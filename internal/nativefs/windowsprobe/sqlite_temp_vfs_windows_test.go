//go:build windows && nativeprobe_sqlite_identity

package windowsprobe

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"modernc.org/libc"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
	"reflect"
	"sync"
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

// This protocol probe keeps database/sql and our connection owner real. Only
// the first stage's native resources are synthetic: sqlite3_close_v2 can report
// success with an idle prepared statement, so the fake intentionally does too.
// Missing drain must fail here before any real SQLite late-close path is tried.
func TestNativeSQLiteTempVFSStatementLifetime(t *testing.T) {
	protocolQualified := false
	passed := t.Run("safe_protocol_before_native", func(t *testing.T) {
		tempStatementLifetimeContract(t, nil)
		protocolQualified = true
	})
	// A -run filter excluding the safety stage must not bypass it.
	if !passed || !protocolQualified {
		t.Fatal("statement protocol RED: idle Conn-bound statement was not drained; real SQLite stage not entered")
	}
	t.Run("real_sqlite_idle_statement", func(t *testing.T) {
		tempStatementLifetimeContract(t, &sqlite.Driver{})
	})
}

func tempStatementLifetimeContract(t *testing.T, base driver.Driver) {
	t.Helper()
	captureHandleInventory(t)
	before := captureHandleInventory(t)
	o, err := NewExperimentalTempVFS()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if e := o.Close(); e != nil {
			t.Error(e)
		}
	}()
	probe := &tempStatementProbeDriver{base: base}
	var idle *sql.Stmt
	err = o.withDriver(context.Background(), func(conn *sql.Conn) error {
		early, e := conn.PrepareContext(context.Background(), "SELECT 11")
		if e != nil {
			return e
		}
		if e = early.Close(); e != nil {
			return e
		}
		if e = early.Close(); e != nil {
			return e
		}
		idle, e = conn.PrepareContext(context.Background(), "SELECT 29")
		if e != nil {
			return e
		}
		if base != nil {
			var n int
			if e = idle.QueryRowContext(context.Background()).Scan(&n); e != nil {
				return e
			}
			if n != 29 {
				return fmt.Errorf("real prepared statement returned %d", n)
			}
		}
		// Deliberately leave an idle Conn-bound statement. No outstanding Rows,
		// transaction or Raw call may make connection release wait for us.
		return nil
	}, probe)
	if err != nil {
		t.Fatalf("connection owner failed: %v", err)
	}
	want := []string{"prepare:1", "stmt-close:1", "prepare:2", "stmt-close:2", "conn-close"}
	if got := probe.events(); !reflect.DeepEqual(got, want) {
		// Do not late-close a real native statement after a failed protocol.
		// The fake owns no native allocation and can safely finish its cleanup.
		if base == nil && idle != nil {
			_ = idle.Close()
		}
		t.Fatalf("missing/incorrect statement drain before physical close: events=%v want=%v", got, want)
	}
	o.mu.Lock()
	active, physical := o.active, o.physical
	o.mu.Unlock()
	if active != 0 || physical != 0 {
		t.Fatalf("connection leases remain: active=%d physical=%d", active, physical)
	}
	if e := o.Close(); e != nil {
		t.Fatal(e)
	}
	if o.vfs != 0 || o.cname != 0 {
		t.Fatal("statement lifetime retained registration allocations")
	}
	tempOwners.Lock()
	_, registered := tempOwners.m[o.token]
	tempOwners.Unlock()
	if registered {
		t.Fatal("statement lifetime retained registration token")
	}
	if idle == nil {
		t.Fatal("fixture did not create idle statement")
	}
	// The wrapper must already have finalized it, while TLS/registration lived.
	// Late sql.Stmt.Close must never forward to the native driver again.
	if e := idle.Close(); e != nil {
		t.Fatalf("late statement close: %v", e)
	}
	if e := idle.Close(); e != nil {
		t.Fatalf("repeated late statement close: %v", e)
	}
	if got := probe.events(); !reflect.DeepEqual(got, want) {
		t.Fatalf("late statement close reached native driver: %v", got)
	}
	if after := captureHandleInventory(t); !sameDiskHandles(before, after) {
		t.Fatalf("statement lifetime leaked disk handles: %v -> %v", before.files, after.files)
	}
}

type tempStatementProbeDriver struct {
	base driver.Driver
	mu   sync.Mutex
	log  []string
}

func (d *tempStatementProbeDriver) record(event string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.log = append(d.log, event)
}
func (d *tempStatementProbeDriver) events() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.log...)
}
func (d *tempStatementProbeDriver) Open(name string) (driver.Conn, error) {
	var raw driver.Conn
	if d.base != nil {
		var err error
		raw, err = d.base.Open(name)
		if err != nil {
			return nil, err
		}
	}
	return &tempStatementProbeConn{raw: raw, driver: d}, nil
}

type tempStatementProbeConn struct {
	raw    driver.Conn
	driver *tempStatementProbeDriver
	next   int
}

func (c *tempStatementProbeConn) Prepare(query string) (driver.Stmt, error) {
	var raw driver.Stmt
	if c.raw != nil {
		var err error
		raw, err = c.raw.Prepare(query)
		if err != nil {
			return nil, err
		}
	}
	c.next++
	c.driver.record(fmt.Sprintf("prepare:%d", c.next))
	return &tempStatementProbeStmt{raw: raw, conn: c, id: c.next}, nil
}
func (c *tempStatementProbeConn) Close() error {
	c.driver.record("conn-close")
	if c.raw != nil {
		return c.raw.Close()
	}
	return nil
}
func (c *tempStatementProbeConn) Begin() (driver.Tx, error) {
	return nil, errors.New("statement lifetime fixture does not use transactions")
}

type tempStatementProbeStmt struct {
	raw  driver.Stmt
	conn *tempStatementProbeConn
	id   int
}

func (s *tempStatementProbeStmt) Close() error {
	s.conn.driver.record(fmt.Sprintf("stmt-close:%d", s.id))
	if s.raw != nil {
		return s.raw.Close()
	}
	return nil
}
func (s *tempStatementProbeStmt) NumInput() int {
	if s.raw != nil {
		return s.raw.NumInput()
	}
	return 0
}
func (s *tempStatementProbeStmt) Exec(args []driver.Value) (driver.Result, error) {
	if s.raw != nil {
		return s.raw.Exec(args)
	}
	return nil, errors.New("fake statement is never executed")
}
func (s *tempStatementProbeStmt) Query(args []driver.Value) (driver.Rows, error) {
	if s.raw != nil {
		return s.raw.Query(args)
	}
	return nil, errors.New("fake statement is never queried")
}
