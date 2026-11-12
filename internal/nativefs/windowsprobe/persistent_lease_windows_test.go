//go:build windows && nativeprobe_sqlite_lease && nativeprobe_sqlite_identity

package windowsprobe

import (
	"bytes"
	"context"
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func leaseFixture(t *testing.T, h persistentLeaseHooks, allowed error) (*Root, *persistentLeaseOwner) {
	t.Helper()
	// Own root cleanup here instead of persistentFixture's synchronous Close.
	// The canonical helper's temporary trusted root has already been closed.
	dir := localRootPath(t)
	for i, name := range append(append([]string{}, persistentNames...), "neighbor.txt") {
		if e := os.WriteFile(filepath.Join(dir, name), bytes.Repeat([]byte{byte(i + 1)}, 128+i), 0600); e != nil {
			t.Fatal(e)
		}
	}
	r, e := OpenLocalRoot(dir)
	if e != nil {
		t.Fatal(e)
	}
	abandonRoot := false
	t.Cleanup(func() {
		if abandonRoot {
			t.Error("root handles retained: owner shutdown timed out; no competing forced cleanup")
			return
		}
		done := make(chan error, 1)
		go func() { done <- r.Close() }()
		select {
		case e := <-done:
			if e != nil {
				t.Error(e)
			}
		case <-time.After(5 * time.Second):
			t.Error("root close timed out; single close remains active, not retried")
		}
	})
	leaseNativeControls(t, r)
	if t.Failed() {
		t.Fatal("lease native prerequisite cleanup failed before owner construction")
	}
	o, e := newPersistentLeaseOwner(r, "store.db", h)
	if e != nil {
		if errors.Is(e, errPersistentLeaseUnimplemented) {
			t.Fatalf("LEASE_OWNER_EXPECTED_RED: native broker and duplicate controls passed; independent lease owner missing: %v", e)
		}
		t.Fatalf("lease owner setup failure after native prerequisite controls: %v", e)
	}
	leaseShutdowns.Store(o, &leaseShutdown{done: make(chan struct{})})
	t.Cleanup(func() {
		e := leaseOwnerClose(o)
		if errors.Is(e, errLeaseShutdownTimeout) {
			abandonRoot = true
			t.Error("owner close timed out; active shutdown retained without retry or forced resource cleanup")
			return
		}
		leaseShutdowns.Delete(o)
		if e != nil && !errors.Is(e, allowed) {
			t.Error(e)
		}
	})
	return r, o
}
func leaseConnection(t *testing.T, o *persistentLeaseOwner) *persistentLeaseConnection {
	t.Helper()
	c, e := o.newConnection(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func leaseAcquire(t *testing.T, c *persistentLeaseConnection, p persistentLeasePurpose) *persistentFileLease {
	t.Helper()
	l, e := c.acquire(context.Background(), p)
	if e != nil {
		t.Fatalf("acquire purpose %d: %v", p, e)
	}
	return l
}
func TestPersistentLeaseIndependentFileObjects(t *testing.T) {
	var e error
	_, o := leaseFixture(t, persistentLeaseHooks{}, nil)
	a, b := leaseConnection(t, o), leaseConnection(t, o)
	for _, purpose := range []persistentLeasePurpose{leaseMain, leaseSHMLock} {
		x, y := leaseAcquire(t, a, purpose), leaseAcquire(t, b, purpose)
		if e = a.borrow(context.Background(), x, func(h windows.Handle) error { _, e := windows.SetFilePointer(h, 17, nil, windows.FILE_BEGIN); return e }); e != nil {
			t.Fatal(e)
		}
		if e = b.borrow(context.Background(), y, func(h windows.Handle) error {
			p, e := windows.SetFilePointer(h, 0, nil, windows.FILE_CURRENT)
			if e == nil && p != 0 {
				return errors.New("leases share FILE_OBJECT cursor")
			}
			return e
		}); e != nil {
			t.Fatal(e)
		}
		if e = x.close(); e != nil {
			t.Fatal(e)
		}
		if e = y.close(); e != nil {
			t.Fatal(e)
		}
	}
	_ = leaseAcquire(t, a, leaseMain)
	remaining := leaseAcquire(t, b, leaseMain)
	if e = a.close(); e != nil {
		t.Fatal(e)
	}
	if e := b.borrow(context.Background(), remaining, func(h windows.Handle) error { _, e := inspect(h); return e }); e != nil {
		t.Fatalf("other connection invalidated: %v", e)
	}
	if e = b.close(); e != nil {
		t.Fatal(e)
	}
	if v := o.evidence(); v.Resources != 0 || v.Connections != 0 {
		t.Fatalf("unreleased leases: %+v", v)
	}
}
func TestPersistentLeasePublishAfterRegistration(t *testing.T) {
	fault := errors.New("registration failed")
	var opened windows.Handle
	closes := 0
	_, o := leaseFixture(t, persistentLeaseHooks{
		openData: func(p windows.Handle, n string) (windows.Handle, error) {
			h, e := persistentOpen(p, n, true)
			opened = h
			return h, e
		},
		afterRegister: func() error { return fault },
		close: func(h windows.Handle) error {
			if h == opened {
				closes++
			}
			return windows.CloseHandle(h)
		},
	}, fault)
	c := leaseConnection(t, o)
	l, e := c.acquire(context.Background(), leaseMain)
	if l != nil || !errors.Is(e, fault) {
		t.Fatalf("unregistered lease published: %v %v", l, e)
	}
	if opened == 0 || closes != 1 || o.evidence().Resources != 0 {
		t.Fatalf("registration rollback: opened=%x closes=%d resources=%d", opened, closes, o.evidence().Resources)
	}
}
func leaseResourceCloseDrain(t *testing.T) {
	closing := make(chan struct{})
	_, o := leaseFixture(t, persistentLeaseHooks{beforeResourceClose: func() { close(closing) }}, nil)
	c := leaseConnection(t, o)
	l := leaseAcquire(t, c, leaseMain)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	borrowed := make(chan error, 1)
	go func() {
		borrowed <- c.borrow(context.Background(), l, func(h windows.Handle) error { close(entered); <-release; _, e := inspect(h); return e })
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("borrow callback missing")
	}
	closed := make(chan error, 1)
	go func() { closed <- l.close() }()
	select {
	case <-closing:
	case <-time.After(5 * time.Second):
		t.Fatal("resource close transition missing")
	}
	select {
	case e := <-closed:
		t.Fatalf("close escaped live borrow: %v", e)
	case <-time.After(40 * time.Millisecond):
	}
	once.Do(func() { close(release) })
	if e := leaseAwait(t, borrowed, "borrow completion"); e != nil {
		t.Fatal(e)
	}
	if e := leaseAwait(t, closed, "close completion"); e != nil {
		t.Fatal(e)
	}
	called := false
	if e := c.borrow(context.Background(), l, func(windows.Handle) error { called = true; return nil }); !errors.Is(e, errPersistentLeaseClosed) || called {
		t.Fatalf("consumed lease borrowed: %v called=%v", e, called)
	}
}
func leaseOpenErrorOwnsHandle(t *testing.T) {
	fault := errors.New("open returned handle and error")
	var h windows.Handle
	closed := 0
	_, o := leaseFixture(t, persistentLeaseHooks{openData: func(p windows.Handle, n string) (windows.Handle, error) {
		v, e := persistentOpen(p, n, true)
		if e != nil {
			return v, e
		}
		h = v
		return v, fault
	}, close: func(v windows.Handle) error {
		if v == h {
			closed++
		}
		return windows.CloseHandle(v)
	}}, nil)
	before := captureHandleInventory(t)
	c := leaseConnection(t, o)
	l, e := c.acquire(context.Background(), leaseMain)
	if l != nil || !errors.Is(e, fault) || h == 0 || closed != 1 {
		t.Fatalf("handle+error lost: lease=%v err=%v handle=%x closed=%d", l, e, h, closed)
	}
	if after := captureHandleInventory(t); !reflect.DeepEqual(before.files, after.files) {
		t.Fatal("acquisition error leaked disk handles")
	}
}
func TestPersistentLeaseCloseTerminalOnce(t *testing.T) {
	fault := errors.New("uncertain lease close")
	calls := map[windows.Handle]int{}
	r, o := leaseFixture(t, persistentLeaseHooks{close: func(h windows.Handle) error { calls[h]++; return errors.Join(windows.CloseHandle(h), fault) }}, fault)
	c := leaseConnection(t, o)
	l := leaseAcquire(t, c, leaseMain)
	if e := l.close(); !errors.Is(e, fault) {
		t.Fatalf("close error: %v", e)
	}
	if e := l.close(); !errors.Is(e, fault) {
		t.Fatalf("repeat terminal: %v", e)
	}
	if len(calls) != 1 {
		t.Fatalf("unexpected cleanup objects: %v", calls)
	}
	for _, n := range calls {
		if n != 1 {
			t.Fatal("close retried")
		}
	}
	if !errors.Is(o.evidence().Terminal, fault) {
		t.Fatal("owner did not latch close error")
	}
	if _, e := c.acquire(context.Background(), leaseMain); e == nil {
		t.Fatal("terminal owner admitted acquire")
	}
	if data, e := o.readWorkspace(context.Background(), r, []string{"neighbor.txt"}); e == nil || data != nil {
		t.Fatal("terminal owner returned workspace bytes")
	}
}
func TestPersistentLeaseCrossOwnerRefused(t *testing.T) {
	_, a := leaseFixture(t, persistentLeaseHooks{}, nil)
	_, b := leaseFixture(t, persistentLeaseHooks{}, nil)
	ca, cb := leaseConnection(t, a), leaseConnection(t, b)
	other := leaseConnection(t, a)
	l := leaseAcquire(t, ca, leaseMain)
	for _, c := range []*persistentLeaseConnection{cb, other} {
		called := false
		if e := c.borrow(context.Background(), l, func(windows.Handle) error { called = true; return nil }); !errors.Is(e, errPersistentLeaseForeign) || called {
			t.Fatalf("foreign borrow: %v called=%v", e, called)
		}
	}
	if e := l.close(); e != nil {
		t.Fatal(e)
	}
	fresh := leaseAcquire(t, cb, leaseMain)
	if e := cb.borrow(context.Background(), fresh, func(h windows.Handle) error { _, e := inspect(h); return e }); e != nil {
		t.Fatal(e)
	}
	called := false
	if e := cb.borrow(context.Background(), l, func(windows.Handle) error { called = true; return nil }); !errors.Is(e, errPersistentLeaseForeign) || called {
		t.Fatalf("old lease revived under new owner resource: %v", e)
	}
	if e := ca.borrow(context.Background(), l, func(windows.Handle) error { called = true; return nil }); !errors.Is(e, errPersistentLeaseClosed) || called {
		t.Fatalf("consumed lease revived: %v", e)
	}
}
func TestPersistentLeaseConnectionLifetimeProtectsWorkspace(t *testing.T) {
	r, o := leaseFixture(t, persistentLeaseHooks{}, nil)
	c := leaseConnection(t, o)
	l := leaseAcquire(t, c, leaseWAL)
	if e := l.close(); e != nil {
		t.Fatal(e)
	}
	if e := c.close(); e != nil {
		t.Fatal(e)
	}
	for _, name := range persistentNames {
		if data, e := o.readWorkspace(context.Background(), r, []string{name}); !errors.Is(e, errProtected) || data != nil {
			t.Fatalf("last lease close released private identity %s: %v", name, e)
		}
	}
	if data, e := o.readWorkspace(context.Background(), r, []string{"neighbor.txt"}); e != nil || len(data) == 0 {
		t.Fatalf("neighbor unexpectedly disabled: %v", e)
	}
	if e := leaseOwnerClose(o); e != nil {
		t.Fatal(e)
	}
	if data, e := o.readWorkspace(context.Background(), r, []string{"neighbor.txt"}); e == nil || data != nil {
		t.Fatal("closed owner falls back to unprotected read")
	}
}
func TestPersistentLeaseWorkspaceReadDrainsOwnerClose(t *testing.T) {
	closing := make(chan struct{})
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	r, o := leaseFixture(t, persistentLeaseHooks{afterClosing: func() { close(closing) }, afterRead: func() error { close(entered); <-release; return nil }}, nil)
	defer once.Do(func() { close(release) })
	type result struct {
		data []byte
		err  error
	}
	read := make(chan result, 1)
	go func() {
		data, e := o.readWorkspace(context.Background(), r, []string{"neighbor.txt"})
		read <- result{data, e}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("real successful read hook missing")
	}
	closed := make(chan error, 1)
	go func() { closed <- leaseOwnerClose(o) }()
	select {
	case <-closing:
	case <-time.After(5 * time.Second):
		t.Fatal("owner close transition missing")
	}
	select {
	case e := <-closed:
		t.Fatalf("owner close escaped active read: %v", e)
	case <-time.After(40 * time.Millisecond):
	}
	once.Do(func() { close(release) })
	var got result
	select {
	case got = <-read:
	case <-time.After(5 * time.Second):
		t.Fatal("read completion timeout")
	}
	if got.err != nil || len(got.data) == 0 {
		t.Fatalf("real read failed: %v", got.err)
	}
	if e := leaseAwait(t, closed, "close completion"); e != nil {
		t.Fatal(e)
	}
}

func TestPersistentLeaseOpenErrorOwnsHandle(t *testing.T) {
	t.Run("returned-handle-error", leaseOpenErrorOwnsHandle)
	t.Run("pre-cancel", func(t *testing.T) {
		calls := 0
		_, o := leaseFixture(t, persistentLeaseHooks{openData: func(p windows.Handle, n string) (windows.Handle, error) { calls++; return persistentOpen(p, n, true) }}, nil)
		c := leaseConnection(t, o)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		l, e := c.acquire(ctx, leaseMain)
		if l != nil || !errors.Is(e, context.Canceled) || calls != 0 {
			t.Fatalf("pre-cancel reached open: lease=%v error=%v calls=%d", l, e, calls)
		}
	})
	t.Run("cancel-during-open", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var opened windows.Handle
		closes := 0
		_, o := leaseFixture(t, persistentLeaseHooks{openData: func(p windows.Handle, n string) (windows.Handle, error) {
			h, e := persistentOpen(p, n, true)
			opened = h
			cancel()
			return h, e
		}, close: func(h windows.Handle) error {
			if h == opened {
				closes++
			}
			return windows.CloseHandle(h)
		}}, nil)
		c := leaseConnection(t, o)
		l, e := c.acquire(ctx, leaseMain)
		if l != nil || !errors.Is(e, context.Canceled) || opened == 0 || closes != 1 {
			t.Fatalf("cancel lost open ownership: lease=%v error=%v handle=%x closes=%d", l, e, opened, closes)
		}
		if o.evidence().Resources != 0 {
			t.Fatal("cancelled resource published")
		}
	})
}
func TestPersistentLeaseBorrowCloseDrain(t *testing.T) {
	t.Run("resource-close", leaseResourceCloseDrain)
	t.Run("callback-panic", func(t *testing.T) {
		_, o := leaseFixture(t, persistentLeaseHooks{}, nil)
		c := leaseConnection(t, o)
		l := leaseAcquire(t, c, leaseMain)
		marker := errors.New("callback panic marker")
		var recovered any
		func() {
			defer func() { recovered = recover() }()
			_ = c.borrow(context.Background(), l, func(windows.Handle) error { panic(marker) })
		}()
		if recovered != marker {
			t.Fatalf("callback panic swallowed/changed: %v", recovered)
		}
		closed := make(chan error, 1)
		go func() { closed <- l.close() }()
		select {
		case e := <-closed:
			if e != nil {
				t.Fatal(e)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("panic leaked admission lock")
		}
	})
	t.Run("owner-closing-admission", func(t *testing.T) {
		closing := make(chan struct{})
		r, o := leaseFixture(t, persistentLeaseHooks{afterClosing: func() { close(closing) }}, nil)
		c := leaseConnection(t, o)
		l := leaseAcquire(t, c, leaseMain)
		entered, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		defer once.Do(func() { close(release) })
		borrowed := make(chan error, 1)
		go func() {
			borrowed <- c.borrow(context.Background(), l, func(h windows.Handle) error { close(entered); <-release; _, e := inspect(h); return e })
		}()
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("admitted callback missing")
		}
		closed := make(chan error, 1)
		go func() { closed <- leaseOwnerClose(o) }()
		select {
		case <-closing:
		case <-time.After(5 * time.Second):
			t.Fatal("owner closing transition missing")
		}
		late := make(chan error, 4)
		called := make(chan struct{}, 1)
		go func() { _, e := o.newConnection(context.Background()); late <- e }()
		go func() { _, e := c.acquire(context.Background(), leaseMain); late <- e }()
		go func() {
			late <- c.borrow(context.Background(), l, func(windows.Handle) error { called <- struct{}{}; return nil })
		}()
		go func() {
			data, e := o.readWorkspace(context.Background(), r, []string{"neighbor.txt"})
			if data != nil {
				late <- errors.New("late read returned bytes")
				return
			}
			late <- e
		}()
		select {
		case e := <-closed:
			t.Fatalf("close did not drain old borrower: %v", e)
		case <-time.After(40 * time.Millisecond):
		}
		once.Do(func() { close(release) })
		if e := leaseAwait(t, borrowed, "borrow completion"); e != nil {
			t.Fatal(e)
		}
		if e := leaseAwait(t, closed, "close completion"); e != nil {
			t.Fatal(e)
		}
		for i := 0; i < 4; i++ {
			select {
			case e := <-late:
				if !errors.Is(e, errPersistentLeaseClosed) {
					t.Fatalf("closing admitted new work: %v", e)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("late admission did not finish")
			}
		}
		if len(called) != 0 {
			t.Fatal("late callback invoked")
		}
	})
}

// Establish real native prerequisites before entering the missing-owner stub.
// Initial RED is missing owner construction, NOT proof the deeper drain and
// lease acquisition assertions executed. Those must run in the implementation.
func leaseNativeControls(t *testing.T, r *Root) {
	t.Helper()
	b, e := newPersistentBroker(r, "store.db", persistentHooks{})
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if e := b.close(); e != nil {
			t.Error(e)
		}
	}()
	persistentAcquireAll(t, b)
	if data, e := b.readWorkspace(context.Background(), r, []string{"neighbor.txt"}); e != nil || len(data) == 0 {
		t.Fatalf("native broker neighbor control: %v", e)
	}
	if data, e := b.readWorkspace(context.Background(), r, []string{"store.db"}); !errors.Is(e, errProtected) || data != nil {
		t.Fatalf("native broker identity control: %v", e)
	}
	// Positive control: DuplicateHandle shares the same synchronous file cursor.
	original, e := persistentOpen(r.h, "store.db", true)
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if e := windows.CloseHandle(original); e != nil {
			t.Error(e)
		}
	}()
	var dup windows.Handle
	if e = windows.DuplicateHandle(windows.CurrentProcess(), original, windows.CurrentProcess(), &dup, 0, false, windows.DUPLICATE_SAME_ACCESS); e != nil {
		t.Fatal(e)
	}
	defer func() {
		if e := windows.CloseHandle(dup); e != nil {
			t.Error(e)
		}
	}()
	if _, e = windows.SetFilePointer(original, 13, nil, windows.FILE_BEGIN); e != nil {
		t.Fatal(e)
	}
	if pos, e := windows.SetFilePointer(dup, 0, nil, windows.FILE_CURRENT); e != nil || pos != 13 {
		t.Fatalf("duplicate positive control: pos=%d %v", pos, e)
	}

	t.Log("LEASE_FIXTURE_NATIVE_CONTROLS_OK: all existing identities, neighbor bytes, protection and duplicate cursor qualified")
}
func leaseAwait(t *testing.T, ch <-chan error, label string) error {
	t.Helper()
	select {
	case e := <-ch:
		return e
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out awaiting %s", label)
		return nil
	}
}

// One asynchronous owner-close attempt shared by explicit test shutdown and
// fixture cleanup. A timeout is sticky: do not launch a competing close or free
// handles still reachable by the first attempt. Retain its state until exit.
type leaseShutdown struct {
	once     sync.Once
	done     chan struct{}
	err      error
	timedOut atomic.Bool
}

var leaseShutdowns sync.Map
var errLeaseShutdownTimeout = errors.New("lease test owner shutdown timeout")

func leaseOwnerClose(o *persistentLeaseOwner) error {
	value, ok := leaseShutdowns.Load(o)
	if !ok {
		return errors.New("lease test missing shutdown ownership")
	}
	state := value.(*leaseShutdown)
	if state.timedOut.Load() {
		return errLeaseShutdownTimeout
	}
	state.once.Do(func() { go func() { state.err = o.close(); close(state.done) }() })
	select {
	case <-state.done:
		return state.err
	case <-time.After(5 * time.Second):
		state.timedOut.Store(true)
		return errLeaseShutdownTimeout
	}
}
