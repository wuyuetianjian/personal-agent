//go:build windows && nativeprobe_sqlite_identity && nativeprobe_sqlite_lease && nativeprobe_sqlite_permit

package windowsprobe

import (
	"context"
	"errors"
	"golang.org/x/sys/windows"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func permitFixture(t *testing.T, h persistentPermitHooks, allowed error) *persistentPermitOwner {
	t.Helper()
	// Existing independent broker/lease controls must succeed before new RED.
	r, _ := leaseFixture(t, persistentLeaseHooks{}, nil)
	o, e := newPersistentPermitOwner(r, "store.db", h)
	if e != nil {
		if errors.Is(e, errPersistentPermitUnimplemented) {
			t.Fatalf("PERMIT_OWNER_EXPECTED_RED: existing broker/lease controls passed; permit owner missing: %v", e)
		}
		t.Fatalf("permit owner setup failure: %v", e)
	}
	permitShutdowns.Store(o, &permitShutdown{done: make(chan struct{})})
	t.Cleanup(func() {
		e := permitOwnerClose(o)
		if errors.Is(e, errPermitShutdownTimeout) {
			t.Error("permit owner shutdown timed out; retained without retry/forced cleanup")
			return
		}
		permitShutdowns.Delete(o)
		if e != nil && !errors.Is(e, allowed) {
			t.Error(e)
		}
	})
	return o
}
func permitConnection(t *testing.T, o *persistentPermitOwner) *persistentPermitConnection {
	t.Helper()
	c, e := o.newConnection(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func permitBegin(t *testing.T, c *persistentPermitConnection) *persistentOperationPermit {
	t.Helper()
	p, e := c.begin(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func permitAcquire(t *testing.T, p *persistentOperationPermit, purpose persistentLeasePurpose) *persistentPermitResource {
	t.Helper()
	r, e := p.acquire(context.Background(), purpose)
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func permitSignal(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timeout waiting %s", what)
	}
}
func permitInspect(h windows.Handle) error { _, e := inspect(h); return e }
func TestPersistentPermitLazyAcquireWithinOperation(t *testing.T) {
	closing := make(chan struct{})
	o := permitFixture(t, persistentPermitHooks{afterClosing: func() { close(closing) }}, nil)
	c := permitConnection(t, o)
	p := permitBegin(t, c)
	closed := make(chan error, 1)
	go func() { closed <- permitOwnerClose(o) }()
	permitSignal(t, closing, "owner closing")
	r := permitAcquire(t, p, leaseMain)
	if e := p.borrow(context.Background(), r, permitInspect); e != nil {
		t.Fatalf("admitted operation could not finish lazy read: %v", e)
	}
	select {
	case e := <-closed:
		t.Fatalf("owner closed live operation: %v", e)
	default:
	}
	if e := r.close(); e != nil {
		t.Fatal(e)
	}
	if e := p.release(); e != nil {
		t.Fatal(e)
	}
	if e := leaseAwait(t, closed, "permit owner close"); e != nil {
		t.Fatal(e)
	}
}
func TestPersistentPermitNestedAcquireWhileBorrowed(t *testing.T) {
	o := permitFixture(t, persistentPermitHooks{}, nil)
	c := permitConnection(t, o)
	p := permitBegin(t, c)
	first := permitAcquire(t, p, leaseMain)
	result := make(chan error, 1)
	go func() {
		result <- p.borrow(context.Background(), first, func(h windows.Handle) error {
			if e := permitInspect(h); e != nil {
				return e
			}
			second, e := p.acquire(context.Background(), leaseSHMLock)
			if e != nil {
				return e
			}
			e = p.borrow(context.Background(), second, permitInspect)
			return errors.Join(e, second.close())
		})
	}()
	if e := leaseAwait(t, result, "nested acquisition while borrowed"); e != nil {
		t.Fatal(e)
	}
	if e := first.close(); e != nil {
		t.Fatal(e)
	}
	if e := p.release(); e != nil {
		t.Fatal(e)
	}
}
func TestPersistentPermitResourceCloseDrainsBorrow(t *testing.T) {
	closing := make(chan struct{})
	o := permitFixture(t, persistentPermitHooks{beforeResourceClose: func() { close(closing) }}, nil)
	c := permitConnection(t, o)
	p := permitBegin(t, c)
	r := permitAcquire(t, p, leaseMain)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	borrowed := make(chan error, 1)
	go func() {
		borrowed <- p.borrow(context.Background(), r, func(h windows.Handle) error { close(entered); <-release; return permitInspect(h) })
	}()
	permitSignal(t, entered, "borrow")
	closed := make(chan error, 1)
	go func() { closed <- r.close() }()
	permitSignal(t, closing, "resource closing")
	called := false
	if e := p.borrow(context.Background(), r, func(windows.Handle) error { called = true; return nil }); !errors.Is(e, errPersistentPermitClosed) || called {
		t.Fatalf("closing resource admitted borrow: %v", e)
	}
	select {
	case e := <-closed:
		t.Fatalf("closed before borrow returned: %v", e)
	case <-time.After(40 * time.Millisecond):
	}
	once.Do(func() { close(release) })
	if e := leaseAwait(t, borrowed, "borrow done"); e != nil {
		t.Fatal(e)
	}
	if e := leaseAwait(t, closed, "resource drained independent of live operation"); e != nil {
		t.Fatal(e)
	}
	if e := p.release(); e != nil {
		t.Fatal(e)
	}
}
func TestPersistentPermitOwnerCloseDrainsOperation(t *testing.T) {
	opened, releaseOpen, ending, closing := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(releaseOpen) })
	o := permitFixture(t, persistentPermitHooks{openData: func(parent windows.Handle, name string) (windows.Handle, error) {
		h, e := persistentOpen(parent, name, true)
		close(opened)
		<-releaseOpen
		return h, e
	}, afterReleaseStart: func() { close(ending) }, afterClosing: func() { close(closing) }}, nil)
	c := permitConnection(t, o)
	p := permitBegin(t, c)
	type acquired struct {
		r *persistentPermitResource
		e error
	}
	acquisition := make(chan acquired, 1)
	go func() { r, e := p.acquire(context.Background(), leaseMain); acquisition <- acquired{r, e} }()
	permitSignal(t, opened, "native open returns owned handle")
	ended := make(chan error, 1)
	go func() { ended <- p.release() }()
	permitSignal(t, ending, "permit ending")
	closed := make(chan error, 1)
	go func() { closed <- permitOwnerClose(o) }()
	permitSignal(t, closing, "owner closing")
	select {
	case e := <-ended:
		t.Fatalf("permit released before native method drained: %v", e)
	case <-time.After(40 * time.Millisecond):
	}
	select {
	case e := <-closed:
		t.Fatalf("owner closed before native method drained: %v", e)
	default:
	}
	once.Do(func() { close(releaseOpen) })
	var got acquired
	select {
	case got = <-acquisition:
	case <-time.After(5 * time.Second):
		t.Fatal("native method did not complete")
	}
	if got.e != nil || got.r == nil {
		t.Fatalf("previously admitted acquire failed: %v", got.e)
	}
	if e := leaseAwait(t, ended, "permit release after acquire"); e != nil {
		t.Fatal(e)
	}
	if e := leaseAwait(t, closed, "owner close after release"); e != nil {
		t.Fatal(e)
	}
}
func TestPersistentPermitLateAdmissionRefused(t *testing.T) {
	closing := make(chan struct{})
	o := permitFixture(t, persistentPermitHooks{afterClosing: func() { close(closing) }}, nil)
	c := permitConnection(t, o)
	p := permitBegin(t, c)
	closed := make(chan error, 1)
	go func() { closed <- permitOwnerClose(o) }()
	permitSignal(t, closing, "closing")
	if _, e := o.newConnection(context.Background()); !errors.Is(e, errPersistentPermitClosed) {
		t.Fatalf("late connection: %v", e)
	}
	if _, e := c.begin(context.Background()); !errors.Is(e, errPersistentPermitClosed) {
		t.Fatalf("late operation: %v", e)
	}
	if e := p.release(); e != nil {
		t.Fatal(e)
	}
	if _, e := p.acquire(context.Background(), leaseMain); !errors.Is(e, errPersistentPermitClosed) {
		t.Fatalf("retired operation: %v", e)
	}
	if e := leaseAwait(t, closed, "close after late refusal"); e != nil {
		t.Fatal(e)
	}
}
func TestPersistentPermitForeignAndRetiredTokens(t *testing.T) {
	a, b := permitFixture(t, persistentPermitHooks{}, nil), permitFixture(t, persistentPermitHooks{}, nil)
	ca, other, cb := permitConnection(t, a), permitConnection(t, a), permitConnection(t, b)
	p, q, foreign := permitBegin(t, ca), permitBegin(t, other), permitBegin(t, cb)
	r := permitAcquire(t, p, leaseMain)
	for _, caller := range []*persistentOperationPermit{q, foreign} {
		called := false
		if e := caller.borrow(context.Background(), r, func(windows.Handle) error { called = true; return nil }); !errors.Is(e, errPersistentPermitForeign) || called {
			t.Fatalf("foreign connection/owner borrowed: %v", e)
		}
	}
	if e := p.release(); e != nil {
		t.Fatal(e)
	}
	called := false
	if e := p.borrow(context.Background(), r, func(windows.Handle) error { called = true; return nil }); !errors.Is(e, errPersistentPermitClosed) || called {
		t.Fatalf("retired token revived: %v", e)
	}
	next := permitBegin(t, ca)
	if e := next.borrow(context.Background(), r, permitInspect); e != nil {
		t.Fatalf("live same-connection resource lost: %v", e)
	}
	if e := next.release(); e != nil {
		t.Fatal(e)
	}
	if e := q.release(); e != nil {
		t.Fatal(e)
	}
	if e := foreign.release(); e != nil {
		t.Fatal(e)
	}
}
func TestPersistentPermitAcquireFailureOwnership(t *testing.T) {
	primary, cleanup := errors.New("native open handle and error"), errors.New("close uncertain")
	var opened windows.Handle
	count := 0
	o := permitFixture(t, persistentPermitHooks{openData: func(parent windows.Handle, name string) (windows.Handle, error) {
		h, e := persistentOpen(parent, name, true)
		if e != nil {
			return h, e
		}
		opened = h
		return h, primary
	}, close: func(h windows.Handle) error {
		if h == opened {
			count++
		}
		return errors.Join(windows.CloseHandle(h), cleanup)
	}}, cleanup)
	c := permitConnection(t, o)
	p := permitBegin(t, c)
	r, e := p.acquire(context.Background(), leaseMain)
	if r != nil || !errors.Is(e, primary) || !errors.Is(e, cleanup) || opened == 0 || count != 1 {
		t.Fatalf("error ownership: %v %v opened=%x closes=%d", r, e, opened, count)
	}
	if _, e := p.acquire(context.Background(), leaseWAL); e == nil {
		t.Fatal("terminal owner admitted new native IO")
	}
	if e := p.release(); e != nil && !errors.Is(e, cleanup) {
		t.Fatal(e)
	}
	if e := permitOwnerClose(o); !errors.Is(e, cleanup) {
		t.Fatalf("owner forgot close uncertainty: %v", e)
	}
	if count != 1 {
		t.Fatal("uncertain handle close retried")
	}
}
func TestPersistentPermitPanicReleasesAdmission(t *testing.T) {
	o := permitFixture(t, persistentPermitHooks{}, nil)
	c := permitConnection(t, o)
	p := permitBegin(t, c)
	r := permitAcquire(t, p, leaseMain)
	marker := errors.New("permit callback panic")
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		_ = p.borrow(context.Background(), r, func(windows.Handle) error { panic(marker) })
	}()
	if recovered != marker {
		t.Fatalf("panic swallowed: %v", recovered)
	}
	done := make(chan error, 1)
	go func() { done <- errors.Join(r.close(), p.release()) }()
	if e := leaseAwait(t, done, "panic method/borrow counts drain"); e != nil {
		t.Fatal(e)
	}
	if e := permitOwnerClose(o); e != nil {
		t.Fatal(e)
	}
}

type permitShutdown struct {
	once     sync.Once
	done     chan struct{}
	err      error
	timedOut atomic.Bool
}

var permitShutdowns sync.Map
var errPermitShutdownTimeout = errors.New("permit owner shutdown timeout")

func permitOwnerClose(o *persistentPermitOwner) error {
	value, ok := permitShutdowns.Load(o)
	if !ok {
		return errors.New("permit fixture shutdown ownership missing")
	}
	s := value.(*permitShutdown)
	if s.timedOut.Load() {
		return errPermitShutdownTimeout
	}
	s.once.Do(func() { go func() { s.err = o.close(); close(s.done) }() })
	select {
	case <-s.done:
		return s.err
	case <-time.After(5 * time.Second):
		s.timedOut.Store(true)
		return errPermitShutdownTimeout
	}
}
