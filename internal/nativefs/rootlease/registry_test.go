package rootlease

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// All backends in this portable contract are test-only. No native root, DB
// protection proof, production constructor or admission path is supplied here.
type fakeRoot struct {
	reads    atomic.Int32
	closes   atomic.Int32
	started  chan struct{}
	release  <-chan struct{}
	once     sync.Once
	closeErr error
}

func (f *fakeRoot) read(_ context.Context, _ []string) ([]byte, error) {
	f.reads.Add(1)
	if f.started != nil {
		f.once.Do(func() { close(f.started) })
	}
	if f.release != nil {
		<-f.release
	}
	return []byte("fixture"), nil
}
func (f *fakeRoot) close() error { f.closes.Add(1); return f.closeErr }

func fixtureRegistry(t *testing.T, capacity int, open opener) *registry {
	t.Helper()
	r, err := newRegistry(capacity, open)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.close(); err != nil {
			t.Error(err)
		}
	})
	return r
}
func fixturePrepared(t *testing.T, r *registry) (*prepared, binding) {
	t.Helper()
	p, err := r.acquire(context.Background(), `C:\Work`)
	if err != nil || p == nil {
		t.Fatalf("acquire: %v", err)
	}
	return p, binding{reference: p.reference(), path: `C:\Work`, scope: []byte("scope"), policy: []byte("policy")}
}
func requireRef(t *testing.T, ref string) []string {
	t.Helper()
	fields := strings.Split(ref, ":")
	if len(fields) != 4 || fields[0] != "winlease" || fields[1] != "v1" || len(fields[2]) != 64 || len(fields[3]) != 64 {
		t.Fatalf("noncanonical ref %q", ref)
	}
	for _, part := range fields[2:] {
		for _, c := range part {
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
				t.Fatalf("noncanonical hex %q", ref)
			}
		}
	}
	return fields
}
func requireRefusal(t *testing.T, r *registry, b binding) {
	t.Helper()
	got, err := r.read(context.Background(), b, []string{"file"})
	if err == nil || got != nil {
		t.Fatalf("refusal returned bytes: %q %v", got, err)
	}
}
func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case got := <-ch:
		return got
	case <-time.After(5 * time.Second):
		t.Fatal("lifecycle operation did not finish")
		var zero T
		return zero
	}
}

