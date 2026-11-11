//go:build windows && nativeprobe_sqlite_identity

package windowsprobe

import (
	"context"
	"crypto/rand"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"time"
	"unsafe"

	"modernc.org/libc"
	sqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

const tempOpenFlags = sqlite3.SQLITE_OPEN_TEMP_DB | sqlite3.SQLITE_OPEN_READWRITE | sqlite3.SQLITE_OPEN_CREATE | sqlite3.SQLITE_OPEN_EXCLUSIVE | sqlite3.SQLITE_OPEN_DELETEONCLOSE

var ErrExperimentalTempBusy = errors.New("experimental TEMP VFS still owns a connection")

type tempVFSFile struct {
	Base  sqlite3.Tsqlite3_file
	Token uintptr
}
type ExperimentalTempStats struct {
	Attempts, TempAttempts, Opened, WrittenBytes, ReadBytes uint64
	LastFlags                                               int32
	NamedAttempts                                           uint64
	ClosedSuccessfully, LiveFiles, InflightCallbacks        uint64
	BackendReadCalls, BackendWriteCalls                     uint64
}
type ExperimentalTempVFS struct {
	mu                sync.Mutex
	vfs, cname, token uintptr
	name              string
	active            int
	physical          int
	closeFailure      error
	closing           bool
	stats             ExperimentalTempStats
	scratch           *scratchStore
	ioMethods         uintptr
	ioCloseErr        error
}

var tempOwners = struct {
	sync.Mutex
	next uintptr
	m    map[uintptr]*ExperimentalTempVFS
}{m: make(map[uintptr]*ExperimentalTempVFS)}

// Slots encode the translated modernc Go ABI, not native Windows callbacks.
// Only the top-level, non-capturing functions listed below may be encoded.
// No closures/method values: a numeric slot is not a GC root for their environment.
// These static function values use the fixed generated ABI for this build.
func tempSlot[T any](f T) uintptr { return *(*uintptr)(unsafe.Pointer(&struct{ F T }{f})) }

var tempSlots = struct{ open, del, access, full, random, sleep, clock, dlOpen, dlSym, dlError, dlClose uintptr }{
	tempSlot(tempOpen), tempSlot(tempDelete), tempSlot(tempAccess), tempSlot(tempFullPath),
	tempSlot(tempRandom), tempSlot(tempSleep), tempSlot(tempClock),
	tempSlot(tempDLOpen), tempSlot(tempDLSym), tempSlot(tempDLError), tempSlot(tempDLClose),
}

func NewExperimentalTempVFS() (*ExperimentalTempVFS, error) {
	tls := libc.NewTLS()
	defer tls.Close()
	o := &ExperimentalTempVFS{}
	tempOwners.Lock()
	if tempOwners.next == ^uintptr(0) {
		tempOwners.Unlock()
		return nil, errors.New("TEMP token exhaustion")
	}
	tempOwners.next++
	o.token = tempOwners.next
	o.name = fmt.Sprintf("agent-temp-refusal-%x", o.token)
	tempOwners.Unlock()
	var err error
	o.cname, err = libc.CString(o.name)
	if err != nil {
		return nil, err
	}
	o.vfs = libc.Xcalloc(tls, 1, uint64(unsafe.Sizeof(sqlite3.Tsqlite3_vfs{})))
	if o.vfs == 0 {
		libc.Xfree(tls, o.cname)
		return nil, errors.New("allocate TEMP VFS")
	}
	*(*sqlite3.Tsqlite3_vfs)(unsafe.Pointer(o.vfs)) = sqlite3.Tsqlite3_vfs{
		FiVersion: 1, FszOsFile: int32(unsafe.Sizeof(tempVFSFile{})), FmxPathname: 512, FzName: o.cname, FpAppData: o.token,
		FxOpen: tempSlots.open, FxDelete: tempSlots.del, FxAccess: tempSlots.access, FxFullPathname: tempSlots.full,
		FxDlOpen: tempSlots.dlOpen, FxDlSym: tempSlots.dlSym, FxDlError: tempSlots.dlError, FxDlClose: tempSlots.dlClose,
		FxRandomness: tempSlots.random, FxSleep: tempSlots.sleep, FxCurrentTime: tempSlots.clock,
	}
	tempOwners.Lock()
	tempOwners.m[o.token] = o
	tempOwners.Unlock()
	if rc := sqlite3.Xsqlite3_vfs_register(tls, o.vfs, 0); rc != sqlite3.SQLITE_OK {
		tempOwners.Lock()
		delete(tempOwners.m, o.token)
		tempOwners.Unlock()
		libc.Xfree(tls, o.vfs)
		libc.Xfree(tls, o.cname)
		return nil, fmt.Errorf("register TEMP VFS: %d", rc)
	}
	return o, nil
}
func (o *ExperimentalTempVFS) Stats() ExperimentalTempStats {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.stats
}

// The callback must close all Rows and finish transactions before returning.
// Idle statements are drained before physical connection Close; late Stmt.Close
// never touches released TLS. No bare VFS name or caller DSN is exposed.
func (o *ExperimentalTempVFS) WithConnection(ctx context.Context, fn func(*sql.Conn) error) error {
	return o.withDriver(ctx, fn, &sqlite.Driver{})
}
func (o *ExperimentalTempVFS) withDriver(ctx context.Context, fn func(*sql.Conn) error, base driver.Driver) (result error) {
	o.mu.Lock()
	if o.closing || o.vfs == 0 {
		o.mu.Unlock()
		return errors.New("TEMP VFS closing")
	}
	o.active++
	name := o.name
	o.mu.Unlock()
	u := url.URL{Scheme: "file", Opaque: "agent-temp-memory"}
	q := url.Values{"mode": {"memory"}, "cache": {"private"}, "vfs": {name}}
	u.RawQuery = q.Encode()
	db := sql.OpenDB(&tempConnector{owner: o, dsn: u.String(), base: base})
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(0)
	var connectionCloseErr error
	defer func() {
		e := db.Close()
		o.mu.Lock()
		physicalError := o.closeFailure
		o.mu.Unlock()
		result = errors.Join(result, e, physicalError)
		// An uncertain physical close must retain the VFS allocation, not free it.
		if e == nil && connectionCloseErr == nil {
			o.mu.Lock()
			o.active--
			o.mu.Unlock()
		}
	}()
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { connectionCloseErr = conn.Close(); result = errors.Join(result, connectionCloseErr) }()
	if err = conn.PingContext(ctx); err != nil {
		return err
	}
	return fn(conn)
}
func (o *ExperimentalTempVFS) Close() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.closing = true
	if o.active != 0 || o.physical != 0 {
		return errors.Join(ErrExperimentalTempBusy, o.closeFailure)
	}
	if o.vfs == 0 {
		return o.ioCloseErr
	}
	tls := libc.NewTLS()
	defer tls.Close()
	if rc := sqlite3.Xsqlite3_vfs_unregister(tls, o.vfs); rc != sqlite3.SQLITE_OK {
		return fmt.Errorf("unregister TEMP VFS: %d", rc)
	}
	tempOwners.Lock()
	delete(tempOwners.m, o.token)
	tempOwners.Unlock()
	// Refusal stage: no file/callback can acquire scratch. No scratch IO or
	// file lock exists to wait on. Real bridge integration must add callback/file
	// drain and release the owner lock before draining the scratch store.
	var scratchErr error
	if o.scratch != nil {
		scratchErr = o.scratch.Close()
		o.scratch = nil
	}
	libc.Xfree(tls, o.ioMethods)
	o.ioMethods = 0
	libc.Xfree(tls, o.vfs)
	libc.Xfree(tls, o.cname)
	o.vfs = 0
	o.cname = 0
	o.ioCloseErr = errors.Join(o.ioCloseErr, scratchErr)
	return o.ioCloseErr
}
func tempOpen(tls *libc.TLS, vfs, name, file uintptr, flags int32, out uintptr) int32 {
	if file != 0 {
		*(*tempVFSFile)(unsafe.Pointer(file)) = tempVFSFile{}
	}
	if out != 0 {
		*(*int32)(unsafe.Pointer(out)) = 0
	}
	if vfs == 0 || file == 0 {
		return sqlite3.SQLITE_CANTOPEN
	}
	token := (*sqlite3.Tsqlite3_vfs)(unsafe.Pointer(vfs)).FpAppData
	tempOwners.Lock()
	o := tempOwners.m[token]
	tempOwners.Unlock()
	if o == nil {
		return sqlite3.SQLITE_CANTOPEN
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.stats.Attempts++
	o.stats.LastFlags = flags
	if name != 0 {
		o.stats.NamedAttempts++
	}
	if name == 0 && flags == tempOpenFlags {
		o.stats.TempAttempts++
	}
	// Stage-one refusal: no filesystem API, no token, no methods published.
	return sqlite3.SQLITE_CANTOPEN
}
func tempDelete(*libc.TLS, uintptr, uintptr, int32) int32 { return sqlite3.SQLITE_IOERR_DELETE }
func tempAccess(_ *libc.TLS, _ uintptr, _ uintptr, _ int32, out uintptr) int32 {
	if out != 0 {
		*(*int32)(unsafe.Pointer(out)) = 0
	}
	return sqlite3.SQLITE_IOERR_ACCESS
}
func tempFullPath(_ *libc.TLS, _ uintptr, _ uintptr, n int32, out uintptr) int32 {
	if n > 0 && out != 0 {
		*(*byte)(unsafe.Pointer(out)) = 0
	}
	return sqlite3.SQLITE_CANTOPEN
}
func tempRandom(_ *libc.TLS, _ uintptr, n int32, out uintptr) int32 {
	if n <= 0 || n > 1<<20 || out == 0 {
		return 0
	}
	b := unsafe.Slice((*byte)(unsafe.Pointer(out)), int(n))
	if _, err := rand.Read(b); err != nil {
		clear(b)
		return 0
	}
	return n
}
func tempSleep(_ *libc.TLS, _ uintptr, micros int32) int32 {
	if micros <= 0 {
		return 0
	}
	time.Sleep(time.Duration(micros) * time.Microsecond)
	return micros
}
func tempClock(_ *libc.TLS, _ uintptr, out uintptr) int32 {
	if out == 0 {
		return sqlite3.SQLITE_ERROR
	}
	*(*float64)(unsafe.Pointer(out)) = 2440587.5 + float64(time.Now().UnixMilli())/86400000
	return sqlite3.SQLITE_OK
}

// Dynamic extensions are unsupported. Never inspect names or operate on handles.
const tempDLDisabled = "dynamic extensions disabled"

func tempDLOpen(*libc.TLS, uintptr, uintptr) uintptr         { return 0 }
func tempDLSym(*libc.TLS, uintptr, uintptr, uintptr) uintptr { return 0 }
func tempDLClose(*libc.TLS, uintptr, uintptr)                {}
func tempDLError(_ *libc.TLS, _ uintptr, n int32, out uintptr) {
	if n <= 0 || out == 0 {
		return
	}
	b := unsafe.Slice((*byte)(unsafe.Pointer(out)), int(n))
	clear(b)
	copy(b[:len(b)-1], tempDLDisabled)
}

// Intentionally implements only the mandatory driver.Conn surface. database/sql
// uses its documented Prepare/Stmt fallbacks; no optional interface is falsely
// advertised. The qualification workload uses default transaction options only.
type tempConnector struct {
	owner *ExperimentalTempVFS
	dsn   string
	base  driver.Driver
}

func (c *tempConnector) Driver() driver.Driver { return c.base }
func (c *tempConnector) Connect(ctx context.Context) (driver.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	raw, err := c.base.Open(c.dsn)
	if err != nil {
		return nil, err
	}
	c.owner.mu.Lock()
	c.owner.physical++
	c.owner.mu.Unlock()
	return &tempOwnedConn{Conn: raw, owner: c.owner}, nil
}

// Conn-bound statements are not in database/sql's driverConn.openStmt map.
// Track every Prepare result ourselves. This owner is for the fixed modernc
// driver (plus test probes), not a generic contract for arbitrary drivers.
type tempOwnedConn struct {
	driver.Conn
	owner                  *ExperimentalTempVFS
	mu                     sync.Mutex
	closed                 bool
	closeDone              chan struct{}
	closeErr, statementErr error
	statements             map[*tempOwnedStmt]struct{}
}

var errTempStatementClosed = errors.New("TEMP statement or connection closed")

func (c *tempOwnedConn) Prepare(query string) (driver.Stmt, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, errTempStatementClosed
	}
	raw, err := c.Conn.Prepare(query)
	if err != nil {
		return nil, err
	}
	s := &tempOwnedStmt{raw: raw, conn: c}
	if c.statements == nil {
		c.statements = make(map[*tempOwnedStmt]struct{})
	}
	c.statements[s] = struct{}{}
	// Preserve exactly the underlying context interface set. In its absence,
	// database/sql still performs its own named-value/context fallback.
	_, exec := raw.(driver.StmtExecContext)
	_, queryContext := raw.(driver.StmtQueryContext)
	switch {
	case exec && queryContext:
		return &tempOwnedStmtBoth{s}, nil
	case exec:
		return &tempOwnedStmtExec{s}, nil
	case queryContext:
		return &tempOwnedStmtQuery{s}, nil
	default:
		return s, nil
	}
}
func (c *tempOwnedConn) Close() error {
	c.mu.Lock()
	if c.closed {
		done := c.closeDone
		c.mu.Unlock()
		<-done
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.closeErr
	}
	c.closed = true
	c.closeDone = make(chan struct{})
	statements := make([]*tempOwnedStmt, 0, len(c.statements))
	for s := range c.statements {
		statements = append(statements, s)
	}
	c.mu.Unlock()
	// Never hold conn.mu while waiting for stmt.mu: stmt.Close removes itself
	// under conn.mu only after finalization. Concurrent Close waits for done.
	for _, s := range statements {
		_ = s.Close()
	}
	physicalErr := c.Conn.Close()
	c.mu.Lock()
	c.closeErr = errors.Join(c.statementErr, physicalErr)
	result := c.closeErr
	c.mu.Unlock()
	c.owner.mu.Lock()
	if physicalErr == nil {
		c.owner.physical--
	}
	// database/sql may swallow driver Close's return. Preserve diagnostics for
	// WithConnection, but only failed physical Close keeps the physical lease.
	c.owner.closeFailure = errors.Join(c.owner.closeFailure, result)
	c.owner.mu.Unlock()
	close(c.closeDone)
	return result
}

