//go:build windows && nativeprobe_sqlite_identity && nativeprobe_sqlite_lease && nativeprobe_sqlite_permit && nativeprobe_sqlite_persistent && nativeprobe_sqlite_main_read && nativeprobe_sqlite_existing_wal && nativeprobe_sqlite_access && nativeprobe_sqlite_private_shm && nativeprobe_sqlite_shm_resources && nativeprobe_sqlite_shm_slots && nativeprobe_sqlite_private_sql

package windowsprobe

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"modernc.org/libc"
	driver "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type privateSQLStock struct {
	path string
	stop func() error
}

func startPrivateSQLStock(t *testing.T, path string) *privateSQLStock {
	t.Helper()
	if path == "" {
		path = filepath.Join(localRootPath(t), "private-sql.db")
	}
	cmd := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$", "-test.timeout=90s")
	for _, entry := range os.Environ() {
		k, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(k, existingWALChildEnv) {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, existingWALChildEnv+"="+path)
	cmd.Stderr = os.Stderr
	stdout, e := cmd.StdoutPipe()
	shmResourceCheck(t, e)
	stdin, e := cmd.StdinPipe()
	if e != nil {
		shmResourceCheck(t, errors.Join(e, stdout.Close()))
	}
	if e = cmd.Start(); e != nil {
		shmResourceCheck(t, errors.Join(e, stdin.Close(), stdout.Close()))
	}
	ready, drained, done := make(chan string, 1), make(chan error, 1), make(chan error, 1)
	go func() {
		scan := bufio.NewScanner(stdout)
		if scan.Scan() {
			ready <- scan.Text()
		} else {
			ready <- "EOF"
		}
		for scan.Scan() {
		}
		drained <- scan.Err()
	}()
	go func() { readErr := <-drained; done <- errors.Join(readErr, cmd.Wait()) }()
	var once sync.Once
	var stopErr error
	stop := func() error {
		once.Do(func() {
			_, e := fmt.Fprintln(stdin, "release")
			stopErr = errors.Join(e, stdin.Close())
			select {
			case e := <-done:
				stopErr = errors.Join(stopErr, e)
			case <-time.After(10 * time.Second):
				stopErr = errors.Join(stopErr, errors.New("stock shutdown timed out"), cmd.Process.Kill())
				select {
				case e := <-done:
					stopErr = errors.Join(stopErr, e)
				case <-time.After(5 * time.Second):
					stopErr = errors.Join(stopErr, errors.New("stock shutdown unconfirmed"))
				}
			}
		})
		return stopErr
	}
	t.Cleanup(func() {
		if e := stop(); e != nil {
			t.Error(e)
		}
	})
	select {
	case line := <-ready:
		if line != "EXISTING_WAL_READY" {
			t.Fatalf("held stock setup: %s", line)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("held stock startup timed out")
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		st, e := os.Stat(path + suffix)
		if e != nil || st.Size() == 0 {
			t.Fatalf("stock sidecar qualification %s %v", suffix, e)
		}
	}
	baseline, e := sql.Open("sqlite", existingWALURI(path, "ro", "")+"&immutable=1")
	shmResourceCheck(t, e)
	var count int
	e = baseline.QueryRow("SELECT count(*) FROM fixture WHERE value='committed-wal-only'").Scan(&count)
	closeErr := baseline.Close()
	if e != nil || closeErr != nil || count != 0 {
		t.Fatalf("main-only positive control count=%d query=%v close=%v", count, e, closeErr)
	}
	existingWALStockRead(t, path)
	return &privateSQLStock{path, stop}
}

type privateSQLAuthority struct {
	*shmSlotAuthority
	metadata *persistentAccessProbe
}

func (a *privateSQLAuthority) Observe(name string, flags int32) (bool, error) {
	yes, e := a.metadata.Observe(name, flags)
	if e == nil && flags == sqlite3.SQLITE_ACCESS_READWRITE {
		return false, nil
	}
	return yes, e
}
func (a *privateSQLAuthority) ObserveSHM(k sqlite3.ExperimentalSHMResourceKey, region int32, mapped uintptr, readSlot int32) (bool, bool) {
	b := a.host
	b.mu.Lock()
	defer b.mu.Unlock()
	for b.busy {
		b.cond.Wait()
	}
	entry := b.attachments[k]
	if entry == nil || entry.connection != a.c || entry.retired || entry.draining || b.last != nil || b.node == nil {
		return false, false
	}
	view := b.node.views[region]
	matches := view != nil && view.base != 0 && view.base+view.shift == mapped
	// C grant writes also require B.busy. B.mu with busy=false excludes writers
	// without nesting C.mu or reentering the file mutex held by diagnostics.
	grant := a.slots.grants[k]
	held := false
	if readSlot >= 0 && readSlot <= 4 && grant != nil && grant.connection == a.c && !grant.closed && !grant.retained {
		for _, r := range grant.ranges {
			held = held || (r.offset == 3+readSlot && r.count == 1 && !r.exclusive && !r.unlockAttempted && !r.uncertain)
		}
	}
	return matches, held
}

type privateSQLFixture struct {
	stock *privateSQLStock
	a     *privateSQLAuthority
	s     *privateSQLSession
	v     uintptr
	uri   string
}

func newPrivateSQLFixture(t *testing.T, stock *privateSQLStock) *privateSQLFixture {
	t.Helper()
	if stock == nil {
		stock = startPrivateSQLStock(t, "")
	}
	root, e := OpenLocalRoot(filepath.Dir(stock.path))
	shmResourceCheck(t, e)
	owner, e := newPersistentWALReadOnlyOwner(root, filepath.Base(stock.path), persistentPermitHooks{})
	if e != nil {
		shmResourceCheck(t, errors.Join(e, root.Close()))
	}
	permitShutdowns.Store(owner, &permitShutdown{done: make(chan struct{})})
	t.Cleanup(func() {
		if e := permitOwnerClose(owner); e != nil {
			t.Error(e)
			return
		}
		if e := root.Close(); e != nil {
			t.Error(e)
		}
	})
	c, e := owner.newConnection(context.Background())
	shmResourceCheck(t, e)
	base := &existingWALAuthority{c: c, root: root}
	host := newPersistentSHMResourceHost(owner)
	metadata, e := newPersistentAccessProbe(root, stock.path)
	shmResourceCheck(t, e)
	a := &privateSQLAuthority{shmSlotAuthority: &shmSlotAuthority{shmResourceTestAuthority: &shmResourceTestAuthority{existingWALAuthority: base, host: host}, slots: newPersistentSHMSlotHost(host)}, metadata: metadata}
	if yes, e := a.Observe(stock.path+"-wal", sqlite3.SQLITE_ACCESS_EXISTS); e != nil || !yes {
		t.Fatalf("WAL qualification %v %v", yes, e)
	}
	tls := libc.NewTLS()
	defaultBefore := sqlite3.Xsqlite3_vfs_find(tls, 0)
	v, name, e := sqlite3.ExperimentalPrivateSQLSessionRegister(a, stock.path)
	defaultAfter := sqlite3.Xsqlite3_vfs_find(tls, 0)
	tls.Close()
	shmResourceCheck(t, e)
	if defaultBefore == 0 || defaultAfter != defaultBefore {
		t.Fatal("private SQL registration changed stock default VFS")
	}
	uri := existingWALURI(stock.path, "ro", name)
	session, e := newPrivateSQLSession(uri, host.close)
	shmResourceCheck(t, e)
	t.Cleanup(func() {
		done := make(chan error, 1)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			done <- session.close(ctx)
		}()
		select {
		case e := <-done:
			if e != nil {
				t.Error(e)
			}
		case <-time.After(3 * time.Second):
			t.Error("session cleanup blocked; resources retained")
		}
	})
	t.Log("PRIVATE_SQL_SESSION_FIXTURE_QUALIFIED real-held-readtxn WAL-only-row owner-metadata")
	return &privateSQLFixture{stock, a, session, v, uri}
}
func (f *privateSQLFixture) firstRow(t *testing.T) *privateSQLRows {
	t.Helper()
	before := sqlite3.ExperimentalPrivateSQLSessionOpenRefusals(f.v)
	rows, e := f.s.query(context.Background(), "SELECT value FROM fixture")
	if e != nil {
		if rows != nil {
			if ce := rows.close(); ce != nil {
				t.Fatalf("unexpected failed-query cleanup: %v", ce)
			}
		}
		if sqlite3.ExperimentalPrivateSQLSessionOpenRefusals(f.v) == before+1 {
			t.Fatalf("PRIVATE_SQL_SESSION_MAIN_EXPECTED_RED actual qualified main xOpen0x141 lacks combined SQL admission: %v", e)
		}
		t.Fatalf("private SQL failed outside intended main refusal: %v", e)
	}
	t.Cleanup(func() {
		if e := rows.close(); e != nil {
			t.Error(e)
		}
	})
	if !rows.next() {
		t.Fatalf("private SQL missing row: %v", rows.err())
	}
	var value string
	shmResourceCheck(t, rows.scan(&value))
	if value != "committed-wal-only" {
		t.Fatalf("private value %q", value)
	}
	return rows
}
func privateSQLWitness(t *testing.T, f *privateSQLFixture, r *privateSQLRows) {
	t.Helper()
	r.mu.Lock()
	state, e := driver.ExperimentalPrivateSQLDiagnostics(f.s.conn)
	r.mu.Unlock()
	shmResourceCheck(t, e)
	if !state.WALPresent || !state.NormalMode || state.HeapMode || state.SHMUnreliable || !state.WALReadOnly || !state.SHMReadOnly || !state.PrivateMainLive || !state.LivePageZeroMatches || !state.LiveReaderSlotMatches || state.ReaderIndex < 0 {
		t.Fatalf("private live WAL witness: %+v", state)
	}
}
func TestPrivateSQLHeldWALRead(t *testing.T) {
	if privateSQLAdmissionChild(t) {
		return
	}
	f := newPrivateSQLFixture(t, nil)
	r := f.firstRow(t)
	if r.next() {
		t.Fatal("extra row")
	}
	shmResourceCheck(t, r.err())
	shmResourceCheck(t, r.close())
	existingWALStockRead(t, f.stock.path)
}
func TestPrivateSQLNativeWALIndexWitness(t *testing.T) {
	if privateSQLAdmissionChild(t) {
		return
	}
	f := newPrivateSQLFixture(t, nil)
	r := f.firstRow(t)
	privateSQLWitness(t, f, r)
	existingWALStockRead(t, f.stock.path)
}
func TestPrivateSQLRowsLifetime(t *testing.T) {
	if privateSQLAdmissionChild(t) {
		return
	}
	f := newPrivateSQLFixture(t, nil)
	r := f.firstRow(t)
	f.a.host.mu.Lock()
	held := f.a.host.tickets
	f.a.host.mu.Unlock()
	if held == 0 {
		t.Fatal("Query returned without real core pointer permit")
	}
	privateSQLWitness(t, f, r)
	if r.next() {
		t.Fatal("unexpected second row")
	}
	shmResourceCheck(t, r.err())
	f.a.host.mu.Lock()
	remaining := f.a.host.tickets
	f.a.host.mu.Unlock()
	if remaining != 0 {
		t.Fatal("Rows EOF did not release its permit")
	}
	r = f.firstRow(t)
	shmResourceCheck(t, r.close())
	f.a.host.mu.Lock()
	remaining = f.a.host.tickets
	f.a.host.mu.Unlock()
	if remaining != 0 {
		t.Fatal("Rows.Close did not release its permit")
	}
}
func TestPrivateSQLStatementDrain(t *testing.T) {
	if privateSQLAdmissionChild(t) {
		return
	}
	f := newPrivateSQLFixture(t, nil)
	r := f.firstRow(t)
	shmResourceCheck(t, r.close())
	stmt, e := f.s.prepare(context.Background(), "SELECT value FROM fixture")
	shmResourceCheck(t, e)
	t.Cleanup(func() {
		if e := stmt.close(); e != nil {
			t.Error(e)
		}
	})
	rows, e := stmt.query(context.Background())
	shmResourceCheck(t, e)
	t.Cleanup(func() {
		if e := rows.close(); e != nil {
			t.Error(e)
		}
	})
	if !rows.next() {
		t.Fatalf("statement row: %v", rows.err())
	}
	f.a.host.mu.Lock()
	held := f.a.host.tickets
	f.a.host.mu.Unlock()
	if held < 2 {
		t.Fatal("statement and Rows lack independent permits")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- f.s.close(ctx) }()
	select {
	case e := <-done:
		if !errors.Is(e, context.DeadlineExceeded) {
			t.Fatalf("close did not report blocked drain: %v", e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("bounded close did not return")
	}
	if _, e := f.s.query(context.Background(), "SELECT value FROM fixture"); e == nil {
		t.Fatal("closing session admitted query")
	}
	shmResourceCheck(t, rows.close())
	shmResourceCheck(t, stmt.close())
	shmResourceCheck(t, f.s.close(context.Background()))
	f.a.host.mu.Lock()
	closed := f.a.host.closed
	f.a.host.mu.Unlock()
	if !closed {
		t.Fatal("storage not retired after actual statement teardown")
	}
}
func TestPrivateSQLReadonlyFailures(t *testing.T) {
	if privateSQLAdmissionChild(t) {
		return
	}
	f := newPrivateSQLFixture(t, nil)
	r := f.firstRow(t)
	shmResourceCheck(t, r.close())
	e := f.s.exec(context.Background(), "INSERT INTO fixture VALUES('must-not-write')")
	var native *driver.Error
	if !errors.As(e, &native) || native.Code()&0xff != sqlite3.SQLITE_READONLY {
		t.Fatalf("private write must fail READONLY, got %v", e)
	}
	existingWALStockRead(t, f.stock.path)
}
func TestPrivateSQLStorageRequalification(t *testing.T) {
	if privateSQLAdmissionChild(t) {
		return
	}
	f := newPrivateSQLFixture(t, nil)
	r := f.firstRow(t)
	shmResourceCheck(t, r.close())
	// Release the external stock holder first so its share mode cannot explain
	// the following refusal. The already-qualified private owner remains active.
	shmResourceCheck(t, f.stock.stop())
	name, e := windows.UTF16PtrFromString(f.stock.path)
	shmResourceCheck(t, e)
	if e = windows.DeleteFile(name); !errors.Is(e, windows.ERROR_SHARING_VIOLATION) {
		t.Fatalf("active share guard delete=%v", e)
	}
	shmResourceCheck(t, f.s.close(context.Background()))
	shmResourceCheck(t, f.stock.stop())
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if e = os.Remove(f.stock.path + suffix); e != nil && !os.IsNotExist(e) {
			t.Fatal(e)
		}
	}
	freshStock := startPrivateSQLStock(t, f.stock.path)
	old, e := sql.Open("sqlite", f.uri)
	shmResourceCheck(t, e)
	var ignored string
	queryErr := old.QueryRow("SELECT value FROM fixture").Scan(&ignored)
	closeErr := old.Close()
	if queryErr == nil || closeErr != nil {
		t.Fatalf("old binding query=%v close=%v", queryErr, closeErr)
	}
	fresh := newPrivateSQLFixture(t, freshStock)
	freshRows := fresh.firstRow(t)
	privateSQLWitness(t, fresh, freshRows)
	existingWALStockRead(t, f.stock.path)
}
