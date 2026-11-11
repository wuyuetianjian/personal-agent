//go:build windows && nativeprobe_sqlite_identity

package windowsprobe

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"modernc.org/libc"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
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
		tempStatementLifetimeContract(t, &tempStatementProbeDriver{})
		protocolQualified = true
	})
	// A -run filter excluding the safety stage must not bypass it.
	if !passed || !protocolQualified {
		t.Fatal("statement protocol RED: idle Conn-bound statement was not drained; real SQLite stage not entered")
	}
	for _, id := range []int{1, 2} {
		if !t.Run(fmt.Sprintf("finalize_error_statement_%d", id), func(t *testing.T) {
			tempStatementLifetimeContract(t, &tempStatementProbeDriver{finalizeID: id, finalizeError: errors.New("injected prior SQL error from finalize")})
		}) {
			t.Fatal("finalize-error protocol failed; real SQLite stage not entered")
		}
	}
	if !t.Run("optional_context_interfaces", tempStatementContextContract) || !t.Run("concurrent_close_drain", tempStatementConcurrentCloseContract) {
		t.Fatal("statement ownership protocol failed; real SQLite stage not entered")
	}
	t.Run("real_sqlite_idle_statement", func(t *testing.T) {
		tempStatementLifetimeContract(t, &tempStatementProbeDriver{base: &sqlite.Driver{}})
	})
	t.Run("real_sqlite_prior_sql_error", func(t *testing.T) {
		tempStatementLifetimeContract(t, &tempStatementProbeDriver{base: &sqlite.Driver{}, priorSQLError: true})
	})
}

func tempStatementLifetimeContract(t *testing.T, probe *tempStatementProbeDriver) {
	t.Helper()
	base, finalizeError := probe.base, probe.finalizeError
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
	var idle *sql.Stmt
	var priorSQLFailure error
	err = o.withDriver(context.Background(), func(conn *sql.Conn) error {
		early, e := conn.PrepareContext(context.Background(), "SELECT 11")
		if e != nil {
			return e
		}
		if e = early.Close(); (probe.finalizeID != 1 && e != nil) || (probe.finalizeID == 1 && !errors.Is(e, finalizeError)) {
			return fmt.Errorf("early close: %v", e)
		}
		if e = early.Close(); e != nil {
			return e
		}
		query := "SELECT 29"
		if probe.priorSQLError {
			query = "SELECT abs(-9223372036854775808)"
		}
		idle, e = conn.PrepareContext(context.Background(), query)
		if e != nil {
			return e
		}
		if base != nil {
			var n int
			e = idle.QueryRowContext(context.Background()).Scan(&n)
			if probe.priorSQLError {
				if e == nil || !strings.Contains(e.Error(), "integer overflow") {
					return fmt.Errorf("expected real SQLite integer overflow, got %v", e)
				}
				priorSQLFailure = e
			} else if e != nil {
				return e
			} else if n != 29 {
				return fmt.Errorf("real prepared statement returned %d", n)
			}
		}
		// Deliberately leave an idle Conn-bound statement. No outstanding Rows,
		// transaction or Raw call may make connection release wait for us.
		return nil
	}, probe)
	// modernc resets after failed step; finalize may already have consumed the
	// original SQL result. Do not require it to manufacture a second SQL error.
	acceptablePrior := priorSQLFailure != nil && err != nil && err.Error() == priorSQLFailure.Error()
	if (finalizeError == nil && err != nil && !acceptablePrior) || (finalizeError != nil && !errors.Is(err, finalizeError)) {
		if base == nil && idle != nil {
			_ = idle.Close()
		}
		t.Fatalf("connection owner error=%v, want finalize error=%v", err, finalizeError)
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
	lateErr := idle.Close()
	wantLate := error(nil)
	if probe.finalizeID == 2 {
		wantLate = finalizeError
	}
	acceptablePrior = priorSQLFailure != nil && lateErr != nil && lateErr.Error() == priorSQLFailure.Error()
	if (wantLate == nil && lateErr != nil && !acceptablePrior) || (wantLate != nil && !errors.Is(lateErr, wantLate)) {
		t.Fatalf("late statement close=%v, want cached finalize error=%v", lateErr, wantLate)
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
	base                       driver.Driver
	finalizeError              error // fixed modernc semantics: error does not leave stmt alive
	finalizeID                 int
	priorSQLError              bool
	contextMask                int
	execCtx, queryCtx          context.Context
	execArgs, queryArgs        []driver.NamedValue
	closeEntered, closeRelease chan struct{}
	mu                         sync.Mutex
	log                        []string
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
	s := &tempStatementProbeStmt{raw: raw, conn: c, id: c.next}
	// The real observation shim preserves the real driver's optional surface.
	mask := c.driver.contextMask
	if raw != nil {
		mask = 0
		if _, ok := raw.(driver.StmtExecContext); ok {
			mask |= 1
		}
		if _, ok := raw.(driver.StmtQueryContext); ok {
			mask |= 2
		}
	}
	switch mask {
	case 1:
		return &tempStatementProbeExec{s}, nil
	case 2:
		return &tempStatementProbeQuery{s}, nil
	case 3:
		return &tempStatementProbeBoth{s}, nil
	default:
		return s, nil
	}
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
	if s.conn.driver.closeEntered != nil {
		s.conn.driver.closeEntered <- struct{}{}
		<-s.conn.driver.closeRelease
	}
	s.conn.driver.record(fmt.Sprintf("stmt-close:%d", s.id))
	if s.raw != nil {
		return s.raw.Close()
	}
	if s.id == s.conn.driver.finalizeID {
		return s.conn.driver.finalizeError
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

type tempStatementProbeExec struct{ *tempStatementProbeStmt }
type tempStatementProbeQuery struct{ *tempStatementProbeStmt }
type tempStatementProbeBoth struct{ *tempStatementProbeStmt }

func (s *tempStatementProbeStmt) execContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	if s.raw != nil {
		return s.raw.(driver.StmtExecContext).ExecContext(ctx, args)
	}
	s.conn.driver.execCtx, s.conn.driver.execArgs = ctx, append([]driver.NamedValue(nil), args...)
	return driver.RowsAffected(1), nil
}
func (s *tempStatementProbeStmt) queryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	if s.raw != nil {
		return s.raw.(driver.StmtQueryContext).QueryContext(ctx, args)
	}
	s.conn.driver.queryCtx, s.conn.driver.queryArgs = ctx, append([]driver.NamedValue(nil), args...)
	return &tempStatementProbeRows{}, nil
}
func (s *tempStatementProbeExec) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	return s.execContext(ctx, args)
}
func (s *tempStatementProbeQuery) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	return s.queryContext(ctx, args)
}
func (s *tempStatementProbeBoth) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	return s.execContext(ctx, args)
}
func (s *tempStatementProbeBoth) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	return s.queryContext(ctx, args)
}

