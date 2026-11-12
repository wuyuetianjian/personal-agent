//go:build windows && nativeprobe_sqlite_identity && nativeprobe_sqlite_lease && nativeprobe_sqlite_permit && nativeprobe_sqlite_persistent && nativeprobe_sqlite_main_read && nativeprobe_sqlite_existing_wal

package windowsprobe

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"modernc.org/libc"
	_ "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
	"unsafe"
)

const existingWALChildEnv = "AGENT_PRIVATE_EXISTING_WAL_FIXTURE"

func existingWALURI(path, mode, vfs string) string {
	u := url.URL{Scheme: "file", Path: "/" + filepath.ToSlash(path)}
	q := url.Values{"mode": {mode}}
	if vfs != "" {
		q.Set("vfs", vfs)
	}
	u.RawQuery = q.Encode()
	return u.String()
}
func existingWALChild(t *testing.T) bool {
	p := os.Getenv(existingWALChildEnv)
	if p == "" {
		return false
	}
	db, e := sql.Open("sqlite", p)
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
		t.Fatalf("child WAL %s %v", mode, e)
	}
	for _, q := range []string{"PRAGMA wal_autocheckpoint=0", "CREATE TABLE fixture(value TEXT)"} {
		if _, e = db.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	var busy, logFrames, checkpointed int
	if e = db.QueryRow("PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &logFrames, &checkpointed); e != nil || busy != 0 || logFrames != 0 || checkpointed != 0 {
		t.Fatalf("baseline checkpoint %d/%d/%d %v", busy, logFrames, checkpointed, e)
	}
	if _, e = db.Exec("INSERT INTO fixture VALUES('committed-wal-only')"); e != nil {
		t.Fatal(e)
	}
	if _, e = fmt.Println("EXISTING_WAL_READY"); e != nil {
		t.Fatal(e)
	}
	scan := bufio.NewScanner(os.Stdin)
	if !scan.Scan() || scan.Text() != "release" {
		t.Fatalf("child release %v", scan.Err())
	}
	return true
}
func existingWALFixture(t *testing.T) string {
	t.Helper()
	p := filepath.Join(localRootPath(t), "existing.db")
	cmd := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$", "-test.timeout=60s")
	cmd.Env = append(os.Environ(), existingWALChildEnv+"="+p)
	cmd.Stderr = os.Stderr
	stdout, e := cmd.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	stdin, e := cmd.StdinPipe()
	if e != nil {
		e = errors.Join(e, stdout.Close())
		t.Fatal(e)
	}
	if e = cmd.Start(); e != nil {
		e = errors.Join(e, stdin.Close(), stdout.Close())
		t.Fatal(e)
	}
	ready := make(chan string, 1)
	drained := make(chan error, 1)
	done := make(chan error, 1)
	go func() {
		s := bufio.NewScanner(stdout)
		if s.Scan() {
			ready <- s.Text()
		} else {
			ready <- "EOF"
		}
		for s.Scan() {
		}
		drained <- s.Err()
	}()
	// Wait begins only after the pipe reader drains, avoiding Wait closing its pipe.
	go func() {
		e := <-drained
		if e != nil {
			e = errors.Join(e, cmd.Process.Kill())
		}
		w := cmd.Wait()
		if e != nil {
			done <- e
		} else {
			done <- w
		}
	}()
	t.Cleanup(func() {
		if _, e := fmt.Fprintln(stdin, "release"); e != nil {
			t.Error(e)
		}
		if e := stdin.Close(); e != nil {
			t.Error(e)
		}
		select {
		case e := <-done:
			if e != nil {
				t.Error(e)
			}
		case <-time.After(10 * time.Second):
			if e := cmd.Process.Kill(); e != nil {
				t.Error(e)
			}
			select {
			case e := <-done:
				t.Errorf("fixture forced shutdown: %v", e)
			case <-time.After(5 * time.Second):
				t.Error("fixture shutdown unconfirmed")
			}
		}
	})
	select {
	case line := <-ready:
		if line != "EXISTING_WAL_READY" {
			t.Fatalf("child setup %q", line)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("child setup timeout")
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		s, e := os.Stat(p + suffix)
		if e != nil || s.Size() == 0 {
			t.Fatalf("stock fixture %s: %v", suffix, e)
		}
	}
	// immutable explicitly reads the main file alone, only as a fixture control.
	uri := existingWALURI(p, "ro", "") + "&immutable=1"
	db, e := sql.Open("sqlite", uri)
	if e != nil {
		t.Fatal(e)
	}
	var count int
	e = db.QueryRow("SELECT count(*) FROM fixture WHERE value='committed-wal-only'").Scan(&count)
	ce := db.Close()
	if e != nil || ce != nil || count != 0 {
		t.Fatalf("WAL-only control count=%d err=%v close=%v", count, e, ce)
	}
	return p
}
func existingWALStockRead(t *testing.T, p string) {
	t.Helper()
	db, e := sql.Open("sqlite", existingWALURI(p, "ro", ""))
	if e != nil {
		t.Fatal(e)
	}
	var value string
	e = db.QueryRow("SELECT value FROM fixture").Scan(&value)
	ce := db.Close()
	if e != nil || ce != nil || value != "committed-wal-only" {
		t.Fatalf("stock positive %q %v %v", value, e, ce)
	}
}

// This NEW adapter uses permits; the older main-only adapter is unchanged.
type existingWALAuthority struct {
	c    *persistentPermitConnection
	root *Root
}
type existingWALLease struct {
	a       *existingWALAuthority
	r       *persistentPermitResource
	purpose persistentLeasePurpose
}

func (a *existingWALAuthority) Revoke(e error) { a.c.owner.poison(e) }
func (a *existingWALAuthority) open(purpose persistentLeasePurpose) (sqlite3.ExperimentalMainReadLease, error) {
	p, e := a.c.begin(context.Background())
	if e != nil {
		return nil, e
	}
	var r *persistentPermitResource
	if purpose == leaseMain {
		r, e = p.acquire(context.Background(), purpose)
	} else {
		r, e = p.acquireReadOnlyWAL(context.Background())
	}
	e = errors.Join(e, p.release())
	if e != nil {
		if r != nil {
			e = errors.Join(e, r.close())
		}
		return nil, e
	}
	if r == nil || r.purpose != purpose || r.connection != a.c {
		if r != nil {
			return nil, errors.Join(errPersistentPermitForeign, r.close())
		}
		return nil, errPersistentPermitForeign
	}
	return &existingWALLease{a, r, purpose}, nil
}
func (a *existingWALAuthority) OpenMain() (sqlite3.ExperimentalMainReadLease, error) {
	return a.open(leaseMain)
}
func (a *existingWALAuthority) OpenWALReadOnly() (sqlite3.ExperimentalMainReadLease, error) {
	return a.open(leaseWALReadOnly)
}
func (l *existingWALLease) Borrow(fn func(uintptr) int32) (rc int32, err error) {
	rc = sqlite3.SQLITE_IOERR
	if l.r == nil || l.r.owner != l.a.c.owner || l.r.connection != l.a.c || l.r.purpose != l.purpose || (l.purpose != leaseMain && l.purpose != leaseWALReadOnly) {
		return rc, errPersistentPermitForeign
	}
	p, e := l.a.c.begin(context.Background())
	if e != nil {
		return rc, e
	}
	defer func() { err = errors.Join(err, p.release()) }()
	err = p.borrow(context.Background(), l.r, func(h windows.Handle) error { rc = fn(uintptr(h)); return nil })
	return
}
func (l *existingWALLease) Close() error { return l.r.close() }
func existingWALSetup(t *testing.T) (string, *existingWALAuthority, uintptr) {
	t.Helper()
	path := existingWALFixture(t)
	existingWALStockRead(t, path)
	r, e := OpenLocalRoot(filepath.Dir(path))
	if e != nil {
		t.Fatal(e)
	}
	o, e := newPersistentWALReadOnlyOwner(r, filepath.Base(path), persistentPermitHooks{})
	if e != nil {
		e = errors.Join(e, r.Close())
		t.Fatalf("existing authority setup: %v", e)
	}
	permitShutdowns.Store(o, &permitShutdown{done: make(chan struct{})})
	t.Cleanup(func() {
		if e := permitOwnerClose(o); e != nil {
			t.Error(e)
			return
		}
		if e := r.Close(); e != nil {
			t.Error(e)
		}
	})
	c, e := o.newConnection(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	a := &existingWALAuthority{c: c, root: r}
	v, e := sqlite3.ExperimentalExistingWALRegisterRO(a, path)
	if e != nil {
		t.Fatal(e)
	}
	// Qualification independently proves genuine WAL bytes; private actual reads
	// below must be produced only by the private file methods.
	bytes, e := os.ReadFile(path + "-wal")
	if e != nil || len(bytes) < 32 {
		t.Fatalf("WAL header setup %v", e)
	}
	magic := binary.BigEndian.Uint32(bytes[:4])
	if magic != 0x377f0682 && magic != 0x377f0683 {
		t.Fatalf("WAL magic %#x", magic)
	}
	if binary.BigEndian.Uint32(bytes[4:8]) != 3007000 {
		t.Fatal("WAL version")
	}
	t.Log("held stock WAL, committed row, main-only baseline and native header qualified")
	return path, a, v
}

type existingWALFile struct {
	tls        *libc.TLS
	file, name uintptr
	closed     bool
}

func existingWALOpen(t *testing.T, v uintptr, name string, flags int32, required bool) *existingWALFile {
	t.Helper()
	tls := libc.NewTLS()
	n, e := libc.CString(name)
	if e != nil {
		tls.Close()
		t.Fatal(e)
	}
	vf := (*sqlite3.Tsqlite3_vfs)(unsafe.Pointer(v))
	file := libc.Xcalloc(tls, 1, uint64(vf.FszOsFile))
	if file == 0 {
		libc.Xfree(tls, n)
		tls.Close()
		t.Fatal("file allocation")
	}
	f := &existingWALFile{tls: tls, file: file, name: n}
	t.Cleanup(func() { f.dispose(t) })
	out := int32(-1)
	before := sqlite3.ExperimentalExistingWALPendingHits(v)
	fn := *(*func(*libc.TLS, uintptr, uintptr, uintptr, int32, uintptr) int32)(unsafe.Pointer(&vf.FxOpen))
	rc := fn(tls, v, n, file, flags, uintptr(unsafe.Pointer(&out)))
	if required && (rc != sqlite3.SQLITE_OK || out != sqlite3.SQLITE_OPEN_READONLY) {
		if sqlite3.ExperimentalExistingWALPendingHits(v) == before+1 && rc == sqlite3.SQLITE_CANTOPEN {
			t.Fatalf("real private FxOpen WAL rejected qualified fixture input=%#x rc=%d", flags, rc)
		}
		t.Fatalf("private open %d out=%#x", rc, out)
	}
	if !required && (rc != sqlite3.SQLITE_CANTOPEN || out != 0 || f.methods() != nil) {
		t.Fatalf("unexpected accepted open %d/%d", rc, out)
	}
	return f
}
func (f *existingWALFile) methods() *sqlite3.Tsqlite3_io_methods {
	m := (*sqlite3.Tsqlite3_file)(unsafe.Pointer(f.file)).FpMethods
	if m == 0 {
		return nil
	}
	return (*sqlite3.Tsqlite3_io_methods)(unsafe.Pointer(m))
}
func (f *existingWALFile) close(t *testing.T) {
	t.Helper()
	if f.closed {
		return
	}
	f.closed = true
	m := f.methods()
	if m == nil {
		return
	}
	if m.FxClose == 0 {
		t.Error("live file missing close; allocation retained")
		return
	}
	fn := *(*func(*libc.TLS, uintptr) int32)(unsafe.Pointer(&m.FxClose))
	if rc := fn(f.tls, f.file); rc != sqlite3.SQLITE_OK {
		t.Errorf("private close %d; allocation retained", rc)
		return
	}
	(*sqlite3.Tsqlite3_file)(unsafe.Pointer(f.file)).FpMethods = 0
}
func (f *existingWALFile) dispose(t *testing.T) {
	f.close(t)
	if f.methods() != nil {
		return
	}
	libc.Xfree(f.tls, f.file)
	libc.Xfree(f.tls, f.name)
	f.tls.Close()
}
func (f *existingWALFile) read(t *testing.T, offset int64, n int, expectedRC int32) []byte {
	t.Helper()
	m := f.methods()
	if m == nil || m.FxRead == 0 {
		t.Fatal("no read method")
	}
	b := libc.Xmalloc(f.tls, uint64(n))
	if b == 0 {
		t.Fatal("buffer allocation")
	}
	defer libc.Xfree(f.tls, b)
	for i := range unsafe.Slice((*byte)(unsafe.Pointer(b)), n) {
		unsafe.Slice((*byte)(unsafe.Pointer(b)), n)[i] = 0xee
	}
	fn := *(*func(*libc.TLS, uintptr, uintptr, int32, int64) int32)(unsafe.Pointer(&m.FxRead))
	rc := fn(f.tls, f.file, b, int32(n), offset)
	if rc != expectedRC {
		t.Fatalf("read rc=%d want=%d", rc, expectedRC)
	}
	return append([]byte{}, unsafe.Slice((*byte)(unsafe.Pointer(b)), n)...)
}

const existingWALFlags = int32(sqlite3.SQLITE_OPEN_READWRITE | sqlite3.SQLITE_OPEN_CREATE | sqlite3.SQLITE_OPEN_WAL)

func TestExistingWALPrivateHeaderAndSize(t *testing.T) {
	if existingWALChild(t) {
		return
	}
	name, _, v := existingWALSetup(t)
	expected, e := os.ReadFile(name + "-wal")
	if e != nil {
		t.Fatal(e)
	}
	f := existingWALOpen(t, v, name+"-wal", existingWALFlags, true)
	if got := f.read(t, 0, 32, sqlite3.SQLITE_OK); !bytes.Equal(got, expected[:32]) {
		t.Fatal("private header mismatch")
	}
	m := f.methods()
	if m.FxFileSize == 0 {
		t.Fatal("missing file size method")
	}
	var size int64
	fn := *(*func(*libc.TLS, uintptr, uintptr) int32)(unsafe.Pointer(&m.FxFileSize))
	if rc := fn(f.tls, f.file, uintptr(unsafe.Pointer(&size))); rc != sqlite3.SQLITE_OK || size != int64(len(expected)) {
		t.Fatalf("size %d/%d", size, rc)
	}
	got := f.read(t, size-4, 12, sqlite3.SQLITE_IOERR_SHORT_READ)
	if !bytes.Equal(got[:4], expected[len(expected)-4:]) || !bytes.Equal(got[4:], make([]byte, 8)) {
		t.Fatal("short-read not zero filled")
	}
	existingWALStockRead(t, name)
}
func TestExistingWALRoleAndFlagsRefused(t *testing.T) {
	if existingWALChild(t) {
		return
	}
	name, a, v := existingWALSetup(t)
	f := existingWALOpen(t, v, name+"-wal", existingWALFlags, true)
	for _, flags := range []int32{sqlite3.SQLITE_OPEN_READONLY | sqlite3.SQLITE_OPEN_WAL, existingWALFlags | sqlite3.SQLITE_OPEN_DELETEONCLOSE, sqlite3.SQLITE_OPEN_READWRITE | sqlite3.SQLITE_OPEN_MAIN_DB} {
		existingWALOpen(t, v, name+"-wal", flags, false)
	}
	existingWALOpen(t, v, name, existingWALFlags, false)
	existingWALOpen(t, v, name+"-other", existingWALFlags, false)
	m := f.methods()
	if m.FxLock == 0 || m.FxWrite == 0 || m.FxTruncate == 0 || m.FxSync == 0 {
		t.Fatal("missing WAL refusal methods")
	}
	lock := *(*func(*libc.TLS, uintptr, int32) int32)(unsafe.Pointer(&m.FxLock))
	if lock(f.tls, f.file, sqlite3.SQLITE_LOCK_SHARED) == sqlite3.SQLITE_OK {
		t.Fatal("WAL accepted main byte-lock")
	}

	before := f.read(t, 0, 32, sqlite3.SQLITE_OK)
	bad, e := libc.CString("bad!")
	if e != nil {
		t.Fatal(e)
	}
	defer libc.Xfree(f.tls, bad)
	write := *(*func(*libc.TLS, uintptr, uintptr, int32, int64) int32)(unsafe.Pointer(&m.FxWrite))
	truncate := *(*func(*libc.TLS, uintptr, int64) int32)(unsafe.Pointer(&m.FxTruncate))
	syncFile := *(*func(*libc.TLS, uintptr, int32) int32)(unsafe.Pointer(&m.FxSync))
	if write(f.tls, f.file, bad, 4, 0) != sqlite3.SQLITE_READONLY || truncate(f.tls, f.file, 0) != sqlite3.SQLITE_READONLY || syncFile(f.tls, f.file, 0) != sqlite3.SQLITE_READONLY {
		t.Fatal("WAL write/truncate/sync capability exposed")
	}
	if !bytes.Equal(before, f.read(t, 0, 32, sqlite3.SQLITE_OK)) {
		t.Fatal("refused write changed header")
	}
	p, e := a.c.begin(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	rw, e := p.acquire(context.Background(), leaseWAL)
	e = errors.Join(e, p.release())
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if e := rw.close(); e != nil {
			t.Error(e)
		}
	}()
	called := false
	fake := &existingWALLease{a: a, r: rw, purpose: leaseWALReadOnly}
	if _, e := fake.Borrow(func(uintptr) int32 { called = true; return 0 }); !errors.Is(e, errPersistentPermitForeign) || called {
		t.Fatal("old RW WAL substituted")
	}
	other, e := a.c.owner.newConnection(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	ro, e := a.OpenWALReadOnly()
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if e := ro.Close(); e != nil {
			t.Error(e)
		}
	}()
	actual := ro.(*existingWALLease)
	fake = &existingWALLease{a: &existingWALAuthority{c: other, root: a.root}, r: actual.r, purpose: leaseWALReadOnly}
	if _, e := fake.Borrow(func(uintptr) int32 { called = true; return 0 }); !errors.Is(e, errPersistentPermitForeign) || called {
		t.Fatal("foreign connection substituted")
	}
}
func TestExistingWALIndependentLifetimes(t *testing.T) {
	if existingWALChild(t) {
		return
	}
	name, a, v := existingWALSetup(t)
	w := existingWALOpen(t, v, name+"-wal", existingWALFlags, true)
	m := existingWALOpen(t, v, name, sqlite3.SQLITE_OPEN_READONLY|sqlite3.SQLITE_OPEN_MAIN_DB, true)
	m.close(t)
	w.read(t, 0, 32, sqlite3.SQLITE_OK)
	m2 := existingWALOpen(t, v, name, sqlite3.SQLITE_OPEN_READONLY|sqlite3.SQLITE_OPEN_MAIN_DB, true)
	w.close(t)
	m2.read(t, 0, 16, sqlite3.SQLITE_OK)
	for _, n := range []string{filepath.Base(name), filepath.Base(name) + "-wal"} {
		if data, e := a.c.owner.broker.readWorkspace(context.Background(), a.root, []string{n}); data != nil || !errors.Is(e, errProtected) {
			t.Fatalf("retired file lost host protection: %v", e)
		}
	}
}
func TestExistingWALMissingNeverCreated(t *testing.T) {
	if existingWALChild(t) {
		return
	}
	name, _, v := existingWALSetup(t)
	existingWALOpen(t, v, name+"-wal", existingWALFlags, true)
	// A separate readonly binding with absent WAL may refuse but must never create.
	dir := localRootPath(t)
	main := filepath.Join(dir, "missing.db")
	if e := os.WriteFile(main, []byte("fixture"), 0600); e != nil {
		t.Fatal(e)
	}
	root, e := OpenLocalRoot(dir)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := root.Close(); e != nil {
			t.Error(e)
		}
	})
	a := &existingWALMissingAuthority{root: root, name: filepath.Base(main) + "-wal"}
	missing, e := sqlite3.ExperimentalExistingWALRegisterRO(a, main)
	if e != nil {
		t.Fatal(e)
	}
	existingWALOpen(t, missing, main+"-wal", existingWALFlags, false)
	if a.attempts != 1 {
		t.Fatal("missing native acquisition not reached")
	}
	if _, e = os.Stat(main + "-wal"); !os.IsNotExist(e) {
		t.Fatalf("missing WAL created: %v", e)
	}
}