func TestRegistryRandomEpochAndIndependentReferences(t *testing.T) {
	open := func(context.Context, string) (backend, error) { return &fakeRoot{}, nil }
	a := fixtureRegistry(t, 4, open)
	b := fixtureRegistry(t, 4, open)
	p1, _ := fixturePrepared(t, a)
	p2, _ := fixturePrepared(t, a)
	p3, _ := fixturePrepared(t, b)
	r1, r2, r3 := requireRef(t, p1.reference()), requireRef(t, p2.reference()), requireRef(t, p3.reference())
	if r1[2] != r2[2] || r1[3] == r2[3] || r1[2] == r3[2] {
		t.Fatal("registry epoch or independent lease randomness lost")
	}
	// Package-private APIs prevent other packages retaining a backend or creating
	// an unprotected root through this disconnected registry experiment.
	if reflect.TypeOf(a).NumMethod() != 0 || reflect.TypeOf(p1).NumMethod() != 0 {
		t.Fatal("registry exposes public methods")
	}
}
func TestRegistryPendingAndUnknownAuthorityNeverReadOrReopen(t *testing.T) {
	var opens atomic.Int32
	root := &fakeRoot{}
	r := fixtureRegistry(t, 4, func(context.Context, string) (backend, error) { opens.Add(1); return root, nil })
	p, b := fixturePrepared(t, r)
	requireRefusal(t, r, b)
	for _, ref := range []string{"", "garbage", "winlease:v1:" + strings.Repeat("0", 64) + ":" + strings.Repeat("1", 64)} {
		bad := b
		bad.reference = ref
		requireRefusal(t, r, bad)
	}
	other := fixtureRegistry(t, 1, func(context.Context, string) (backend, error) { return &fakeRoot{}, nil })
	_, old := fixturePrepared(t, other)
	if err := other.close(); err != nil {
		t.Fatal(err)
	}
	requireRefusal(t, r, old)
	if opens.Load() != 1 || root.reads.Load() != 0 {
		t.Fatal("unknown/pending reference opened or read a root")
	}
	if err := p.activate(b); err != nil {
		t.Fatal(err)
	}
	got, err := r.read(context.Background(), b, []string{"file"})
	if err != nil || string(got) != "fixture" || opens.Load() != 1 || root.reads.Load() != 1 {
		t.Fatalf("active read: %q %v", got, err)
	}
}
func TestRegistryActivationCopiesExactImmutableBinding(t *testing.T) {
	root := &fakeRoot{}
	r := fixtureRegistry(t, 1, func(context.Context, string) (backend, error) { return root, nil })
	p, b := fixturePrepared(t, r)
	original := b
	original.scope = append([]byte(nil), b.scope...)
	original.policy = append([]byte(nil), b.policy...)
	wrong := b
	wrong.path = `C:\Other`
	if err := p.activate(wrong); err == nil {
		t.Fatal("activation accepted different acquisition path")
	}
	if err := p.activate(b); err != nil {
		t.Fatal(err)
	}
	b.scope[0] = 'X'
	b.policy[0] = 'Y'
	requireRefusal(t, r, b)
	for _, mutate := range []func(*binding){func(b *binding) { b.path = `C:\Other` }, func(b *binding) { b.scope = []byte("other") }, func(b *binding) { b.policy = []byte("other") }, func(b *binding) { b.reference += "x" }} {
		changed := original
		mutate(&changed)
		requireRefusal(t, r, changed)
	}
	got, err := r.read(context.Background(), original, []string{"file"})
	if err != nil || string(got) != "fixture" || root.reads.Load() != 1 {
		t.Fatalf("activation retained mutable input: %q %v", got, err)
	}
	if err := p.abort(); err != nil {
		t.Fatal(err)
	}
	if root.closes.Load() != 0 {
		t.Fatal("deferred Abort closed successfully activated root")
	}
}
func TestRegistryAbortClosesExactlyOnce(t *testing.T) {
	root := &fakeRoot{}
	r := fixtureRegistry(t, 1, func(context.Context, string) (backend, error) { return root, nil })
	p, b := fixturePrepared(t, r)
	if err := p.abort(); err != nil {
		t.Fatal(err)
	}
	if err := p.abort(); err != nil {
		t.Fatal(err)
	}
	if err := p.activate(b); err == nil {
		t.Fatal("aborted root activated")
	}
	requireRefusal(t, r, b)
	if err := r.close(); err != nil {
		t.Fatal(err)
	}
	if root.closes.Load() != 1 {
		t.Fatalf("close count=%d", root.closes.Load())
	}
}
func TestRegistryOpeningReservationSurvivesCancellation(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	var opens atomic.Int32
	first := &fakeRoot{}
	r := fixtureRegistry(t, 1, func(context.Context, string) (backend, error) {
		if opens.Add(1) == 1 {
			close(entered)
			<-release
			return first, nil
		}
		return &fakeRoot{}, nil
	})
	t.Cleanup(unblock)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		p, err := r.acquire(ctx, `C:\Work`)
		if p != nil {
			p.abort()
		}
		done <- err
	}()
	receive(t, entered)
	cancel()
	if p, err := r.acquire(context.Background(), `C:\Other`); err == nil || p != nil {
		t.Fatal("cancelled but still-opening reservation released capacity")
	}
	if opens.Load() != 1 {
		t.Fatal("capacity overflow invoked another opener")
	}
	select {
	case <-done:
		t.Fatal("acquire abandoned a still-running opener")
	case <-time.After(25 * time.Millisecond):
	}
	unblock()
	if err := receive(t, done); err == nil {
		t.Fatal("cancelled open published a prepared root")
	}
	if first.closes.Load() != 1 {
		t.Fatal("cancelled open did not close exactly once")
	}
	p, err := r.acquire(context.Background(), `C:\Other`)
	if err != nil || p == nil {
		t.Fatalf("completed cleanup did not release capacity: %v", err)
	}
	if err := p.abort(); err != nil {
		t.Fatal(err)
	}
}
func TestRegistryCloseWaitsForReadAndRefusesNewWork(t *testing.T) {
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	root := &fakeRoot{started: make(chan struct{}), release: release}
	r := fixtureRegistry(t, 1, func(context.Context, string) (backend, error) { return root, nil })
	t.Cleanup(unblock)
	p, b := fixturePrepared(t, r)
	if err := p.activate(b); err != nil {
		t.Fatal(err)
	}
	readDone := make(chan error, 1)
	go func() { _, err := r.read(context.Background(), b, []string{"file"}); readDone <- err }()
	receive(t, root.started)
	closeDone := make(chan error, 1)
	go func() { closeDone <- r.close() }()
	receive(t, r.stopping)
	requireRefusal(t, r, b)
	if p, err := r.acquire(context.Background(), `C:\Other`); err == nil || p != nil {
		t.Fatal("acquired after shutdown began")
	}
	select {
	case <-closeDone:
		t.Fatal("close returned while a read still owned the backend")
	case <-time.After(25 * time.Millisecond):
	}
	if root.closes.Load() != 0 {
		t.Fatal("backend closed during read")
	}
	unblock()
	if err := receive(t, readDone); err != nil {
		t.Fatal(err)
	}
	if err := receive(t, closeDone); err != nil {
		t.Fatal(err)
	}
	if root.closes.Load() != 1 {
		t.Fatal("shutdown did not close backend exactly once")
	}
}
func TestRegistryCloseDuringAcquireWaitsAndClosesReturnedBackend(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	root := &fakeRoot{}
	r := fixtureRegistry(t, 1, func(context.Context, string) (backend, error) { close(entered); <-release; return root, nil })
	t.Cleanup(unblock)
	acquireDone := make(chan error, 1)
	go func() {
		p, err := r.acquire(context.Background(), `C:\Work`)
		if p != nil {
			p.abort()
		}
		acquireDone <- err
	}()
	receive(t, entered)
	closeDone := make(chan error, 1)
	go func() { closeDone <- r.close() }()
	receive(t, r.stopping)
	select {
	case <-closeDone:
		t.Fatal("close abandoned an opening native root")
	case <-time.After(25 * time.Millisecond):
	}
	unblock()
	if err := receive(t, acquireDone); err == nil {
		t.Fatal("open result published after close")
	}
	if err := receive(t, closeDone); err != nil {
		t.Fatal(err)
	}
	if root.closes.Load() != 1 {
		t.Fatalf("close count=%d", root.closes.Load())
	}
}
func TestRegistryOpenFailureReleasesCapacity(t *testing.T) {
	var opens atomic.Int32
	sentinel := errors.New("fixture open failed")
	r := fixtureRegistry(t, 1, func(context.Context, string) (backend, error) {
		if opens.Add(1) == 1 {
			return nil, sentinel
		}
		return &fakeRoot{}, nil
	})
	if p, err := r.acquire(context.Background(), `C:\Work`); p != nil || !errors.Is(err, sentinel) {
		t.Fatalf("open failure: %v %v", p, err)
	}
	p, _ := fixturePrepared(t, r)
	if err := p.abort(); err != nil {
		t.Fatal(err)
	}
}

