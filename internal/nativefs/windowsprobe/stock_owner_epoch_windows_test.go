//go:build windows && nativeprobe_sqlite_identity && nativeprobe_sidecar_diagnostic && nativeprobe_stock_epoch

package windowsprobe

import (
	"context"
	"database/sql"
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Stock protocol oracle only. This is not the private VFS or a production gate.
// A dedicated sql.Conn prevents database/sql from returning the physical owner
// to its idle pool between statements. No transaction/Rows is left open.
type stockEpochOwner struct {
	db     *sql.DB
	conn   *sql.Conn
	once   sync.Once
	err    error
	closed atomic.Bool
}

func newStockEpochOwner(t *testing.T, name string) *stockEpochOwner {
	t.Helper()
	db, e := sql.Open("sqlite", name)
	if e != nil {
		t.Fatal(e)
	}
	o := &stockEpochOwner{db: db}
	t.Cleanup(func() {
		if e := o.close(); e != nil {
			t.Error(e)
		}
	})
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	o.conn, e = db.Conn(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	var mode string
	if e = o.conn.QueryRowContext(context.Background(), "PRAGMA journal_mode=WAL").Scan(&mode); e != nil || mode != "wal" {
		t.Fatalf("owner WAL %q %v", mode, e)
	}
	for _, q := range []string{"PRAGMA wal_autocheckpoint=0", "CREATE TABLE fixture(v TEXT)", "INSERT INTO fixture VALUES('owner-baseline')"} {
		if _, e = o.conn.ExecContext(context.Background(), q); e != nil {
			t.Fatal(e)
		}
	}
	stockEpochRows(t, o, 1)
	return o
}
func (o *stockEpochOwner) close() error {
	o.once.Do(func() {
		if o.conn != nil {
			o.err = errors.Join(o.err, o.conn.Close())
		}
		o.err = errors.Join(o.err, o.db.Close())
		o.closed.Store(true)
	})
	return o.err
}
func stockEpochRows(t *testing.T, o *stockEpochOwner, want int) {
	t.Helper()
	var n int
	if e := o.conn.QueryRowContext(context.Background(), "SELECT count(*) FROM fixture").Scan(&n); e != nil || n != want {
		t.Fatalf("owner committed rows %d want%d: %v", n, want, e)
	}
}

// Observe and close immediately: no long-lived broker or extra file guards.
func stockEpochID(t *testing.T, name string) idInfo {
	t.Helper()
	p, e := windows.UTF16PtrFromString(name)
	if e != nil {
		t.Fatal(e)
	}
	h, e := windows.CreateFile(p, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if e != nil {
		t.Fatal(e)
	}
	s, e := inspect(h)
	ce := windows.CloseHandle(h)
	if e != nil || ce != nil {
		t.Fatalf("metadata control %v %v", e, ce)
	}
	return s.ID
}
func stockEpochIDs(t *testing.T, name string) [3]idInfo {
	return [3]idInfo{stockEpochID(t, name), stockEpochID(t, name+"-wal"), stockEpochID(t, name+"-shm")}
}

// Real independent data-capable FILE_OBJECT; shared-delete is deliberate so the
// witness itself is not a sidecar-retirement guard. Any acquired lock is released
// before its handle closes. Only ERROR_LOCK_VIOLATION counts as lock evidence.
func stockEpochLockWitness(t *testing.T, name string, offset, length uint32, conflict bool) {
	t.Helper()
	p, e := windows.UTF16PtrFromString(name)
	if e != nil {
		t.Fatal(e)
	}
	h, e := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if e != nil {
		t.Fatalf("witness open, not lock result: %v", e)
	}
	ov := windows.Overlapped{Offset: offset}
	e = windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, length, 0, &ov)
	var ue error
	if e == nil {
		ue = windows.UnlockFileEx(h, 0, length, 0, &ov)
	}
	ce := windows.CloseHandle(h)
	if ue != nil || ce != nil {
		t.Fatalf("witness cleanup %v %v", ue, ce)
	}
	if conflict {
		if !errors.Is(e, windows.ERROR_LOCK_VIOLATION) {
			t.Fatalf("expected live stock lock conflict, got %v", e)
		}
	} else if e != nil {
		t.Fatalf("released owner lock still conflicts: %v", e)
	}
}
func TestStockOwnerEpochIdleProtocol(t *testing.T) {
	dir := localRootPath(t)
	name := filepath.Join(dir, "epoch.db")
	o := newStockEpochOwner(t, name)
	ids := stockEpochIDs(t, name)
	// Main SHARED range and DMS=128 from the pinned real Windows SQLite source.
	stockEpochLockWitness(t, name, 0x40000002, 510, true)
	stockEpochLockWitness(t, name+"-shm", 128, 1, true)
	for i, payload := range []string{"external-first", "external-reopen"} {
		child := startSidecarChild(t, name, payload)
		child.finish(t, "close")
		stockEpochRows(t, o, 2+i)
		if now := stockEpochIDs(t, name); now != ids {
			t.Fatal("stock child close/reopen changed owner sidecar generation")
		}
		stockEpochLockWitness(t, name, 0x40000002, 510, true)
		stockEpochLockWitness(t, name+"-shm", 128, 1, true)
	}
	if e := o.close(); e != nil {
		t.Fatal(e)
	}
	stockEpochLockWitness(t, name, 0x40000002, 510, false)
	for _, s := range []string{"-wal", "-shm"} {
		if _, e := os.Stat(name + s); !errors.Is(e, os.ErrNotExist) {
			t.Fatalf("last stock owner did not retire %s: %v", s, e)
		}
	}
	child := startSidecarChild(t, name, "after-owner-close")
	stockEpochIDs(t, name)
	child.finish(t, "close")
	t.Log("STOCK_EPOCH_PROTOCOL_CONFIRMED: idle physical stock WAL owner retains main SHARED/DMS; external close/reopen preserves generation; last owner close permits normal retirement/recreation. STOCK ORACLE ONLY")
}

// Explicit test model. It cannot intercept arbitrary Root.Read calls, external
// stock creates, pre-baseline aliases, or private-VFS failure transitions.
type stockEpochAdmission struct {
	mu            sync.Mutex
	cond          *sync.Cond
	closing       bool
	active        int
	owner         *stockEpochOwner
	closingSignal chan struct{}
}

func newStockEpochAdmission(o *stockEpochOwner) *stockEpochAdmission {
	g := &stockEpochAdmission{owner: o, closingSignal: make(chan struct{})}
	g.cond = sync.NewCond(&g.mu)
	return g
}
func (g *stockEpochAdmission) read(r *Root, parts []string, excluded map[idInfo]struct{}, ops handleOps) ([]byte, error) {
	g.mu.Lock()
	if g.closing {
		g.mu.Unlock()
		return nil, errPersistentClosed
	}
	g.active++
	g.mu.Unlock()
	defer func() { g.mu.Lock(); g.active--; g.cond.Broadcast(); g.mu.Unlock() }()
	return r.readWithHandleOps(context.Background(), parts, excluded, ops)
}
func (g *stockEpochAdmission) shutdown() error {
	g.mu.Lock()
	if !g.closing {
		g.closing = true
		close(g.closingSignal)
	}
	for g.active != 0 {
		g.cond.Wait()
	}
	g.mu.Unlock()
	return g.owner.close()
}
func TestStockOwnerEpochDrainAndClose(t *testing.T) {
	dir := localRootPath(t)
	name := filepath.Join(dir, "epoch.db")
	o := newStockEpochOwner(t, name)
	r := sidecarRoot(t, dir)
	ids := stockEpochIDs(t, name)
	excluded := map[idInfo]struct{}{}
	for _, id := range ids {
		excluded[id] = struct{}{}
	}
	g := newStockEpochAdmission(o)
	if e := os.WriteFile(filepath.Join(dir, "neighbor.txt"), []byte("ordinary epoch neighbor"), 0600); e != nil {
		t.Fatal(e)
	}
	for _, n := range []string{"epoch.db", "epoch.db-wal", "epoch.db-shm"} {
		if b, e := g.read(r, []string{n}, excluded, nativeHandleOps()); !errors.Is(e, errProtected) || len(b) != 0 {
			t.Fatalf("active epoch protected read %v", e)
		}
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	ops := nativeHandleOps()
	nativeOpen := ops.open
	ops.open = func(parent windows.Handle, n string, data bool, share uint32) (windows.Handle, error) {
		h, e := nativeOpen(parent, n, data, share)
		if e == nil && data {
			close(entered)
			<-release
		}
		return h, e
	}
	type result struct {
		b []byte
		e error
	}
	readDone := make(chan result, 1)
	go func() { b, e := g.read(r, []string{"neighbor.txt"}, excluded, ops); readDone <- result{b, e} }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("real reader never reached data-handle admission")
	}
	closed := make(chan error, 1)
	go func() { closed <- g.shutdown() }()
	select {
	case <-g.closingSignal:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown not admitted")
	}
	// New requests refuse before root.mu, while the actual existing read is held
	// at native data acquisition. This is not an artificial sleep-based overlap.
	if b, e := g.read(r, []string{"neighbor.txt"}, excluded, nativeHandleOps()); !errors.Is(e, errPersistentClosed) || len(b) != 0 {
		t.Fatalf("closing admission %v", e)
	}
	if o.closed.Load() {
		t.Fatal("owner closed before real read drained")
	}
	stockEpochLockWitness(t, name, 0x40000002, 510, true)
	stockEpochLockWitness(t, name+"-shm", 128, 1, true)
	select {
	case e := <-closed:
		t.Fatalf("close overtook active read: %v", e)
	default:
	}
	unblock()
	select {
	case v := <-readDone:
		if v.e != nil || string(v.b) != "ordinary epoch neighbor" {
			t.Fatalf("admitted reader %v", v.e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reader drain timeout")
	}
	select {
	case e := <-closed:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("owner close timeout")
	}
	if b, e := g.read(r, []string{"neighbor.txt"}, excluded, nativeHandleOps()); !errors.Is(e, errPersistentClosed) || len(b) != 0 {
		t.Fatalf("closed epoch revived %v", e)
	}
	stockEpochLockWitness(t, name, 0x40000002, 510, false)
	for _, s := range []string{"-wal", "-shm"} {
		if _, e := os.Stat(name + s); !errors.Is(e, os.ErrNotExist) {
			t.Fatalf("drained owner sidecar retirement %s: %v", s, e)
		}
	}
	child := startSidecarChild(t, name, "new-generation-after-gate-close")
	stockEpochIDs(t, name)
	if b, e := g.read(r, []string{"epoch.db-wal"}, excluded, nativeHandleOps()); !errors.Is(e, errPersistentClosed) || len(b) != 0 {
		t.Fatalf("new generation revived old gate %v", e)
	}
	child.finish(t, "close")
	t.Log("STOCK_EPOCH_DRAIN_CONFIRMED: test-model admission drains actual Root.read before closing stock owner, remains closed across later stock recreation. NOT PRIVATE OWNER INTEGRATION OR SECURITY GREEN")
}