type tempOwnedStmt struct {
	mu       sync.Mutex
	raw      driver.Stmt
	conn     *tempOwnedConn
	closed   bool
	closeErr error
}

func (s *tempOwnedStmt) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return s.closeErr
	}
	s.closed = true
	s.closeErr = s.raw.Close()
	// Fixed modernc stmt.Close finalizes and clears pstmt/frees psql even when
	// sqlite3_finalize returns the previous step's SQL error. It is destroyed,
	// not a reason to keep a zombie statement or retry against a freed TLS.
	s.raw = nil
	s.conn.mu.Lock()
	delete(s.conn.statements, s)
	s.conn.statementErr = errors.Join(s.conn.statementErr, s.closeErr)
	s.conn.mu.Unlock()
	return s.closeErr
}
func (s *tempOwnedStmt) NumInput() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return -1
	}
	return s.raw.NumInput()
}
func (s *tempOwnedStmt) Exec(args []driver.Value) (driver.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errTempStatementClosed
	}
	return s.raw.Exec(args)
}
func (s *tempOwnedStmt) Query(args []driver.Value) (driver.Rows, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errTempStatementClosed
	}
	return s.raw.Query(args)
}
func (s *tempOwnedStmt) execContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errTempStatementClosed
	}
	return s.raw.(driver.StmtExecContext).ExecContext(ctx, args)
}
func (s *tempOwnedStmt) queryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errTempStatementClosed
	}
	return s.raw.(driver.StmtQueryContext).QueryContext(ctx, args)
}

type tempOwnedStmtExec struct{ *tempOwnedStmt }
type tempOwnedStmtQuery struct{ *tempOwnedStmt }
type tempOwnedStmtBoth struct{ *tempOwnedStmt }

func (s *tempOwnedStmtExec) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	return s.execContext(ctx, args)
}
func (s *tempOwnedStmtQuery) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	return s.queryContext(ctx, args)
}
func (s *tempOwnedStmtBoth) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	return s.execContext(ctx, args)
}
func (s *tempOwnedStmtBoth) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	return s.queryContext(ctx, args)
}