type existingWALMissingAuthority struct {
	root     *Root
	name     string
	attempts int
}

func (*existingWALMissingAuthority) OpenMain() (sqlite3.ExperimentalMainReadLease, error) {
	return nil, os.ErrNotExist
}
func (a *existingWALMissingAuthority) OpenWALReadOnly() (sqlite3.ExperimentalMainReadLease, error) {
	a.attempts++
	a.root.mu.Lock()
	defer a.root.mu.Unlock()
	h, e := persistentOpen(a.root.h, a.name, false)
	if h != 0 && h != windows.InvalidHandle {
		e = errors.Join(e, windows.CloseHandle(h))
	}
	if e == nil {
		e = errUnsafe
	}
	return nil, e
}
func (*existingWALMissingAuthority) Revoke(error) {}
func TestExistingWALReadOnlyResourceQualification(t *testing.T) {
	if existingWALChild(t) {
		return
	}
	_, a, _ := existingWALSetup(t)
	p, e := a.c.begin(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if e := p.release(); e != nil {
			t.Error(e)
		}
	}()
	r, e := p.acquireReadOnlyWAL(context.Background())
	if e != nil {
		if errors.Is(e, errExistingWALReadOnlyUnimplemented) {
			t.Fatalf("EXISTING_WAL_RESOURCE_EXPECTED_RED: %v", e)
		}
		t.Fatal(e)
	}
	if r.purpose != leaseWALReadOnly {
		t.Fatal("wrong resource purpose")
	}
	defer func() {
		if e := r.close(); e != nil {
			t.Error(e)
		}
	}()
	if e = p.borrow(context.Background(), r, func(h windows.Handle) error {
		var mask uint32
		var io windows.IO_STATUS_BLOCK
		e := windows.NtQueryInformationFile(h, &io, (*byte)(unsafe.Pointer(&mask)), uint32(unsafe.Sizeof(mask)), 8)
		if e != nil {
			return e
		}
		if io.Status != 0 || io.Information != unsafe.Sizeof(mask) {
			return errUnsafe
		}
		forbidden := uint32(windows.FILE_WRITE_DATA | windows.FILE_APPEND_DATA | windows.DELETE | windows.WRITE_DAC | windows.WRITE_OWNER | windows.FILE_WRITE_ATTRIBUTES | windows.FILE_WRITE_EA)
		if mask&forbidden != 0 || mask&windows.FILE_READ_DATA == 0 {
			return fmt.Errorf("not genuinely RO access mask %#x", mask)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	second, e := p.acquireReadOnlyWAL(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if e := second.close(); e != nil {
			t.Error(e)
		}
	}()
	if second == r || second.id != r.id || second.purpose != leaseWALReadOnly {
		t.Fatal("independent resource identity")
	}
	if e = p.borrow(context.Background(), r, func(h windows.Handle) error { _, e := windows.SetFilePointer(h, 17, nil, windows.FILE_BEGIN); return e }); e != nil {
		t.Fatal(e)
	}
	if e = p.borrow(context.Background(), second, func(h windows.Handle) error {
		pos, e := windows.SetFilePointer(h, 0, nil, windows.FILE_CURRENT)
		if e != nil {
			return e
		}
		if pos != 0 {
			return fmt.Errorf("WAL resources share FILE_OBJECT cursor %d", pos)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, e := p.acquireReadOnlyWAL(ctx); got != nil || !errors.Is(e, context.Canceled) {
		if got != nil {
			e = errors.Join(e, got.close())
		}
		t.Fatalf("pre-cancel resource %v %v", got, e)
	}

	existingWALResourceFaultContracts(t)

}

func existingWALROFixture(t *testing.T, hooks persistentPermitHooks, expected error) (*persistentPermitOwner, *persistentPermitConnection) {
	t.Helper()
	_, root := persistentFixture(t)
	o, e := newPersistentWALReadOnlyOwner(root, "store.db", hooks)
	if e != nil {
		t.Fatal(e)
	}
	permitShutdowns.Store(o, &permitShutdown{done: make(chan struct{})})
	t.Cleanup(func() {
		if e := permitOwnerClose(o); !mainReadCleanupOnly(e, expected) {
			t.Errorf("RO owner cleanup: %v", e)
		}
	})
	c, e := o.newConnection(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	return o, c
}
func existingWALROBegin(t *testing.T, c *persistentPermitConnection, expected error) *persistentOperationPermit {
	t.Helper()
	p, e := c.begin(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := p.release(); !mainReadCleanupOnly(e, expected) {
			t.Errorf("RO permit release: %v", e)
		}
	})
	return p
}
func existingWALResourceFaultContracts(t *testing.T) {
	t.Helper()
	// Legacy owner lacks the factory's close-panic guarantee and must not acquire.
	legacy := permitFixture(t, persistentPermitHooks{}, nil)
	lc := permitConnection(t, legacy)
	lp := permitBegin(t, lc)
	if r, e := lp.acquireReadOnlyWAL(context.Background()); r != nil || !errors.Is(e, errPersistentPermitForeign) {
		t.Fatalf("legacy owner accepted RO acquire %v %v", r, e)
	}
	if e := lp.release(); e != nil {
		t.Fatal(e)
	}
	primary := errors.New("RO candidate native handle plus error")
	count := 0
	_, c := existingWALROFixture(t, persistentPermitHooks{openData: func(parent windows.Handle, n string) (windows.Handle, error) {
		h, e := persistentOpenReadOnlyWAL(parent, n)
		if e != nil {
			return h, e
		}
		return h, primary
	}, close: func(h windows.Handle) error { count++; return windows.CloseHandle(h) }}, nil)
	p := existingWALROBegin(t, c, nil)
	if r, e := p.acquireReadOnlyWAL(context.Background()); r != nil || !errors.Is(e, primary) || count != 1 {
		t.Fatalf("RO handle+error ownership %v %v closes=%d", r, e, count)
	}
	// Returning an existing RW candidate is not a permissible RO substitute.
	rwCloses := 0
	_, c = existingWALROFixture(t, persistentPermitHooks{openData: func(parent windows.Handle, n string) (windows.Handle, error) { return persistentOpen(parent, n, true) }, close: func(h windows.Handle) error { rwCloses++; return windows.CloseHandle(h) }}, nil)
	p = existingWALROBegin(t, c, nil)
	if r, e := p.acquireReadOnlyWAL(context.Background()); r != nil || e == nil || rwCloses != 1 {
		t.Fatalf("RW candidate qualified as RO %v %v closes=%d", r, e, rwCloses)
	}
	for _, panicClose := range []bool{false, true} {
		fault := errors.New(fmt.Sprintf("RO published cleanup fault panic=%t", panicClose))
		closes := 0
		o, c := existingWALROFixture(t, persistentPermitHooks{close: func(h windows.Handle) error {
			closes++
			e := windows.CloseHandle(h)
			if e != nil {
				return e
			}
			if panicClose {
				panic(fault)
			}
			return fault
		}}, fault)
		p := existingWALROBegin(t, c, fault)
		r, e := p.acquireReadOnlyWAL(context.Background())
		if e != nil {
			t.Fatal(e)
		}
		if e = r.close(); !errors.Is(e, fault) {
			t.Fatalf("RO close uncertainty not returned: %v", e)
		}
		if e = r.close(); !errors.Is(e, fault) || closes != 1 {
			t.Fatalf("RO uncertain close retried %v/%d", e, closes)
		}
		if got, e := p.acquireReadOnlyWAL(context.Background()); got != nil || !errors.Is(e, fault) {
			t.Fatalf("RO terminal authority revived %v %v", got, e)
		}
		if e = p.release(); !errors.Is(e, fault) {
			t.Fatal("RO release lost terminal")
		}
		if e = permitOwnerClose(o); !errors.Is(e, fault) || closes != 1 {
			t.Fatalf("RO owner terminal %v/%d", e, closes)
		}
	}

	// An unpublished candidate failing its native open still owns its handle, and
	// cleanup failure must preserve BOTH primary and terminal cleanup errors.
	candidateFault := errors.New("RO candidate cleanup failed")
	candidateCloses := 0
	_, c = existingWALROFixture(t, persistentPermitHooks{openData: func(parent windows.Handle, n string) (windows.Handle, error) {
		h, e := persistentOpenReadOnlyWAL(parent, n)
		if e != nil {
			return h, e
		}
		return h, primary
	}, close: func(h windows.Handle) error {
		candidateCloses++
		return errors.Join(windows.CloseHandle(h), candidateFault)
	}}, candidateFault)
	p = existingWALROBegin(t, c, candidateFault)
	if r, e := p.acquireReadOnlyWAL(context.Background()); r != nil || !errors.Is(e, primary) || !errors.Is(e, candidateFault) || candidateCloses != 1 {
		t.Fatalf("candidate error join %v %v closes=%d", r, e, candidateCloses)
	}
	// Native open returns a handle after cancellation: ownership still transfers.
	ctx, cancel := context.WithCancel(context.Background())
	cancelCloses := 0
	_, c = existingWALROFixture(t, persistentPermitHooks{openData: func(parent windows.Handle, n string) (windows.Handle, error) {
		h, e := persistentOpenReadOnlyWAL(parent, n)
		cancel()
		return h, e
	}, close: func(h windows.Handle) error { cancelCloses++; return windows.CloseHandle(h) }}, nil)
	p = existingWALROBegin(t, c, nil)
	if r, e := p.acquireReadOnlyWAL(ctx); r != nil || !errors.Is(e, context.Canceled) || cancelCloses != 1 {
		t.Fatalf("cancel-during-open ownership %v %v/%d", r, e, cancelCloses)
	}
	// An admitted acquisition owns its method count through real native IO and
	// publication, even after permit.release and owner.close start draining.
	opened := make(chan struct{})
	resume := make(chan struct{})
	releasing := make(chan struct{})
	closing := make(chan struct{})
	o, c := existingWALROFixture(t, persistentPermitHooks{openData: func(parent windows.Handle, n string) (windows.Handle, error) {
		h, e := persistentOpenReadOnlyWAL(parent, n)
		close(opened)
		<-resume
		return h, e
	}, afterReleaseStart: func() { close(releasing) }, afterClosing: func() { close(closing) }}, nil)
	p = existingWALROBegin(t, c, nil)
	var once sync.Once
	unblock := func() { once.Do(func() { close(resume) }) }
	defer unblock()
	acquired := make(chan error, 1)
	go func() {
		r, e := p.acquireReadOnlyWAL(context.Background())
		if e == nil && r == nil {
			e = errUnsafe
		}
		acquired <- e
	}()
	select {
	case <-opened:
	case <-time.After(5 * time.Second):
		t.Fatal("RO native open never reached")
	}
	released := make(chan error, 1)
	go func() { released <- p.release() }()
	select {
	case <-releasing:
	case <-time.After(5 * time.Second):
		t.Fatal("RO release never started")
	}
	closed := make(chan error, 1)
	go func() { closed <- permitOwnerClose(o) }()
	select {
	case <-closing:
	case <-time.After(5 * time.Second):
		t.Fatal("RO owner close never started")
	}
	for _, ch := range []chan error{released, closed} {
		select {
		case e := <-ch:
			t.Fatalf("RO lifetime drained before native IO: %v", e)
		case <-time.After(40 * time.Millisecond):
		}
	}
	unblock()
	if e := leaseAwait(t, acquired, "RO acquire completion"); e != nil {
		t.Fatal(e)
	}
	if e := leaseAwait(t, released, "RO release completion"); e != nil {
		t.Fatal(e)
	}
	if e := leaseAwait(t, closed, "RO owner close completion"); e != nil {
		t.Fatal(e)
	}
}
