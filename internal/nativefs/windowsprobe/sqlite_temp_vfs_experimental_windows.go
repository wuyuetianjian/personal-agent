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

// The callback must close all Rows/Stmt before returning; connection and pool
// stay owned until then. No bare VFS name or caller-supplied DSN is exposed.
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
		return nil
	}
	tls := libc.NewTLS()
	defer tls.Close()
	if rc := sqlite3.Xsqlite3_vfs_unregister(tls, o.vfs); rc != sqlite3.SQLITE_OK {
		return fmt.Errorf("unregister TEMP VFS: %d", rc)
	}
	tempOwners.Lock()
	delete(tempOwners.m, o.token)
	tempOwners.Unlock()
	libc.Xfree(tls, o.vfs)
	libc.Xfree(tls, o.cname)
	o.vfs = 0
	o.cname = 0
	return nil
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

type tempOwnedConn struct {
	driver.Conn
	owner    *ExperimentalTempVFS
	mu       sync.Mutex
	closed   bool
	closeErr error
}

func (c *tempOwnedConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return c.closeErr
	}
	c.closed = true
	c.closeErr = c.Conn.Close()
	c.owner.mu.Lock()
	defer c.owner.mu.Unlock()
	if c.closeErr == nil {
		c.owner.physical--
	} else {
		c.owner.closeFailure = errors.Join(c.owner.closeFailure, c.closeErr)
	}
	return c.closeErr
}