func TestRegistryEntropyFailureNeverOpens(t *testing.T) {
	var opens atomic.Int32
	open := func(context.Context, string) (backend, error) { opens.Add(1); return &fakeRoot{}, nil }
	if r, err := newRegistryWithEntropy(2, open, bytes.NewReader(make([]byte, 31))); r != nil || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("epoch entropy failure: %v %v", r, err)
	}
	r, err := newRegistryWithEntropy(2, open, bytes.NewReader(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	defer r.close()
	if p, err := r.acquire(context.Background(), `C:\Work`); p != nil || err == nil {
		t.Fatalf("lease entropy failure: %v %v", p, err)
	}
	if opens.Load() != 0 {
		t.Fatal("entropy failure acquired a native root")
	}
}
func TestRegistryLeaseCollisionRetriesAreBoundedAndNeverOverwrite(t *testing.T) {
	// Epoch, first lease, four colliding candidates, then a fresh candidate.
	material := append(bytes.Repeat([]byte{0x11}, 32), bytes.Repeat([]byte{0x22}, 32*5)...)
	material = append(material, bytes.Repeat([]byte{0x33}, 32)...)
	entropy := bytes.NewReader(material)
	var opens atomic.Int32
	root := &fakeRoot{}
	r, err := newRegistryWithEntropy(4, func(context.Context, string) (backend, error) {
		if opens.Add(1) == 1 {
			return root, nil
		}
		return &fakeRoot{}, nil
	}, entropy)
	if err != nil {
		t.Fatal(err)
	}
	defer r.close()
	p, b := fixturePrepared(t, r)
	if next, err := r.acquire(context.Background(), `C:\Other`); next != nil || err == nil {
		t.Fatal("collision overwrote an existing reference")
	}
	if entropy.Len() != 32 || opens.Load() != 1 {
		t.Fatalf("collision retry budget violated: remaining=%d opens=%d", entropy.Len(), opens.Load())
	}
	if err := p.activate(b); err != nil {
		t.Fatal(err)
	}
	if got, err := r.read(context.Background(), b, []string{"file"}); err != nil || string(got) != "fixture" {
		t.Fatalf("collision damaged existing root: %q %v", got, err)
	}
	next, err := r.acquire(context.Background(), `C:\Other`)
	if err != nil || next == nil || next.reference() == p.reference() {
		t.Fatalf("fresh candidate after collision: %v %v", next, err)
	}
	if err := next.abort(); err != nil {
		t.Fatal(err)
	}
}

func TestRegistryAbortedReferenceNeverReissued(t *testing.T) {
	material := append(bytes.Repeat([]byte{0x11}, 32), bytes.Repeat([]byte{0x22}, 32*5)...)
	material = append(material, bytes.Repeat([]byte{0x33}, 32)...)
	entropy := bytes.NewReader(material)
	var opens atomic.Int32
	root := &fakeRoot{}
	r, err := newRegistryWithEntropy(2, func(context.Context, string) (backend, error) {
		if opens.Add(1) == 1 {
			return root, nil
		}
		return &fakeRoot{}, nil
	}, entropy)
	if err != nil {
		t.Fatal(err)
	}
	defer r.close()
	p, old := fixturePrepared(t, r)
	if err := p.abort(); err != nil {
		t.Fatal(err)
	}
	if root.closes.Load() != 1 {
		t.Fatal("abort did not release ownership")
	}
	if next, err := r.acquire(context.Background(), `C:\Work`); next != nil || err == nil {
		t.Fatal("aborted authority reference reissued")
	}
	requireRefusal(t, r, old)
	if entropy.Len() != 32 || opens.Load() != 1 {
		t.Fatal("aborted nonce was not retained in lifetime collision set")
	}
	next, err := r.acquire(context.Background(), `C:\Work`)
	if err != nil || next == nil || next.reference() == old.reference {
		t.Fatalf("new authority: %v %v", next, err)
	}
	if err := next.abort(); err != nil {
		t.Fatal(err)
	}
}

func TestRegistryOpenErrorWithOwnerClosesExactlyOnce(t *testing.T) {
	root := &fakeRoot{}
	sentinel := errors.New("fixture late open failure")
	r := fixtureRegistry(t, 1, func(context.Context, string) (backend, error) { return root, sentinel })
	if p, err := r.acquire(context.Background(), `C:\Work`); p != nil || !errors.Is(err, sentinel) {
		t.Fatalf("open failure ownership: %v %v", p, err)
	}
	if root.closes.Load() != 1 {
		t.Fatal("error-returned native owner leaked")
	}
	if err := r.close(); err != nil {
		t.Fatal(err)
	}
	if root.closes.Load() != 1 {
		t.Fatal("error-returned owner closed again at shutdown")
	}
}

type blockingCloseRoot struct {
	fakeRoot
	entered chan struct{}
	finish  <-chan struct{}
}

func (f *blockingCloseRoot) close() error { close(f.entered); <-f.finish; return f.fakeRoot.close() }
func TestRegistryConcurrentAbortWaitsForOwnedCleanup(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	root := &blockingCloseRoot{entered: make(chan struct{}), finish: release}
	var opens atomic.Int32
	r := fixtureRegistry(t, 1, func(context.Context, string) (backend, error) {
		if opens.Add(1) == 1 {
			return root, nil
		}
		return &fakeRoot{}, nil
	})
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	p, _ := fixturePrepared(t, r)
	first := make(chan error, 1)
	go func() { first <- p.abort() }()
	receive(t, root.entered)
	if q, err := r.acquire(context.Background(), `C:\Other`); err == nil || q != nil {
		t.Error("abort released capacity before cleanup")
	}
	second := make(chan error, 1)
	go func() { second <- p.abort() }()
	returned := false
	select {
	case <-second:
		returned = true
		t.Error("second abort returned before owned cleanup finished")
	case <-time.After(25 * time.Millisecond):
	}
	once.Do(func() { close(release) })
	if err := receive(t, first); err != nil {
		t.Fatal(err)
	}
	if !returned {
		if err := receive(t, second); err != nil {
			t.Fatal(err)
		}
	}
	if root.closes.Load() != 1 {
		t.Fatal("concurrent abort double close")
	}
}

func TestRegistryShutdownReportsFailedOpeningCleanup(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	sentinel := errors.New("native close failed")
	root := &fakeRoot{closeErr: sentinel}
	r, err := newRegistry(1, func(context.Context, string) (backend, error) { close(entered); <-release; return root, nil })
	if err != nil {
		t.Fatal(err)
	}
	a := make(chan error, 1)
	go func() { _, err := r.acquire(context.Background(), `C:\Work`); a <- err }()
	receive(t, entered)
	c1, c2 := make(chan error, 1), make(chan error, 1)
	go func() { c1 <- r.close() }()
	receive(t, r.stopping)
	go func() { c2 <- r.close() }()
	once.Do(func() { close(release) })
	for _, ch := range []<-chan error{a, c1, c2} {
		if err := receive(t, ch); !errors.Is(err, sentinel) {
			t.Errorf("cleanup failure omitted: %v", err)
		}
	}
	if root.closes.Load() != 1 {
		t.Fatal("cleanup not exactly once")
	}
}