type tempStatementProbeRows struct{}

func (*tempStatementProbeRows) Columns() []string         { return []string{"value"} }
func (*tempStatementProbeRows) Close() error              { return nil }
func (*tempStatementProbeRows) Next([]driver.Value) error { return io.EOF }

func tempStatementContextContract(t *testing.T) {
	for mask := 0; mask < 4; mask++ {
		t.Run(fmt.Sprintf("mask_%d", mask), func(t *testing.T) {
			o := &ExperimentalTempVFS{}
			probe := &tempStatementProbeDriver{contextMask: mask}
			c, err := (&tempConnector{owner: o, base: probe}).Connect(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if e := c.Close(); e != nil {
					t.Error(e)
				}
			}()
			s, err := c.Prepare("SELECT :value")
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if e := s.Close(); e != nil {
					t.Error(e)
				}
			}()
			exec, hasExec := s.(driver.StmtExecContext)
			query, hasQuery := s.(driver.StmtQueryContext)
			if hasExec != (mask&1 != 0) || hasQuery != (mask&2 != 0) {
				t.Fatalf("optional interface set changed: mask=%d exec=%v query=%v", mask, hasExec, hasQuery)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			args := []driver.NamedValue{{Name: "value", Ordinal: 1, Value: int64(17)}}
			if hasExec {
				if _, e := exec.ExecContext(ctx, args); e != nil {
					t.Fatal(e)
				}
				if probe.execCtx != ctx || !reflect.DeepEqual(probe.execArgs, args) {
					t.Fatal("ExecContext lost context or named values")
				}
			}
			if hasQuery {
				rows, e := query.QueryContext(ctx, args)
				if e != nil {
					t.Fatal(e)
				}
				if e = rows.Close(); e != nil {
					t.Fatal(e)
				}
				if probe.queryCtx != ctx || !reflect.DeepEqual(probe.queryArgs, args) {
					t.Fatal("QueryContext lost context or named values")
				}
			}
		})
	}
}

func tempStatementConcurrentCloseContract(t *testing.T) {
	o := &ExperimentalTempVFS{}
	probe := &tempStatementProbeDriver{closeEntered: make(chan struct{}, 1), closeRelease: make(chan struct{})}
	var release sync.Once
	unblock := func() { release.Do(func() { close(probe.closeRelease) }) }
	defer unblock()
	c, err := (&tempConnector{owner: o, base: probe}).Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	s, err := c.Prepare("SELECT 1")
	if err != nil {
		_ = c.Close()
		t.Fatal(err)
	}
	stmtDone := make(chan error, 1)
	go func() { stmtDone <- s.Close() }()
	select {
	case <-probe.closeEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("statement close did not reach barrier")
	}
	connDone := make(chan error, 2)
	go func() { connDone <- c.Close() }()
	go func() { connDone <- c.Close() }()
	select {
	case e := <-connDone:
		t.Fatalf("connection close returned before statement drain: %v", e)
	case <-time.After(25 * time.Millisecond):
	}
	unblock()
	for i := 0; i < 3; i++ {
		var done <-chan error = connDone
		if i == 0 {
			done = stmtDone
		}
		select {
		case e := <-done:
			if e != nil {
				t.Fatal(e)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("statement/connection close lock inversion")
		}
	}
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	if e := c.Close(); e != nil {
		t.Fatal(e)
	}
	if _, e := c.Prepare("SELECT 2"); e == nil {
		t.Fatal("closed physical connection accepted Prepare")
	}
	want := []string{"prepare:1", "stmt-close:1", "conn-close"}
	if got := probe.events(); !reflect.DeepEqual(got, want) {
		t.Fatalf("concurrent drain repeated/misordered native close: %v", got)
	}
	o.mu.Lock()
	physical := o.physical
	o.mu.Unlock()
	if physical != 0 {
		t.Fatalf("physical lease remains: %d", physical)
	}
}
